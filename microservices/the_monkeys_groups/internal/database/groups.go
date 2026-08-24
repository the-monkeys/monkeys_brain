package database

import (
	"context"
	"database/sql"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// rowScanner is satisfied by *sql.Row and *sql.Rows so scan helpers serve both
// single-row and iterating callers.
type rowScanner interface {
	Scan(dest ...any) error
}

// groupColumns is the shared projection for hydrating a *pb.Group. It joins the
// organizer's public identifiers so callers never leak numeric ids.
const groupColumns = ` g.id, g.slug, g.name, g.description, g.visibility, g.status,
	g.city, g.region, g.country, g.timezone, g.latitude, g.longitude,
	g.cover_image, g.logo_image, ua.account_id, ua.username, g.member_count,
	g.created_at, g.updated_at`

const groupFrom = ` FROM groups g JOIN user_account ua ON ua.id = g.organizer_id`

// scanGroup materializes one group row from the groupColumns projection.
func scanGroup(s rowScanner) (*pb.Group, error) {
	var (
		g         pb.Group
		desc      sql.NullString
		city      sql.NullString
		region    sql.NullString
		country   sql.NullString
		lat       sql.NullFloat64
		lng       sql.NullFloat64
		cover     sql.NullString
		logo      sql.NullString
		createdAt sql.NullTime
		updatedAt sql.NullTime
	)
	if err := s.Scan(
		&g.Id, &g.Slug, &g.Name, &desc, &g.Visibility, &g.Status,
		&city, &region, &country, &g.Timezone, &lat, &lng,
		&cover, &logo, &g.OrganizerAccountId, &g.OrganizerUsername, &g.MemberCount,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	g.Description = nullStringVal(desc)
	g.City = nullStringVal(city)
	g.Region = nullStringVal(region)
	g.Country = nullStringVal(country)
	g.CoverImage = nullStringVal(cover)
	g.LogoImage = nullStringVal(logo)
	if lat.Valid {
		g.Latitude = lat.Float64
	}
	if lng.Valid {
		g.Longitude = lng.Float64
	}
	if createdAt.Valid {
		g.CreatedAt = timestamppb.New(createdAt.Time)
	}
	if updatedAt.Valid {
		g.UpdatedAt = timestamppb.New(updatedAt.Time)
	}
	return &g, nil
}

// loadTopics returns the topic names attached to a group, ordered for stable
// output.
func loadTopics(ctx context.Context, q querier, groupID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT topic_name FROM group_topics WHERE group_id = $1 ORDER BY topic_name ASC`, groupID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load topics")
	}
	defer rows.Close()

	var topics []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, status.Error(codes.Internal, "failed to scan topic")
		}
		topics = append(topics, t)
	}
	return topics, rows.Err()
}

// replaceTopics clears and rewrites the topic set for a group.
func replaceTopics(ctx context.Context, q querier, groupID int64, topics []string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM group_topics WHERE group_id = $1`, groupID); err != nil {
		return status.Error(codes.Internal, "failed to reset topics")
	}
	seen := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO group_topics (group_id, topic_name) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`, groupID, t); err != nil {
			return status.Error(codes.Internal, "failed to attach topic")
		}
	}
	return nil
}

// hydrateViewer stamps the caller's role and membership status onto the group
// so the gateway can render member-aware UI without a second call. Anonymous
// callers (empty account id) leave both fields blank.
func hydrateViewer(ctx context.Context, q querier, g *pb.Group, accountID string) error {
	if strings.TrimSpace(accountID) == "" {
		return nil
	}
	var (
		isOrganizer  bool
		role         sql.NullString
		memberStatus sql.NullString
	)
	err := q.QueryRowContext(ctx, `
		WITH viewer AS (SELECT id FROM user_account WHERE account_id = $2)
		SELECT
			COALESCE(g.organizer_id = (SELECT id FROM viewer), false),
			(SELECT m.role   FROM group_members m WHERE m.group_id = g.id AND m.user_id = (SELECT id FROM viewer)),
			(SELECT m.status FROM group_members m WHERE m.group_id = g.id AND m.user_id = (SELECT id FROM viewer))
		FROM groups g WHERE g.id = $1`,
		g.Id, accountID).Scan(&isOrganizer, &role, &memberStatus)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return status.Error(codes.Internal, "failed to resolve viewer standing")
	}
	g.ViewerMemberStatus = nullStringVal(memberStatus)
	switch {
	case isOrganizer:
		g.ViewerRole = roleOrganizer
	case role.Valid:
		g.ViewerRole = role.String
	}
	return nil
}

// getGroupByID hydrates a full group (topics + rules + viewer) from its numeric
// id on the supplied querier.
func (db *groupDB) getGroupByID(ctx context.Context, q querier, groupID int64, viewerAccountID string) (*pb.Group, error) {
	g, err := scanGroup(q.QueryRowContext(ctx, "SELECT"+groupColumns+groupFrom+" WHERE g.id = $1", groupID))
	if err == sql.ErrNoRows {
		return nil, status.Error(codes.NotFound, "group not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load group")
	}

	if g.Topics, err = loadTopics(ctx, q, groupID); err != nil {
		return nil, err
	}
	if g.Rules, err = loadRules(ctx, q, groupID); err != nil {
		return nil, err
	}
	if err = hydrateViewer(ctx, q, g, viewerAccountID); err != nil {
		return nil, err
	}
	return g, nil
}

// -----------------------------------------------------------------------------
// Group lifecycle
// -----------------------------------------------------------------------------

func defaultVisibilityValue(v string) string {
	switch v {
	case "public", "private", "unlisted":
		return v
	default:
		return "public"
	}
}

// CreateGroup inserts a group, enrolls the organizer as its first active member
// and attaches any topics — atomically. The organizer's rights are implicit, so
// no permission rows are written.
func (db *groupDB) CreateGroup(ctx context.Context, req *pb.CreateGroupReq) (*pb.Group, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, status.Error(codes.InvalidArgument, "group name is required")
	}

	var out *pb.Group
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		organizerID, err := resolveAccount(ctx, tx, req.AccountId)
		if err != nil {
			return err
		}

		slug := slugify(req.Name)
		timezone := req.Timezone
		if strings.TrimSpace(timezone) == "" {
			timezone = "UTC"
		}

		var groupID int64
		err = tx.QueryRowContext(ctx, `
			INSERT INTO groups
				(slug, name, description, visibility, status, city, region, country,
				 timezone, latitude, longitude, cover_image, logo_image, organizer_id, member_count)
			VALUES ($1,$2,$3,$4,'draft',$5,$6,$7,$8,$9,$10,$11,$12,$13,1)
			RETURNING id`,
			slug, req.Name, nullifyStr(req.Description), defaultVisibilityValue(req.Visibility),
			nullifyStr(req.City), nullifyStr(req.Region), nullifyStr(req.Country), timezone,
			nullifyCoord(req.Latitude), nullifyCoord(req.Longitude),
			nullifyStr(req.CoverImage), nullifyStr(req.LogoImage), organizerID,
		).Scan(&groupID)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to create group: %v", err)
		}

		if _, err = tx.ExecContext(ctx,
			`INSERT INTO group_members (group_id, user_id, role, status)
			 VALUES ($1, $2, $3, $4)`,
			groupID, organizerID, roleOrganizer, memberStatusActive); err != nil {
			return status.Errorf(codes.Internal, "failed to enroll organizer: %v", err)
		}

		if err = replaceTopics(ctx, tx, groupID, req.Topics); err != nil {
			return err
		}

		out, err = db.getGroupByID(ctx, tx, groupID, req.AccountId)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateGroup mutates the editable group settings. Requires the edit_group
// grant (organizer implicitly holds it).
func (db *groupDB) UpdateGroup(ctx context.Context, req *pb.UpdateGroupReq) (*pb.Group, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, status.Error(codes.InvalidArgument, "group name is required")
	}

	var out *pb.Group
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permEditGroup)
		if err != nil {
			return err
		}

		timezone := req.Timezone
		if strings.TrimSpace(timezone) == "" {
			timezone = "UTC"
		}

		if _, err = tx.ExecContext(ctx, `
			UPDATE groups SET
				name = $2, description = $3, visibility = $4, city = $5, region = $6,
				country = $7, timezone = $8, latitude = $9, longitude = $10,
				cover_image = $11, logo_image = $12, updated_at = CURRENT_TIMESTAMP
			WHERE id = $1`,
			groupID, req.Name, nullifyStr(req.Description), defaultVisibilityValue(req.Visibility),
			nullifyStr(req.City), nullifyStr(req.Region), nullifyStr(req.Country), timezone,
			nullifyCoord(req.Latitude), nullifyCoord(req.Longitude),
			nullifyStr(req.CoverImage), nullifyStr(req.LogoImage)); err != nil {
			return status.Errorf(codes.Internal, "failed to update group: %v", err)
		}

		if err = replaceTopics(ctx, tx, groupID, req.Topics); err != nil {
			return err
		}

		out, err = db.getGroupByID(ctx, tx, groupID, req.AccountId)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

var validGroupStatuses = map[string]struct{}{
	"draft": {}, "published": {}, "archived": {}, "suspended": {},
}

// SetGroupStatus transitions a group's lifecycle status (publish / archive).
// Requires the edit_group grant.
func (db *groupDB) SetGroupStatus(ctx context.Context, slug, accountID, newStatus string) (*pb.Group, error) {
	if _, ok := validGroupStatuses[newStatus]; !ok {
		return nil, status.Errorf(codes.InvalidArgument, "invalid group status %q", newStatus)
	}

	var out *pb.Group
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, err := authorizeGroup(ctx, tx, slug, accountID, permEditGroup)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx,
			`UPDATE groups SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
			groupID, newStatus); err != nil {
			return status.Errorf(codes.Internal, "failed to set group status: %v", err)
		}
		out, err = db.getGroupByID(ctx, tx, groupID, accountID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteGroup removes a group and everything that cascades from it. Requires
// the delete_group grant, which only the organizer holds implicitly. Deletion
// is refused while the group still owns upcoming paid events so attendees who
// paid are never silently stranded by a cascade.
func (db *groupDB) DeleteGroup(ctx context.Context, req *pb.GroupActionReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permDeleteGroup)
		if err != nil {
			return err
		}

		var hasPaid bool
		if err = tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM events e
				WHERE e.group_id = $1
				  AND e.status IN ('published', 'live')
				  AND e.start_time > CURRENT_TIMESTAMP
				  AND EXISTS (SELECT 1 FROM event_ticket_tiers t
				               WHERE t.event_id = e.id AND t.price > 0))`,
			groupID).Scan(&hasPaid); err != nil {
			return status.Error(codes.Internal, "failed to check group events")
		}
		if hasPaid {
			return status.Error(codes.FailedPrecondition,
				"cannot delete a group with upcoming paid events; cancel or move them first")
		}

		if _, err = tx.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, groupID); err != nil {
			return status.Errorf(codes.Internal, "failed to delete group: %v", err)
		}
		return nil
	})
}

// GetGroup hydrates a single group by slug for an optional viewer.
func (db *groupDB) GetGroup(ctx context.Context, req *pb.GetGroupReq) (*pb.Group, error) {
	groupID, _, err := resolveGroup(ctx, db.db, req.Slug)
	if err != nil {
		return nil, err
	}
	return db.getGroupByID(ctx, db.db, groupID, req.AccountId)
}
