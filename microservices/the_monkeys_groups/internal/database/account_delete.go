package database

import (
	"context"
	"database/sql"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const accountOrganizedBlockingGroupsSQL = `
SELECT g.slug
FROM groups g
JOIN user_account ua ON ua.id = g.organizer_id
WHERE ua.account_id = $1
  AND EXISTS (
    SELECT 1 FROM events e
    WHERE e.group_id = g.id
      AND (
          EXISTS (SELECT 1 FROM event_payments ep WHERE ep.event_id = e.id)
          OR EXISTS (
              SELECT 1 FROM event_attendees a
              WHERE a.event_id = e.id AND (
                  a.status = 'pending_payment'
                  OR (a.status = 'confirmed' AND (
                      a.payment_id IS NOT NULL
                      OR COALESCE(a.amount_captured_paise, 0) > 0
                      OR COALESCE(a.amount_paid, 0) > 0
                  ))
              )
          )
      )
  )`

const unlinkGroupMembersSQL = `DELETE FROM group_members WHERE user_id = $1`
const unlinkGroupJoinRequestsSQL = `DELETE FROM group_join_requests WHERE user_id = $1`
const unlinkGroupPermsSQL = `DELETE FROM group_permissions WHERE user_id = $1`
const unlinkSavedGroupsSQL = `DELETE FROM saved_groups WHERE user_id = $1`

func (db *groupDB) CheckUserGroupRemoval(ctx context.Context, accountID string) ([]string, error) {
	if accountID == "" {
		return nil, status.Error(codes.InvalidArgument, "account id is required")
	}
	rows, err := db.db.QueryContext(ctx, accountOrganizedBlockingGroupsSQL, accountID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to check organized groups")
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, status.Error(codes.Internal, "failed to scan blocking group")
		}
		slugs = append(slugs, slug)
	}
	return slugs, rows.Err()
}

func (db *groupDB) RemoveUserFromGroups(ctx context.Context, accountID string) ([]string, error) {
	if accountID == "" {
		return nil, status.Error(codes.InvalidArgument, "account id is required")
	}
	var deleted []string
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		userID, err := resolveAccount(ctx, tx, accountID)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return nil
			}
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, slug FROM groups WHERE organizer_id = $1`, userID)
		if err != nil {
			return status.Error(codes.Internal, "failed to list organized groups")
		}
		type owned struct {
			id   int64
			slug string
		}
		var groups []owned
		for rows.Next() {
			var g owned
			if err := rows.Scan(&g.id, &g.slug); err != nil {
				rows.Close()
				return status.Error(codes.Internal, "failed to scan organized group")
			}
			groups = append(groups, g)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, g := range groups {
			if err := refuseIfGroupHasPaidEvents(ctx, tx, g.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, g.id); err != nil {
				return status.Errorf(codes.Internal, "failed to delete group %s: %v", g.slug, err)
			}
			deleted = append(deleted, g.slug)
		}
		for _, q := range []string{
			unlinkGroupMembersSQL,
			unlinkGroupJoinRequestsSQL,
			unlinkGroupPermsSQL,
			unlinkSavedGroupsSQL,
		} {
			if _, err := tx.ExecContext(ctx, q, userID); err != nil {
				return status.Errorf(codes.Internal, "failed to unlink account from groups: %v", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}
