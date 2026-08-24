package database

import (
	"context"
	"time"
)

// Reminder identifies one attendee to nudge about an upcoming event.
type Reminder struct {
	Slug     string
	Title    string
	Offset   string // '24h' or '1h'
	Username string
}

// ClaimDueReminders atomically claims the reminder slot for every event
// starting between earliest and latest from now, returning the attendees to
// notify. The insert into event_reminders_sent is the claim, so concurrent
// service replicas each send at most once. The window has a lower bound so an
// event created shortly before it starts does not fire both reminders at once.
func (db *eventDB) ClaimDueReminders(ctx context.Context, offset string, earliest, latest time.Duration) ([]Reminder, error) {
	rows, err := db.db.QueryContext(ctx, `
		WITH due AS (
			SELECT id, slug, title FROM events
			WHERE status IN ('published', 'live')
			  AND start_time > NOW() + make_interval(mins => $2)
			  AND start_time <= NOW() + make_interval(mins => $3)
		), claimed AS (
			INSERT INTO event_reminders_sent (event_id, reminder)
			SELECT id, $1 FROM due
			ON CONFLICT DO NOTHING
			RETURNING event_id
		)
		SELECT d.slug, d.title, u.username
		FROM claimed c
		JOIN due d ON d.id = c.event_id
		JOIN event_attendees a ON a.event_id = c.event_id AND a.status = 'confirmed'
		JOIN user_account u ON u.id = a.user_id`,
		offset, int(earliest.Minutes()), int(latest.Minutes()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Reminder
	for rows.Next() {
		r := Reminder{Offset: offset}
		if err := rows.Scan(&r.Slug, &r.Title, &r.Username); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ArchivePastEvents moves finished events out of the active listings.
func (db *eventDB) ArchivePastEvents(ctx context.Context) (int64, error) {
	res, err := db.db.ExecContext(ctx, `
		UPDATE events SET status = 'completed', updated_at = NOW()
		WHERE status IN ('published', 'live') AND end_time < NOW()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
