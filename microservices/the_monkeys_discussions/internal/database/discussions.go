package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/the-monkeys/the_monkeys/common/audience"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/discussions"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const discussionPageSize = 20
const discussionReplyCap = 100

type groupStanding struct {
	id         int64
	visibility string
	status     string
	active     bool
	moderate   bool
}

func decisionErr(d discussions.Decision, hide, forbid string) error {
	switch d {
	case discussions.Allow:
		return nil
	case discussions.Hide:
		return status.Error(codes.NotFound, hide)
	case discussions.Forbid:
		return status.Error(codes.PermissionDenied, forbid)
	case discussions.NeedLogin:
		return status.Error(codes.Unauthenticated, "Log in to post a discussion.")
	default:
		return status.Error(codes.NotFound, hide)
	}
}

func newDiscussionID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", status.Error(codes.Internal, "could not create a discussion")
	}
	return hex.EncodeToString(buf), nil
}

const groupStandingSQL = `
SELECT g.id, g.visibility, g.status,
    EXISTS (
        SELECT 1 FROM group_members m
        JOIN user_account ua ON ua.id = m.user_id
        WHERE m.group_id = g.id AND ua.account_id = $2 AND m.status = 'active'
    ),
    EXISTS (
        SELECT 1 FROM user_account ua
        WHERE ua.account_id = $2 AND ua.account_id <> '' AND (
            ua.id = g.organizer_id
            OR EXISTS (
                SELECT 1 FROM group_permissions p
                WHERE p.group_id = g.id AND p.user_id = ua.id
                  AND p.permission_type = 'manage_discussions'
            )
        )
    )
FROM groups g
WHERE g.slug = $1`

func loadGroupStanding(ctx context.Context, q querier, slug, accountID string) (groupStanding, error) {
	var st groupStanding
	err := q.QueryRowContext(ctx, groupStandingSQL, slug, accountID).
		Scan(&st.id, &st.visibility, &st.status, &st.active, &st.moderate)
	if err == sql.ErrNoRows {
		return st, status.Error(codes.NotFound, "group not found")
	}
	if err != nil {
		return st, status.Error(codes.Internal, "could not load the group")
	}
	if st.status != "published" {
		return st, status.Error(codes.NotFound, "group not found")
	}
	return st, nil
}

func (db *discussionDB) CreateDiscussion(ctx context.Context, in discussions.CreateInput) (*discussions.Post, error) {
	body, err := discussions.ValidateBody(in.Body, len(in.Files))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := discussions.ValidateFiles(in.Files); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	var post *discussions.Post
	err = db.inTx(ctx, func(tx *sql.Tx) error {
		authorID, err := resolveAccount(ctx, tx, in.AccountID)
		if err != nil {
			return err
		}
		var groupID any
		postAudience := discussions.CreateAudience("", "", in.Audience)
		if strings.TrimSpace(in.GroupSlug) != "" {
			st, err := loadGroupStanding(ctx, tx, in.GroupSlug, in.AccountID)
			if err != nil {
				return err
			}
			if err := decisionErr(discussions.CanWrite(discussions.Scope{
				Visibility: st.visibility, LoggedIn: true, ActiveMember: st.active,
			}), "group not found", "Join the group to post a discussion."); err != nil {
				return err
			}
			postAudience = discussions.CreateAudience(in.GroupSlug, st.visibility, in.Audience)
			if err := discussions.FilesAllowed(st.visibility, postAudience, len(in.Files)); err != nil {
				return status.Error(codes.InvalidArgument, err.Error())
			}
			groupID = st.id
		}
		publicID, err := insertDiscussionPost(ctx, tx, authorID, groupID, body, postAudience, in.Files)
		if err != nil {
			return err
		}
		post, err = loadDiscussion(ctx, tx, in.AccountID, publicID)
		return err
	})
	return post, err
}

func insertDiscussionPost(ctx context.Context, tx *sql.Tx, authorID int64, groupID any, body, postAudience string, files []discussions.File) (string, error) {
	var publicID string
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		publicID, err = newDiscussionID()
		if err != nil {
			return "", err
		}
		var id int64
		err = tx.QueryRowContext(ctx, `
			INSERT INTO discussion_posts (public_id, author_id, group_id, body, audience, edited_until)
			VALUES ($1, $2, $3, $4, $5, NOW() + INTERVAL '15 minutes')
			RETURNING id`, publicID, authorID, groupID, body, postAudience).Scan(&id)
		if err == nil {
			for i, f := range files {
				if _, err = tx.ExecContext(ctx, `
					INSERT INTO discussion_files (post_id, sort_order, storage_key, content_type)
					VALUES ($1, $2, $3, $4)`, id, i, f.StorageKey, f.ContentType); err != nil {
					return "", status.Error(codes.Internal, "could not save the images")
				}
			}
			return publicID, nil
		}
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			continue
		}
		return "", status.Error(codes.Internal, "could not save the discussion")
	}
	return "", status.Error(codes.Internal, "could not save the discussion")
}

