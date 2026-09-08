package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/lib/pq"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/constants"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func adminLimit(n int32) int32 {
	if n <= 0 || n > 100 {
		return 20
	}
	return n
}

var assignableRoles = map[string]struct{}{
	constants.RoleViewer:    {},
	constants.RoleSupport:   {},
	constants.RoleCommunity: {},
	constants.RoleAdmin:     {},
}

func actorLabel(a *pb.AdminActor) string {
	if a == nil {
		return "staff"
	}
	return strings.TrimSpace(a.Username + " " + a.AccountId + " " + a.Role)
}

func (uh *uDBHandler) writeAudit(ctx context.Context, actor *pb.AdminActor, action, entityType, entityID string, payload map[string]any) error {
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
	_, err = uh.db.ExecContext(ctx, `
		INSERT INTO admin_audit_log (actor_ip, action, entity_type, entity_id, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)`,
		ip, action, entityType, entityID, string(raw))
	if err != nil {
		return status.Error(codes.Internal, "failed to write audit")
	}
	return nil
}

func (uh *uDBHandler) AdminWriteAudit(ctx context.Context, req *pb.AdminWriteAuditReq) error {
	payload := map[string]any{}
	if strings.TrimSpace(req.GetPayload()) != "" {
		_ = json.Unmarshal([]byte(req.Payload), &payload)
	}
	return uh.writeAudit(ctx, req.GetActor(), req.GetAction(), req.GetEntityType(), req.GetEntityId(), payload)
}

func (uh *uDBHandler) AdminListUsers(ctx context.Context, q string, limit, offset int32) ([]*pb.AdminUserRow, int32, error) {
	limit = adminLimit(limit)
	if offset < 0 {
		offset = 0
	}
	q = strings.TrimSpace(q)
	var total int32
	if err := uh.db.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM user_account ua
		WHERE $1 = '' OR ua.username ILIKE '%' || $1 || '%' OR ua.email ILIKE '%' || $1 || '%'`,
		q).Scan(&total); err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to count users")
	}
	rows, err := uh.db.QueryContext(ctx, `
		SELECT ua.account_id, ua.username, ua.email, us.status, ur.role_desc, ua.is_verified, ua.created_at,
			COALESCE((SELECT string_agg(f.flag_type, ',') FROM user_admin_flags f WHERE f.user_id = ua.id), '')
		FROM user_account ua
		JOIN user_status us ON us.id = ua.user_status
		JOIN user_role ur ON ur.id = ua.role_id
		WHERE $1 = '' OR ua.username ILIKE '%' || $1 || '%' OR ua.email ILIKE '%' || $1 || '%'
		ORDER BY ua.created_at DESC
		LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to list users")
	}
	defer rows.Close()
	var out []*pb.AdminUserRow
	for rows.Next() {
		row := &pb.AdminUserRow{}
		var created sql.NullTime
		var flagCSV string
		if err := rows.Scan(&row.AccountId, &row.Username, &row.Email, &row.Status, &row.Role,
			&row.Verified, &created, &flagCSV); err != nil {
			return nil, 0, status.Error(codes.Internal, "failed to scan user")
		}
		if created.Valid {
			row.CreatedAt = timestamppb.New(created.Time)
		}
		if flagCSV != "" {
			row.Flags = strings.Split(flagCSV, ",")
		}
		out = append(out, row)
	}
	return out, total, rows.Err()
}

