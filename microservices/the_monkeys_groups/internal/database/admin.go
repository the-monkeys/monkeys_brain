package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func adminLimit(n int32) int32 {
	if n <= 0 || n > 100 {
		return 20
	}
	return n
}

func (db *groupDB) writeAudit(ctx context.Context, tx *sql.Tx, actor *pb.AdminActor, action, entityType, entityID string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	if actor != nil {
		payload["actor_username"] = actor.Username
		payload["actor_account_id"] = actor.AccountId
		payload["actor_role"] = actor.Role
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return status.Error(codes.Internal, "failed to encode audit")
	}
	ip := ""
	if actor != nil {
		ip = actor.Ip
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO admin_audit_log (actor_ip, action, entity_type, entity_id, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)`,
		ip, action, entityType, entityID, string(raw))
	if err != nil {
		return status.Error(codes.Internal, "failed to write audit")
	}
	return nil
}

func (db *groupDB) AdminListGroups(ctx context.Context, req *pb.AdminListGroupsReq) (*pb.AdminListGroupsResp, error) {
	limit := adminLimit(req.GetLimit())
	offset := req.GetOffset()
	if offset < 0 {
		offset = 0
	}
	q := strings.TrimSpace(req.GetQuery())
	st := strings.TrimSpace(req.GetStatus())
	var total int32
	if err := db.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM groups g
		WHERE ($1 = '' OR g.status = $1)
		  AND ($2 = '' OR g.name ILIKE '%' || $2 || '%' OR g.slug ILIKE '%' || $2 || '%')`,
		st, q).Scan(&total); err != nil {
		return nil, status.Error(codes.Internal, "failed to count groups")
	}
	rows, err := db.db.QueryContext(ctx, `
		SELECT g.slug, g.name, g.status, g.visibility, u.username, g.member_count
		FROM groups g
		JOIN user_account u ON u.id = g.organizer_id
		WHERE ($1 = '' OR g.status = $1)
		  AND ($2 = '' OR g.name ILIKE '%' || $2 || '%' OR g.slug ILIKE '%' || $2 || '%')
		ORDER BY g.created_at DESC
		LIMIT $3 OFFSET $4`, st, q, limit, offset)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list groups")
	}
	defer rows.Close()
	out := &pb.AdminListGroupsResp{Total: total}
	for rows.Next() {
		row := &pb.AdminGroupRow{}
		if err := rows.Scan(&row.Slug, &row.Name, &row.Status, &row.Visibility, &row.OrganizerUsername, &row.MemberCount); err != nil {
			return nil, status.Error(codes.Internal, "failed to scan group")
		}
		out.Groups = append(out.Groups, row)
	}
	return out, rows.Err()
}

func (db *groupDB) AdminGroupStats(ctx context.Context) (*pb.AdminGroupStatsResp, error) {
	out := &pb.AdminGroupStatsResp{}
	err := db.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'draft')::int,
			COUNT(*) FILTER (WHERE status = 'published')::int,
			COUNT(*) FILTER (WHERE status = 'archived')::int,
			COUNT(*) FILTER (WHERE status = 'suspended')::int
		FROM groups`).Scan(&out.Draft, &out.Published, &out.Archived, &out.Suspended)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count groups")
	}
	return out, nil
}

func (db *groupDB) AdminSuspendGroup(ctx context.Context, req *pb.AdminSuspendGroupReq) (*pb.Group, error) {
	slug := strings.TrimSpace(req.GetSlug())
	if slug == "" {
		return nil, status.Error(codes.InvalidArgument, "slug is required")
	}
	var out *pb.Group
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx, "SELECT id FROM groups WHERE slug = $1 FOR UPDATE", slug).Scan(&id); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "group not found")
			}
			return status.Error(codes.Internal, "failed to load group")
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE groups SET status = 'suspended', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, id); err != nil {
			return status.Error(codes.Internal, "failed to suspend group")
		}
		if err := db.writeAudit(ctx, tx, req.GetActor(), "group.suspend", "group", slug, map[string]any{
			"reason": strings.TrimSpace(req.GetReason()),
		}); err != nil {
			return err
		}
		g, err := db.getGroupByID(ctx, tx, id, "")
		out = g
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
