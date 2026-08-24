package database

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// memberSelect projects a group member joined to its public identifiers plus a
// flattened permission list, so the caller never sees numeric ids.
const memberSelect = `
SELECT m.id, m.group_id, ua.account_id, ua.username, m.role, m.status, m.joined_at,
	array_to_string(ARRAY(
		SELECT p.permission_type FROM group_permissions p
		 WHERE p.group_id = m.group_id AND p.user_id = m.user_id), ',')
FROM group_members m JOIN user_account ua ON ua.id = m.user_id`

// scanMember materializes one row from memberSelect.
func scanMember(s rowScanner) (*pb.GroupMember, error) {
	var (
		m        pb.GroupMember
		joinedAt sql.NullTime
		granted  string
	)
	if err := s.Scan(&m.Id, &m.GroupId, &m.AccountId, &m.Username,
		&m.Role, &m.Status, &joinedAt, &granted); err != nil {
		return nil, err
	}
	if joinedAt.Valid {
		m.JoinedAt = timestamppb.New(joinedAt.Time)
	}
	if granted != "" {
		m.Permissions = strings.Split(granted, ",")
	}
	return &m, nil
}

// getMember hydrates a single membership row by numeric ids.
func getMember(ctx context.Context, q querier, groupID, userID int64) (*pb.GroupMember, error) {
	m, err := scanMember(q.QueryRowContext(ctx,
		memberSelect+" WHERE m.group_id = $1 AND m.user_id = $2", groupID, userID))
	if err == sql.ErrNoRows {
		return nil, status.Error(codes.NotFound, "membership not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load member")
	}
	return m, nil
}

// lockGroup takes a row lock on the group to serialize member_count updates and
// returns its numeric id, organizer id, visibility and status.
func lockGroup(ctx context.Context, q querier, slug string) (groupID, organizerID int64, visibility, groupStatus string, err error) {
	err = q.QueryRowContext(ctx,
		`SELECT id, organizer_id, visibility, status FROM groups WHERE slug = $1 FOR UPDATE`, slug).
		Scan(&groupID, &organizerID, &visibility, &groupStatus)
	if err == sql.ErrNoRows {
		return 0, 0, "", "", status.Error(codes.NotFound, "group not found")
	}
	if err != nil {
		return 0, 0, "", "", status.Error(codes.Internal, "failed to lock group")
	}
	return groupID, organizerID, visibility, groupStatus, nil
}

// adjustMemberCount moves the denormalized counter by delta under the caller's
// (assumed held) row lock. The CHECK constraint guards against negatives.
func adjustMemberCount(ctx context.Context, q querier, groupID int64, delta int) error {
	if delta == 0 {
		return nil
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE groups SET member_count = member_count + $2 WHERE id = $1`, groupID, delta); err != nil {
		return status.Error(codes.Internal, "failed to adjust member count")
	}
	return nil
}

// currentMemberStatus reads the caller's existing membership status under lock,
// returning "" when there is no row.
func currentMemberStatus(ctx context.Context, q querier, groupID, userID int64) (string, error) {
	var s sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT status FROM group_members WHERE group_id = $1 AND user_id = $2 FOR UPDATE`,
		groupID, userID).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", status.Error(codes.Internal, "failed to read membership")
	}
	return nullStringVal(s), nil
}

