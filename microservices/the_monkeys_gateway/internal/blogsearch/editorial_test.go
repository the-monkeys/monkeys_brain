package blogsearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
	"go.uber.org/zap"
)

type editorialTransport func(*http.Request) (*http.Response, error)

func (f editorialTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSearchAndSuggestPassOptionalCanonicalWithoutChangingLegacySlug(t *testing.T) {
	for _, suggest := range []bool{false, true} {
		for _, present := range []bool{false, true} {
			name := map[bool]string{false: "search", true: "suggest"}[suggest] + map[bool]string{false: "/legacy", true: "/canonical"}[present]
			t.Run(name, func(t *testing.T) {
				source := map[string]interface{}{"blog_id": "1uvwek", "title": "Grafana tutorial", "slug": "full-legacy-stored-title-1uvwek"}
				if present {
					source["canonical_slug"] = "explicit-canonical-1uvwek"
				}
				body, _ := json.Marshal(map[string]interface{}{"hits": map[string]interface{}{"total": map[string]interface{}{"value": 1}, "hits": []interface{}{map[string]interface{}{"_source": source, "_score": 2}}}})
				es, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{"http://offline.invalid"}, DisableRetry: true, Transport: editorialTransport(func(req *http.Request) (*http.Response, error) {
					if !strings.Contains(req.URL.Query().Get("_source_includes"), "canonical_slug") {
						t.Error("canonical_slug missing from search projection")
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"X-Elastic-Product": []string{"Elasticsearch"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				})})
				if err != nil {
					t.Fatal(err)
				}
				client := &Client{es: es, log: zap.NewNop().Sugar()}
				var result *SearchResult
				if suggest {
					result, err = client.Suggest(context.Background(), SuggestOpts{Query: "grafana", Limit: 5})
				} else {
					result, err = client.Search(context.Background(), SearchOpts{Query: "grafana", Limit: 5})
				}
				if err != nil || len(result.Hits) != 1 {
					t.Fatalf("result=%v err=%v", result, err)
				}
				raw, _ := json.Marshal(result.Hits[0])
				var hit map[string]interface{}
				if err := json.Unmarshal(raw, &hit); err != nil {
					t.Fatal(err)
				}
				got, exists := hit["canonical_slug"]
				if exists != present || present && got != "explicit-canonical-1uvwek" {
					t.Errorf("canonical=%v present=%t", got, exists)
				}
				if hit["slug"] != "full-legacy-stored-title-1uvwek" {
					t.Fatalf("legacy slug changed: %v", hit)
				}
			})
		}
	}
}
