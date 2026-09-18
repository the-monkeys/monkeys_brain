package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// JobDetail is the fully-hydrated view a worker needs to execute a mock
// publish: enough of the rendition/post/account to build a provider payload,
// without exposing unrelated columns.
type JobDetail struct {
	JobID, PostID, RenditionID, SocialAccountID, Platform string
	OwnerUserID                                           int64
	AttemptCount, MaxAttempts                             int
	Text                                                  string
}

// MarkPublishing transitions a claimed job's rendition (and, on first claim,
// its post) into the publishing state and returns everything the worker
// needs to perform the mock publish call.
func MarkPublishing(ctx context.Context, db *sql.DB, jobID string) (*JobDetail, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin mark publishing tx: %w", err)
	}
	defer tx.Rollback()

	d := &JobDetail{JobID: jobID}
	err = tx.QueryRowContext(ctx, `
		SELECT j.post_id::text, j.rendition_id::text, r.social_account_id::text, r.platform,
		       p.owner_user_id, j.attempt_count, j.max_attempts, COALESCE(NULLIF(r.text_override, ''), p.base_text)
		FROM social_publish_jobs j
		JOIN social_post_renditions r ON r.id = j.rendition_id
		JOIN social_posts p ON p.id = j.post_id
		WHERE j.id = $1::uuid FOR UPDATE OF r, p`, jobID).Scan(
		&d.PostID, &d.RenditionID, &d.SocialAccountID, &d.Platform, &d.OwnerUserID, &d.AttemptCount, &d.MaxAttempts, &d.Text)
	if err != nil {
		return nil, fmt.Errorf("load job detail for publishing: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE social_post_renditions SET state = 'publishing', updated_at = NOW() WHERE id = $1::uuid AND state <> 'published'`,
		d.RenditionID); err != nil {
		return nil, fmt.Errorf("mark rendition publishing: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE social_posts SET state = 'publishing', updated_at = NOW() WHERE id = $1::uuid AND state = 'scheduled'`,
		d.PostID); err != nil {
		return nil, fmt.Errorf("mark post publishing: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit mark publishing tx: %w", err)
	}
	return d, nil
}

// CompleteJobSuccess records a successful mock-publish attempt and recomputes
// the aggregate post state (see recomputePostState).
func CompleteJobSuccess(ctx context.Context, db *sql.DB, jobID, renditionID, socialAccountID, postID, workerID string,
	attemptNumber int, providerRef string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin complete job success tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO social_publish_attempts (job_id, rendition_id, social_account_id, attempt_number, worker_id,
		    finished_at, outcome, retryable, provider_request_id, provider_result)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, NOW(), 'succeeded', false, $6, '{}'::jsonb)`,
		jobID, renditionID, socialAccountID, attemptNumber, workerID, providerRef); err != nil {
		return fmt.Errorf("record successful publish attempt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE social_publish_jobs SET status = 'succeeded', attempt_count = attempt_count + 1,
		    completed_at = NOW(), lease_owner = NULL, lease_expires_at = NULL, updated_at = NOW()
		WHERE id = $1::uuid`, jobID); err != nil {
		return fmt.Errorf("mark publish job succeeded: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE social_post_renditions SET state = 'published', provider_post_ref = $1, published_at = NOW(),
		    last_error_code = NULL, last_error_message = NULL, updated_at = NOW()
		WHERE id = $2::uuid`, providerRef, renditionID); err != nil {
		return fmt.Errorf("mark rendition published: %w", err)
	}
	if err := RecordEvent(ctx, tx, postID, renditionID, jobID, "worker", 0, "rendition.published", "", "", ""); err != nil {
		return err
	}
	if err := recomputePostState(ctx, tx, postID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit complete job success tx: %w", err)
	}
	return nil
}

// CompleteJobFailure records a failed mock-publish attempt. If attempts are
// exhausted the job is marked dead and the rendition failed; otherwise the
// job is retried after an exponential backoff, capped at 15 minutes.
func CompleteJobFailure(ctx context.Context, db *sql.DB, jobID, renditionID, socialAccountID, postID, workerID string,
	attemptNumber, maxAttempts int, errCode, errMessage string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin complete job failure tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO social_publish_attempts (job_id, rendition_id, social_account_id, attempt_number, worker_id,
		    finished_at, outcome, retryable, error_code, error_message)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, NOW(), 'failed', $6, $7, $8)`,
		jobID, renditionID, socialAccountID, attemptNumber, workerID, attemptNumber < maxAttempts, errCode, errMessage); err != nil {
		return fmt.Errorf("record failed publish attempt: %w", err)
	}

	exhausted := attemptNumber >= maxAttempts
	if exhausted {
		if _, err := tx.ExecContext(ctx, `
			UPDATE social_publish_jobs SET status = 'dead', attempt_count = $1, last_error_code = $2,
			    last_error_message = $3, lease_owner = NULL, lease_expires_at = NULL, updated_at = NOW()
			WHERE id = $4::uuid`, attemptNumber, errCode, errMessage, jobID); err != nil {
			return fmt.Errorf("mark publish job dead: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE social_post_renditions SET state = 'failed', last_error_code = $1, last_error_message = $2, updated_at = NOW()
			WHERE id = $3::uuid`, errCode, errMessage, renditionID); err != nil {
			return fmt.Errorf("mark rendition failed: %w", err)
		}
		if err := RecordEvent(ctx, tx, postID, renditionID, jobID, "worker", 0, "rendition.failed", "", "", ""); err != nil {
			return err
		}
	} else {
		backoff := time.Duration(attemptNumber) * time.Duration(attemptNumber) * 30 * time.Second
		if backoff > 15*time.Minute {
			backoff = 15 * time.Minute
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE social_publish_jobs SET status = 'retry_wait', attempt_count = $1, run_at = NOW() + $2::interval,
			    last_error_code = $3, last_error_message = $4, lease_owner = NULL, lease_expires_at = NULL, updated_at = NOW()
			WHERE id = $5::uuid`, attemptNumber, backoff.String(), errCode, errMessage, jobID); err != nil {
			return fmt.Errorf("schedule publish job retry: %w", err)
		}
		if err := RecordEvent(ctx, tx, postID, renditionID, jobID, "worker", 0, "job.retry_scheduled", "", "", ""); err != nil {
			return err
		}
	}

	if err := recomputePostState(ctx, tx, postID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit complete job failure tx: %w", err)
	}
	return nil
}

// recomputePostState derives the post's aggregate state from its renditions'
// terminal/non-terminal states, per the approved state machine: any active
// rendition keeps the post in-flight; all-published succeeds; all-failed
// fails; a mix of published and failed (with nothing left active) reports
// published_with_errors so partial success is never silently hidden.
func recomputePostState(ctx context.Context, tx *sql.Tx, postID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT state FROM social_post_renditions WHERE post_id = $1::uuid`, postID)
	if err != nil {
		return fmt.Errorf("load rendition states for post recompute: %w", err)
	}
	var total, published, failed, active int
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			rows.Close()
			return fmt.Errorf("scan rendition state for post recompute: %w", err)
		}
		total++
		switch state {
		case "published":
			published++
		case "failed":
			failed++
		case "scheduled", "publishing":
			active++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if total == 0 || active > 0 {
		return nil
	}

	var newState string
	switch {
	case published == total:
		newState = "published"
	case failed == total:
		newState = "failed"
	default:
		newState = "published_with_errors"
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE social_posts SET state = $1, updated_at = NOW(),
		    published_at = CASE WHEN $1 IN ('published', 'published_with_errors') THEN NOW() ELSE published_at END
		WHERE id = $2::uuid AND state NOT IN ('published', 'failed', 'published_with_errors')`, newState, postID)
	if err != nil {
		return fmt.Errorf("apply recomputed post state: %w", err)
	}
	return nil
}