// JoinGroup enrolls the caller. Public groups admit immediately as an active
// member; private/unlisted groups record a pending join request the organizers
// must approve. Banned callers are refused.
func (db *groupDB) JoinGroup(ctx context.Context, req *pb.JoinGroupReq) (*pb.GroupMember, error) {
	var out *pb.GroupMember
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		actorID, err := resolveAccount(ctx, tx, req.AccountId)
		if err != nil {
			return err
		}
		groupID, organizerID, visibility, groupStatus, err := lockGroup(ctx, tx, req.Slug)
		if err != nil {
			return err
		}
		if organizerID == actorID {
			return status.Error(codes.FailedPrecondition, "organizer is already a member")
		}
		if groupStatus != "published" {
			return status.Error(codes.FailedPrecondition, "group is not open to join")
		}

		var banned bool
		if err = tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM group_bans WHERE group_id = $1 AND user_id = $2)`,
			groupID, actorID).Scan(&banned); err != nil {
			return status.Error(codes.Internal, "failed to check ban")
		}
		if banned {
			return status.Error(codes.PermissionDenied, "you are banned from this group")
		}

		cur, err := currentMemberStatus(ctx, tx, groupID, actorID)
		if err != nil {
			return err
		}
		if cur == memberStatusActive {
			return status.Error(codes.AlreadyExists, "already a member")
		}
		if cur == memberStatusPending {
			return status.Error(codes.AlreadyExists, "join request already pending")
		}

		if visibility == "public" {
			if _, err = tx.ExecContext(ctx, `
				INSERT INTO group_members (group_id, user_id, role, status)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (group_id, user_id)
				DO UPDATE SET status = EXCLUDED.status, role = EXCLUDED.role,
					joined_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP`,
				groupID, actorID, roleMember, memberStatusActive); err != nil {
				return status.Errorf(codes.Internal, "failed to join group: %v", err)
			}
			if err = adjustMemberCount(ctx, tx, groupID, 1); err != nil {
				return err
			}
			out, err = getMember(ctx, tx, groupID, actorID)
			return err
		}

		// Private / unlisted: record a pending request and a pending member row.
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO group_join_requests (group_id, user_id, answers, status)
			VALUES ($1, $2, $3::jsonb, 'pending')
			ON CONFLICT (group_id, user_id)
			DO UPDATE SET answers = EXCLUDED.answers, status = 'pending',
				decided_by = NULL, decided_at = NULL, created_at = CURRENT_TIMESTAMP`,
			groupID, actorID, nullifyStr(req.AnswersJson)); err != nil {
			return status.Errorf(codes.Internal, "failed to record join request: %v", err)
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, user_id, role, status)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (group_id, user_id)
			DO UPDATE SET status = EXCLUDED.status, role = EXCLUDED.role,
				updated_at = CURRENT_TIMESTAMP`,
			groupID, actorID, roleMember, memberStatusPending); err != nil {
			return status.Errorf(codes.Internal, "failed to record pending membership: %v", err)
		}
		out, err = getMember(ctx, tx, groupID, actorID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// LeaveGroup removes the caller's own active/pending membership. The organizer
// cannot leave their own group.
func (db *groupDB) LeaveGroup(ctx context.Context, req *pb.GroupActionReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		actorID, err := resolveAccount(ctx, tx, req.AccountId)
		if err != nil {
			return err
		}
		groupID, organizerID, _, _, err := lockGroup(ctx, tx, req.Slug)
		if err != nil {
			return err
		}
		if organizerID == actorID {
			return status.Error(codes.FailedPrecondition, "organizer cannot leave; transfer or delete the group")
		}
		cur, err := currentMemberStatus(ctx, tx, groupID, actorID)
		if err != nil {
			return err
		}
		if cur == "" || cur == memberStatusLeft || cur == memberStatusRemoved {
			return status.Error(codes.FailedPrecondition, "not a member")
		}
		if _, err = tx.ExecContext(ctx,
			`UPDATE group_members SET status = $3, updated_at = CURRENT_TIMESTAMP
			 WHERE group_id = $1 AND user_id = $2`,
			groupID, actorID, memberStatusLeft); err != nil {
			return status.Errorf(codes.Internal, "failed to leave group: %v", err)
		}
		if err = grantGroupPermissions(ctx, tx, groupID, actorID, nil); err != nil {
			return err
		}
		if cur == memberStatusActive {
			return adjustMemberCount(ctx, tx, groupID, -1)
		}
		return nil
	})
}

// ListMembers returns a page of members, optionally filtered by role/status.
func (db *groupDB) ListMembers(ctx context.Context, req *pb.ListGroupMembersReq) ([]*pb.GroupMember, int32, error) {
	groupID, _, err := resolveGroup(ctx, db.db, req.Slug)
	if err != nil {
		return nil, 0, err
	}

	args := []any{groupID}
	where := " WHERE m.group_id = $1"
	if r := strings.TrimSpace(req.Role); r != "" {
		args = append(args, r)
		where += " AND m.role = $" + strconv.Itoa(len(args))
	}
	if s := strings.TrimSpace(req.Status); s != "" {
		args = append(args, s)
		where += " AND m.status = $" + strconv.Itoa(len(args))
	} else {
		where += " AND m.status = 'active'"
	}

	var total int32
	if err = db.db.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM group_members m"+where, args...).Scan(&total); err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to count members")
	}

	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	args = append(args, limit)
	limitPos := len(args)
	args = append(args, req.Offset)
	offsetPos := len(args)

	rows, err := db.db.QueryContext(ctx,
		memberSelect+where+" ORDER BY m.joined_at ASC LIMIT $"+strconv.Itoa(limitPos)+
			" OFFSET $"+strconv.Itoa(offsetPos), args...)
	if err != nil {
		return nil, 0, status.Error(codes.Internal, "failed to list members")
	}
	defer rows.Close()

	var members []*pb.GroupMember
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, 0, status.Error(codes.Internal, "failed to scan member")
		}
		members = append(members, m)
	}
	return members, total, rows.Err()
}

var assignableRoles = map[string]struct{}{
	roleCoOrganizer: {}, roleModerator: {}, roleMember: {},
}

// UpdateMemberRole changes a member's role and resets their permission bundle to
// match. Requires manage_roles. The organizer's role is immutable here.
func (db *groupDB) UpdateMemberRole(ctx context.Context, req *pb.UpdateMemberRoleReq) error {
	if _, ok := assignableRoles[req.Role]; !ok {
		return status.Errorf(codes.InvalidArgument, "invalid role %q", req.Role)
	}
	return db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permManageRoles)
		if err != nil {
			return err
		}
		targetID, err := resolveUsername(ctx, tx, req.TargetUsername)
		if err != nil {
			return err
		}
		var organizerID int64
		if err = tx.QueryRowContext(ctx, `SELECT organizer_id FROM groups WHERE id = $1`, groupID).
			Scan(&organizerID); err != nil {
			return status.Error(codes.Internal, "failed to load group owner")
		}
		if targetID == organizerID {
			return status.Error(codes.FailedPrecondition, "cannot change the organizer's role")
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE group_members SET role = $3, updated_at = CURRENT_TIMESTAMP
			 WHERE group_id = $1 AND user_id = $2 AND status = 'active'`,
			groupID, targetID, req.Role)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to update role: %v", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return status.Error(codes.NotFound, "active member not found")
		}
		return grantGroupPermissions(ctx, tx, groupID, targetID, permissionsForRole(req.Role))
	})
}

