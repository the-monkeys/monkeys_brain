package database

import (
	"context"
	"database/sql"
	"fmt"
)

// UpsertRendition creates or updates the per-account override row for a post.
// Ownership of both the post and the destination social account is enforced
// in the same statement so cross-account/cross-user references are rejected
// server-side, not merely trusted from client input.
func UpsertRendition(ctx context.Context, db *sql.DB, userID int64, postID, socialAccountID, textOverride,
	scheduledAtOverride, scheduleTimezoneOverride string, expectedVersion int64) (*Rendition, error) {

	var platform string
	if err := db.QueryRowContext(ctx, `
		SELECT platform FROM social_accounts WHERE id = $1::uuid AND owner_user_id = $2`,
		socialAccountID, userID).Scan(&platform); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolve social account platform: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin upsert rendition tx: %w", err)
	}
	defer tx.Rollback()

	var postVersion int64
	var postState string
	if err := tx.QueryRowContext(ctx, `
		SELECT version, state FROM social_posts
		WHERE id = $1::uuid AND owner_user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
		postID, userID).Scan(&postVersion, &postState); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock social post for rendition upsert: %w", err)
	}
	if expectedVersion != 0 && postVersion != expectedVersion {
		return nil, ErrVersionConflict
	}

	r := &Rendition{}
	nullText := sql.NullString{String: textOverride, Valid: textOverride != ""}
	nullSchedAt := sql.NullString{String: scheduledAtOverride, Valid: scheduledAtOverride != ""}
	nullTZ := sql.NullString{String: scheduleTimezoneOverride, Valid: scheduleTimezoneOverride != ""}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO social_post_renditions (post_id, social_account_id, platform, text_override,
		    scheduled_at_override, schedule_timezone_override)
		VALUES ($1::uuid, $2::uuid, $3, $4, NULLIF($5, '')::timestamptz, NULLIF($6, ''))
		ON CONFLICT (post_id, social_account_id) DO UPDATE
		SET text_override = EXCLUDED.text_override,
		    scheduled_at_override = EXCLUDED.scheduled_at_override,
		    schedule_timezone_override = EXCLUDED.schedule_timezone_override,
		    version = social_post_renditions.version + 1,
		    updated_at = NOW()
		RETURNING id::text, social_account_id::text, platform, state, version,
		          text_override, scheduled_at_override::text, COALESCE(schedule_timezone_override, ''),
		          COALESCE(provider_post_ref, ''), last_error_code, last_error_message`,
		postID, socialAccountID, platform, nullText, nullSchedAt, nullTZ).Scan(
		&r.ID, &r.SocialAccountID, &r.Platform, &r.State, &r.Version,
		&r.TextOverride, &r.ScheduledAtOverride, &r.ScheduleTimezoneOverride,
		&r.ProviderPostRef, &r.LastErrorCode, &r.LastErrorMessage)
	if err != nil {
		return nil, fmt.Errorf("upsert social post rendition: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE social_posts SET updated_at = NOW() WHERE id = $1::uuid`, postID); err != nil {
		return nil, fmt.Errorf("touch social post on rendition upsert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit upsert rendition tx: %w", err)
	}
	return r, nil
}

// SetRenditionMedia replaces the ordered media list attached to a rendition.
// Every asset id must be owned by userID, preventing cross-tenant media reuse.
func SetRenditionMedia(ctx context.Context, db *sql.DB, userID int64, postID, socialAccountID string, assetIDs []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set rendition media tx: %w", err)
	}
	defer tx.Rollback()

	var renditionID string
	if err := tx.QueryRowContext(ctx, `
		SELECT r.id::text FROM social_post_renditions r
		JOIN social_posts p ON p.id = r.post_id
		WHERE r.post_id = $1::uuid AND r.social_account_id = $2::uuid AND p.owner_user_id = $3 AND p.deleted_at IS NULL`,
		postID, socialAccountID, userID).Scan(&renditionID); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("resolve rendition for media set: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM social_rendition_media WHERE rendition_id = $1::uuid`, renditionID); err != nil {
		return fmt.Errorf("clear rendition media: %w", err)
	}
	for i, assetID := range assetIDs {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO social_rendition_media (rendition_id, asset_id, position)
			SELECT $1::uuid, $2::uuid, $3
			WHERE EXISTS (SELECT 1 FROM social_media_assets WHERE id = $2::uuid AND owner_user_id = $4 AND deleted_at IS NULL)`,
			renditionID, assetID, i, userID)
		if err != nil {
			return fmt.Errorf("attach rendition media: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("read attach rendition media result: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf("social media asset %s not found or not owned", assetID)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE social_post_renditions SET version = version + 1, updated_at = NOW() WHERE id = $1::uuid`, renditionID); err != nil {
		return fmt.Errorf("bump rendition version on media set: %w", err)
	}
	return tx.Commit()
}