func (uh *uDBHandler) AdminSetUserRole(ctx context.Context, username, role string, actor *pb.AdminActor) error {
	username = strings.TrimSpace(username)
	role = strings.TrimSpace(role)
	if username == "" {
		return status.Error(codes.InvalidArgument, "username is required")
	}
	if _, ok := assignableRoles[role]; !ok {
		return status.Error(codes.InvalidArgument, "role must be Viewer, Support, Community, or Admin")
	}

	var currentRole string
	var userID int64
	if err := uh.db.QueryRowContext(ctx, `
		SELECT ua.id, ur.role_desc
		FROM user_account ua
		JOIN user_role ur ON ur.id = ua.role_id
		WHERE ua.username = $1 OR ua.account_id = $1`, username).Scan(&userID, &currentRole); err != nil {
		if err == sql.ErrNoRows {
			return status.Error(codes.NotFound, "user not found")
		}
		return status.Error(codes.Internal, "failed to load user")
	}
	if currentRole == constants.RoleAdmin && role != constants.RoleAdmin {
		var admins int
		if err := uh.db.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM user_account ua
			JOIN user_role ur ON ur.id = ua.role_id
			WHERE ur.role_desc = $1`, constants.RoleAdmin).Scan(&admins); err != nil {
			return status.Error(codes.Internal, "failed to count admins")
		}
		if admins <= 1 {
			return status.Error(codes.FailedPrecondition, "cannot demote the last Admin")
		}
	}
	res, err := uh.db.ExecContext(ctx, `
		UPDATE user_account SET role_id = (SELECT id FROM user_role WHERE role_desc = $2), updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`, userID, role)
	if err != nil {
		return status.Error(codes.Internal, "failed to set role")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return status.Error(codes.NotFound, "user not found")
	}
	return uh.writeAudit(ctx, actor, "user.role", "user", username, map[string]any{
		"from": currentRole, "to": role,
	})
}

func (uh *uDBHandler) AdminFlagUser(ctx context.Context, username, flagType, reason string, actor *pb.AdminActor) error {
	username = strings.TrimSpace(username)
	flagType = strings.ToLower(strings.TrimSpace(flagType))
	reason = strings.TrimSpace(reason)
	if username == "" || reason == "" {
		return status.Error(codes.InvalidArgument, "username and reason are required")
	}
	switch flagType {
	case "bot", "fake", "spam":
	default:
		return status.Error(codes.InvalidArgument, "type must be bot, fake, or spam")
	}
	res, err := uh.db.ExecContext(ctx, `
		INSERT INTO user_admin_flags (user_id, flag_type, reason, created_by)
		SELECT id, $2, $3, $4 FROM user_account WHERE username = $1 OR account_id = $1`,
		username, flagType, reason, actorLabel(actor))
	if err != nil {
		return status.Error(codes.Internal, "failed to flag user")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return status.Error(codes.NotFound, "user not found")
	}
	return uh.writeAudit(ctx, actor, "user.flag", "user", username, map[string]any{
		"flag_type": flagType, "reason": reason,
	})
}

func (uh *uDBHandler) AdminUnflagUser(ctx context.Context, username, flagType, reason string, actor *pb.AdminActor) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return status.Error(codes.NotFound, "user not found")
	}
	flagType = strings.ToLower(strings.TrimSpace(flagType))
	q := `DELETE FROM user_admin_flags f USING user_account ua
		WHERE f.user_id = ua.id AND (ua.username = $1 OR ua.account_id = $1)`
	args := []any{username}
	if flagType != "" {
		q += " AND f.flag_type = $2"
		args = append(args, flagType)
	}
	res, err := uh.db.ExecContext(ctx, q, args...)
	if err != nil {
		return status.Error(codes.Internal, "failed to unflag user")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return status.Error(codes.NotFound, "no flags to remove")
	}
	return uh.writeAudit(ctx, actor, "user.unflag", "user", username, map[string]any{
		"flag_type": flagType, "reason": reason,
	})
}

func (uh *uDBHandler) AdminSuspendUser(ctx context.Context, username, reason string, actor *pb.AdminActor) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return status.Error(codes.InvalidArgument, "username is required")
	}
	res, err := uh.db.ExecContext(ctx, `
		UPDATE user_account
		SET user_status = (SELECT id FROM user_status WHERE status = 'suspended'), updated_at = CURRENT_TIMESTAMP
		WHERE username = $1 OR account_id = $1`, username)
	if err != nil {
		return status.Error(codes.Internal, "failed to suspend user")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return status.Error(codes.NotFound, "user not found")
	}
	return uh.writeAudit(ctx, actor, "user.suspend", "user", username, map[string]any{"reason": reason})
}

func (uh *uDBHandler) AdminUserStats(ctx context.Context) (*pb.AdminUserStatsResp, error) {
	out := &pb.AdminUserStatsResp{}
	err := uh.db.QueryRowContext(ctx, `
		SELECT COUNT(1)::int, COUNT(1) FILTER (WHERE created_at > NOW() - INTERVAL '7 days')::int
		FROM user_account`).Scan(&out.Total, &out.New_7D)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count users")
	}
	return out, nil
}

func (uh *uDBHandler) AdminListBlogs(ctx context.Context, q, st string, limit, offset int32) ([]*pb.AdminBlogRow, int32, error) {
	limit = adminLimit(limit)
	if offset < 0 {
		offset = 0
	}
	q = strings.TrimSpace(q)
	st = strings.TrimSpace(st)
	var total int32
	if err := uh.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM blog b
		JOIN user_account u ON u.id = b.user_id
		WHERE ($1 = '' OR b.blog_id ILIKE '%' || $1 || '%' OR u.username ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR b.status = $2)`, q, st).Scan(&total); err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to count blogs")
	}
	rows, err := uh.db.QueryContext(ctx, `
		SELECT b.blog_id, u.username, COALESCE(b.status, ''), b.created_at
		FROM blog b
		JOIN user_account u ON u.id = b.user_id
		WHERE ($1 = '' OR b.blog_id ILIKE '%' || $1 || '%' OR u.username ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR b.status = $2)
		ORDER BY b.created_at DESC
		LIMIT $3 OFFSET $4`, q, st, limit, offset)
	if err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to list blogs")
	}
	defer rows.Close()
	var out []*pb.AdminBlogRow
	for rows.Next() {
		row := &pb.AdminBlogRow{}
		var created sql.NullTime
		if err := rows.Scan(&row.BlogId, &row.Username, &row.Status, &created); err != nil {
			return nil, 0, status.Error(codes.Internal, "failed to scan blog")
		}
		if created.Valid {
			row.CreatedAt = timestamppb.New(created.Time)
		}
		out = append(out, row)
	}
	return out, total, rows.Err()
}

func (uh *uDBHandler) AdminBlogStats(ctx context.Context) (*pb.AdminBlogStatsResp, error) {
	out := &pb.AdminBlogStatsResp{}
	err := uh.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = $1)::int,
			COUNT(*) FILTER (WHERE status = $2)::int,
			COUNT(*) FILTER (WHERE status = $3)::int,
			COUNT(*) FILTER (WHERE status = $4)::int
		FROM blog`,
		constants.BlogStatusDraft, constants.BlogStatusPublished,
		constants.BlogStatusScheduled, constants.BlogStatusArchived).
		Scan(&out.Draft, &out.Published, &out.Scheduled, &out.Archived)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count blogs")
	}
	return out, nil
}

func (uh *uDBHandler) AdminMissingBlogIds(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := uh.db.QueryContext(ctx, `
		SELECT x FROM unnest($1::text[]) AS t(x)
		WHERE NOT EXISTS (SELECT 1 FROM blog b WHERE b.blog_id = t.x)`, pq.Array(ids))
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to compare blog ids")
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, status.Error(codes.Internal, "failed to scan blog id")
		}
		missing = append(missing, id)
	}
	return missing, rows.Err()
}
