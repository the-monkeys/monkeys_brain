package services

import (
	"context"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_discussion/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/discussions"
)

type followingDB struct {
	stubDB
	input discussions.ListInput
}

func (db *followingDB) ListDiscussions(_ context.Context, in discussions.ListInput) ([]*discussions.Post, error) {
	db.input = in
	return []*discussions.Post{{PublicID: "p1", Body: "hello", Status: "visible"}}, nil
}

func TestListPassesAndAcknowledgesFollowingFilter(t *testing.T) {
	for _, following := range []bool{false, true} {
		db := &followingDB{}
		res, err := NewDiscussionService(db).ListDiscussions(context.Background(), &pb.ListDiscussionsReq{AccountId: "viewer", BeforePublicId: "cursor", FollowingOnly: following})
		if err != nil {
			t.Fatal(err)
		}
		if db.input.FollowingOnly != following || db.input.AccountID != "viewer" || db.input.BeforePublicID != "cursor" || res.GetFollowingOnly() != following {
			t.Fatalf("input=%+v response=%v", db.input, res)
		}
	}
}
