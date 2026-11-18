package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/database"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

type homepageStore struct {
	database.ElasticsearchStorage
	calls  int
	legacy int
	fail   error
}

func (s *homepageStore) GetHomepageBlogs(ctx context.Context, limit, offset int, ids []string) ([]map[string]interface{}, bool, error) {
	s.calls++
	if limit != 2 || offset != 3 || len(ids) != 1 || ids[0] != "ranked" {
		return nil, false, errors.New("wrong homepage arguments")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return []map[string]interface{}{{"blog_id": "ranked"}}, true, s.fail
}
func (s *homepageStore) GetAllPublishedBlogsMetadata(context.Context, int, int) ([]map[string]interface{}, int, error) {
	s.legacy++
	return []map[string]interface{}{}, 9, nil
}

type homepageServerStream struct {
	grpc.ServerStream
	ctx     context.Context
	payload *anypb.Any
}

func (s *homepageServerStream) Context() context.Context { return s.ctx }
func (s *homepageServerStream) Send(p *anypb.Any) error  { s.payload = p; return nil }

func TestHomepageMetadataExactOptIn(t *testing.T) {
	for _, selector := range [][]string{nil, {"homepage-v1"}, {"homepage-v2"}, {"homepage-v1", "homepage-v1"}, {" homepage-v1"}} {
		store := &homepageStore{}
		svc := &BlogService{osClient: store, logger: zap.NewNop().Sugar()}
		ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{"x-monkeys-homepage-policy": selector})
		stream := &homepageServerStream{ctx: ctx}
		// Tags suppress legacy asynchronous activity fan-out; non-selected callers keep legacy behavior.
		req := &pb.BlogListReq{Limit: 2, Offset: 3, BlogIds: []string{"ranked"}}
		exact := len(selector) == 1 && selector[0] == "homepage-v1"
		if !exact {
			req.AccountId = "legacy"
			req.Tags = []string{"legacy"}
			store.ElasticsearchStorage = &legacyTagStore{}
		}
		err := svc.GetBlogsMetadata(req, stream)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]interface{}
		if err := json.Unmarshal(stream.payload.Value, &got); err != nil {
			t.Fatal(err)
		}
		if exact {
			if store.calls != 1 || got["homepage_policy_version"] != "homepage-v1" || got["has_more"] != true || got["total_blogs"] != nil {
				t.Fatalf("calls=%d response=%v", store.calls, got)
			}
		} else if store.calls != 0 || got["total_blogs"] != float64(7) || got["homepage_policy_version"] != nil {
			t.Fatalf("legacy response changed: %v", got)
		}
	}
}

type legacyTagStore struct{ database.ElasticsearchStorage }

func (*legacyTagStore) GetBlogsMetadataByTags(context.Context, []string, bool, int32, int32) ([]map[string]interface{}, int, error) {
	return []map[string]interface{}{}, 7, nil
}

func TestHomepageMetadataScopeBoundsAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		req   *pb.BlogListReq
		store database.ElasticsearchStorage
		code  codes.Code
	}{
		{"account", &pb.BlogListReq{Limit: 2, AccountId: "private"}, &homepageStore{}, codes.InvalidArgument},
		{"tags", &pb.BlogListReq{Limit: 2, Tags: []string{"tag"}}, &homepageStore{}, codes.InvalidArgument},
		{"draft", &pb.BlogListReq{Limit: 2, IsDraft: true}, &homepageStore{}, codes.InvalidArgument},
		{"scheduled", &pb.BlogListReq{Limit: 2, IsScheduled: true}, &homepageStore{}, codes.InvalidArgument},
		{"group selector", &pb.BlogListReq{Limit: 2, GroupSlug: "private-group"}, &homepageStore{}, codes.InvalidArgument},
		{"group-only", &pb.BlogListReq{Limit: 2, IncludeGroupOnly: true}, &homepageStore{}, codes.InvalidArgument},
		{"limit", &pb.BlogListReq{Limit: 51}, &homepageStore{}, codes.InvalidArgument},
		{"offset", &pb.BlogListReq{Limit: 2, Offset: 1001}, &homepageStore{}, codes.InvalidArgument},
		{"old storage", &pb.BlogListReq{Limit: 2}, &legacyTagStore{}, codes.Unavailable},
		{"storage failure", &pb.BlogListReq{Limit: 2, Offset: 3, BlogIds: []string{"ranked"}}, &homepageStore{fail: errors.New("storage failed")}, codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &BlogService{osClient: tc.store, logger: zap.NewNop().Sugar()}
			stream := &homepageServerStream{ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-monkeys-homepage-policy", "homepage-v1"))}
			if err := svc.GetBlogsMetadata(tc.req, stream); status.Code(err) != tc.code || stream.payload != nil {
				t.Fatalf("err=%v payload=%v", err, stream.payload)
			}
		})
	}
}
