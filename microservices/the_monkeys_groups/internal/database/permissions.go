package database

import (
	"context"
	"database/sql"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Group permission types (mirror group_permissions.permission_type).
const (
	permEditGroup          = "edit_group"
	permManageMembers      = "manage_members"
	permManageEvents       = "manage_events"
	permManageDiscussions  = "manage_discussions"
	permManageDues         = "manage_dues"
	permManageRoles        = "manage_roles"
	permViewGroupAnalytics = "view_group_analytics"
	permDeleteGroup        = "delete_group"
)

// Group roles (mirror group_members.role CHECK).
const (
	roleOrganizer   = "organizer"
	roleCoOrganizer = "co_organizer"
	roleModerator   = "moderator"
	roleMember      = "member"
)

// Group member status (mirror group_members.status CHECK).
const (
	memberStatusActive  = "active"
	memberStatusPending = "pending"
	memberStatusLeft    = "left"
	memberStatusRemoved = "removed"
	memberStatusBanned  = "banned"
)

// organizerPermissions is the full rights bundle. The organizer holds it
// implicitly; co-organizers are granted the same explicit rows on promotion.
var organizerPermissions = []string{
	permEditGroup, permManageMembers, permManageEvents, permManageDiscussions,
	permManageDues, permManageRoles, permViewGroupAnalytics, permDeleteGroup,
}

// moderatorPermissions is the reduced bundle for moderators: they police
// members and discussions but cannot touch money, roles or group settings.
var moderatorPermissions = []string{
	permManageMembers, permManageDiscussions,
}

// permissionsForRole returns the explicit grant bundle for a role. Members get
// nothing; the organizer's rights are implicit and never written as rows.
func permissionsForRole(role string) []string {
	switch role {
	case roleCoOrganizer:
		return organizerPermissions
	case roleModerator:
		return moderatorPermissions
	default:
		return nil
	}
}

// grantGroupPermissions replaces a user's explicit permission rows with the
// bundle for the given role, on the supplied querier so it can join a
// transaction. Passing an empty bundle simply clears the rows.
func grantGroupPermissions(ctx context.Context, q querier, groupID, userID int64, perms []string) error {
	if _, err := q.ExecContext(ctx,
		`DELETE FROM group_permissions WHERE group_id = $1 AND user_id = $2`,
		groupID, userID); err != nil {
		return status.Error(codes.Internal, "failed to reset group permissions")
	}
	for _, p := range perms {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO group_permissions (group_id, user_id, permission_type)
			 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			groupID, userID, p); err != nil {
			return status.Errorf(codes.Internal, "failed to grant permission %s: %v", p, err)
		}
	}
	return nil
}

// authorizeGroup resolves the actor, loads the group and verifies the actor
// holds the permission. The organizer implicitly holds every permission. It
// returns the group id and the actor's numeric id so callers proceed directly.
func authorizeGroup(ctx context.Context, q querier, slug, accountID, perm string) (groupID, actorID int64, err error) {
	actorID, err = resolveAccount(ctx, q, accountID)
	if err != nil {
		return 0, 0, err
	}
	groupID, organizerID, err := resolveGroup(ctx, q, slug)
	if err != nil {
		return 0, 0, err
	}
	if organizerID == actorID {
		return groupID, actorID, nil
	}

	var n int
	if err = q.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM group_permissions
		 WHERE group_id = $1 AND user_id = $2 AND permission_type = $3`,
		groupID, actorID, perm).Scan(&n); err != nil {
		return 0, 0, status.Error(codes.Internal, "failed to check permission")
	}
	if n == 0 {
		return 0, 0, status.Errorf(codes.PermissionDenied, "requires %s permission", perm)
	}
	return groupID, actorID, nil
}

// authorizeGroupQuery answers every question the gateway asks about a caller's
// standing on a group in one round trip. It collapses to nothing useful for an
// anonymous caller: the viewer CTE is empty, so the organizer comparison is
// NULL (coalesced to false) and every lookup misses.
const authorizeGroupQuery = `
WITH viewer AS (
    SELECT id FROM user_account WHERE account_id = $2
)
SELECT
    g.status,
    g.visibility,
    COALESCE(g.organizer_id = (SELECT id FROM viewer), false),
    COALESCE((SELECT m.role   FROM group_members m
               WHERE m.group_id = g.id AND m.user_id = (SELECT id FROM viewer)), ''),
    COALESCE((SELECT m.status FROM group_members m
               WHERE m.group_id = g.id AND m.user_id = (SELECT id FROM viewer)), ''),
    array_to_string(ARRAY(
        SELECT p.permission_type FROM group_permissions p
         WHERE p.group_id = g.id AND p.user_id = (SELECT id FROM viewer)), ','),
    EXISTS (SELECT 1 FROM group_bans b
             WHERE b.group_id = g.id AND b.user_id = (SELECT id FROM viewer))
FROM groups g
WHERE g.slug = $1`

// AuthorizeGroup resolves the caller's role, membership and grants on a group.
//
// This backs the gateway's fast-reject layer. It is advisory only: the write
// paths still call authorizeGroup() inside their transaction, so a caller who
// slips past a stale gateway grant is refused at the row.
func (db *groupDB) AuthorizeGroup(ctx context.Context, req *pb.AuthorizeGroupReq) (*pb.AuthorizeGroupResp, error) {
	var (
		res          pb.AuthorizeGroupResp
		isOrganizer  bool
		memberRole   string
		memberStatus string
		granted      string
		bannedRow    bool
	)

	err := db.db.QueryRowContext(ctx, authorizeGroupQuery, req.GroupSlug, req.AccountId).
		Scan(&res.GroupStatus, &res.GroupVisibility, &isOrganizer,
			&memberRole, &memberStatus, &granted, &bannedRow)
	if err == sql.ErrNoRows {
		// GroupExists stays false; the gateway turns this into a 404.
		return &res, nil
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to authorize group: %v", err)
	}

	res.GroupExists = true
	res.MemberStatus = memberStatus
	res.IsBanned = bannedRow || memberStatus == memberStatusBanned
	res.IsMember = memberStatus == memberStatusActive

	if granted != "" {
		res.Permissions = strings.Split(granted, ",")
	}

	switch {
	case isOrganizer:
		res.Role = roleOrganizer
		// The organizer holds every right implicitly and has no rows in
		// group_permissions, so spell the bundle out for the gateway.
		res.Permissions = append([]string(nil), organizerPermissions...)
	case res.IsMember && memberRole != "":
		res.Role = memberRole
	default:
		res.Role = ""
	}

	return &res, nil
}
