package database

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/discussions"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestListPrivateGroupHidesFromStranger(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}

	mock.ExpectQuery(`SELECT g.id, g.visibility`).
		WithArgs("secret", "").
		WillReturnRows(sqlmock.NewRows([]string{"id", "visibility", "status", "active", "moderate"}).
			AddRow(int64(1), "private", "published", false, false))

	_, err = db.ListDiscussions(context.Background(), discussions.ListInput{GroupSlug: "secret"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, err = %v", status.Code(err), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func sitePostRows(when time.Time) *sqlmock.Rows {
	return postSelectRows(when, "", "", "public", false, false)
}

func membersOnlyStrangerRows(when time.Time) *sqlmock.Rows {
	return postSelectRows(when, "tea", "public", "group_only", false, false)
}

func postSelectRows(when time.Time, slug, visibility, postAudience string, self, member bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"public_id", "body", "status", "reply_count", "like_count", "created_at", "edited_until",
		"username", "gone", "slug", "visibility", "group_status", "self", "staff", "member", "liked", "files",
		"audience", "group_name",
	}).AddRow("post1", "hello", "visible", int32(2), int32(0), when, when,
		"ada", false, slug, visibility, "published", self, false, member, false, []byte("[]"),
		postAudience, "Tea")
}

func TestReplyToADeepReplyIsSaved(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
	when := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM user_account`).
		WithArgs("acc").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery(`SELECT p.public_id, p.body`).
		WithArgs("acc", "post1").
		WillReturnRows(sitePostRows(when))
	mock.ExpectQuery(`SELECT r.id\s+FROM discussion_replies`).
		WithArgs("level2", "post1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))
	mock.ExpectQuery(`INSERT INTO discussion_replies`).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(when))
	mock.ExpectExec(`UPDATE discussion_posts SET reply_count`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(`SELECT username FROM user_account`).
		WithArgs("acc").
		WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("bea"))

	reply, err := db.ReplyToDiscussion(context.Background(), discussions.ReplyInput{
		AccountID: "acc", PostPublicID: "post1", ParentPublicID: "level2", Body: "deeper",
	})
	if err != nil {
		t.Fatalf("deep reply rejected: %v", err)
	}
	if reply.ParentPublicID != "level2" || reply.AuthorUsername != "bea" {
		t.Fatalf("reply = %+v", reply)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRepliesKeepsHiddenReplyWithoutItsText(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	when := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT r.public_id`).
		WithArgs("post1").
		WillReturnRows(sqlmock.NewRows([]string{"public_id", "parent", "body", "status", "username", "gone", "created_at"}).
			AddRow("r1", "", "rude words", "hidden", "ada", false, when).
			AddRow("r2", "r1", "kind words", "visible", "bea", false, when))

	got, err := loadReplies(context.Background(), sqlDB, "post1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("replies = %+v", got)
	}
	if got[0].Status != "hidden" || got[0].Body != "" {
		t.Fatalf("hidden reply = %+v", got[0])
	}
	if got[1].ParentPublicID != "r1" || got[1].Body != "kind words" {
		t.Fatalf("child reply = %+v", got[1])
	}
}

func TestCreateMembersOnlyOnPublicGroupRejectsImages(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM user_account`).
		WithArgs("acc").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery(`SELECT g.id, g.visibility`).
		WithArgs("tea", "acc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "visibility", "status", "active", "moderate"}).
			AddRow(int64(1), "public", "published", true, false))
	mock.ExpectRollback()

	_, err = db.CreateDiscussion(context.Background(), discussions.CreateInput{
		AccountID: "acc",
		GroupSlug: "tea",
		Body:      "hello",
		Audience:  "group_only",
		Files:     []discussions.File{{StorageKey: "discussions/x/0", ContentType: "image/png"}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, err = %v", status.Code(err), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListSiteFeedIncludesPublicGroupPosts(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
	when := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`g.visibility = 'public'`).
		WithArgs("").
		WillReturnRows(sitePostRows(when))

	posts, err := db.ListDiscussions(context.Background(), discussions.ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v", posts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListGroupHidesMembersOnlyFromStranger(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
	when := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT g.id, g.visibility`).
		WithArgs("tea", "").
		WillReturnRows(sqlmock.NewRows([]string{"id", "visibility", "status", "active", "moderate"}).
			AddRow(int64(1), "public", "published", false, false))
	mock.ExpectQuery(`p.audience = 'public'`).
		WithArgs("", int64(1)).
		WillReturnRows(sitePostRows(when))

	posts, err := db.ListDiscussions(context.Background(), discussions.ListInput{GroupSlug: "tea"})
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v", posts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetMembersOnlyHidesFromStranger(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
	when := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT p.public_id, p.body`).
		WithArgs("", "secret").
		WillReturnRows(membersOnlyStrangerRows(when))

	_, err = db.GetDiscussion(context.Background(), "", "secret")
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, err = %v", status.Code(err), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreatePrivateGroupRejectsImages(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM user_account`).
		WithArgs("acc").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery(`SELECT g.id, g.visibility`).
		WithArgs("secret", "acc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "visibility", "status", "active", "moderate"}).
			AddRow(int64(1), "private", "published", true, false))
	mock.ExpectRollback()

	_, err = db.CreateDiscussion(context.Background(), discussions.CreateInput{
		AccountID: "acc",
		GroupSlug: "secret",
		Body:      "hello",
		Files:     []discussions.File{{StorageKey: "discussions/x/0", ContentType: "image/png"}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, err = %v", status.Code(err), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
