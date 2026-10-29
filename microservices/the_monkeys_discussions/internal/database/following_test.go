package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/discussions"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFollowingFeedRequiresLoginAndSeparateScope(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input discussions.ListInput
		code  codes.Code
	}{
		{"guest", discussions.ListInput{FollowingOnly: true}, codes.Unauthenticated},
		{"group filter", discussions.ListInput{AccountID: "viewer", GroupSlug: "tea", FollowingOnly: true}, codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
			_, err = db.ListDiscussions(context.Background(), tc.input)
			if status.Code(err) != tc.code {
				t.Fatalf("code=%v want %v: %v", status.Code(err), tc.code, err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFollowingFeedFiltersBeforeLimitAndKeepsPublicVisibility(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
	mock.ExpectQuery(`(?s)p.status = 'visible'.*g.visibility = 'public'.*p.audience = 'public'.*EXISTS.*FROM user_follows.*follower_id = viewer.id.*following_id = p.author_id.*ORDER BY p.created_at DESC, p.id DESC.*LIMIT 20`).WithArgs("viewer").WillReturnRows(sitePostRows(time.Now().UTC()))
	posts, err := db.ListDiscussions(context.Background(), discussions.ListInput{AccountID: "viewer", FollowingOnly: true})
	if err != nil || len(posts) != 1 {
		t.Fatalf("posts=%v err=%v", posts, err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFollowingFeedRejectsUnfollowedCursor(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db := &discussionDB{db: sqlDB, log: zap.NewNop().Sugar()}
	mock.ExpectQuery(`(?s)SELECT p.id, p.created_at.*FROM user_follows.*account_id = \$2`).WithArgs("other-post", "viewer").WillReturnError(sql.ErrNoRows)
	_, err = db.ListDiscussions(context.Background(), discussions.ListInput{AccountID: "viewer", FollowingOnly: true, BeforePublicID: "other-post"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code=%v err=%v", status.Code(err), err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