func (db *discussionDB) ListDiscussions(ctx context.Context, in discussions.ListInput) ([]*discussions.Post, error) {
	slug := strings.TrimSpace(in.GroupSlug)
	if slug == "" {
		return db.listDiscussionFeed(ctx, in, nil)
	}
	st, err := loadGroupStanding(ctx, db.db, slug, in.AccountID)
	if err != nil {
		return nil, err
	}
	if err := decisionErr(discussions.CanRead(discussions.Scope{
		Visibility: st.visibility, ActiveMember: st.active,
	}), "group not found", ""); err != nil {
		return nil, err
	}
	return db.listDiscussionFeed(ctx, in, &st)
}

func (db *discussionDB) listDiscussionFeed(ctx context.Context, in discussions.ListInput, st *groupStanding) ([]*discussions.Post, error) {
	args := []any{in.AccountID}
	where := `p.status = 'visible' AND (
		(p.group_id IS NULL AND p.audience <> 'group_only')
		OR (g.visibility = 'public' AND g.status = 'published' AND p.audience = 'public')
	)`
	if st != nil {
		args = append(args, st.id)
		where = `p.status = 'visible' AND p.group_id = $2`
		if !st.active {
			where += ` AND p.audience = 'public'`
		}
	}
	if before := strings.TrimSpace(in.BeforePublicID); before != "" {
		var cursorID int64
		var cursorAt time.Time
		var cursorGroup sql.NullInt64
		var cursorAudience string
		var cursorVis string
		var cursorStatus string
		err := db.db.QueryRowContext(ctx, `
			SELECT p.id, p.created_at, p.group_id, p.audience,
			       COALESCE(g.visibility, ''), COALESCE(g.status, '')
			FROM discussion_posts p
			LEFT JOIN groups g ON g.id = p.group_id
			WHERE p.public_id = $1`, before).
			Scan(&cursorID, &cursorAt, &cursorGroup, &cursorAudience, &cursorVis, &cursorStatus)
		if err == sql.ErrNoRows {
			return nil, status.Error(codes.InvalidArgument, "That page is not part of this feed.")
		}
		if err != nil {
			return nil, status.Error(codes.Internal, "could not load the feed")
		}
		same := false
		if st != nil {
			same = cursorGroup.Valid && cursorGroup.Int64 == st.id
			if same && !st.active && audience.Normalize(cursorAudience) == audience.AudienceGroupOnly {
				same = false
			}
		} else {
			same = onSiteFeed(cursorGroup, cursorAudience, cursorVis, cursorStatus)
		}
		if !same {
			return nil, status.Error(codes.InvalidArgument, "That page is not part of this feed.")
		}
		args = append(args, cursorAt, cursorID)
		n := len(args)
		where += ` AND (p.created_at, p.id) < ($` + strconv.Itoa(n-1) + `, $` + strconv.Itoa(n) + `)`
	}
	rows, err := db.db.QueryContext(ctx, discussionSelectSQL+where+`
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT `+strconv.Itoa(discussionPageSize), args...)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load the feed")
	}
	defer rows.Close()
	return scanPosts(rows, false)
}

func (db *discussionDB) GetDiscussion(ctx context.Context, accountID, publicID string) (*discussions.Post, error) {
	post, err := loadDiscussion(ctx, db.db, accountID, publicID)
	if err != nil {
		return nil, err
	}
	replies, err := loadReplies(ctx, db.db, publicID)
	if err != nil {
		return nil, err
	}
	post.Replies = replies
	return post, nil
}

