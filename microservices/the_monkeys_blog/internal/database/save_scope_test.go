package database

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
	"go.uber.org/zap"
)

type saveScopeTransport func(*http.Request) (*http.Response, error)

func (f saveScopeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func scopeResponse(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"X-Elastic-Product": []string{"Elasticsearch"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func scopeStorage(t *testing.T, transport saveScopeTransport) *elasticsearchStorage {
	t.Helper()
	client, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses:    []string{"http://offline.invalid"},
		Transport:    transport,
		DisableRetry: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &elasticsearchStorage{client: client, log: zap.NewNop().Sugar()}
}

func TestSaveBlogPreservesAbsentScopeKeys(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stored   map[string]interface{}
		incoming map[string]interface{}
		want     map[string]interface{}
	}{
		{
			name:   "legacy edit keeps group and audience",
			stored: map[string]interface{}{"group_slug": "tea", "audience": "group_only"},
			want:   map[string]interface{}{"group_slug": "tea", "audience": "group_only"},
		},
		{
			name:     "audience-only edit keeps group",
			stored:   map[string]interface{}{"group_slug": "tea", "audience": "group_only"},
			incoming: map[string]interface{}{"audience": "public"},
			want:     map[string]interface{}{"group_slug": "tea", "audience": "public"},
		},
		{
			name:     "group-only edit keeps audience",
			stored:   map[string]interface{}{"group_slug": "tea", "audience": "group_only"},
			incoming: map[string]interface{}{"group_slug": "coffee"},
			want:     map[string]interface{}{"group_slug": "coffee", "audience": "group_only"},
		},
		{
			name:     "explicit detach keeps omitted audience",
			stored:   map[string]interface{}{"group_slug": "tea", "audience": "group_only"},
			incoming: map[string]interface{}{"group_slug": ""},
			want:     map[string]interface{}{"group_slug": "", "audience": "group_only"},
		},
		{
			name:     "explicit public detach",
			stored:   map[string]interface{}{"group_slug": "tea", "audience": "group_only"},
			incoming: map[string]interface{}{"group_slug": "", "audience": "public"},
			want:     map[string]interface{}{"group_slug": "", "audience": "public"},
		},
		{
			name:     "explicit group scope wins",
			stored:   map[string]interface{}{"group_slug": "tea", "audience": "public"},
			incoming: map[string]interface{}{"group_slug": "coffee", "audience": "group_only"},
			want:     map[string]interface{}{"group_slug": "coffee", "audience": "group_only"},
		},
		{name: "existing legacy public draft", stored: map[string]interface{}{}, want: map[string]interface{}{}},
		{name: "new legacy public draft", want: map[string]interface{}{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incoming := map[string]interface{}{"blog_id": "draft1", "title": "Updated title", "is_draft": true}
			for key, value := range tc.incoming {
				incoming[key] = value
			}
			original, err := json.Marshal(incoming)
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]interface{}
			writes := 0
			es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/the_monkeys_blogs/_doc/draft1" {
					return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				switch req.Method {
				case http.MethodGet:
					if tc.stored == nil {
						return scopeResponse(http.StatusNotFound, `{"found":false}`), nil
					}
					body, err := json.Marshal(map[string]interface{}{"_source": tc.stored, "_seq_no": 4, "_primary_term": 2})
					if err != nil {
						return nil, err
					}
					return scopeResponse(http.StatusOK, string(body)), nil
				case http.MethodPut:
					writes++
					if err := json.NewDecoder(req.Body).Decode(&saved); err != nil {
						return nil, err
					}
					return scopeResponse(http.StatusCreated, `{"result":"updated"}`), nil
				default:
					return nil, fmt.Errorf("unexpected method %s", req.Method)
				}
			})

			res, err := es.SaveBlog(context.Background(), incoming)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if writes != 1 || saved["title"] != "Updated title" || saved["is_draft"] != true {
				t.Fatalf("writes=%d, saved=%+v", writes, saved)
			}
			gotScope := make(map[string]interface{})
			for _, key := range []string{"group_slug", "audience"} {
				if value, exists := saved[key]; exists {
					gotScope[key] = value
				}
			}
			if !reflect.DeepEqual(gotScope, tc.want) {
				t.Fatalf("saved scope=%+v, want %+v", gotScope, tc.want)
			}
			after, err := json.Marshal(incoming)
			if err != nil || string(after) != string(original) {
				t.Fatalf("caller document mutated: before=%s after=%s err=%v", original, after, err)
			}
		})
	}
}

func TestSaveBlogFailsClosedWhenScopeCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{name: "unavailable", code: http.StatusServiceUnavailable, body: `{"error":"unavailable"}`},
		{name: "malformed source", code: http.StatusOK, body: `{"_source":`},
		{name: "missing source", code: http.StatusOK, body: `{"_seq_no":4,"_primary_term":2}`},
		{name: "missing version", code: http.StatusOK, body: `{"_source":{"group_slug":"tea","audience":"group_only"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return scopeResponse(tc.code, tc.body), nil
				}
				writes++
				return scopeResponse(http.StatusCreated, `{"result":"updated"}`), nil
			})
			res, err := es.SaveBlog(context.Background(), map[string]interface{}{"blog_id": "draft1", "title": "Updated title"})
			if res != nil {
				defer res.Body.Close()
			}
			if err == nil || writes != 0 {
				t.Fatalf("error=%v writes=%d, want refusal without replacing stored scope", err, writes)
			}
		})
	}
}

func TestSaveBlogRejectsConcurrentScopeChange(t *testing.T) {
	for _, existing := range []bool{true, false} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			clobbered := false
			es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					if !existing {
						return scopeResponse(http.StatusNotFound, `{"found":false}`), nil
					}
					return scopeResponse(http.StatusOK, `{"_source":{"group_slug":"tea","audience":"public"},"_seq_no":4,"_primary_term":2}`), nil
				}
				// Another writer changed scope after the read (or created the draft).
				query := req.URL.Query()
				if existing && query.Get("if_seq_no") == "4" && query.Get("if_primary_term") == "2" ||
					!existing && query.Get("op_type") == "create" {
					return scopeResponse(http.StatusConflict, `{"error":{"type":"version_conflict_engine_exception"}}`), nil
				}
				clobbered = true
				return scopeResponse(http.StatusCreated, `{"result":"updated"}`), nil
			})
			res, err := es.SaveBlog(context.Background(), map[string]interface{}{"blog_id": "draft1", "title": "Updated title"})
			if res != nil {
				defer res.Body.Close()
			}
			if err == nil || res == nil || res.StatusCode != http.StatusConflict || clobbered {
				t.Fatalf("response=%v error=%v clobbered=%t, want conflict without overwriting concurrent scope", res, err, clobbered)
			}
		})
	}
}
