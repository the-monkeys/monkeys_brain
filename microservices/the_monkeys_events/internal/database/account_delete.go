package database

import (
	"context"
	"database/sql"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// accountOrganizedBlockingEventsSQL returns slugs that must block account
// delete: events this account organizes with captured/pending payments, and
// events they bought a ticket for that still have an event_payments row
// (attendee_id is ON DELETE RESTRICT — those RSVPs cannot be unlinked).
const accountOrganizedBlockingEventsSQL = `
SELECT e.slug
FROM events e
JOIN user_account ua ON ua.id = e.organizer_id
WHERE ua.account_id = $1
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
UNION
SELECT e.slug
FROM event_attendees a
JOIN events e ON e.id = a.event_id
JOIN user_account ua ON ua.id = a.user_id
WHERE ua.account_id = $1
  AND EXISTS (SELECT 1 FROM event_payments ep WHERE ep.attendee_id = a.id)`

const unlinkCoHostsSQL = `DELETE FROM event_co_hosts WHERE co_host_id = $1`
const unlinkEventPermsSQL = `DELETE FROM event_permissions WHERE user_id = $1`
const unlinkEventCommentsSQL = `DELETE FROM event_comments WHERE user_id = $1`
const unlinkEventReactionsSQL = `DELETE FROM event_reactions WHERE user_id = $1`
const unlinkSavedEventsSQL = `DELETE FROM saved_events WHERE user_id = $1`
const unlinkUnpaidAttendeesSQL = `
DELETE FROM event_attendees a
WHERE a.user_id = $1
  AND NOT EXISTS (SELECT 1 FROM event_payments ep WHERE ep.attendee_id = a.id)`

func (db *eventDB) CheckUserEventRemoval(ctx context.Context, accountID string) ([]string, error) {
	if accountID == "" {
		return nil, status.Error(codes.InvalidArgument, "account id is required")
	}
	rows, err := db.db.QueryContext(ctx, accountOrganizedBlockingEventsSQL, accountID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to check organized events")
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, status.Error(codes.Internal, "failed to scan blocking event")
		}
		slugs = append(slugs, slug)
	}
	return slugs, rows.Err()
}

func (db *eventDB) RemoveUserFromEvents(ctx context.Context, accountID string) ([]string, error) {
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
		rows, err := tx.QueryContext(ctx, `SELECT id, slug FROM events WHERE organizer_id = $1`, userID)
		if err != nil {
			return status.Error(codes.Internal, "failed to list organized events")
		}
		type owned struct {
			id   int64
			slug string
		}
		var events []owned
		for rows.Next() {
			var e owned
			if err := rows.Scan(&e.id, &e.slug); err != nil {
				rows.Close()
				return status.Error(codes.Internal, "failed to scan organized event")
			}
			events = append(events, e)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, e := range events {
			if err := refuseIfPaidEvent(ctx, tx, e.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id = $1`, e.id); err != nil {
				return status.Errorf(codes.Internal, "failed to delete event %s: %v", e.slug, err)
			}
			deleted = append(deleted, e.slug)
		}
		for _, q := range []string{
			unlinkCoHostsSQL,
			unlinkEventPermsSQL,
			unlinkEventCommentsSQL,
			unlinkEventReactionsSQL,
			unlinkSavedEventsSQL,
			unlinkUnpaidAttendeesSQL,
		} {
			if _, err := tx.ExecContext(ctx, q, userID); err != nil {
				return status.Errorf(codes.Internal, "failed to unlink account from events: %v", err)
			}
		}
		var leftover int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM event_attendees a
			JOIN event_payments ep ON ep.attendee_id = a.id
			WHERE a.user_id = $1`, userID).Scan(&leftover); err != nil {
			return status.Error(codes.Internal, "failed to verify leftover payments")
		}
		if leftover > 0 {
			return status.Error(codes.FailedPrecondition,
				"account has captured or pending ticket purchases; cancel or settle them first")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}
