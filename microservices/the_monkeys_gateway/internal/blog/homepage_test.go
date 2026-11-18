package blog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	activitypb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"
)

func homepageRow(id string) map[string]interface{} {
	return map[string]interface{}{"blog_id": id, "title": "Authored title", "homepage_eligibility": map[string]interface{}{"eligible": true, "version": "homepage-v1"}}
}
func homepageRPC(t *testing.T, reply func(context.Context, *blogpb.BlogListReq) (any, error)) blogpb.BlogServiceClient {
	return blogpb.NewBlogServiceClient(metadataTransport{open: func(ctx context.Context) (grpc.ClientStream, error) {
		md, _ := metadata.FromOutgoingContext(ctx)
		if !reflect.DeepEqual(md.Get("x-monkeys-homepage-policy"), []string{"homepage-v1"}) {
			t.Errorf("selector=%v", md)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing bounded deadline")
		}
		var req *blogpb.BlogListReq
		received := false
		return &metadataStream{ctx: ctx, send: func(r *blogpb.BlogListReq) { req = r }, recv: func(out *anypb.Any) error {
			if received {
				return io.EOF
			}
			received = true
			if req.AccountId != "" || req.IsDraft || len(req.Tags) > 0 {
				t.Fatal("homepage is public only", req)
			}
			payload, err := reply(ctx, req)
			if err != nil {
				return err
			}
			out.Value, err = json.Marshal(payload)
			return err
		}}, nil
	}})
}
func homepageReply(rows []map[string]interface{}, more bool) map[string]interface{} {
	return map[string]interface{}{"blogs": rows, "has_more": more, "homepage_policy_version": "homepage-v1"}
}
func serveHomepage(asc *BlogServiceClient, query string, ctx context.Context) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/v2/blog/homepage", asc.GetHomepageBlogs)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/blog/homepage"+query, nil).WithContext(ctx)
	req.Header.Set("x-monkeys-homepage-policy", "client-controlled-v9")
	r.ServeHTTP(w, req)
	return w
}

