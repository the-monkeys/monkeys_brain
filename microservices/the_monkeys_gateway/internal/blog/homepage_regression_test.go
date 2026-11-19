package blog

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	activitypb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/common/homepage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/anypb"
)

type homepageEnvelopeServer struct {
	blogpb.UnimplementedBlogServiceServer
	payload []byte
}

func (s *homepageEnvelopeServer) GetBlogsMetadata(_ *blogpb.BlogListReq, stream grpc.ServerStreamingServer[anypb.Any]) error {
	return stream.Send(&anypb.Any{Value: s.payload})
}

func TestHomepageLargeEnvelopeUsesScopedReceiveLimit(t *testing.T) {
	rows := make([]map[string]interface{}, 50)
	for i := range rows {
		rows[i] = homepageRow(string(rune('A' + i)))
		rows[i]["first_paragraph"] = strings.Repeat("knowledge ", 9<<10)
	}
	payload, err := json.Marshal(homepageReply(rows, false))
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	blogpb.RegisterBlogServiceServer(server, &homepageEnvelopeServer{payload: payload})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///homepage-test",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := blogpb.NewBlogServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w := serveHomepage(&BlogServiceClient{Client: client}, "?limit=50", ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("valid envelope over 4 MiB returned status=%d body=%s", w.Code, w.Body.String())
	}
	var page homepageCatalog
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Blogs) != 50 {
		t.Fatalf("homepage rows=%d err=%v", len(page.Blogs), err)
	}
	// The same client must retain its ordinary receive limit on unrelated calls.
	stream, err := client.GetBlogsMetadata(ctx, &blogpb.BlogListReq{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("ordinary RPC receive limit changed: %v", err)
	}
}

func TestHomepageTrendingSendsExactActivityPolicyMarker(t *testing.T) {
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(homepage.MetadataKey, "untrusted", "trace-id", "preserved"))
	calls := 0
	asc := &BlogServiceClient{ActivityCli: homepageActivity{get: func(ctx context.Context, _ *activitypb.GetTrendingBlogsRequest) (*activitypb.GetTrendingBlogsResponse, error) {
		calls++
		md, _ := metadata.FromOutgoingContext(ctx)
		if !reflect.DeepEqual(md.Get(homepage.MetadataKey), []string{homepage.Version}) || !reflect.DeepEqual(md.Get("trace-id"), []string{"preserved"}) {
			t.Errorf("activity metadata=%v", md)
		}
		return &activitypb.GetTrendingBlogsResponse{StatusCode: 200}, nil
	}}}
	if w := serveHomepage(asc, "?sort=trending", ctx); w.Code != http.StatusOK || calls != 4 {
		t.Fatalf("status=%d calls=%d", w.Code, calls)
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	if !reflect.DeepEqual(md.Get(homepage.MetadataKey), []string{"untrusted"}) {
		t.Fatal("homepage mutated its caller's metadata")
	}
}

func TestHomepageTrendingActivityFailureDoesNotFallBack(t *testing.T) {
	calls := 0
	asc := &BlogServiceClient{ActivityCli: homepageActivity{get: func(context.Context, *activitypb.GetTrendingBlogsRequest) (*activitypb.GetTrendingBlogsResponse, error) {
		calls++
		return &activitypb.GetTrendingBlogsResponse{StatusCode: 500, Error: &activitypb.Error{Error: "database_error"}}, nil
	}}}
	w := serveHomepage(asc, "?sort=trending", context.Background())
	if w.Code != http.StatusServiceUnavailable || calls != 1 {
		t.Fatalf("partial activity failure triggered fallback: status=%d calls=%d", w.Code, calls)
	}
}
