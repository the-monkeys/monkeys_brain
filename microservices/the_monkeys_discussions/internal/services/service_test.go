package services

import (
	"context"
	"testing"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_discussion/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/discussions"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestCreateDiscussionMapsThePost(t *testing.T) {
	when := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	db := &stubDB{post: &discussions.Post{
		PublicID:       "abc",
		Body:           "hello",
		Status:         "visible",
		AuthorUsername: "ada",
		GroupSlug:      "tea",
		GroupName:      "Tea Circle",
		Audience:       "group_only",
		ReplyCount:     1,
		LikeCount:      2,
		Liked:          true,
		CreatedAt:      when,
		EditedUntil:    when.Add(15 * time.Minute),
		Files:          []discussions.File{{StorageKey: "discussions/abc/0", ContentType: "image/png"}},
		Replies: []discussions.Reply{{
			PublicID: "r1", Body: "hi", Status: "visible", AuthorUsername: "bea", CreatedAt: when,
		}},
	}}
	svc := NewDiscussionService(db)

	res, err := svc.CreateDiscussion(context.Background(), &pb.CreateDiscussionReq{
		AccountId: "acc-1",
		GroupSlug: "tea",
		Body:      "hello",
		Audience:  "group_only",
		Files:     []*pb.DiscussionFile{{StorageKey: "discussions/abc/0", ContentType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if db.created.AccountID != "acc-1" || db.created.GroupSlug != "tea" || db.created.Body != "hello" || db.created.Audience != "group_only" {
		t.Fatalf("create input = %+v", db.created)
	}
	if len(db.created.Files) != 1 || db.created.Files[0].ContentType != "image/png" {
		t.Fatalf("files = %+v", db.created.Files)
	}
	got := res.GetDiscussion()
	if got.GetPublicId() != "abc" || got.GetAuthorUsername() != "ada" || !got.GetLiked() {
		t.Fatalf("discussion = %+v", got)
	}
	if got.GetCreatedAt().AsTime() != when || len(got.GetFiles()) != 1 || len(got.GetReplies()) != 1 {
		t.Fatalf("discussion = %+v", got)
	}
	if got.GetReplies()[0].GetPublicId() != "r1" {
		t.Fatalf("reply = %+v", got.GetReplies()[0])
	}
	if got.GetAudience() != "group_only" {
		t.Fatalf("audience = %s", got.GetAudience())
	}
}

func TestGetDiscussionPassesNotFoundThrough(t *testing.T) {
	db := &stubDB{err: status.Error(codes.NotFound, "discussion not found")}
	svc := NewDiscussionService(db)

	_, err := svc.GetDiscussion(context.Background(), &pb.DiscussionActionReq{PublicId: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, err = %v", status.Code(err), err)
	}
}

func TestHideDiscussionUsesTheStaffPath(t *testing.T) {
	db := &stubDB{post: &discussions.Post{PublicID: "abc", Status: "hidden"}}
	svc := NewDiscussionService(db)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(discussionActionHeader, discussionActionHide))

	res, err := svc.DeleteDiscussion(ctx, &pb.DiscussionActionReq{AccountId: "staff", PublicId: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !db.hidden || db.deleted {
		t.Fatalf("hidden=%v deleted=%v", db.hidden, db.deleted)
	}
	if res.GetDiscussion().GetStatus() != "hidden" {
		t.Fatalf("status = %s", res.GetDiscussion().GetStatus())
	}
}

func TestDeleteDiscussionReturnsThePost(t *testing.T) {
	db := &stubDB{post: &discussions.Post{PublicID: "abc", Status: "deleted", AuthorGone: true}}
	svc := NewDiscussionService(db)

	res, err := svc.DeleteDiscussion(context.Background(), &pb.DiscussionActionReq{
		AccountId: "acc-1",
		PublicId:  "abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !db.deleted {
		t.Fatal("delete was not called")
	}
	if !res.GetDiscussion().GetAuthorGone() || res.GetDiscussion().GetStatus() != "deleted" {
		t.Fatalf("discussion = %+v", res.GetDiscussion())
	}
}

type stubDB struct {
	created discussions.CreateInput
	post    *discussions.Post
	err     error
	deleted bool
	hidden  bool
}

func (s *stubDB) CreateDiscussion(_ context.Context, in discussions.CreateInput) (*discussions.Post, error) {
	s.created = in
	return s.post, s.err
}

func (s *stubDB) ListDiscussions(context.Context, discussions.ListInput) ([]*discussions.Post, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []*discussions.Post{s.post}, nil
}

func (s *stubDB) GetDiscussion(context.Context, string, string) (*discussions.Post, error) {
	return s.post, s.err
}

func (s *stubDB) ReplyToDiscussion(context.Context, discussions.ReplyInput) (*discussions.Reply, error) {
	if s.err != nil || s.post == nil || len(s.post.Replies) == 0 {
		return nil, s.err
	}
	return &s.post.Replies[0], nil
}

func (s *stubDB) LikeDiscussion(context.Context, string, string) (bool, error) {
	return true, s.err
}

func (s *stubDB) EditDiscussion(context.Context, string, string, string) (*discussions.Post, error) {
	return s.post, s.err
}

func (s *stubDB) DeleteDiscussion(context.Context, string, string) error {
	s.deleted = true
	return s.err
}

func (s *stubDB) HideDiscussion(context.Context, string, string) error {
	s.hidden = true
	return s.err
}

func (s *stubDB) Close() error { return nil }
