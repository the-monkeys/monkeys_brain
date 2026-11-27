package database

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

func TestPublicationTransitionsRefreshSearchVisibility(t *testing.T) {
	transitions := []struct {
		name string
		run  func(*elasticsearchStorage) (*esapi.Response, error)
	}{
		{"publish", func(es *elasticsearchStorage) (*esapi.Response, error) {
			return es.PublishBlogById(context.Background(), "visibility-test", PublishBlogFields{})
		}},
		{"schedule", func(es *elasticsearchStorage) (*esapi.Response, error) {
			return es.ScheduleBlogById(context.Background(), ScheduleBlogOptions{
				BlogID: "visibility-test", ScheduleTime: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
			})
		}},
		{"move to draft", func(es *elasticsearchStorage) (*esapi.Response, error) {
			return es.MoveBlogToDraft(context.Background(), "visibility-test")
		}},
	}
	for _, transition := range transitions {
		t.Run(transition.name, func(t *testing.T) {
			for _, status := range []int{http.StatusOK, http.StatusConflict} {
				t.Run(http.StatusText(status), func(t *testing.T) {
					calls := 0
					storage := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
						calls++
						if got := req.URL.Query().Get("refresh"); got != "true" {
							t.Errorf("publication must be searchable before success: refresh = %q, want true", got)
						}
						if got := req.URL.Query().Get("retry_on_conflict"); got != "3" {
							t.Errorf("concurrent autosave protection lost: retry_on_conflict = %q", got)
						}
						if req.Method != http.MethodPost || req.URL.Path != "/the_monkeys_blogs/_update/visibility-test" {
							t.Errorf("unexpected publication request: %s %s", req.Method, req.URL.Path)
						}
						return scopeResponse(status, `{}`), nil
					})
					_, err := transition.run(storage)
					if (err != nil) != (status != http.StatusOK) {
						t.Errorf("status %d returned error %v", status, err)
					}
					if calls != 1 {
						t.Errorf("expected one update request, got %d", calls)
					}
				})
			}
		})
	}
}
