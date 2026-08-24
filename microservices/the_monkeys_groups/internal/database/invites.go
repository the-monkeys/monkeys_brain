package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// newInviteToken returns a 64-char hex token from 32 cryptographically random
// bytes. Collisions are astronomically unlikely; the UNIQUE constraint is the
// backstop.
func newInviteToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", status.Error(codes.Internal, "failed to generate invite token")
	}
	return hex.EncodeToString(buf), nil
}

// normalizeRole clamps an arbitrary wire role to an assignable one, defaulting
// to plain member. Organizer is never assignable through these paths.
func normalizeRole(role string) string {
	if _, ok := assignableRoles[role]; ok {
		return role
	}
	return roleMember
}

// AddMember enrolls a user directly as an active member, bypassing the pending
// queue. Requires manage_members. Banned users are refused; the organizer is
// already a member. A non-default role also provisions its permission bundle.
func (db *groupDB) AddMember(ctx context.Context, req *pb.AddMemberReq) error {
	role := normalizeRole(req.Role)
	return db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, organizerID, _, _, err := lockGroup(ctx, tx, req.Slug)
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
		if targetID == organizerID {
			return status.Error(codes.AlreadyExists, "user is the organizer")
		}

		var banned bool
		if err = tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM group_bans WHERE group_id = $1 AND user_id = $2)`,
			groupID, targetID).Scan(&banned); err != nil {
			return status.Error(codes.Internal, "failed to check ban")
		}
		if banned {
			return status.Error(codes.FailedPrecondition, "user is banned from this group")
		}

		cur, err := currentMemberStatus(ctx, tx, groupID, targetID)
		if err != nil {
			return err
		}
		if cur == memberStatusActive {
			return status.Error(codes.AlreadyExists, "already a member")
		}

		if _, err = tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, user_id, role, status)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (group_id, user_id)
			DO UPDATE SET status = EXCLUDED.status, role = EXCLUDED.role,
				joined_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP`,
			groupID, targetID, role, memberStatusActive); err != nil {
			return status.Errorf(codes.Internal, "failed to add member: %v", err)
		}
		if err = grantGroupPermissions(ctx, tx, groupID, targetID, permissionsForRole(role)); err != nil {
			return err
		}
		// Resolve any pending request so it leaves the review queue.
		if _, err = tx.ExecContext(ctx, `
			UPDATE group_join_requests SET status = 'approved', decided_at = CURRENT_TIMESTAMP
			WHERE group_id = $1 AND user_id = $2 AND status = 'pending'`,
			groupID, targetID); err != nil {
			return status.Errorf(codes.Internal, "failed to resolve join request: %v", err)
		}
		if cur != memberStatusActive {
			return adjustMemberCount(ctx, tx, groupID, 1)
		}
		return nil
	})
}

// inviteSelect projects an invite joined to its group and creator identifiers.
const inviteSelect = `
SELECT i.id, i.group_id, i.token, i.role, i.max_uses, i.uses, i.expires_at,
	ua.username, i.created_at, i.revoked, g.slug, g.name, g.visibility
FROM group_invites i
JOIN groups g ON g.id = i.group_id
JOIN user_account ua ON ua.id = i.created_by`

// scanInvite materializes one row from inviteSelect and computes the derived
// `active` flag (not revoked, not expired, uses remaining).
func scanInvite(s rowScanner) (*pb.GroupInvite, error) {
	var (
		inv       pb.GroupInvite
		expiresAt sql.NullTime
		createdAt sql.NullTime
	)
	if err := s.Scan(&inv.Id, &inv.GroupId, &inv.Token, &inv.Role, &inv.MaxUses,
		&inv.Uses, &expiresAt, &inv.CreatedByUsername, &createdAt, &inv.Revoked,
		&inv.GroupSlug, &inv.GroupName, &inv.GroupVisibility); err != nil {
		return nil, err
	}
	if expiresAt.Valid {
		inv.ExpiresAt = timestamppb.New(expiresAt.Time)
	}
	if createdAt.Valid {
		inv.CreatedAt = timestamppb.New(createdAt.Time)
	}
	inv.Active = inviteIsActive(inv.Revoked, expiresAt, inv.Uses, inv.MaxUses)
	return &inv, nil
}

// inviteIsActive centralizes the "is this link still usable" rule.
func inviteIsActive(revoked bool, expiresAt sql.NullTime, uses, maxUses int32) bool {
	if revoked {
		return false
	}
	if expiresAt.Valid && !expiresAt.Time.After(time.Now()) {
		return false
	}
	if maxUses > 0 && uses >= maxUses {
		return false
	}
	return true
}

