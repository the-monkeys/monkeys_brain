package database

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	"github.com/the-monkeys/the_monkeys/common/homepage"
	"google.golang.org/grpc/metadata"
)

type homepageActivityTransport func(*http.Request) (*http.Response, error)

func (f homepageActivityTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type homepageActivityBody struct {
	io.Reader
	closed bool
}

func (b *homepageActivityBody) Close() error { b.closed = true; return nil }

func TestHomepageTrendingRejectsPartialSearchOnlyWithExactMarker(t *testing.T) {
	for _, selector := range [][]string{nil, {homepage.Version}, {"homepage-v2"}, {homepage.Version, homepage.Version}, {" homepage-v1"}} {
		for _, fault := range []struct {
			name, flags string
			partial     bool
		}{
			{"complete", `"timed_out":false,"_shards":{"failed":0},`, false},
			{"timeout", `"timed_out":true,"_shards":{"failed":0},`, true},
			{"failed shard", `"timed_out":false,"_shards":{"failed":1},`, true},
			{"missing search status", "", true},
			{"malformed search status", `"timed_out":"invalid","_shards":null,`, true},
		} {
			t.Run(fmt.Sprint(selector)+"/"+fault.name, func(t *testing.T) {
				body := &homepageActivityBody{Reader: strings.NewReader(`{` + fault.flags + `"aggregations":{"trending":{"buckets":[{"key":"eligible","doc_count":7,"likes":{"doc_count":2}}]}}}`)}
				client, err := elasticsearch.NewClient(elasticsearch.Config{
					Addresses: []string{"http://offline.invalid"}, DisableRetry: true,
					Transport: homepageActivityTransport(func(*http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: 200, Header: http.Header{"X-Elastic-Product": []string{"Elasticsearch"}}, Body: body}, nil
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{homepage.MetadataKey: selector})
				rows, err := (&ActivityDB{client: client}).GetTrendingBlogs(ctx, &pb.GetTrendingBlogsRequest{TimeRange: "1d", Limit: 100})
				marked := len(selector) == 1 && selector[0] == homepage.Version
				if marked && fault.partial {
					if err == nil || len(rows) != 0 {
						t.Fatalf("partial search returned rows=%v err=%v", rows, err)
					}
				} else if err != nil || len(rows) != 1 || rows[0].BlogId != "eligible" || rows[0].Score != 17 {
					t.Fatalf("rows=%v err=%v", rows, err)
				}
				if marked && !body.closed {
					t.Error("homepage activity response was not closed")
				}
			})
		}
	}
}
