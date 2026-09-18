package database

import (
	"context"
	"database/sql"
	"fmt"
)

// ErrNotFound signals a missing or not-owned row; callers translate this to
// codes.NotFound without leaking existence of other owners' data.
var ErrNotFound = fmt.Errorf("social post: not found")

// ErrVersionConflict signals an optimistic-concurrency mismatch.
var ErrVersionConflict = fmt.Errorf("social post: version conflict")

type Post struct {
	ID, BaseText, State, ScheduleTimezone string
	Version                               int64
	ScheduledAt                           sql.NullString
	QueuePosition                         sql.NullInt64
	LastErrorCode, LastErrorMessage       sql.NullString
	CreatedAt, UpdatedAt                  string
}

type Rendition struct {
	ID, SocialAccountID, Platform, State                        string
	Version                                                     int64
	TextOverride, ScheduledAtOverride, ScheduleTimezoneOverride sql.NullString
	ProviderPostRef, LastErrorCode, LastErrorMessage            sql.NullString
}

type MediaRef struct {
	ID, ObjectKey, Checksum, ContentType, MediaKind, SourceKind string
	ByteSize                                                    int64
	Width, Height                                               sql.NullInt64
	DurationMs                                                  sql.NullInt64
}

// ResolveOwner maps an authenticated account id (gateway-trusted identity) to
// the internal numeric user id used throughout the schema. It never trusts a
// client-supplied user id, closing the ownership-bypass class of bug.
func ResolveOwner(ctx context.Context, db *sql.DB, accountID string) (int64, error) {
	var userID int64
	err := db.QueryRowContext(ctx, `SELECT id FROM user_account WHERE account_id = $1`, accountID).Scan(&userID)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("resolve social post owner: %w", err)
	}
	return userID, nil
}

// CreatePost inserts a draft post owned by userID.
func CreatePost(ctx context.Context, db *sql.DB, userID int64, baseText string) (*Post, error) {
	p := &Post{}
	err := db.QueryRowContext(ctx, `
		INSERT INTO social_posts (owner_user_id, base_text)
		VALUES ($1, $2)
		RETURNING id::text, base_text, state, version, scheduled_at::text, COALESCE(schedule_timezone, ''),
		          queue_position, last_error_code, last_error_message, created_at::text, updated_at::text`,
		userID, baseText).Scan(&p.ID, &p.BaseText, &p.State, &p.Version, &p.ScheduledAt, &p.ScheduleTimezone,
		&p.QueuePosition, &p.LastErrorCode, &p.LastErrorMessage, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create social post: %w", err)
	}
	return p, nil
}

