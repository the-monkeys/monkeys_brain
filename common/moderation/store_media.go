package moderation

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"strings"

	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// storagePath unifies API aliases and object keys without cleaning traversal
// into a different valid object. Profiles and unrelated legacy paths pass through
// unless an actual discussion file or asset relation says they are restricted.
func storagePath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return "", status.Error(codes.InvalidArgument, "invalid media path")
	}
	p := u.Path
	if strings.ContainsAny(p, "\\\x00") {
		return "", status.Error(codes.InvalidArgument, "invalid media path")
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "." || segment == ".." {
			return "", status.Error(codes.InvalidArgument, "invalid media path")
		}
	}
	for _, prefix := range []string{"/api/v2/storage/", "/api/storage/", "/api/v1/files/", "/api/v1.1/files/"} {
		if strings.HasPrefix(p, prefix) {
			p = strings.TrimPrefix(p, prefix)
			break
		}
	}
	p = strings.TrimLeft(p, "/")
	if strings.HasPrefix(p, "post/") {
		p = "posts/" + strings.TrimPrefix(p, "post/")
	}
	if strings.HasPrefix(p, "profile/") {
		p = "profiles/" + strings.TrimPrefix(p, "profile/")
	}
	return p, nil
}

// Each relation is additive. Flagging a group never implicitly rates posts,
// discussions or events that merely happen to be associated with that group.
const storeMediaRelations = `SELECT kind,target_id,checksum FROM content_media_owners`

func storeTargetScanner(ctx context.Context, q storeQuery, t *pb.Target) (bool, error) {
	var flagged bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM (`+storeMediaRelations+`) relation JOIN storage_assets a ON a.checksum=relation.checksum WHERE relation.kind=$1 AND relation.target_id=$2 AND a.is_nsfw IS TRUE)`, t.Kind, t.Id).Scan(&flagged)
	return flagged, err
}
func mediaTarget(p string) *pb.Target {
	parts := strings.Split(p, "/")
	if len(parts) < 2 || parts[1] == "" {
		return nil
	}
	switch parts[0] {
	case "posts":
		return &pb.Target{Kind: "blog", Id: parts[1]}
	case "groups":
		if len(parts) >= 3 && (parts[2] == "logo" || parts[2] == "cover") {
			return &pb.Target{Kind: "group", Id: parts[1]}
		}
	case "events":
		if len(parts) >= 3 && (parts[2] == "cover" || parts[2] == "photos") {
			return &pb.Target{Kind: "event", Id: parts[1]}
		}
	}
	return nil
}
func (s *Store) CheckMedia(ctx context.Context, req *pb.MediaRequest) (*pb.MediaResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "media request required")
	}
	key, err := storagePath(req.Path)
	if err != nil {
		return nil, err
	}
	tx, err := s.transaction(ctx, true)
	if err != nil {
		return nil, storeFailure(err)
	}
	defer tx.Rollback()
	a, err := loadStoreActor(ctx, tx, req.AccountId)
	if err != nil {
		return nil, storeFailure(err)
	}
	out := &pb.MediaResponse{Allowed: true}
	targets := map[string]*pb.Target{}
	add := func(t *pb.Target) {
		if t != nil {
			targets[t.Kind+"/"+t.Id] = t
		}
	}
	add(mediaTarget(key))
	cas := strings.HasPrefix(key, "assets/sha256/")
	checksum := ""
	if cas {
		parts := strings.Split(key, "/")
		if len(parts) != 5 {
			return nil, status.Error(codes.InvalidArgument, "invalid asset path")
		}
		checksum = strings.TrimSuffix(parts[4], path.Ext(parts[4]))
		decoded, e := hex.DecodeString(checksum)
		if e != nil || len(decoded) != 32 || parts[2] != checksum[:2] || parts[3] != checksum[2:4] {
			return nil, status.Error(codes.InvalidArgument, "invalid asset checksum")
		}
		var flagged bool
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(is_nsfw,false) FROM storage_assets WHERE checksum=$1`, checksum).Scan(&flagged)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, storeFailure(err)
		}
		if flagged {
			out.Restricted = true
		}
		rows, e := tx.QueryContext(ctx, `SELECT DISTINCT kind,target_id FROM (`+storeMediaRelations+`) relation WHERE checksum=$1`, checksum)
		if e != nil {
			return nil, storeFailure(e)
		}
		for rows.Next() {
			t := &pb.Target{}
			if e = rows.Scan(&t.Kind, &t.Id); e != nil {
				rows.Close()
				return nil, storeFailure(e)
			}
			add(t)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, storeFailure(e)
		}
	}
	// Discussion keys are user-supplied URLs, not a dedicated /discussions route.
	// Compare canonical storage keys, including legacy aliases and absolute URLs.
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT p.public_id FROM discussion_files f JOIN discussion_posts p ON p.id=f.post_id WHERE moderation_storage_path(f.storage_key)=$1`, key)
	if err != nil {
		return nil, storeFailure(err)
	}
	for rows.Next() {
		t := &pb.Target{Kind: "discussion"}
		if err = rows.Scan(&t.Id); err != nil {
			rows.Close()
			return nil, storeFailure(err)
		}
		add(t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, storeFailure(err)
	}
	// An object-key read can also be an asset even when a historical key did not
	// use the CAS layout. Intrinsically flagged assets never get a raw URL bypass.
	if !cas {
		var flagged bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM storage_assets WHERE moderation_storage_path(object_key)=$1 AND is_nsfw IS TRUE)`, key).Scan(&flagged)
		if err != nil {
			return nil, storeFailure(err)
		}
		if flagged && len(targets) == 0 {
			out.Allowed = false
			out.Restricted = true
		}
	}
	hasVisible := false
	hasRestrictedOwner := false
	for _, t := range targets {
		c, visible, e := loadStoreContent(ctx, tx, a, t)
		if e != nil {
			return nil, storeFailure(e)
		}
		// CAS restrictions apply to every owning reference, even when
		// another public owner makes the checksum address discoverable.
		access, e := storeAccess(ctx, tx, a, t, c, visible || (cas && c.id != 0))
		if e != nil {
			return nil, storeFailure(e)
		}
		hasVisible = hasVisible || visible
		if !visible && !cas {
			out.Allowed = false
		}
		if access.IsNsfw {
			out.Restricted = true
			hasRestrictedOwner = true
			if !visible || !access.Revealed {
				out.Allowed = false
			}
		}
	}
	if cas {
		if len(targets) > 0 && !hasVisible {
			out.Allowed = false
		}
		if out.Restricted && !hasRestrictedOwner {
			out.Allowed = false
		}
	}
	// This is a server-owned admission signal, never a client-supplied ACL bypass.
	out.Resolved = out.Allowed && hasVisible && !strings.HasPrefix(key, "verifications/")
	if err = tx.Commit(); err != nil {
		return nil, storeFailure(err)
	}
	return out, nil
}
