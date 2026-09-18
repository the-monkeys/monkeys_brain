package database

import (
	"context"
	"database/sql"
	"fmt"
)

type JobStatusRow struct {
	ID, PostID, RenditionID, Status string
	AttemptCount                    int
	RunAt                           string
	LastErrorCode, LastErrorMessage sql.NullString
}

// GetJobStatus returns a job's current status, scoped to userID via the
// owning post so a job id cannot be probed cross-tenant.
func GetJobStatus(ctx context.Context, db *sql.DB, userID int64, jobID string) (*JobStatusRow, error) {
	j := &JobStatusRow{}
	err := db.QueryRowContext(ctx, `
		SELECT j.id::text, j.post_id::text, j.rendition_id::text, j.status, j.attempt_count,
		       COALESCE(j.run_at::text, ''), j.last_error_code, j.last_error_message
		FROM social_publish_jobs j
		JOIN social_posts p ON p.id = j.post_id
		WHERE j.id = $1::uuid AND p.owner_user_id = $2`, jobID, userID).Scan(
		&j.ID, &j.PostID, &j.RenditionID, &j.Status, &j.AttemptCount, &j.RunAt, &j.LastErrorCode, &j.LastErrorMessage)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get social publish job status: %w", err)
	}
	return j, nil
}

// ReplayJob resets a dead (exhausted-retry) job so the worker picks it up
// again immediately. Only dead jobs may be replayed — succeeded jobs must
// never be re-run, and in-flight jobs are left to the worker's own lease
// lifecycle, guarding against double-publish.
func ReplayJob(ctx context.Context, db *sql.DB, userID int64, jobID string) (*JobStatusRow, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin replay job tx: %w", err)
	}
	defer tx.Rollback()

	var status, renditionID, postID string
	if err := tx.QueryRowContext(ctx, `
		SELECT j.status, j.rendition_id::text, j.post_id::text
		FROM social_publish_jobs j JOIN social_posts p ON p.id = j.post_id
		WHERE j.id = $1::uuid AND p.owner_user_id = $2 FOR UPDATE OF j`, jobID, userID).Scan(&status, &renditionID, &postID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock social publish job for replay: %w", err)
	}
	if status != "dead" {
		return nil, fmt.Errorf("job in status %q cannot be replayed", status)
	}

	j := &JobStatusRow{}
	if err := tx.QueryRowContext(ctx, `
		UPDATE social_publish_jobs SET status = 'ready', attempt_count = 0, run_at = NOW(),
		    lease_owner = NULL, lease_expires_at = NULL, last_error_code = NULL, last_error_message = NULL,
		    updated_at = NOW()
		WHERE id = $1::uuid
		RETURNING id::text, post_id::text, rendition_id::text, status, attempt_count,
		          COALESCE(run_at::text, ''), last_error_code, last_error_message`, jobID).Scan(
		&j.ID, &j.PostID, &j.RenditionID, &j.Status, &j.AttemptCount, &j.RunAt, &j.LastErrorCode, &j.LastErrorMessage); err != nil {
		return nil, fmt.Errorf("reset social publish job for replay: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE social_post_renditions SET state = 'scheduled', updated_at = NOW() WHERE id = $1::uuid`, renditionID); err != nil {
		return nil, fmt.Errorf("reset rendition state for replay: %w", err)
	}
	if err := RecordEvent(ctx, tx, postID, renditionID, jobID, "user", userID, "job.replayed", "", "", ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit replay job tx: %w", err)
	}
	return j, nil
}
