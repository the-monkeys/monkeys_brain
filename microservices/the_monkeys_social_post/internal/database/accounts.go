package database

import (
	"context"
	"database/sql"
	"fmt"
)

type Account struct {
	ID, Platform, DisplayName, Handle, Status, AvatarURL string
	IsMock                                               bool
}

func GetAccountPlatform(ctx context.Context, db *sql.DB, userID int64, accountID string) (string, error) {
	var platform string
	err := db.QueryRowContext(ctx, `
		SELECT platform FROM social_accounts
		WHERE id = $1::uuid AND owner_user_id = $2 AND status = 'active'`,
		accountID, userID).Scan(&platform)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get social account platform: %w", err)
	}
	return platform, nil
}

// ListAccounts returns the caller's linked destination accounts that are not disconnected.
func ListAccounts(ctx context.Context, db *sql.DB, userID int64) ([]*Account, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id::text, platform, display_name, handle, status, COALESCE(avatar_url, ''), is_mock
		FROM social_accounts
		WHERE owner_user_id = $1 AND status != 'disconnected'
		ORDER BY platform ASC, created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list social accounts: %w", err)
	}
	defer rows.Close()
	accounts := []*Account{}
	for rows.Next() {
		a := &Account{}
		if err := rows.Scan(&a.ID, &a.Platform, &a.DisplayName, &a.Handle, &a.Status, &a.AvatarURL, &a.IsMock); err != nil {
			return nil, fmt.Errorf("scan social account: %w", err)
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// LinkAccount inserts or updates a destination account on conflict (owner_user_id, platform, external_account_ref).
func LinkAccount(
	ctx context.Context,
	db *sql.DB,
	userID int64,
	platform, handle, displayName, externalRef, avatarURL string,
	isMock bool,
	accessToken, refreshToken []byte,
) (*Account, error) {
	var tokenParam = func(b []byte) any {
		if len(b) == 0 {
			return nil
		}
		return b
	}

	a := &Account{}
	err := db.QueryRowContext(ctx, `
		INSERT INTO social_accounts (
			owner_user_id, platform, handle, display_name, external_account_ref,
			avatar_url, is_mock, status, encrypted_access_token, encrypted_refresh_token,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8, $9, NOW())
		ON CONFLICT (owner_user_id, platform, external_account_ref) DO UPDATE SET
			status = 'active',
			handle = EXCLUDED.handle,
			display_name = EXCLUDED.display_name,
			avatar_url = EXCLUDED.avatar_url,
			is_mock = EXCLUDED.is_mock,
			encrypted_access_token = EXCLUDED.encrypted_access_token,
			encrypted_refresh_token = EXCLUDED.encrypted_refresh_token,
			updated_at = NOW()
		RETURNING id::text, platform, display_name, handle, status, COALESCE(avatar_url, ''), is_mock`,
		userID, platform, handle, displayName, externalRef, avatarURL, isMock,
		tokenParam(accessToken), tokenParam(refreshToken),
	).Scan(&a.ID, &a.Platform, &a.DisplayName, &a.Handle, &a.Status, &a.AvatarURL, &a.IsMock)
	if err != nil {
		return nil, fmt.Errorf("link social account: %w", err)
	}
	return a, nil
}

// DisconnectAccount sets account status to 'disconnected', clears tokens, cancels pending jobs,
// and reverts scheduled posts to 'draft' in a single transaction.
func DisconnectAccount(ctx context.Context, db *sql.DB, userID int64, accountID string) (int64, int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin disconnect account tx: %w", err)
	}
	defer tx.Rollback()

	// 1. Check ownership and set status = 'disconnected', tokens = NULL.
	res, err := tx.ExecContext(ctx, `
		UPDATE social_accounts
		SET status = 'disconnected',
			encrypted_access_token = NULL,
			encrypted_refresh_token = NULL,
			updated_at = NOW()
		WHERE id = $1::uuid AND owner_user_id = $2`,
		accountID, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("disconnect social account: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("rows affected disconnect social account: %w", err)
	}
	if rowsAffected == 0 {
		return 0, 0, ErrNotFound
	}

	// 2. Identify scheduled posts that have a rendition on this account, and revert them to draft.
	rows, err := tx.QueryContext(ctx, `
		UPDATE social_posts
		SET state = 'draft', version = version + 1, updated_at = NOW(),
			scheduled_at = NULL, schedule_timezone = NULL, queue_position = NULL
		WHERE owner_user_id = $2
		  AND state = 'scheduled'
		  AND id IN (
			  SELECT DISTINCT post_id FROM social_post_renditions WHERE social_account_id = $1::uuid
		  )
		RETURNING id::text`,
		accountID, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("revert scheduled posts: %w", err)
	}
	defer rows.Close()

	var revertedPostIDs []string
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			return 0, 0, fmt.Errorf("scan reverted post id: %w", err)
		}
		revertedPostIDs = append(revertedPostIDs, pid)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate reverted post ids: %w", err)
	}
	draftsReverted := int64(len(revertedPostIDs))

	// 3. For all reverted multi-account posts, revert all other non-published renditions to draft as well.
	if len(revertedPostIDs) > 0 {
		_, err = tx.ExecContext(ctx, `
			UPDATE social_post_renditions
			SET state = 'draft', version = version + 1, updated_at = NOW()
			WHERE post_id = ANY($1) AND state <> 'published'`,
			revertedPostIDs)
		if err != nil {
			return 0, 0, fmt.Errorf("revert post renditions to draft: %w", err)
		}
	}

	// Also revert any remaining scheduled renditions directly belonging to this account.
	_, err = tx.ExecContext(ctx, `
		UPDATE social_post_renditions
		SET state = 'draft', version = version + 1, updated_at = NOW()
		WHERE social_account_id = $1::uuid AND state = 'scheduled'`,
		accountID)
	if err != nil {
		return 0, 0, fmt.Errorf("revert account renditions to draft: %w", err)
	}

	// 4. Cancel pending publish jobs (status IN ('ready', 'retry_wait')):
	// This cancels jobs directly tied to this account's renditions, plus any pending jobs on reverted posts.
	var resJobs sql.Result
	if len(revertedPostIDs) > 0 {
		resJobs, err = tx.ExecContext(ctx, `
			UPDATE social_publish_jobs
			SET status = 'cancelled', cancelled_at = NOW(), updated_at = NOW()
			WHERE (
				rendition_id IN (SELECT id FROM social_post_renditions WHERE social_account_id = $1::uuid)
				OR post_id = ANY($2)
			) AND status IN ('ready', 'retry_wait')`,
			accountID, revertedPostIDs)
	} else {
		resJobs, err = tx.ExecContext(ctx, `
			UPDATE social_publish_jobs
			SET status = 'cancelled', cancelled_at = NOW(), updated_at = NOW()
			WHERE rendition_id IN (
				SELECT id FROM social_post_renditions WHERE social_account_id = $1::uuid
			) AND status IN ('ready', 'retry_wait')`,
			accountID)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("cancel pending publish jobs: %w", err)
	}
	cancelledJobs, err := resJobs.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("rows affected cancel jobs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit disconnect account tx: %w", err)
	}
	return cancelledJobs, draftsReverted, nil
}