// RemoveMember evicts a member (status removed) and clears their grants.
// Requires manage_members. The organizer cannot be removed.
func (db *groupDB) RemoveMember(ctx context.Context, req *pb.UpdateMemberRoleReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, _, _, err := lockGroup(ctx, tx, req.Slug)
		if err != nil {
			return err
		}
		if _, _, err = authorizeGroup(ctx, tx, req.Slug, req.AccountId, permManageMembers); err != nil {
			return err
		}
		targetID, err := resolveUsername(ctx, tx, req.TargetUsername)
		if err != nil {
			return err
		}
		var organizerID int64
		if err = tx.QueryRowContext(ctx, `SELECT organizer_id FROM groups WHERE id = $1`, groupID).
			Scan(&organizerID); err != nil {
			return status.Error(codes.Internal, "failed to load group owner")
		}
		if targetID == organizerID {
			return status.Error(codes.FailedPrecondition, "cannot remove the organizer")
		}
		cur, err := currentMemberStatus(ctx, tx, groupID, targetID)
		if err != nil {
			return err
		}
		if cur == "" || cur == memberStatusRemoved || cur == memberStatusLeft {
			return status.Error(codes.NotFound, "active member not found")
		}
		if _, err = tx.ExecContext(ctx,
			`UPDATE group_members SET status = $3, updated_at = CURRENT_TIMESTAMP
			 WHERE group_id = $1 AND user_id = $2`,
			groupID, targetID, memberStatusRemoved); err != nil {
			return status.Errorf(codes.Internal, "failed to remove member: %v", err)
		}
		if err = grantGroupPermissions(ctx, tx, groupID, targetID, nil); err != nil {
			return err
		}
		if cur == memberStatusActive {
			return adjustMemberCount(ctx, tx, groupID, -1)
		}
		return nil
	})
}

