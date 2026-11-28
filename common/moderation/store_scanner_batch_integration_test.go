package moderation

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
)

func scannerBatchTarget(t *testing.T, db *sql.DB, kind, id string) *pb.Target {
	t.Helper()
	switch kind {
	case "blog":
		execStore(t, db, `INSERT INTO blog(blog_id,user_id,status) SELECT $1,id,'Published' FROM user_account WHERE account_id='owner'`, id)
	case "event":
		execStore(t, db, `INSERT INTO events(slug,title,organizer_id,start_time,end_time,event_type,status) SELECT $1,'Event',id,NOW(),NOW()+interval '1 hour','virtual','published' FROM user_account WHERE account_id='owner'`, id)
	case "group":
		execStore(t, db, `INSERT INTO groups(slug,name,organizer_id,status) SELECT $1,'Group',id,'published' FROM user_account WHERE account_id='owner'`, id)
	case "discussion":
		execStore(t, db, `INSERT INTO discussion_posts(public_id,author_id,body,edited_until) SELECT $1,id,'Fixture',NOW() FROM user_account WHERE account_id='owner'`, id)
	default:
		t.Fatalf("unsupported fixture kind %q", kind)
	}
	return &pb.Target{Kind: kind, Id: id}
}

func scannerBatchRef(t *testing.T, db *sql.DB, sum string, target *pb.Target) {
	t.Helper()
	execStore(t, db, `INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,$3,$4,'image','image.jpg')`, uuid.NewString(), sum, target.Kind, target.Id)
}

func TestStoreScannerBatchAllMediaRelations(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	cases := []struct {
		name, kind, relation string
		flagged, want        bool
	}{
		{"blog-ref", "blog", "ref", true, true},
		{"event-ref", "event", "ref", true, true},
		{"group-ref", "group", "ref", true, true},
		{"discussion-ref", "discussion", "ref", true, true},
		{"discussion-file", "discussion", "file", true, true},
		{"discussion-legacy", "discussion", "legacy", true, true},
		{"group-logo", "group", "logo", true, true},
		{"group-cover", "group", "cover", true, true},
		{"event-cover", "event", "cover", true, true},
		{"deleted-ref", "blog", "deleted", true, false},
		{"null-scanner", "blog", "null", false, false},
		{"orphan", "blog", "orphan", true, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := scannerBatchTarget(t, db, tc.kind, tc.name)
			sum := fmt.Sprintf("%064x", i+1)
			key := "objects/" + tc.name + ".jpg"
			execStore(t, db, `INSERT INTO storage_assets(checksum,object_key,is_nsfw) VALUES($1,$2,$3)`, sum, key, tc.flagged)
			switch tc.relation {
			case "ref", "null", "deleted":
				scannerBatchRef(t, db, sum, target)
				if tc.relation == "null" {
					execStore(t, db, `UPDATE storage_assets SET is_nsfw=NULL WHERE checksum=$1`, sum)
				}
				if tc.relation == "deleted" {
					execStore(t, db, `UPDATE storage_asset_refs SET deleted_at=NOW() WHERE checksum=$1`, sum)
				}
			case "file":
				execStore(t, db, `INSERT INTO discussion_files(post_id,storage_key,content_type) SELECT id,$2,'image/jpeg' FROM discussion_posts WHERE public_id=$1`, target.Id, "https://cdn.example.test/api/v2/storage/"+key+"?v=2")
			case "legacy":
				owner := scannerBatchTarget(t, db, "blog", "legacy-owner")
				scannerBatchRef(t, db, sum, owner)
				execStore(t, db, `INSERT INTO discussion_files(post_id,storage_key,content_type) SELECT id,'https://cdn.example.test/api/v1/files/post/legacy-owner/image.jpg?v=2','image/jpeg' FROM discussion_posts WHERE public_id=$1`, target.Id)
			case "logo":
				execStore(t, db, `UPDATE groups SET logo_image=$2 WHERE slug=$1`, target.Id, "/api/storage/"+key)
			case "cover":
				if tc.kind == "group" {
					execStore(t, db, `UPDATE groups SET cover_image=$2 WHERE slug=$1`, target.Id, "/api/storage/"+key)
				} else {
					execStore(t, db, `UPDATE events SET cover_image=$2 WHERE slug=$1`, target.Id, "/api/storage/"+key)
				}
			}
			// Two visible, distinct targets force the real batch SQL, not its scalar fallback.
			got, err := s.Check(ctx, &pb.CheckRequest{Targets: []*pb.Target{target, targetStore("blog"), target}})
			mustStore(t, err)
			if len(got.Items) != 3 || got.Items[0].IsNsfw != tc.want || got.Items[2].IsNsfw != tc.want || got.Items[1].IsNsfw {
				t.Fatalf("relation %s lost or spread its scanner flag: %v", tc.name, got)
			}
			if tc.want && (got.Items[0].Reason != "login_required" || got.Items[0].Revealed || got.Items[0].CanReveal) {
				t.Fatalf("positive scanner relation bypassed admission: %v", got)
			}
		})
	}
}

