package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	"github.com/the-monkeys/the_monkeys/common/homepage"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_activity/internal/database"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
)

func TestHomepageTrendingBypassesOrdinaryCacheAndPropagatesSearchFailure(t *testing.T) {
	var searches atomic.Int32
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			return // Existing indices; the real constructor performs no writes.
		}
		searches.Add(1)
		_, _ = w.Write([]byte(`{"timed_out":true,"_shards":{"failed":0},"aggregations":{"trending":{"buckets":[{"key":"partial","doc_count":7}]}}}`))
	}))
	defer search.Close()
	db, err := database.NewActivityDB(&config.Config{Opensearch: config.Opensearch{Host: search.URL}}, zap.NewNop().Sugar())
	if err != nil {
		t.Fatal(err)
	}
	svc := &ActivityServiceServer{db: db, logger: zap.NewNop().Sugar(), trendingCache: []*pb.TrendingBlog{{BlogId: "ordinary-cached"}}}
	for _, selector := range [][]string{nil, {"homepage-v2"}, {homepage.Version, homepage.Version}, {homepage.Version}, nil} {
		before := searches.Load()
		ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{homepage.MetadataKey: selector})
		resp, err := svc.GetTrendingBlogs(ctx, &pb.GetTrendingBlogsRequest{TimeRange: "24h", Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		marked := len(selector) == 1 && selector[0] == homepage.Version
		if marked {
			if resp.StatusCode != 500 || resp.Error == nil || len(resp.Blogs) != 0 || searches.Load() != before+1 {
				t.Fatalf("homepage accepted cached/partial ranking: %v searches=%d", resp, searches.Load()-before)
			}
		} else if resp.StatusCode != 200 || len(resp.Blogs) != 1 || resp.Blogs[0].BlogId != "ordinary-cached" || searches.Load() != before {
			t.Fatalf("ordinary cache behavior changed: %v searches=%d", resp, searches.Load()-before)
		}
	}
}