func TestHomepageLatestAndBounds(t *testing.T) {
	calls := 0
	asc := &BlogServiceClient{Client: homepageRPC(t, func(_ context.Context, req *blogpb.BlogListReq) (any, error) {
		calls++
		if req.Limit != 12 || req.Offset != 0 || len(req.BlogIds) != 0 {
			t.Fatal(req)
		}
		rows := make([]map[string]interface{}, 12)
		for i := range rows {
			rows[i] = homepageRow(fmt.Sprint(i))
		}
		return homepageReply(rows, true), nil
	})}
	w := serveHomepage(asc, "?account_id=ignored&blog_ids=ignored", context.Background())
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["homepage_policy_version"] != "homepage-v1" || got["has_more"] != true || got["total_blogs"] != nil {
		t.Fatal(got)
	}
	for _, q := range []string{"?limit=0", "?limit=51", "?limit=invalid", "?offset=-1", "?offset=1001", "?offset=bad", "?sort=unknown", "?limit=4294967296"} {
		if w := serveHomepage(asc, q, context.Background()); w.Code != 400 {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("invalid input reached RPC: %d", calls)
	}
}

func TestHomepageRejectsUnmarkedAndIneligibleServicePayloads(t *testing.T) {
	for _, p := range []any{
		map[string]any{"blogs": []any{homepageRow("old")}, "total_blogs": 10},
		map[string]any{"blogs": []any{}, "has_more": false, "homepage_policy_version": "homepage-v2"},
		homepageReply([]map[string]interface{}{{"blog_id": "unmarked"}}, false),
		homepageReply([]map[string]interface{}{{"blog_id": "bad", "homepage_eligibility": map[string]any{"eligible": false, "version": "homepage-v1"}}}, false),
		map[string]any{"blogs": nil, "has_more": false, "homepage_policy_version": "homepage-v1"},
	} {
		asc := &BlogServiceClient{Client: homepageRPC(t, func(context.Context, *blogpb.BlogListReq) (any, error) { return p, nil })}
		w := serveHomepage(asc, "", context.Background())
		if w.Code != 503 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

type homepageActivity struct {
	activitypb.ActivityServiceClient
	get func(context.Context, *activitypb.GetTrendingBlogsRequest) (*activitypb.GetTrendingBlogsResponse, error)
}

func (a homepageActivity) GetTrendingBlogs(c context.Context, r *activitypb.GetTrendingBlogsRequest, _ ...grpc.CallOption) (*activitypb.GetTrendingBlogsResponse, error) {
	return a.get(c, r)
}

func TestHomepageTrendingWidensBeforeEligiblePagination(t *testing.T) {
	windows := []string{}
	limits := []int32{}
	rpcIDs := []int{}
	asc := &BlogServiceClient{ActivityCli: homepageActivity{get: func(ctx context.Context, req *activitypb.GetTrendingBlogsRequest) (*activitypb.GetTrendingBlogsResponse, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("activity deadline missing")
		}
		if req.AccountId != "" || req.TimeRange == "24h" {
			t.Fatal("personalized or cached request", req)
		}
		windows = append(windows, req.TimeRange)
		limits = append(limits, req.Limit)
		out := &activitypb.GetTrendingBlogsResponse{StatusCode: 200}
		for i := 0; i < int(req.Limit); i++ {
			out.Blogs = append(out.Blogs, &activitypb.TrendingBlog{BlogId: fmt.Sprint(i), Views: int64(i), Score: float64(i)})
		}
		return out, nil
	}}}
	asc.Client = homepageRPC(t, func(_ context.Context, req *blogpb.BlogListReq) (any, error) {
		rpcIDs = append(rpcIDs, len(req.BlogIds))
		if req.Limit != 1 || req.Offset != 1 {
			t.Fatal("eligibility pagination changed", req)
		}
		if len(req.BlogIds) == 100 {
			return homepageReply([]map[string]interface{}{}, false), nil
		}
		return homepageReply([]map[string]interface{}{homepageRow("150")}, true), nil
	})
	w := serveHomepage(asc, "?sort=trending&limit=1&offset=1&account_id=private", context.Background())
	if w.Code != 200 || !reflect.DeepEqual(limits, []int32{100, 200}) || !reflect.DeepEqual(rpcIDs, []int{100, 200}) || !reflect.DeepEqual(windows, []string{"1d", "1d"}) {
		t.Fatalf("status=%d body=%s limits=%v ids=%v windows=%v", w.Code, w.Body.String(), limits, rpcIDs, windows)
	}
	var got struct {
		Blogs []map[string]interface{} `json:"blogs"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Blogs[0]["views"] != float64(150) {
		t.Fatal("activity enrichment lost", got)
	}
}

func TestHomepageTrendingFallbackAndEmptyIDs(t *testing.T) {
	windows := []string{}
	asc := &BlogServiceClient{ActivityCli: homepageActivity{get: func(_ context.Context, r *activitypb.GetTrendingBlogsRequest) (*activitypb.GetTrendingBlogsResponse, error) {
		windows = append(windows, r.TimeRange)
		return &activitypb.GetTrendingBlogsResponse{StatusCode: 200}, nil
	}}}
	w := serveHomepage(asc, "?sort=trending", context.Background())
	if w.Code != 200 || !reflect.DeepEqual(windows, []string{"1d", "14d", "90d", "1y"}) {
		t.Fatalf("%d %s windows=%v", w.Code, w.Body.String(), windows)
	}
	var got map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["has_more"] != false || !reflect.DeepEqual(got["blogs"], []interface{}{}) {
		t.Fatal(got)
	}
}

func TestHomepageTrendingCeilingAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if canceled {
			cancel()
		}
		calls := 0
		asc := &BlogServiceClient{ActivityCli: homepageActivity{get: func(ctx context.Context, r *activitypb.GetTrendingBlogsRequest) (*activitypb.GetTrendingBlogsResponse, error) {
			calls++
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out := &activitypb.GetTrendingBlogsResponse{StatusCode: 200}
			for i := 0; i < int(r.Limit); i++ {
				out.Blogs = append(out.Blogs, &activitypb.TrendingBlog{BlogId: fmt.Sprint(i)})
			}
			return out, nil
		}}}
		asc.Client = homepageRPC(t, func(context.Context, *blogpb.BlogListReq) (any, error) {
			return homepageReply([]map[string]interface{}{}, false), nil
		})
		w := serveHomepage(asc, "?sort=trending", ctx)
		if w.Code != 503 || (!canceled && calls != 100) {
			t.Fatalf("canceled=%v status=%d calls=%d", canceled, w.Code, calls)
		}
	}
}

func TestHomepageRegisteredAsPublicRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	cfg := &config.Config{Microservices: config.Microservices{TheMonkeysBlog: "localhost", BlogPort: 1, TheMonkeysActivity: "localhost", ActivityPort: 1}}
	asc := RegisterBlogRouter(router, cfg, nil, nil, zap.NewNop().Sugar())
	asc.Client = homepageRPC(t, func(context.Context, *blogpb.BlogListReq) (any, error) {
		return homepageReply([]map[string]interface{}{}, false), nil
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/blog/homepage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("public homepage route status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHomepageTrendingFallbackAfterIneligibleWindow(t *testing.T) {
	windows := []string{}
	asc := &BlogServiceClient{ActivityCli: homepageActivity{get: func(_ context.Context, r *activitypb.GetTrendingBlogsRequest) (*activitypb.GetTrendingBlogsResponse, error) {
		windows = append(windows, r.TimeRange)
		id := "thin"
		if r.TimeRange == "14d" {
			id = "eligible"
		}
		return &activitypb.GetTrendingBlogsResponse{StatusCode: 200, Blogs: []*activitypb.TrendingBlog{{BlogId: id}}}, nil
	}}}
	asc.Client = homepageRPC(t, func(_ context.Context, r *blogpb.BlogListReq) (any, error) {
		if r.BlogIds[0] == "thin" {
			return homepageReply([]map[string]interface{}{}, false), nil
		}
		return homepageReply([]map[string]interface{}{homepageRow("eligible")}, false), nil
	})
	w := serveHomepage(asc, "?sort=trending&limit=1", context.Background())
	var got homepageCatalog
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.TimeRange != "14d" || len(got.Blogs) != 1 || !reflect.DeepEqual(windows, []string{"1d", "14d"}) {
		t.Fatalf("status=%d body=%s windows=%v", w.Code, w.Body.String(), windows)
	}
}

func TestHomepageTrendingPastEndKeepsWindow(t *testing.T) {
	activityCalls := 0
	metadataCalls := 0
	asc := &BlogServiceClient{ActivityCli: homepageActivity{get: func(context.Context, *activitypb.GetTrendingBlogsRequest) (*activitypb.GetTrendingBlogsResponse, error) {
		activityCalls++
		return &activitypb.GetTrendingBlogsResponse{StatusCode: 200, Blogs: []*activitypb.TrendingBlog{{BlogId: "eligible"}}}, nil
	}}}
	asc.Client = homepageRPC(t, func(_ context.Context, r *blogpb.BlogListReq) (any, error) {
		metadataCalls++
		if r.Offset == 0 {
			return homepageReply([]map[string]interface{}{homepageRow("eligible")}, false), nil
		}
		return homepageReply([]map[string]interface{}{}, false), nil
	})
	w := serveHomepage(asc, "?sort=trending&offset=1", context.Background())
	var got homepageCatalog
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.TimeRange != "24h" || len(got.Blogs) != 0 || activityCalls != 1 || metadataCalls != 2 {
		t.Fatalf("status=%d body=%s activity=%d metadata=%d", w.Code, w.Body.String(), activityCalls, metadataCalls)
	}
}

func TestHomepageLatestStreamFailureAndCancellation(t *testing.T) {
	for _, phase := range []string{"open", "receive", "after envelope"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client := blogpb.NewBlogServiceClient(metadataTransport{open: func(ctx context.Context) (grpc.ClientStream, error) {
				if phase == "open" {
					return nil, fmt.Errorf("unavailable")
				}
				return &metadataStream{ctx: ctx, send: func(*blogpb.BlogListReq) {}, recv: func(out *anypb.Any) error {
					calls++
					if phase == "receive" || calls > 1 {
						cancel()
						if ctx.Err() == nil {
							t.Error("cancellation did not reach RPC")
						}
						return fmt.Errorf("interrupted")
					}
					out.Value, _ = json.Marshal(homepageReply([]map[string]interface{}{homepageRow("eligible")}, false))
					return nil
				}}, nil
			}})
			w := serveHomepage(&BlogServiceClient{Client: client}, "", ctx)
			if w.Code != 503 || strings.Contains(w.Body.String(), "eligible") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHomepageHeaderDoesNotOptOrdinaryFeedsIntoPolicy(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		client := blogpb.NewBlogServiceClient(metadataTransport{open: func(ctx context.Context) (grpc.ClientStream, error) {
			md, _ := metadata.FromOutgoingContext(ctx)
			if len(md.Get("x-monkeys-homepage-policy")) != 0 {
				t.Fatal("client header selected homepage policy on an ordinary feed")
			}
			received := false
			return &metadataStream{ctx: ctx, send: func(*blogpb.BlogListReq) {}, recv: func(out *anypb.Any) error {
				if received {
					return io.EOF
				}
				received = true
				out.Value = []byte(`{"blogs":[],"total_blogs":0}`)
				return nil
			}}, nil
		}})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, "/api/v2/blog/meta-feed", strings.NewReader(`{"tags":[]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("x-monkeys-homepage-policy", "homepage-v1")
		asc := &BlogServiceClient{Client: client, log: zap.NewNop().Sugar()}
		if method == http.MethodGet {
			asc.GetsMetaFeed(c)
		} else {
			asc.GetFeedPostsMeta(c)
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"total_blogs":0`) {
			t.Fatalf("ordinary feed changed: %d %s", w.Code, w.Body.String())
		}
	}
}