func TestStoreScannerBatchPairsKindsWithIDs(t *testing.T) {
	s, db := storeFixture(t)
	safeBlog := scannerBatchTarget(t, db, "blog", "event")
	safeDiscussion := scannerBatchTarget(t, db, "discussion", "post")
	sum := strings.Repeat("d", 64)
	execStore(t, db, `INSERT INTO storage_assets(checksum,object_key,is_nsfw) VALUES($1,'objects/paired.jpg',true)`, sum)
	scannerBatchRef(t, db, sum, targetStore("blog"))
	scannerBatchRef(t, db, sum, targetStore("event"))
	targets := []*pb.Target{targetStore("blog"), safeBlog, targetStore("event"), safeDiscussion, targetStore("blog"), {Kind: "blog", Id: "missing"}}
	got, err := s.Check(context.Background(), &pb.CheckRequest{Targets: targets})
	mustStore(t, err)
	want := []bool{true, false, true, false, true, false}
	if len(got.Items) != len(want) {
		t.Fatal(got)
	}
	for i, item := range got.Items {
		if item.Target.Kind != targets[i].Kind || item.Target.Id != targets[i].Id || item.IsNsfw != want[i] {
			t.Fatalf("pair/order %d mixed: %v", i, item)
		}
	}
	if got.Items[5].Reason != "not_found" || got.Items[5].Revision != 0 {
		t.Fatalf("missing placeholder leaked: %v", got.Items[5])
	}
}

func TestStoreScannerBatchACLUsersRevisionsAndSharedCAS(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	sum := strings.Repeat("f", 64)
	key := "assets/sha256/ff/ff/" + sum + ".jpg"
	execStore(t, db, `INSERT INTO storage_assets(checksum,object_key,is_nsfw) VALUES($1,$2,true)`, sum, key)
	targets := []*pb.Target{targetStore("blog"), targetStore("event"), targetStore("group"), targetStore("discussion")}
	for _, target := range targets {
		scannerBatchRef(t, db, sum, target)
	}
	execStore(t, db, `UPDATE groups SET visibility='private'; UPDATE blog SET group_id=(SELECT id FROM groups),audience='public'; UPDATE events SET group_id=(SELECT id FROM groups),visibility='group_members'; UPDATE discussion_posts SET group_id=(SELECT id FROM groups),audience='public'`)
	check := func(account string) []*pb.Access {
		t.Helper()
		got, err := s.Check(ctx, &pb.CheckRequest{AccountId: account, Targets: targets})
		mustStore(t, err)
		if len(got.Items) != 4 {
			t.Fatal(got)
		}
		return got.Items
	}
	for _, item := range check("adult") {
		if item.Reason != "not_found" || item.IsNsfw || item.Revision != 0 || item.ScanStatus != "" {
			t.Fatalf("private metadata leaked: %v", item)
		}
	}
	execStore(t, db, `INSERT INTO group_members(group_id,user_id,status) SELECT g.id,u.id,'active' FROM groups g,user_account u WHERE u.account_id IN ('adult','young')`)
	for _, item := range check("adult") {
		if !item.IsNsfw || !item.CanReveal || item.Revealed || item.Reason != "reveal_required" {
			t.Fatal(item)
		}
	}
	for _, target := range targets {
		_, err := s.Reveal(ctx, &pb.ActionRequest{Actor: &pb.Actor{AccountId: "adult"}, Target: target})
		mustStore(t, err)
	}
	before := check("adult")
	for _, item := range before {
		if !item.Revealed {
			t.Fatalf("batch lost current user consent: %v", item)
		}
	}
	for _, item := range check("owner") {
		if !item.IsNsfw || item.Revealed || item.Reason != "reveal_required" {
			t.Fatalf("another user's reveal leaked: %v", item)
		}
	}
	for _, item := range check("young") {
		if !item.IsNsfw || item.CanReveal || item.Revealed || item.Reason != "underage" {
			t.Fatalf("underage admission changed: %v", item)
		}
	}
	media, err := s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: "/api/storage/" + key})
	mustStore(t, err)
	if !media.Allowed || !media.Restricted {
		t.Fatalf("scalar CAS check lost all-owner consent: %v", media)
	}
	execStore(t, db, `UPDATE storage_assets SET is_nsfw=false WHERE checksum=$1;`, sum)
	execStore(t, db, `UPDATE storage_assets SET is_nsfw=true WHERE checksum=$1;`, sum)
	for i, item := range check("adult") {
		if !item.IsNsfw || item.Revealed || item.Revision <= before[i].Revision {
			t.Fatalf("batch retained stale scanner consent: %v", item)
		}
	}
	media, err = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: "/api/storage/" + key})
	mustStore(t, err)
	if media.Allowed || !media.Restricted {
		t.Fatalf("scalar CAS admitted stale consent: %v", media)
	}
	execStore(t, db, `UPDATE user_account SET date_of_birth='2020-01-01' WHERE account_id='adult'`)
	for _, item := range check("adult") {
		if item.CanReveal || item.Revealed || item.Reason != "underage" {
			t.Fatalf("batch cached DOB: %v", item)
		}
	}
	execStore(t, db, `UPDATE group_members SET status='banned' WHERE user_id=(SELECT id FROM user_account WHERE account_id='adult')`)
	for _, item := range check("adult") {
		if item.Reason != "not_found" || item.IsNsfw || item.Revision != 0 {
			t.Fatalf("batch bypassed removed membership: %v", item)
		}
	}
	execStore(t, db, `UPDATE user_account SET user_status=(SELECT id FROM user_status WHERE status='suspended') WHERE account_id='owner'`)
	for _, item := range check("owner") {
		if item.Reason != "not_found" || item.IsNsfw || item.Revision != 0 {
			t.Fatalf("batch admitted inactive owner: %v", item)
		}
	}
}