func (db *discussionDB) ReplyToDiscussion(ctx context.Context, in discussions.ReplyInput) (*discussions.Reply, error) {
	body, err := discussions.ValidateBody(in.Body, 0)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	var reply *discussions.Reply
	err = db.inTx(ctx, func(tx *sql.Tx) error {
		authorID, err := resolveAccount(ctx, tx, in.AccountID)
		if err != nil {
			return err
		}
		post, err := loadDiscussion(ctx, tx, in.AccountID, in.PostPublicID)
		if err != nil {
			return err
		}
		if err := requireDiscussionWrite(ctx, tx, in.AccountID, post); err != nil {
			return err
		}
		if post.Status != "visible" {
			return status.Error(codes.NotFound, "discussion not found")
		}
		var parentID any
		if parent := strings.TrimSpace(in.ParentPublicID); parent != "" {
			var id int64
			err = tx.QueryRowContext(ctx, `
				SELECT r.id
				FROM discussion_replies r
				JOIN discussion_posts p ON p.id = r.post_id
				WHERE r.public_id = $1 AND p.public_id = $2 AND r.status = 'visible'`,
				parent, in.PostPublicID).Scan(&id)
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "That reply is not on this discussion.")
			}
			if err != nil {
				return status.Error(codes.Internal, "could not load the reply")
			}
			parentID = id
		}
		publicID, err := newDiscussionID()
		if err != nil {
			return err
		}
		var created time.Time
		err = tx.QueryRowContext(ctx, `
			INSERT INTO discussion_replies (public_id, post_id, parent_reply_id, author_id, body)
			SELECT $1, p.id, $2, $3, $4 FROM discussion_posts p WHERE p.public_id = $5
			RETURNING created_at`, publicID, parentID, authorID, body, in.PostPublicID).Scan(&created)
		if err != nil {
			return status.Error(codes.Internal, "could not save the reply")
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE discussion_posts SET reply_count = reply_count + 1, updated_at = NOW()
			WHERE public_id = $1`, in.PostPublicID); err != nil {
			return status.Error(codes.Internal, "could not save the reply")
		}
		reply = &discussions.Reply{
			PublicID: publicID, ParentPublicID: strings.TrimSpace(in.ParentPublicID),
			Body: body, Status: "visible", CreatedAt: created,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var username string
	_ = db.db.QueryRowContext(ctx, `SELECT username FROM user_account WHERE account_id = $1`, in.AccountID).Scan(&username)
	reply.AuthorUsername = username
	return reply, err
}

func (db *discussionDB) LikeDiscussion(ctx context.Context, accountID, publicID string) (bool, error) {
	var liked bool
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		userID, err := resolveAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		post, err := loadDiscussion(ctx, tx, accountID, publicID)
		if err != nil {
			return err
		}
		if err := requireDiscussionWrite(ctx, tx, accountID, post); err != nil {
			return err
		}
		var postID int64
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM discussion_posts WHERE public_id = $1 AND status = 'visible'`, publicID).Scan(&postID)
		if err == sql.ErrNoRows {
			return status.Error(codes.NotFound, "discussion not found")
		}
		if err != nil {
			return status.Error(codes.Internal, "could not update the like")
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM discussion_likes WHERE user_id = $1 AND post_id = $2`, userID, postID)
		if err != nil {
			return status.Error(codes.Internal, "could not update the like")
		}
		n, _ := res.RowsAffected()
		delta := 1
		liked = true
		if n == 1 {
			delta = -1
			liked = false
		} else if _, err = tx.ExecContext(ctx, `
			INSERT INTO discussion_likes (user_id, post_id) VALUES ($1, $2)`, userID, postID); err != nil {
			return status.Error(codes.Internal, "could not update the like")
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE discussion_posts SET like_count = GREATEST(like_count + $2, 0) WHERE id = $1`, postID, delta); err != nil {
			return status.Error(codes.Internal, "could not update the like")
		}
		return nil
	})
	return liked, err
}

func (db *discussionDB) EditDiscussion(ctx context.Context, accountID, publicID, body string) (*discussions.Post, error) {
	var post *discussions.Post
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		authorID, err := resolveAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		var fileCount int
		if err = tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM discussion_files f
			JOIN discussion_posts p ON p.id = f.post_id
			WHERE p.public_id = $1`, publicID).Scan(&fileCount); err != nil {
			return status.Error(codes.Internal, "could not edit the discussion")
		}
		trimmed, err := discussions.ValidateBody(body, fileCount)
		if err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE discussion_posts
			SET body = $1, updated_at = NOW()
			WHERE public_id = $2 AND author_id = $3 AND status = 'visible' AND edited_until > NOW()`,
			trimmed, publicID, authorID)
		if err != nil {
			return status.Error(codes.Internal, "could not edit the discussion")
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return editRefusal(ctx, tx, authorID, publicID)
		}
		post, err = loadDiscussion(ctx, tx, accountID, publicID)
		return err
	})
	return post, err
}