// CreatePostIdempotent serializes a command key within the transaction and
// returns the original post for a replay. The advisory lock is scoped to the
// owner/action/key tuple, so concurrent retries cannot create duplicate posts.
func CreatePostIdempotent(ctx context.Context, db *sql.DB, userID int64, baseText, idempotencyKey string) (*Post, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin idempotent social post create: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		fmt.Sprintf("social-post:create:%d:%s", userID, idempotencyKey)); err != nil {
		return nil, false, fmt.Errorf("lock social post create command: %w", err)
	}
	var existingID sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT response->>'post_id'
		FROM social_command_idempotency
		WHERE owner_user_id = $1 AND action = 'post.create' AND idempotency_key = $2`,
		userID, idempotencyKey).Scan(&existingID); err != nil && err != sql.ErrNoRows {
		return nil, false, fmt.Errorf("read social post idempotency record: %w", err)
	}
	if existingID.Valid && existingID.String != "" {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit idempotent social post replay: %w", err)
		}
		p, err := GetPost(ctx, db, userID, existingID.String)
		if err != nil {
			return nil, false, fmt.Errorf("load idempotent social post replay: %w", err)
		}
		return p, false, nil
	}

	p := &Post{}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO social_posts (owner_user_id, base_text)
		VALUES ($1, $2)
		RETURNING id::text, base_text, state, version, scheduled_at::text, COALESCE(schedule_timezone, ''),
		          queue_position, last_error_code, last_error_message, created_at::text, updated_at::text`,
		userID, baseText).Scan(&p.ID, &p.BaseText, &p.State, &p.Version, &p.ScheduledAt, &p.ScheduleTimezone,
		&p.QueuePosition, &p.LastErrorCode, &p.LastErrorMessage, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, false, fmt.Errorf("create idempotent social post: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO social_command_idempotency (owner_user_id, action, idempotency_key, request_hash, response)
		VALUES ($1, 'post.create', $2, md5($3), jsonb_build_object('post_id', $4))`,
		userID, idempotencyKey, baseText, p.ID); err != nil {
		return nil, false, fmt.Errorf("record social post idempotency command: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit idempotent social post create: %w", err)
	}
	return p, true, nil
}

// GetPost loads a post owned by userID, or ErrNotFound if missing/not owned/deleted.
func GetPost(ctx context.Context, db *sql.DB, userID int64, postID string) (*Post, error) {
	p := &Post{}
	err := db.QueryRowContext(ctx, `
		SELECT id::text, base_text, state, version, scheduled_at::text, COALESCE(schedule_timezone, ''),
		       queue_position, last_error_code, last_error_message, created_at::text, updated_at::text
		FROM social_posts WHERE id = $1::uuid AND owner_user_id = $2 AND deleted_at IS NULL`,
		postID, userID).Scan(&p.ID, &p.BaseText, &p.State, &p.Version, &p.ScheduledAt, &p.ScheduleTimezone,
		&p.QueuePosition, &p.LastErrorCode, &p.LastErrorMessage, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get social post: %w", err)
	}
	return p, nil
}

// UpdatePost updates base_text with optimistic concurrency on version.
func UpdatePost(ctx context.Context, db *sql.DB, userID int64, postID, baseText string, expectedVersion int64) (*Post, error) {
	p := &Post{}
	err := db.QueryRowContext(ctx, `
		UPDATE social_posts SET base_text = $1, version = version + 1, updated_at = NOW()
		WHERE id = $2::uuid AND owner_user_id = $3 AND version = $4 AND deleted_at IS NULL
		  AND state = 'draft'
		RETURNING id::text, base_text, state, version, scheduled_at::text, COALESCE(schedule_timezone, ''),
		          queue_position, last_error_code, last_error_message, created_at::text, updated_at::text`,
		baseText, postID, userID, expectedVersion).Scan(&p.ID, &p.BaseText, &p.State, &p.Version, &p.ScheduledAt,
		&p.ScheduleTimezone, &p.QueuePosition, &p.LastErrorCode, &p.LastErrorMessage, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		if _, getErr := GetPost(ctx, db, userID, postID); getErr == nil {
			return nil, ErrVersionConflict
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update social post: %w", err)
	}
	return p, nil
}

// DeleteDraft soft-deletes a draft post. Non-draft posts must be cancelled
// first so an in-flight schedule/publish is never silently dropped.
func DeleteDraft(ctx context.Context, db *sql.DB, userID int64, postID string, expectedVersion int64) error {
	result, err := db.ExecContext(ctx, `
		UPDATE social_posts SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid AND owner_user_id = $2 AND version = $3 AND deleted_at IS NULL AND state = 'draft'`,
		postID, userID, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete social post draft: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read delete social post draft result: %w", err)
	}
	if affected == 1 {
		return nil
	}
	existing, getErr := GetPost(ctx, db, userID, postID)
	if getErr != nil {
		return ErrNotFound
	}
	if existing.Version != expectedVersion {
		return ErrVersionConflict
	}
	return fmt.Errorf("social post: only draft posts can be deleted")
}

// ListFilter drives ListPosts, ListCalendar, and ListQueue with the same
// underlying query shape so behavior stays consistent across surfaces.
type ListFilter struct {
	States         []string
	Platform       string
	From, To       string
	QueueOnly      bool
	OrderByQueue   bool
	Limit          int
	AfterCreatedAt string
	AfterID        string
}

// ListPosts returns posts (with hydrated renditions) matching filter, newest
// first unless OrderByQueue requests manual-queue ordering.
func ListPosts(ctx context.Context, db *sql.DB, userID int64, f ListFilter) ([]*Post, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	query := `
		SELECT id::text, base_text, state, version, scheduled_at::text, COALESCE(schedule_timezone, ''),
		       queue_position, last_error_code, last_error_message, created_at::text, updated_at::text
		FROM social_posts
		WHERE owner_user_id = $1 AND deleted_at IS NULL`
	args := []interface{}{userID}
	idx := 2
	if len(f.States) > 0 {
		query += fmt.Sprintf(" AND state = ANY($%d)", idx)
		args = append(args, f.States)
		idx++
	}
	if f.Platform != "" {
		query += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM social_post_renditions r WHERE r.post_id = social_posts.id AND r.platform = $%d)`, idx)
		args = append(args, f.Platform)
		idx++
	}
	if f.From != "" {
		query += fmt.Sprintf(" AND scheduled_at >= $%d::timestamptz", idx)
		args = append(args, f.From)
		idx++
	}
	if f.To != "" {
		query += fmt.Sprintf(" AND scheduled_at <= $%d::timestamptz", idx)
		args = append(args, f.To)
		idx++
	}
	if f.QueueOnly {
		query += " AND queue_position IS NOT NULL"
	}
	if f.OrderByQueue {
		query += " ORDER BY queue_position ASC NULLS LAST, created_at DESC"
	} else {
		query += " ORDER BY created_at DESC, id DESC"
	}
	query += fmt.Sprintf(" LIMIT $%d", idx)
	args = append(args, f.Limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list social posts: %w", err)
	}
	defer rows.Close()

	posts := make([]*Post, 0, f.Limit)
	for rows.Next() {
		p := &Post{}
		if err := rows.Scan(&p.ID, &p.BaseText, &p.State, &p.Version, &p.ScheduledAt, &p.ScheduleTimezone,
			&p.QueuePosition, &p.LastErrorCode, &p.LastErrorMessage, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan social post: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// ReorderQueue applies a caller-supplied ordering to the given post ids, only
// for posts owned by userID; unknown/foreign ids are silently skipped so a
// partially-stale client payload cannot corrupt other users' queues.
func ReorderQueue(ctx context.Context, db *sql.DB, userID int64, postIDsInOrder []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reorder queue tx: %w", err)
	}
	defer tx.Rollback()
	for i, id := range postIDsInOrder {
		if _, err := tx.ExecContext(ctx, `
			UPDATE social_posts SET queue_position = $1, queued_at = NOW(), updated_at = NOW()
			WHERE id = $2::uuid AND owner_user_id = $3 AND deleted_at IS NULL`, i+1, id, userID); err != nil {
			return fmt.Errorf("reorder queue position: %w", err)
		}
	}
	return tx.Commit()
}

// LoadRenditions hydrates all renditions for a post (used by every endpoint
// that returns a full SocialPost).
func LoadRenditions(ctx context.Context, db *sql.DB, postID string) ([]*Rendition, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id::text, social_account_id::text, platform, state, version,
		       text_override, scheduled_at_override::text, COALESCE(schedule_timezone_override, ''),
		       COALESCE(provider_post_ref, ''), last_error_code, last_error_message
		FROM social_post_renditions WHERE post_id = $1::uuid ORDER BY created_at ASC`, postID)
	if err != nil {
		return nil, fmt.Errorf("load social post renditions: %w", err)
	}
	defer rows.Close()
	renditions := []*Rendition{}
	for rows.Next() {
		r := &Rendition{}
		if err := rows.Scan(&r.ID, &r.SocialAccountID, &r.Platform, &r.State, &r.Version,
			&r.TextOverride, &r.ScheduledAtOverride, &r.ScheduleTimezoneOverride,
			&r.ProviderPostRef, &r.LastErrorCode, &r.LastErrorMessage); err != nil {
			return nil, fmt.Errorf("scan social post rendition: %w", err)
		}
		renditions = append(renditions, r)
	}
	return renditions, rows.Err()
}

// LoadRenditionMedia hydrates ordered media for a single rendition.
func LoadRenditionMedia(ctx context.Context, db *sql.DB, renditionID string) ([]*MediaRef, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.id::text, a.object_key, a.checksum, a.content_type, a.media_kind, a.source_kind,
		       a.byte_size, a.width, a.height, a.duration_ms
		FROM social_rendition_media m
		JOIN social_media_assets a ON a.id = m.asset_id
		WHERE m.rendition_id = $1::uuid ORDER BY m.position ASC`, renditionID)
	if err != nil {
		return nil, fmt.Errorf("load rendition media: %w", err)
	}
	defer rows.Close()
	media := []*MediaRef{}
	for rows.Next() {
		m := &MediaRef{}
		if err := rows.Scan(&m.ID, &m.ObjectKey, &m.Checksum, &m.ContentType, &m.MediaKind, &m.SourceKind,
			&m.ByteSize, &m.Width, &m.Height, &m.DurationMs); err != nil {
			return nil, fmt.Errorf("scan rendition media: %w", err)
		}
		media = append(media, m)
	}
	return media, rows.Err()
}
