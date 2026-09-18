package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Job is the minimum worker projection. Provider payloads are loaded only after
// a lease is obtained, keeping claim scans inexpensive.
type Job struct {
	ID          string
	PostID      string
	RenditionID string
	Attempts    int
}

// ClaimDueJobs leases due work atomically. SKIP LOCKED makes multiple service
// replicas safe without serializing unrelated publisher work.
func ClaimDueJobs(ctx context.Context, db *sql.DB, workerID string, limit int, lease time.Duration) ([]Job, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("job claim limit must be between 1 and 100")
	}
	if workerID == "" || lease <= 0 {
		return nil, fmt.Errorf("worker id and positive lease are required")
	}

	rows, err := db.QueryContext(ctx, `
		WITH due AS (
			SELECT id FROM social_publish_jobs
			WHERE (status IN ('ready', 'retry_wait') AND run_at <= NOW())
			   OR (status = 'leased' AND lease_expires_at < NOW())
			ORDER BY run_at, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE social_publish_jobs j
		SET status = 'leased', lease_owner = $2,
		    lease_expires_at = NOW() + $3::interval, updated_at = NOW()
		FROM due
		WHERE j.id = due.id
		RETURNING j.id::text, j.post_id::text, j.rendition_id::text, j.attempt_count`,
		limit, workerID, lease.String())
	if err != nil {
		return nil, fmt.Errorf("claim due social publish jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]Job, 0, limit)
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.PostID, &job.RenditionID, &job.Attempts); err != nil {
			return nil, fmt.Errorf("scan claimed social publish job: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed social publish jobs: %w", err)
	}
	return jobs, nil
}

// RenewLease succeeds only for its owning live worker; a stale worker can never
// reclaim a job after another replica has recovered it.
func RenewLease(ctx context.Context, db *sql.DB, jobID, workerID string, lease time.Duration) (bool, error) {
	result, err := db.ExecContext(ctx, `
		UPDATE social_publish_jobs
		SET lease_expires_at = NOW() + $3::interval, updated_at = NOW()
		WHERE id = $1::uuid AND status = 'leased' AND lease_owner = $2
		  AND lease_expires_at >= NOW()`, jobID, workerID, lease.String())
	if err != nil {
		return false, fmt.Errorf("renew social publish job lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read renewed social publish job lease: %w", err)
	}
	return affected == 1, nil
}
