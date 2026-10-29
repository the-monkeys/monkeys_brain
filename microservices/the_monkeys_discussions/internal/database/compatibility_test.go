package database

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type compatibilityPost struct {
	slug        string
	visibility  string
	groupStatus string
	audience    string
	body        string
	status      string
	self        bool
	staff       bool
	member      bool
	filesJSON   string
	missing     bool
}

func compatibilityPostRows(when time.Time, p compatibilityPost) *sqlmock.Rows {
	files := p.filesJSON
	if files == "" {
		files = "[]"
	}
	rows := sqlmock.NewRows([]string{
		"public_id", "body", "status", "reply_count", "like_count", "created_at", "edited_until",
		"username", "gone", "slug", "visibility", "group_status", "self", "staff", "member", "liked", "files",
		"audience", "group_name",
	})
	if p.missing {
		return rows
	}
	return rows.AddRow("post1", p.body, p.status, int32(0), int32(0), when, when.Add(15*time.Minute),
		"ada", false, p.slug, p.visibility, p.groupStatus, p.self, p.staff, p.member, false,
		[]byte(files), p.audience, "Tea")
}

func TestEditDiscussionHidesUnreadablePosts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		visibility  string
		audience    string
		groupStatus string
		body        string
		missing     bool
	}{
		{name: "private", visibility: "private", audience: "public", groupStatus: "published", body: "edited"},
		{name: "unlisted", visibility: "unlisted", audience: "public", groupStatus: "published", body: "edited"},
		{name: "members only", visibility: "public", audience: "group_only", groupStatus: "published", body: "edited"},
		{name: "unpublished", visibility: "public", audience: "public", groupStatus: "draft", body: "edited"},
		{name: "private empty body", visibility: "private", audience: "group_only", groupStatus: "published"},
		{name: "private overlong body", visibility: "private", audience: "group_only", groupStatus: "published", body: strings.Repeat("x", 501)},
		{name: "missing", body: "edited", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
			when := time.Now().UTC()

			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT id FROM user_account`).WithArgs("outsider").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
			rows := compatibilityPostRows(when, compatibilityPost{
				slug: "tea", visibility: tc.visibility, groupStatus: tc.groupStatus,
				audience: tc.audience, body: "hello", status: "visible", missing: tc.missing,
			})
			mock.ExpectQuery(`SELECT p.public_id, p.body`).WithArgs("outsider", "post1").WillReturnRows(rows)
			mock.ExpectRollback()

			post, err := db.EditDiscussion(context.Background(), "outsider", "post1", tc.body)
			if status.Code(err) != codes.NotFound || status.Convert(err).Message() != "discussion not found" || post != nil {
				t.Fatalf("edit = (%+v, %v), want no post and indistinguishable NotFound", post, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEditDiscussionPreservesReadableRefusals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		visibility string
		audience   string
		postStatus string
		self       bool
		member     bool
		expired    bool
		want       codes.Code
	}{
		{name: "public nonauthor", visibility: "public", audience: "public", postStatus: "visible", want: codes.PermissionDenied},
		{name: "members only nonauthor", visibility: "public", audience: "group_only", postStatus: "visible", member: true, want: codes.PermissionDenied},
		{name: "private member nonauthor", visibility: "private", audience: "group_only", postStatus: "visible", member: true, want: codes.PermissionDenied},
		{name: "former owner expired", visibility: "private", audience: "group_only", postStatus: "visible", self: true, expired: true, want: codes.FailedPrecondition},
		{name: "former owner hidden", visibility: "private", audience: "group_only", postStatus: "hidden", self: true, want: codes.FailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
			when := time.Now().UTC()
			if tc.expired {
				when = when.Add(-time.Hour)
			}
			ownerID := int64(8)
			if tc.self {
				ownerID = 7
			}

			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT id FROM user_account`).WithArgs("viewer").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
			mock.ExpectQuery(`SELECT p.public_id, p.body`).WithArgs("viewer", "post1").
				WillReturnRows(compatibilityPostRows(when, compatibilityPost{
					slug: "tea", visibility: tc.visibility, groupStatus: "published", audience: tc.audience,
					body: "hello", status: tc.postStatus, self: tc.self, member: tc.member,
				}))
			mock.ExpectQuery(`SELECT COUNT\(\*\) FROM discussion_files`).WithArgs("post1").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			mock.ExpectExec(`UPDATE discussion_posts`).WithArgs("edited", "post1", int64(7)).
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT author_id, edited_until, status`).WithArgs("post1").
				WillReturnRows(sqlmock.NewRows([]string{"author_id", "edited_until", "status"}).
					AddRow(ownerID, when.Add(15*time.Minute), tc.postStatus))
			mock.ExpectRollback()

			post, err := db.EditDiscussion(context.Background(), "viewer", "post1", "edited")
			if status.Code(err) != tc.want || post != nil {
				t.Fatalf("edit = (%+v, %v), want no post and %v", post, err, tc.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEditDiscussionPreservesOwnerAccessAfterLeaving(t *testing.T) {
	for _, visibility := range []string{"private", "unlisted"} {
		t.Run(visibility, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
			when := time.Now().UTC()
			fixture := compatibilityPost{
				slug: "tea", visibility: visibility, groupStatus: "published", audience: "group_only",
				body: "hello", status: "visible", self: true, member: false,
			}

			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT id FROM user_account`).WithArgs("owner").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
			mock.ExpectQuery(`SELECT p.public_id, p.body`).WithArgs("owner", "post1").
				WillReturnRows(compatibilityPostRows(when, fixture))
			mock.ExpectQuery(`SELECT COUNT\(\*\) FROM discussion_files`).WithArgs("post1").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			mock.ExpectExec(`UPDATE discussion_posts`).WithArgs("edited", "post1", int64(7)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			fixture.body = "edited"
			mock.ExpectQuery(`SELECT p.public_id, p.body`).WithArgs("owner", "post1").
				WillReturnRows(compatibilityPostRows(when, fixture))
			mock.ExpectCommit()

			post, err := db.EditDiscussion(context.Background(), "owner", "post1", "  edited  ")
			if err != nil {
				t.Fatal(err)
			}
			if post.Body != "edited" || post.GroupSlug != "tea" || post.Audience != "group_only" {
				t.Fatalf("edited post = %+v", post)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGetDiscussionRedactsTombstoneAttachments(t *testing.T) {
	for _, tc := range []struct {
		name       string
		postStatus string
		body       string
		self       bool
		staff      bool
		wantBody   string
		wantFiles  int
	}{
		{name: "hidden reader", postStatus: "hidden", body: "hello"},
		{name: "deleted reader", postStatus: "deleted", body: "hello"},
		{name: "hidden author", postStatus: "hidden", body: "hello", self: true, wantBody: "hello", wantFiles: 1},
		{name: "deleted author", postStatus: "deleted", body: "hello", self: true, wantBody: "hello", wantFiles: 1},
		{name: "hidden staff", postStatus: "hidden", body: "hello", staff: true, wantBody: "hello", wantFiles: 1},
		{name: "deleted staff", postStatus: "deleted", body: "hello", staff: true, wantBody: "hello", wantFiles: 1},
		{name: "visible image only", postStatus: "visible", wantFiles: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
			mock.ExpectQuery(`SELECT p.public_id, p.body`).WithArgs("viewer", "post1").
				WillReturnRows(compatibilityPostRows(time.Now().UTC(), compatibilityPost{
					slug: "tea", visibility: "public", groupStatus: "published", audience: "public",
					body: tc.body, status: tc.postStatus, self: tc.self, staff: tc.staff,
					filesJSON: `[{"key":"discussions/post1/0","type":"image/png"}]`,
				}))
			mock.ExpectQuery(`SELECT r.public_id`).WithArgs("post1").
				WillReturnRows(sqlmock.NewRows([]string{"public_id", "parent", "body", "status", "username", "gone", "created_at"}))

			post, err := db.GetDiscussion(context.Background(), "viewer", "post1")
			if err != nil {
				t.Fatal(err)
			}
			if post.Status != tc.postStatus || post.Body != tc.wantBody || len(post.Files) != tc.wantFiles {
				t.Fatalf("post = %+v, want status %q, body %q, %d files", post, tc.postStatus, tc.wantBody, tc.wantFiles)
			}
			if tc.wantFiles != 0 && (post.Files[0].StorageKey != "discussions/post1/0" || post.Files[0].ContentType != "image/png") {
				t.Fatalf("attachment changed: %+v", post.Files)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
