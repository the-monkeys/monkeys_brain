package database

import (
	"context"
	"database/sql"
	"fmt"
)

// schedulableStates lists post states from which (re)scheduling is allowed.
// Terminal-success posts are excluded so a fully published post cannot be
// silently re-queued by a stale client retry.
var schedulableStates = map[string]bool{"draft": true, "scheduled": true, "failed": true, "published_with_errors": true}

// enqueueJobForRendition inserts a durable job for one rendition version. It
// is idempotent: re-scheduling the same rendition version is a no-op thanks
// to the dedupe_key unique constraint, and the partial unique index on
// (rendition_id, rendition_version) guarantees only one active job exists
// per rendition version at a time.
func enqueueJobForRendition(ctx context.Context, tx *sql.Tx, postID, renditionID string, renditionVersion int64, runAt string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO social_publish_jobs (post_id, rendition_id, rendition_version, dedupe_key, run_at, provider_idempotency_key)
		VALUES ($1::uuid, $2::uuid, $3::bigint, $2::text || ':' || $3::text,
		        CASE WHEN $4 = 'NOW()' THEN NOW() ELSE NULLIF($4, '')::timestamptz END,
		        gen_random_uuid()::text)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		postID, renditionID, renditionVersion, runAt)
	if err != nil {
		return fmt.Errorf("enqueue social publish job: %w", err)
	}
	return nil
}

// cancelActiveJobsForPost cancels every not-yet-leased job belonging to the
// post's renditions. Leased (in-flight) jobs are intentionally left alone —
// cancelling a job a worker already owns would race the worker's own
// transition and is out of scope for the mock publisher.
func cancelActiveJobsForPost(ctx context.Context, tx *sql.Tx, postID string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE social_publish_jobs SET status = 'cancelled', cancelled_at = NOW(), updated_at = NOW()
		WHERE post_id = $1::uuid AND status IN ('ready', 'retry_wait')`, postID)
	if err != nil {
		return fmt.Errorf("cancel active social publish jobs: %w", err)
	}
	return nil
}

// SchedulePost transitions a draft/failed post into scheduled state and
// enqueues one durable job per rendition (each rendition may run at its own
// override time, else it inherits the post-level schedule).
func SchedulePost(ctx context.Context, db *sql.DB, userID int64, postID, scheduledAt, timezone string, expectedVersion int64) (*Post, error) {
	return runScheduleTx(ctx, db, userID, postID, scheduledAt, timezone, expectedVersion, "scheduled")
}

// ReschedulePost moves an already-scheduled (or previously failed) post to a
// new time, cancelling any not-yet-leased jobs and enqueueing fresh ones.
func ReschedulePost(ctx context.Context, db *sql.DB, userID int64, postID, scheduledAt, timezone string, expectedVersion int64) (*Post, error) {
	return runScheduleTx(ctx, db, userID, postID, scheduledAt, timezone, expectedVersion, "scheduled")
}

// PublishNow enqueues every rendition's job to run immediately and marks the
// post as publishing right away instead of waiting for a scheduled time.
func PublishNow(ctx context.Context, db *sql.DB, userID int64, postID string, expectedVersion int64) (*Post, error) {
	return runScheduleTx(ctx, db, userID, postID, "now", "UTC", expectedVersion, "publishing")
}

func runScheduleTx(ctx context.Context, db *sql.DB, userID int64, postID, scheduledAt, timezone string, expectedVersion int64, targetState string) (*Post, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin schedule tx: %w", err)
	}
	defer tx.Rollback()

	var currentState string
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `
		SELECT state, version FROM social_posts
		WHERE id = $1::uuid AND owner_user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
		postID, userID).Scan(&currentState, &currentVersion); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock social post for schedule: %w", err)
	}
	if expectedVersion != 0 && currentVersion != expectedVersion {
		return nil, ErrVersionConflict
	}
	if !schedulableStates[currentState] {
		return nil, fmt.Errorf("social post in state %q cannot be scheduled", currentState)
	}

	runAtExpr := "NOW()"
	if scheduledAt != "" && scheduledAt != "now" {
		runAtExpr = scheduledAt
	}

	// A post entering the "scheduled" state joins the manual publishing
	// queue automatically (if it isn't already queued), otherwise the Queue
	// view would never show posts scheduled from the Composer — the queue
	// column was previously only ever populated by explicit reordering.
	// PublishNow's targetState is "publishing", not "scheduled", so it never
	// touches queue_position here; the decision is made in Go (not SQL) to
	// avoid ambiguous parameter typing from a runtime state comparison.
	queuePositionFragment := func(userIDParamIdx int) string {
		if targetState != "scheduled" {
			return ""
		}
		return fmt.Sprintf(`,
		    queue_position = COALESCE(queue_position,
		        (SELECT COALESCE(MAX(queue_position), 0) + 1 FROM social_posts WHERE owner_user_id = $%d))`,
			userIDParamIdx)
	}
	queueArgs := func() []interface{} {
		if targetState != "scheduled" {
			return nil
		}
		return []interface{}{userID}
	}

	p := &Post{}
	if scheduledAt == "now" {
		args := append([]interface{}{targetState, timezone, postID}, queueArgs()...)
		err = tx.QueryRowContext(ctx, fmt.Sprintf(`
			UPDATE social_posts SET state = $1, version = version + 1, updated_at = NOW(),
			    scheduled_at = NOW(), schedule_timezone = $2%s
			WHERE id = $3::uuid
			RETURNING id::text, base_text, state, version, scheduled_at::text, COALESCE(schedule_timezone, ''),
			          queue_position, last_error_code, last_error_message, created_at::text, updated_at::text`,
			queuePositionFragment(4)),
			args...).Scan(&p.ID, &p.BaseText, &p.State, &p.Version, &p.ScheduledAt,
			&p.ScheduleTimezone, &p.QueuePosition, &p.LastErrorCode, &p.LastErrorMessage, &p.CreatedAt, &p.UpdatedAt)
	} else {
		args := append([]interface{}{targetState, scheduledAt, timezone, postID}, queueArgs()...)
		err = tx.QueryRowContext(ctx, fmt.Sprintf(`
			UPDATE social_posts SET state = $1, version = version + 1, updated_at = NOW(),
			    scheduled_at = $2::timestamptz, schedule_timezone = $3%s
			WHERE id = $4::uuid
			RETURNING id::text, base_text, state, version, scheduled_at::text, COALESCE(schedule_timezone, ''),
			          queue_position, last_error_code, last_error_message, created_at::text, updated_at::text`,
			queuePositionFragment(5)),
			args...).Scan(&p.ID, &p.BaseText, &p.State, &p.Version, &p.ScheduledAt,
			&p.ScheduleTimezone, &p.QueuePosition, &p.LastErrorCode, &p.LastErrorMessage, &p.CreatedAt, &p.UpdatedAt)
	}
	if err != nil {
		return nil, fmt.Errorf("update social post schedule: %w", err)
	}

	if err := cancelActiveJobsForPost(ctx, tx, postID); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id::text, version, scheduled_at_override::text
		FROM social_post_renditions WHERE post_id = $1::uuid AND state <> 'published'`, postID)
	if err != nil {
		return nil, fmt.Errorf("load renditions for schedule: %w", err)
	}
	type renditionRow struct {
		id       string
		version  int64
		override sql.NullString
	}
	var renditions []renditionRow
	for rows.Next() {
		var r renditionRow
		if err := rows.Scan(&r.id, &r.version, &r.override); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan rendition for schedule: %w", err)
		}
		renditions = append(renditions, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, r := range renditions {
		runAt := runAtExpr
		if r.override.Valid && r.override.String != "" {
			runAt = r.override.String
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE social_post_renditions SET state = $1, version = version + 1, updated_at = NOW() WHERE id = $2::uuid`,
			targetState, r.id); err != nil {
			return nil, fmt.Errorf("update rendition state for schedule: %w", err)
		}
		if err := enqueueJobForRendition(ctx, tx, postID, r.id, r.version+1, runAt); err != nil {
			return nil, err
		}
	}

	if err := RecordEvent(ctx, tx, postID, "", "", "user", userID, "post."+targetState, "", "", ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit schedule tx: %w", err)
	}
	return p, nil
}

