package blog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	userpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/user_service"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// Keep generated RPC clients real; substitute only their in-memory transport.
type metadataTransport struct {
	grpc.ClientConnInterface
	open   func(context.Context) (grpc.ClientStream, error)
	invoke func(string, any, any) error
}

func (tr metadataTransport) NewStream(ctx context.Context, _ *grpc.StreamDesc, method string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
	if method != blogpb.BlogService_GetBlogsMetadata_FullMethodName {
		panic("unexpected RPC: " + method)
	}
	return tr.open(ctx)
}
func (tr metadataTransport) Invoke(_ context.Context, method string, req, reply any, _ ...grpc.CallOption) error {
	return tr.invoke(method, req, reply)
}

type metadataStream struct {
	grpc.ClientStream
	ctx  context.Context
	send func(*blogpb.BlogListReq)
	recv func(*anypb.Any) error
}

func (s *metadataStream) Context() context.Context { return s.ctx }
func (s *metadataStream) CloseSend() error         { return nil }
func (s *metadataStream) SendMsg(req any) error    { s.send(req.(*blogpb.BlogListReq)); return nil }
func (s *metadataStream) RecvMsg(reply any) error  { return s.recv(reply.(*anypb.Any)) }

func serveMetadataRequest(asc *BlogServiceClient, method, query string, ctx context.Context) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/v2/blog/meta-feed"+query, strings.NewReader(`{"tags":["go"]}`)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("accountId", "reader")
	if method == http.MethodPost {
		asc.GetFeedPostsMeta(c)
	} else {
		asc.GetsMetaFeed(c)
	}
	return w
}

func TestMetadataFeedPageBounds(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, tc := range []struct {
			name, query string
			want        int32
		}{
			{"default", "", 500}, {"invalid default", "?limit=bad&offset=7", 500},
			{"small", "?limit=2&offset=7", 2}, {"normal", "?limit=500&offset=7", 500},
			{"maximum", "?limit=1000&offset=7", 1000}, {"over maximum", "?limit=1001&offset=7", 1000},
			{"review reproduction", "?limit=2147483647&offset=0", 1000},
			{"before int32 conversion", "?limit=4294967296&offset=7", 1000},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				calls, reads, enrichments := 0, 0, 0
				tr := metadataTransport{open: func(ctx context.Context) (grpc.ClientStream, error) {
					calls++
					return &metadataStream{ctx: ctx, send: func(req *blogpb.BlogListReq) {
						wantOffset := int32(7)
						if tc.name == "default" || tc.name == "review reproduction" {
							wantOffset = 0
						}
						if req.Limit != tc.want || req.Offset != wantOffset {
							t.Errorf("RPC limit/offset=%d/%d; want %d/%d", req.Limit, req.Offset, tc.want, wantOffset)
						}
						if method == http.MethodPost && !reflect.DeepEqual(req.Tags, []string{"go"}) {
							t.Error("tags changed")
						}
						if method == http.MethodGet && req.AccountId != "reader" {
							t.Error("account tracking changed")
						}
						if req.ClientInfo == nil {
							t.Error("client metadata missing")
						}
					}, recv: func(reply *anypb.Any) error {
						reads++
						if reads > 1 {
							return io.EOF
						}
						reply.Value = []byte(`{"total_blogs":20000,"blogs":[{"blog_id":"post-7","title":"Authored H1","first_paragraph":"Intro"}]}`)
						return nil
					}}, nil
				}, invoke: func(method string, req, reply any) error {
					if req.(*userpb.BookMarkReq).BlogId != "post-7" {
						t.Fatal("wrong enrichment ID")
					}
					switch method {
					case userpb.UserService_GetLikeCounts_FullMethodName, userpb.UserService_GetBookMarkCounts_FullMethodName:
						enrichments++
						reply.(*userpb.CountResp).Count = 3
					default:
						t.Fatalf("unexpected RPC %s", method)
					}
					return nil
				}}
				asc := &BlogServiceClient{Client: blogpb.NewBlogServiceClient(tr), UserCli: &user_service.UserServiceClient{Client: userpb.NewUserServiceClient(tr)}, log: zap.NewNop().Sugar()}
				w := serveMetadataRequest(asc, method, tc.query, context.Background())
				var got map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"total_blogs": float64(20000), "blogs": []any{map[string]any{"blog_id": "post-7", "title": "Authored H1", "first_paragraph": "Intro", "like_count": float64(3), "bookmark_count": float64(3)}}}
				if w.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
					t.Fatalf("status=%d response=%v; want %v", w.Code, got, want)
				}
				if calls != 1 || reads != 2 || enrichments != 2 {
					t.Fatalf("calls=%d reads=%d enrichments=%d", calls, reads, enrichments)
				}
			})
		}
	}
}

func TestMetadataFeedRequestCancellation(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, phase := range []string{"opening", "receiving"} {
			t.Run(method+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				calls, reads := 0, 0
				checkCanceled := func(rpcCtx context.Context) error {
					cancel() // Models the server canceling the HTTP request on disconnect.
					if !errors.Is(rpcCtx.Err(), context.Canceled) {
						t.Error("HTTP disconnect did not cancel metadata RPC")
					}
					return status.Error(codes.Canceled, "request disconnected")
				}
				tr := metadataTransport{open: func(rpcCtx context.Context) (grpc.ClientStream, error) {
					calls++
					wantDeadline, _ := ctx.Deadline()
					if deadline, ok := rpcCtx.Deadline(); !ok || !deadline.Equal(wantDeadline) {
						t.Error("HTTP deadline did not reach metadata RPC")
					}
					if phase == "opening" {
						return nil, checkCanceled(rpcCtx)
					}
					return &metadataStream{ctx: rpcCtx, send: func(*blogpb.BlogListReq) {}, recv: func(*anypb.Any) error {
						reads++
						return checkCanceled(rpcCtx)
					}}, nil
				}}
				asc := &BlogServiceClient{Client: blogpb.NewBlogServiceClient(tr), log: zap.NewNop().Sugar()}
				w := serveMetadataRequest(asc, method, "?limit=2", ctx)
				wantReads := 0
				if phase == "receiving" {
					wantReads = 1
				}
				if w.Code != http.StatusInternalServerError || calls != 1 || reads != wantReads {
					t.Fatalf("status=%d calls=%d reads=%d", w.Code, calls, reads)
				}
			})
		}
	}
}
