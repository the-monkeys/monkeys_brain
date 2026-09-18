package database

import (
	"context"
	"database/sql"
	"fmt"
)

// RecordEvent appends an immutable timeline entry. Events are the audit trail
// consumed by ListPostHistory and are never updated or deleted.
func RecordEvent(ctx context.Context, exec sqlExecer, postID, renditionID, jobID, actorType string,
	actorUserID int64, eventType, correlationID, idempotencyKey, detailJSON string) error {
	if detailJSON == "" {
		detailJSON = "{}"
	}
	var actorUserIDArg interface{}
	if actorUserID > 0 {
		actorUserIDArg = actorUserID
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO social_post_events (post_id, rendition_id, job_id, actor_type, actor_user_id,
		    event_type, correlation_id, idempotency_key, detail)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9::jsonb)`,
		postID, renditionID, jobID, actorType, actorUserIDArg, eventType, correlationID, idempotencyKey, detailJSON)
	if err != nil {
		return fmt.Errorf("record social post event: %w", err)
	}
	return nil
}

// sqlExecer is satisfied by both *sql.DB and *sql.Tx, letting event recording
// participate in the caller's transaction when one is open.
type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

type HistoryEntry struct {
	ID, EventType, ActorType, CreatedAt, DetailJSON string
}

// ListHistory returns the immutable event timeline for a post owned by userID.
func ListHistory(ctx context.Context, db *sql.DB, userID int64, postID string, limit int) ([]*HistoryEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var owner int64
	if err := db.QueryRowContext(ctx, `SELECT owner_user_id FROM social_posts WHERE id = $1::uuid`, postID).Scan(&owner); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolve social post owner for history: %w", err)
	}
	if owner != userID {
		return nil, ErrNotFound
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id::text, event_type, actor_type, created_at::text, detail::text
		FROM social_post_events WHERE post_id = $1::uuid ORDER BY created_at DESC LIMIT $2`, postID, limit)
	if err != nil {
		return nil, fmt.Errorf("list social post history: %w", err)
	}
	defer rows.Close()
	entries := []*HistoryEntry{}
	for rows.Next() {
		e := &HistoryEntry{}
		if err := rows.Scan(&e.ID, &e.EventType, &e.ActorType, &e.CreatedAt, &e.DetailJSON); err != nil {
			return nil, fmt.Errorf("scan social post history entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