// CancelSchedule reverts a scheduled (not yet publishing) post back to draft
// and cancels every not-yet-leased job for it.
func CancelSchedule(ctx context.Context, db *sql.DB, userID int64, postID string, expectedVersion int64) (*Post, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin cancel schedule tx: %w", err)
	}
	defer tx.Rollback()

	var currentState string
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `
		SELECT state, version FROM social_posts
		WHERE id = $1::uuid AND owner_user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
		postID, userID).Scan(&currentState, &currentVersion); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock social post for cancel: %w", err)
	}
	if expectedVersion != 0 && currentVersion != expectedVersion {
		return nil, ErrVersionConflict
	}
	if currentState != "scheduled" {
		return nil, fmt.Errorf("social post in state %q cannot be cancelled", currentState)
	}

	p := &Post{}
	if err := tx.QueryRowContext(ctx, `
		UPDATE social_posts SET state = 'draft', version = version + 1, updated_at = NOW(),
		    scheduled_at = NULL, schedule_timezone = NULL, queue_position = NULL
		WHERE id = $1::uuid
		RETURNING id::text, base_text, state, version, scheduled_at::text, COALESCE(schedule_timezone, ''),
		          queue_position, last_error_code, last_error_message, created_at::text, updated_at::text`,
		postID).Scan(&p.ID, &p.BaseText, &p.State, &p.Version, &p.ScheduledAt, &p.ScheduleTimezone,
		&p.QueuePosition, &p.LastErrorCode, &p.LastErrorMessage, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("revert social post to draft: %w", err)
	}
	if err := cancelActiveJobsForPost(ctx, tx, postID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE social_post_renditions SET state = 'draft', version = version + 1, updated_at = NOW()
		WHERE post_id = $1::uuid AND state <> 'published'`, postID); err != nil {
		return nil, fmt.Errorf("revert renditions to draft: %w", err)
	}
	if err := RecordEvent(ctx, tx, postID, "", "", "user", userID, "post.schedule_cancelled", "", "", ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit cancel schedule tx: %w", err)
	}
	return p, nil
}