func editRefusal(ctx context.Context, q querier, authorID int64, publicID string) error {
	var owner sql.NullInt64
	var until time.Time
	var st string
	err := q.QueryRowContext(ctx, `
		SELECT author_id, edited_until, status FROM discussion_posts WHERE public_id = $1`, publicID).
		Scan(&owner, &until, &st)
	if err == sql.ErrNoRows {
		return status.Error(codes.NotFound, "discussion not found")
	}
	if err != nil {
		return status.Error(codes.Internal, "could not edit the discussion")
	}
	if !owner.Valid || owner.Int64 != authorID {
		return status.Error(codes.PermissionDenied, "You can only edit your own discussion.")
	}
	if st != "visible" || !until.After(time.Now()) {
		return status.Error(codes.FailedPrecondition, "The edit window has closed.")
	}
	return status.Error(codes.Internal, "could not edit the discussion")
}

func (db *discussionDB) DeleteDiscussion(ctx context.Context, accountID, publicID string) error {
	return db.setPostStatus(ctx, accountID, publicID, "deleted")
}

func (db *discussionDB) HideDiscussion(ctx context.Context, accountID, publicID string) error {
	return db.setPostStatus(ctx, accountID, publicID, "hidden")
}

func (db *discussionDB) setPostStatus(ctx context.Context, accountID, publicID, next string) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		authorID, err := resolveAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		post, err := loadDiscussion(ctx, tx, accountID, publicID)
		if err != nil {
			return err
		}
		if post.Status != "visible" {
			return nil
		}
		var owner sql.NullInt64
		var groupID sql.NullInt64
		if err = tx.QueryRowContext(ctx, `
			SELECT author_id, group_id FROM discussion_posts WHERE public_id = $1`, publicID).
			Scan(&owner, &groupID); err != nil {
			return status.Error(codes.NotFound, "discussion not found")
		}
		staff := false
		if groupID.Valid {
			var slug string
			if err = tx.QueryRowContext(ctx, `SELECT slug FROM groups WHERE id = $1`, groupID.Int64).Scan(&slug); err != nil {
				return status.Error(codes.NotFound, "discussion not found")
			}
			st, err := loadGroupStanding(ctx, tx, slug, accountID)
			if err != nil {
				return err
			}
			staff = st.moderate
		}
		if err := discussions.StatusChange(owner.Valid && owner.Int64 == authorID, staff, next); err != nil {
			return status.Error(codes.PermissionDenied, err.Error())
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE discussion_posts SET status = $2, updated_at = NOW() WHERE public_id = $1`, publicID, next)
		if err != nil {
			return status.Error(codes.Internal, "could not update the discussion")
		}
		return nil
	})
}

func onSiteFeed(groupID sql.NullInt64, postAudience, vis, status string) bool {
	if !groupID.Valid {
		return audience.Normalize(postAudience) != audience.AudienceGroupOnly
	}
	return audience.Normalize(postAudience) == audience.AudiencePublic &&
		vis == "public" && status == "published"
}

const discussionSelectSQL = `
SELECT p.public_id, p.body, p.status, p.reply_count, p.like_count,
       p.created_at, p.edited_until,
       COALESCE(ua.username, ''),
       p.author_id IS NULL,
       COALESCE(g.slug, ''),
       COALESCE(g.visibility, ''),
       COALESCE(g.status, ''),
       COALESCE(viewer.id = p.author_id, false),
       CASE WHEN g.id IS NULL THEN false ELSE EXISTS (
           SELECT 1 FROM user_account staff
           WHERE staff.account_id = $1 AND staff.account_id <> '' AND (
               staff.id = g.organizer_id
               OR EXISTS (
                   SELECT 1 FROM group_permissions perm
                   WHERE perm.group_id = g.id AND perm.user_id = staff.id
                     AND perm.permission_type = 'manage_discussions'
               )
           )
       ) END,
       EXISTS (
           SELECT 1 FROM group_members m
           JOIN user_account mem ON mem.id = m.user_id
           WHERE g.id IS NOT NULL AND m.group_id = g.id AND mem.account_id = $1 AND m.status = 'active'
       ),
       EXISTS (
           SELECT 1 FROM discussion_likes l
           JOIN user_account liker ON liker.id = l.user_id
           WHERE l.post_id = p.id AND liker.account_id = $1
       ),
       COALESCE((
           SELECT json_agg(json_build_object('key', f.storage_key, 'type', f.content_type) ORDER BY f.sort_order)
           FROM discussion_files f WHERE f.post_id = p.id
       ), '[]'::json),
       p.audience,
       COALESCE(g.name, '')
FROM discussion_posts p
LEFT JOIN user_account ua ON ua.id = p.author_id
LEFT JOIN groups g ON g.id = p.group_id
LEFT JOIN user_account viewer ON viewer.account_id = $1
WHERE `

func loadDiscussion(ctx context.Context, q querier, accountID, publicID string) (*discussions.Post, error) {
	rows, err := q.QueryContext(ctx, discussionSelectSQL+`p.public_id = $2`, accountID, publicID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load the discussion")
	}
	defer rows.Close()
	posts, err := scanPosts(rows, true)
	if err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return nil, status.Error(codes.NotFound, "discussion not found")
	}
	return posts[0], nil
}

func scanPosts(rows *sql.Rows, enforceRead bool) ([]*discussions.Post, error) {
	var out []*discussions.Post
	for rows.Next() {
		var (
			p            discussions.Post
			visibility   string
			groupStatus  string
			authorSelf   bool
			staff        bool
			activeMember bool
			rawFiles     []byte
			storedAud    string
		)
		if err := rows.Scan(
			&p.PublicID, &p.Body, &p.Status, &p.ReplyCount, &p.LikeCount,
			&p.CreatedAt, &p.EditedUntil,
			&p.AuthorUsername, &p.AuthorGone, &p.GroupSlug,
			&visibility, &groupStatus, &authorSelf, &staff, &activeMember, &p.Liked, &rawFiles,
			&storedAud, &p.GroupName,
		); err != nil {
			return nil, status.Error(codes.Internal, "could not load the discussion")
		}
		if p.GroupSlug != "" {
			p.Audience = audience.Coerce(storedAud, visibility)
		} else {
			p.Audience = audience.Normalize(storedAud)
		}
		if enforceRead {
			if !discussions.DiscussionVisible(p.GroupSlug, groupStatus) {
				return nil, status.Error(codes.NotFound, "discussion not found")
			}
			scope := discussions.Scope{
				Site: p.GroupSlug == "", Visibility: visibility, ActiveMember: activeMember,
				Audience: storedAud, Author: authorSelf,
			}
			if err := decisionErr(discussions.CanRead(scope), "discussion not found", ""); err != nil {
				return nil, err
			}
		}
		p.Body = discussions.VisibleBody(p.Body, p.Status, authorSelf || staff)
		var packed []struct {
			Key  string `json:"key"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawFiles, &packed); err != nil {
			return nil, status.Error(codes.Internal, "could not load the images")
		}
		for _, f := range packed {
			p.Files = append(p.Files, discussions.File{StorageKey: f.Key, ContentType: f.Type})
		}
		out = append(out, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, status.Error(codes.Internal, "could not load the discussion")
	}
	return out, nil
}

