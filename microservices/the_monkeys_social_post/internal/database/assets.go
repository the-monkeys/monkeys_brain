package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

type Asset struct {
	ID, ObjectKey, Checksum, ContentType, MediaKind, SourceKind string
	ByteSize                                                    int64
	Width, Height, DurationMs                                   sql.NullInt64
}

// ImportStudioAsset registers a caller-owned Studio/Snapshot asset reference as
// a social-media asset. The gateway is responsible for verifying ownership of
// sourceAssetRef against the blog/storage service *before* calling this RPC;
// this repository call additionally scopes every row to userID so a forged or
// stale reference can never be attributed to another user's library.
//
// NOTE: this records the import reference; it does not yet copy or
// re-transcode the underlying bytes into social-post-owned storage. A full
// byte-level copy pipeline requires wiring into the storage service and is
// intentionally deferred (see handoff notes) — recorded here as a documented,
// narrow gap rather than a silent one.
func ImportStudioAsset(ctx context.Context, db *sql.DB, userID int64, sourceAssetRef, sourceKind string) (*Asset, error) {
	if sourceAssetRef == "" {
		return nil, fmt.Errorf("source asset reference is required")
	}
	if sourceKind == "" {
		sourceKind = "snapshot"
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", userID, sourceKind, sourceAssetRef)))
	objectKey := fmt.Sprintf("social/%d/import/%s", userID, hex.EncodeToString(sum[:16]))

	a := &Asset{}
	err := db.QueryRowContext(ctx, `
		INSERT INTO social_media_assets (owner_user_id, object_key, source_asset_checksum, checksum,
		    content_type, byte_size, media_kind, source_kind, source_asset_ref, processing_status)
		VALUES ($1, $2, $3, $3, 'application/octet-stream', 0, 'image', $4, $5, 'pending')
		ON CONFLICT (object_key) DO UPDATE SET updated_at = NOW()
		RETURNING id::text, object_key, checksum, content_type, media_kind, source_kind, byte_size, width, height, duration_ms`,
		userID, objectKey, hex.EncodeToString(sum[:]), sourceKind, sourceAssetRef).Scan(
		&a.ID, &a.ObjectKey, &a.Checksum, &a.ContentType, &a.MediaKind, &a.SourceKind, &a.ByteSize, &a.Width, &a.Height, &a.DurationMs)
	if err != nil {
		return nil, fmt.Errorf("import studio asset: %w", err)
	}
	return a, nil
}

// ListMediaAssets returns the caller's non-deleted media library, newest first.
func ListMediaAssets(ctx context.Context, db *sql.DB, userID int64, limit int) ([]*Asset, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id::text, object_key, checksum, content_type, media_kind, source_kind, byte_size, width, height, duration_ms
		FROM social_media_assets WHERE owner_user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list social media assets: %w", err)
	}
	defer rows.Close()
	assets := []*Asset{}
	for rows.Next() {
		a := &Asset{}
		if err := rows.Scan(&a.ID, &a.ObjectKey, &a.Checksum, &a.ContentType, &a.MediaKind, &a.SourceKind,
			&a.ByteSize, &a.Width, &a.Height, &a.DurationMs); err != nil {
			return nil, fmt.Errorf("scan social media asset: %w", err)
		}
		assets = append(assets, a)
	}
	return assets, rows.Err()
}

// DeleteMediaAsset soft-deletes an asset owned by userID. Existing rendition
// attachments are left intact (historical renditions keep referencing the
// asset row); only future SetRenditionMedia calls will no longer find it.
func DeleteMediaAsset(ctx context.Context, db *sql.DB, userID int64, assetID string) (bool, error) {
	result, err := db.ExecContext(ctx, `
		UPDATE social_media_assets SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid AND owner_user_id = $2 AND deleted_at IS NULL`, assetID, userID)
	if err != nil {
		return false, fmt.Errorf("delete social media asset: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read delete social media asset result: %w", err)
	}
	return affected == 1, nil
}