// BanMember records a ban, evicts the member and clears their grants. Requires
// manage_members. The organizer cannot be banned. The reason travels in the
// request's Reason field.
func (db *groupDB) BanMember(ctx context.Context, req *pb.UpdateMemberRoleReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, _, _, err := lockGroup(ctx, tx, req.Slug)
		if err != nil {
			return err
		}
		_, actorID, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permManageMembers)
		if err != nil {
			return err
		}
		targetID, err := resolveUsername(ctx, tx, req.TargetUsername)
		if err != nil {
			return err
		}
		var organizerID int64
		if err = tx.QueryRowContext(ctx, `SELECT organizer_id FROM groups WHERE id = $1`, groupID).
			Scan(&organizerID); err != nil {
			return status.Error(codes.Internal, "failed to load group owner")
		}
		if targetID == organizerID {
			return status.Error(codes.FailedPrecondition, "cannot ban the organizer")
		}

		cur, err := currentMemberStatus(ctx, tx, groupID, targetID)
		if err != nil {
			return err
		}

		if _, err = tx.ExecContext(ctx, `
			INSERT INTO group_bans (group_id, user_id, reason, banned_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (group_id, user_id)
			DO UPDATE SET reason = EXCLUDED.reason, banned_by = EXCLUDED.banned_by,
				created_at = CURRENT_TIMESTAMP`,
			groupID, targetID, nullifyStr(req.Reason), actorID); err != nil {
			return status.Errorf(codes.Internal, "failed to record ban: %v", err)
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, user_id, role, status)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (group_id, user_id)
			DO UPDATE SET status = EXCLUDED.status, updated_at = CURRENT_TIMESTAMP`,
			groupID, targetID, roleMember, memberStatusBanned); err != nil {
			return status.Errorf(codes.Internal, "failed to ban member: %v", err)
		}
		if err = grantGroupPermissions(ctx, tx, groupID, targetID, nil); err != nil {
			return err
		}
		if cur == memberStatusActive {
			return adjustMemberCount(ctx, tx, groupID, -1)
		}
		return nil
	})
}

// ApproveJoinRequest admits a pending applicant as an active member. Requires
// manage_members.
func (db *groupDB) ApproveJoinRequest(ctx context.Context, req *pb.JoinDecisionReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, _, _, err := lockGroup(ctx, tx, req.Slug)
		if err != nil {
			return err
		}
		_, actorID, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permManageMembers)
		if err != nil {
			return err
		}
		targetID, err := resolveUsername(ctx, tx, req.TargetUsername)
		if err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE group_join_requests
			SET status = 'approved', decided_by = $3, decided_at = CURRENT_TIMESTAMP
			WHERE group_id = $1 AND user_id = $2 AND status = 'pending'`,
			groupID, targetID, actorID)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to approve request: %v", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return status.Error(codes.NotFound, "pending join request not found")
		}

		cur, err := currentMemberStatus(ctx, tx, groupID, targetID)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, user_id, role, status)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (group_id, user_id)
			DO UPDATE SET status = EXCLUDED.status, role = EXCLUDED.role,
				joined_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP`,
			groupID, targetID, roleMember, memberStatusActive); err != nil {
			return status.Errorf(codes.Internal, "failed to activate member: %v", err)
		}
		if cur != memberStatusActive {
			return adjustMemberCount(ctx, tx, groupID, 1)
		}
		return nil
	})
}

// RejectJoinRequest denies a pending applicant. Requires manage_members.
func (db *groupDB) RejectJoinRequest(ctx context.Context, req *pb.JoinDecisionReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permManageMembers)
		if err != nil {
			return err
		}
		actorID, err := resolveAccount(ctx, tx, req.AccountId)
		if err != nil {
			return err
		}
		targetID, err := resolveUsername(ctx, tx, req.TargetUsername)
		if err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE group_join_requests
			SET status = 'rejected', decided_by = $3, decided_at = CURRENT_TIMESTAMP
			WHERE group_id = $1 AND user_id = $2 AND status = 'pending'`,
			groupID, targetID, actorID)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to reject request: %v", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return status.Error(codes.NotFound, "pending join request not found")
		}

		// Clear the pending membership placeholder if present.
		if _, err = tx.ExecContext(ctx,
			`UPDATE group_members SET status = $3, updated_at = CURRENT_TIMESTAMP
			 WHERE group_id = $1 AND user_id = $2 AND status = 'pending'`,
			groupID, targetID, memberStatusRemoved); err != nil {
			return status.Errorf(codes.Internal, "failed to clear pending membership: %v", err)
		}
		return nil
	})
}