func loadReplies(ctx context.Context, q querier, postPublicID string) ([]discussions.Reply, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT r.public_id, COALESCE(parent.public_id, ''), r.body, r.status,
		       COALESCE(ua.username, ''), r.author_id IS NULL, r.created_at
		FROM discussion_replies r
		JOIN discussion_posts p ON p.id = r.post_id
		LEFT JOIN discussion_replies parent ON parent.id = r.parent_reply_id
		LEFT JOIN user_account ua ON ua.id = r.author_id
		WHERE p.public_id = $1
		ORDER BY r.created_at
		LIMIT `+strconv.Itoa(discussionReplyCap), postPublicID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load the replies")
	}
	defer rows.Close()
	var out []discussions.Reply
	for rows.Next() {
		var r discussions.Reply
		if err := rows.Scan(&r.PublicID, &r.ParentPublicID, &r.Body, &r.Status, &r.AuthorUsername, &r.AuthorGone, &r.CreatedAt); err != nil {
			return nil, status.Error(codes.Internal, "could not load the replies")
		}
		r.Body = discussions.VisibleBody(r.Body, r.Status, false)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, status.Error(codes.Internal, "could not load the replies")
	}
	return out, nil
}

func requireDiscussionWrite(ctx context.Context, q querier, accountID string, post *discussions.Post) error {
	if post.GroupSlug == "" {
		return nil
	}
	st, err := loadGroupStanding(ctx, q, post.GroupSlug, accountID)
	if err != nil {
		return err
	}
	return decisionErr(discussions.CanWrite(discussions.Scope{
		Visibility: st.visibility, LoggedIn: true, ActiveMember: st.active,
	}), "discussion not found", "Join the group to post a discussion.")
}