// CreateInvite mints a new invite link for the group. Requires manage_members.
func (db *groupDB) CreateInvite(ctx context.Context, req *pb.CreateInviteReq) (*pb.GroupInvite, error) {
	role := normalizeRole(req.Role)
	token, err := newInviteToken()
	if err != nil {
		return nil, err
	}
	var out *pb.GroupInvite
	err = db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, actorID, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permManageMembers)
		if err != nil {
			return err
		}
		maxUses := req.MaxUses
		if maxUses < 0 {
			maxUses = 0
		}
		var expiresAt any
		if req.ExpiresInHours > 0 {
			expiresAt = time.Now().Add(time.Duration(req.ExpiresInHours) * time.Hour)
		}
		var id int64
		if err = tx.QueryRowContext(ctx, `
			INSERT INTO group_invites (group_id, token, role, max_uses, expires_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			groupID, token, role, maxUses, expiresAt, actorID).Scan(&id); err != nil {
			return status.Errorf(codes.Internal, "failed to create invite: %v", err)
		}
		out, err = scanInvite(tx.QueryRowContext(ctx, inviteSelect+" WHERE i.id = $1", id))
		if err != nil {
			return status.Error(codes.Internal, "failed to load invite")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListInvites returns the group's invite links, newest first. Requires
// manage_members.
func (db *groupDB) ListInvites(ctx context.Context, req *pb.ListInvitesReq) ([]*pb.GroupInvite, error) {
	groupID, _, err := authorizeGroup(ctx, db.db, req.Slug, req.AccountId, permManageMembers)
	if err != nil {
		return nil, err
	}

	rows, err := db.db.QueryContext(ctx,
		inviteSelect+" WHERE i.group_id = $1 ORDER BY i.created_at DESC", groupID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list invites")
	}
	defer rows.Close()

	var invites []*pb.GroupInvite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to scan invite")
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

// RevokeInvite disables an invite link. Requires manage_members.
func (db *groupDB) RevokeInvite(ctx context.Context, req *pb.RevokeInviteReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		groupID, _, err := authorizeGroup(ctx, tx, req.Slug, req.AccountId, permManageMembers)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE group_invites SET revoked = TRUE WHERE id = $1 AND group_id = $2`,
			req.InviteId, groupID)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to revoke invite: %v", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return status.Error(codes.NotFound, "invite not found")
		}
		return nil
	})
}

// GetInvite resolves an invite by token for the public accept page. It never
// requires authentication; the token is the capability.
func (db *groupDB) GetInvite(ctx context.Context, req *pb.GetInviteReq) (*pb.GroupInvite, error) {
	inv, err := scanInvite(db.db.QueryRowContext(ctx, inviteSelect+" WHERE i.token = $1", req.Token))
	if err == sql.ErrNoRows {
		return nil, status.Error(codes.NotFound, "invite not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load invite")
	}
	return inv, nil
}

// AcceptInvite admits the caller to the group using a valid invite token. It
// consumes one use and enrolls the caller as an active member with the invite's
// role. Expired, revoked or exhausted links are refused, as are banned callers.
func (db *groupDB) AcceptInvite(ctx context.Context, req *pb.AcceptInviteReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		actorID, err := resolveAccount(ctx, tx, req.AccountId)
		if err != nil {
			return err
		}

		var (
			inviteID   int64
			groupID    int64
			role       string
			maxUses    int32
			uses       int32
			expiresAt  sql.NullTime
			revoked    bool
			groupState string
		)
		err = tx.QueryRowContext(ctx, `
			SELECT i.id, i.group_id, i.role, i.max_uses, i.uses, i.expires_at, i.revoked, g.status
			FROM group_invites i JOIN groups g ON g.id = i.group_id
			WHERE i.token = $1 FOR UPDATE OF i`, req.Token).
			Scan(&inviteID, &groupID, &role, &maxUses, &uses, &expiresAt, &revoked, &groupState)
		if err == sql.ErrNoRows {
			return status.Error(codes.NotFound, "invite not found")
		}
		if err != nil {
			return status.Error(codes.Internal, "failed to load invite")
		}
		if !inviteIsActive(revoked, expiresAt, uses, maxUses) {
			return status.Error(codes.FailedPrecondition, "this invite link is no longer valid")
		}
		if groupState != "published" {
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

		enrolledRole := normalizeRole(role)
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, user_id, role, status)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (group_id, user_id)
			DO UPDATE SET status = EXCLUDED.status, role = EXCLUDED.role,
				joined_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP`,
			groupID, actorID, enrolledRole, memberStatusActive); err != nil {
			return status.Errorf(codes.Internal, "failed to join group: %v", err)
		}
		if err = grantGroupPermissions(ctx, tx, groupID, actorID, permissionsForRole(enrolledRole)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx,
			`UPDATE group_invites SET uses = uses + 1 WHERE id = $1`, inviteID); err != nil {
			return status.Errorf(codes.Internal, "failed to record invite use: %v", err)
		}
		// Clear any pending request so the applicant leaves the review queue.
		if _, err = tx.ExecContext(ctx, `
			UPDATE group_join_requests SET status = 'approved', decided_at = CURRENT_TIMESTAMP
			WHERE group_id = $1 AND user_id = $2 AND status = 'pending'`,
			groupID, actorID); err != nil {
			return status.Errorf(codes.Internal, "failed to resolve join request: %v", err)
		}
		if cur != memberStatusActive {
			return adjustMemberCount(ctx, tx, groupID, 1)
		}
		return nil
	})
}
