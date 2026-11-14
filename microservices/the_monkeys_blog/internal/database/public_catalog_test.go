package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type catalogCall func(*elasticsearchStorage) ([]map[string]interface{}, int, error)

var catalogCalls = map[string]catalogCall{
	"published": func(es *elasticsearchStorage) ([]map[string]interface{}, int, error) {
		return es.GetAllPublishedBlogsMetadata(context.Background(), 2, 4)
	},
	"tags": func(es *elasticsearchStorage) ([]map[string]interface{}, int, error) {
		return es.GetBlogsMetadataByTags(context.Background(), []string{" Go "}, false, 2, 4)
	},
}

// All requests terminate in this in-memory transport; no network transport is used.
func catalogStorage(t *testing.T, body string, inspect func(map[string]interface{})) *elasticsearchStorage {
	t.Helper()
	return scopeStorage(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "offline.invalid" || req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/_search") {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		var query map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&query); err != nil {
			t.Fatal(err)
		}
		if inspect != nil {
			inspect(query)
		}
		return scopeResponse(http.StatusOK, body), nil
	})
}

func callCatalog(t *testing.T, call catalogCall, es *elasticsearchStorage) (rows []map[string]interface{}, total int, err error) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("catalog panicked: %v", p)
		}
	}()
	return call(es)
}

func TestPublicCatalogTitlesAndFields(t *testing.T) {
	cases := []struct{ name, blocks, title, paragraph, image string }{
		{"H1 wins over earlier header and paragraph", `[{"type":"paragraph","data":{"text":"Intro"}},{"type":"header","data":{"level":2,"text":"Section"}},{"type":"header","data":{"level":1,"text":"H1"}},{"type":"header","data":{"level":1,"text":"Later H1"}},{"type":"image","data":{"file":{"url":"/first.png"}}},{"type":"image","data":{"file":{"url":"/later.png"}}}]`, "H1", "Intro", "/first.png"},
		{"legacy header without level", `[{"type":"header","data":{"text":"Old title"}}]`, "Old title", "", ""},
		{"legacy section header", `[{"type":"paragraph","data":{"text":"Intro"}},{"type":"header","data":{"level":2,"text":"Section"}}]`, "Section", "Intro", ""},
		{"legacy string H1 wins", `[{"type":"header","data":{"level":2,"text":"Section"}},{"type":"header","data":{"level":"1","text":"Old H1"}}]`, "Old H1", "", ""},
		{"paragraph only preserves authored markup", `[{"type":"paragraph","data":{"text":"<b>Existing text</b>"}}]`, "<b>Existing text</b>", "<b>Existing text</b>", ""},
		{"blank title candidates", `[{"type":"header","data":{"level":1,"text":"  "}},{"type":"paragraph","data":{"text":"Fallback"}}]`, "Fallback", "Fallback", ""},
		{"malformed blocks before useful content", `[null,7,"bad",{}, {"type":4,"data":{}},{"type":"header"},{"type":"header","data":null},{"type":"header","data":{"level":1,"text":7}},{"type":"paragraph","data":{"text":false}},{"type":"image","data":{"file":null}},{"type":"image","data":{"file":{"url":7}}},{"type":"paragraph","data":{"text":"Recovered"}},{"type":"image","data":{"file":{"url":"/ok.png"}}}]`, "Recovered", "Recovered", "/ok.png"},
		{"empty content invents nothing", `[]`, "", "", ""},
	}
	for method, call := range catalogCalls {
		for _, tc := range cases {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"hits":{"total":{"value":12001,"relation":"eq"},"hits":[{"_source":{"blog_id":"post1","owner_account_id":"owner1","tags":["go"],"content_type":"editorjs","published_time":"2020-01-01T00:00:00Z","ai_score":0.2,"ai_features":{"x":true},"blog":{"blocks":%s}}}]}}`, tc.blocks)
				rows, total, err := callCatalog(t, call, catalogStorage(t, body, nil))
				if err != nil || total != 12001 || len(rows) != 1 {
					t.Fatalf("rows=%v total=%d err=%v", rows, total, err)
				}
				want := map[string]interface{}{"blog_id": "post1", "owner_account_id": "owner1", "tags": []interface{}{"go"}, "content_type": "editorjs", "published_time": "2020-01-01T00:00:00Z", "title": tc.title, "first_paragraph": tc.paragraph, "first_image": tc.image, "ai_score": 0.2, "ai_features": map[string]interface{}{"x": true}}
				if !reflect.DeepEqual(rows[0], want) {
					t.Errorf("metadata=%#v want=%#v", rows[0], want)
				}
			})
		}
	}
}

func TestPublicCatalogPartialRecords(t *testing.T) {
	for method, call := range catalogCalls {
		for _, source := range []string{`{}`, `{"blog":null}`, `{"blog":[]}`, `{"blog":{"blocks":null}}`, `{"blog":{"blocks":{}}}`} {
			t.Run(method+"/"+source, func(t *testing.T) {
				body := fmt.Sprintf(`{"hits":{"total":{"value":6},"hits":[null,7,{}, {"_source":null},{"_source":"bad"},{"_source":%s}]}}`, source)
				rows, total, err := callCatalog(t, call, catalogStorage(t, body, nil))
				if err != nil || total != 6 || len(rows) != 1 {
					t.Fatalf("rows=%v total=%d err=%v", rows, total, err)
				}
				want := map[string]interface{}{"blog_id": nil, "owner_account_id": nil, "tags": nil, "content_type": nil, "published_time": nil, "title": "", "first_paragraph": "", "first_image": ""}
				if !reflect.DeepEqual(rows[0], want) {
					t.Errorf("metadata=%#v want=%#v", rows[0], want)
				}
			})
		}
	}
}

func TestPublicCatalogResponseShapes(t *testing.T) {
	for method, call := range catalogCalls {
		for _, tc := range []struct {
			name, body string
			total      int
			wantErr    bool
		}{
			{"empty", `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`, 0, false},
			{"past last page", `{"hits":{"total":{"value":12001,"relation":"eq"},"hits":[]}}`, 12001, false},
			{"legacy numeric total", `{"hits":{"total":42,"hits":[]}}`, 42, false},
			{"missing total", `{"hits":{"hits":[]}}`, 0, true},
			{"missing hits", `{}`, 0, true},
			{"null hits", `{"hits":null}`, 0, true},
			{"wrong hits type", `{"hits":[]}`, 0, true},
			{"missing hit list", `{"hits":{"total":{"value":42}}}`, 42, true},
			{"null hit list", `{"hits":{"total":{"value":42},"hits":null}}`, 42, true},
			{"invalid JSON", `{`, 0, true},
			{"timeout", `{"timed_out":true,"hits":{"total":{"value":0},"hits":[]}}`, 0, true},
			{"failed shard", `{"_shards":{"failed":1},"hits":{"total":{"value":0},"hits":[]}}`, 0, true},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				rows, total, err := callCatalog(t, call, catalogStorage(t, tc.body, nil))
				if (err != nil) != tc.wantErr || total != tc.total {
					t.Fatalf("rows=%v total=%d err=%v; want total=%d error=%v", rows, total, err, tc.total, tc.wantErr)
				}
				if !tc.wantErr && rows == nil {
					t.Error("successful empty result must be an array")
				}
			})
		}
	}
}

func TestPublicCatalogQuery(t *testing.T) {
	for method, call := range catalogCalls {
		t.Run(method, func(t *testing.T) {
			_, _, err := callCatalog(t, call, catalogStorage(t, `{"hits":{"total":{"value":0},"hits":[]}}`, func(q map[string]interface{}) {
				if q["from"] != float64(4) || q["size"] != float64(2) {
					t.Errorf("pagination changed: %v", q)
				}
				if q["track_total_hits"] != true {
					t.Error("exact totals require track_total_hits=true")
				}
				b := q["query"].(map[string]interface{})["bool"].(map[string]interface{})
				contains := func(clauses interface{}, field string, value interface{}) bool {
					for _, c := range clauses.([]interface{}) {
						term, _ := c.(map[string]interface{})["term"].(map[string]interface{})
						if reflect.DeepEqual(term[field], value) {
							return true
						}
					}
					return false
				}
				if !contains(b["must"], "is_draft", false) {
					t.Error("public query must require published posts")
				}
				for _, f := range []string{"is_archived", "is_scheduled"} {
					if !contains(b["must_not"], f, true) {
						t.Errorf("public query must exclude %s=true", f)
					}
				}
				if !contains(b["must_not"], "audience", "group_only") {
					t.Error("public query must exclude group_only")
				}
				sort := q["sort"].([]interface{})[0].(map[string]interface{})["published_time"].(map[string]interface{})
				if sort["order"] != "desc" || sort["unmapped_type"] != "date" {
					t.Errorf("publication sort changed: %v", sort)
				}
				source := q["_source"].([]interface{})
				for _, field := range []string{"blog_id", "owner_account_id", "blog.blocks", "tags", "content_type", "published_time", "ai_score"} {
					found := false
					for _, s := range source {
						if s == field {
							found = true
						}
					}
					if !found {
						t.Errorf("missing source field %s", field)
					}
				}
				if method == "tags" {
					should := b["must"].([]interface{})[0].(map[string]interface{})["bool"].(map[string]interface{})
					term := should["should"].([]interface{})[0].(map[string]interface{})["term"].(map[string]interface{})["tags"].(map[string]interface{})
					if term["value"] != "go" || term["case_insensitive"] != true || should["minimum_should_match"] != float64(1) {
						t.Errorf("tag matching changed: %v", should)
					}
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPublicCatalogTagsDraftCompatibility(t *testing.T) {
	es := catalogStorage(t, `{"hits":{"total":0,"hits":[]}}`, func(q map[string]interface{}) {
		b := q["query"].(map[string]interface{})["bool"].(map[string]interface{})
		must := b["must"].([]interface{})
		if must[1].(map[string]interface{})["term"].(map[string]interface{})["is_draft"] != true {
			t.Error("draft parameter lost")
		}
		excluded := b["must_not"].([]interface{})
		if len(excluded) != 1 || excluded[0].(map[string]interface{})["term"].(map[string]interface{})["is_archived"] != true {
			t.Errorf("draft filters changed: %v", excluded)
		}
	})
	if _, _, err := es.GetBlogsMetadataByTags(context.Background(), []string{"Go"}, true, 2, 4); err != nil {
		t.Fatal(err)
	}
	es = scopeStorage(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("empty tags must not issue a request")
		return nil, nil
	})
	if _, _, err := es.GetBlogsMetadataByTags(context.Background(), nil, false, 2, 4); err == nil {
		t.Error("empty tags must fail")
	}
}

func TestPublicCatalogRejectsInvalidTotals(t *testing.T) {
	for method, call := range catalogCalls {
		for _, total := range []string{`null`, `"42"`, `false`, `[]`, `-1`, `0.5`, `1e99`, `{}`, `{"value":null}`, `{"value":"42"}`, `{"value":-1}`, `{"value":0.5}`, `{"value":1e99}`, `{"value":42,"relation":"gte"}`, `{"value":42,"relation":false}`} {
			t.Run(method+"/"+total, func(t *testing.T) {
				rows, _, err := callCatalog(t, call, catalogStorage(t, `{"hits":{"total":`+total+`,"hits":[]}}`, nil))
				if err == nil || rows != nil {
					t.Fatalf("invalid total must fail instead of claiming completeness: rows=%v err=%v", rows, err)
				}
			})
		}
	}
}

func catalogRangeCall(es *elasticsearchStorage, method string, ctx context.Context, limit, offset int) ([]map[string]interface{}, int, error) {
	if method == "tags" {
		return es.GetBlogsMetadataByTags(ctx, []string{" Go "}, false, int32(limit), int32(offset))
	}
	return es.GetAllPublishedBlogsMetadata(ctx, limit, offset)
}

func catalogGeneratedPage(start, size, total int, scrollID string) map[string]interface{} {
	hits := make([]interface{}, 0)
	for i := start; i < total && i-start < size; i++ {
		hits = append(hits, map[string]interface{}{"_source": map[string]interface{}{"blog_id": fmt.Sprintf("post-%d", i), "blog": map[string]interface{}{"blocks": []interface{}{map[string]interface{}{"type": "paragraph", "data": map[string]interface{}{"text": fmt.Sprintf("Title %d", i)}}}}}})
	}
	response := map[string]interface{}{"hits": map[string]interface{}{"total": map[string]interface{}{"value": total, "relation": "eq"}, "hits": hits}}
	if scrollID != "" {
		response["_scroll_id"] = scrollID
	}
	return response
}

func catalogJSONResponse(t *testing.T, code int, value interface{}) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return scopeResponse(code, string(body))
}

type catalogTrackedBody struct {
	io.ReadCloser
	closed *int
}

func (b *catalogTrackedBody) Close() error { *b.closed++; return b.ReadCloser.Close() }

func TestPublicCatalogWindowPagination(t *testing.T) {
	for _, method := range []string{"published", "tags"} {
		for _, tc := range []struct {
			name                 string
			offset, limit, total int
			scroll               bool
		}{
			{"at boundary", 9998, 2, 10005, false},
			{"zero at boundary", 10000, 0, 10005, false},
			{"cross boundary", 9999, 3, 10005, true},
			{"deep page", 10000, 3, 10005, true},
			{"short final page", 10003, 5, 10005, true},
			{"empty end", 10005, 2, 10005, true},
			{"past end", 10020, 2, 10005, true},
			{"zero beyond boundary", 10001, 0, 10005, true},
			{"empty catalog", 10000, 2, 0, true},
			{"maximum normal page", 0, 1000, 10005, false},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				searches, scrolls, clears, next, batchSize, requests, closed := 0, 0, 0, 0, 0, 0, 0
				latest := ""
				es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
					requests++
					var body map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					var response *http.Response
					switch {
					case req.Method == http.MethodDelete && req.URL.Path == "/_search/scroll":
						clears++
						ids, ok := body["scroll_id"].([]interface{})
						if !ok || len(ids) != 1 || ids[0] != latest {
							t.Error("cleanup must use latest returned scroll ID")
						}
						if req.Context().Err() != nil {
							t.Error("cleanup context is already canceled")
						}
						if d, ok := req.Context().Deadline(); !ok || time.Until(d) > 5*time.Second {
							t.Error("cleanup must have a short deadline")
						}
						response = catalogJSONResponse(t, 200, map[string]interface{}{"succeeded": true, "num_freed": 1})
					case req.URL.Path == "/_search/scroll" && req.Method == http.MethodPost:
						scrolls++
						if body["scroll_id"] != latest {
							t.Error("continuation must use latest returned scroll ID")
						}
						latest = fmt.Sprintf("test-scroll-%d", scrolls+1)
						response = catalogJSONResponse(t, 200, catalogGeneratedPage(next, batchSize, tc.total, latest))
						next += batchSize
					case strings.HasSuffix(req.URL.Path, "/_search"):
						searches++
						b := body["query"].(map[string]interface{})["bool"].(map[string]interface{})
						raw, _ := json.Marshal(b)
						for _, filter := range []string{`"is_draft":false`, `"is_archived":true`, `"is_scheduled":true`, `"audience":"group_only"`} {
							if !strings.Contains(string(raw), filter) {
								t.Errorf("missing public filter %s", filter)
							}
						}
						if method == "tags" && !strings.Contains(string(raw), `"value":"go"`) {
							t.Error("tag filter changed")
						}
						sort := body["sort"].([]interface{})[0].(map[string]interface{})["published_time"].(map[string]interface{})
						if sort["order"] != "desc" || sort["unmapped_type"] != "date" {
							t.Error("sort changed")
						}
						if body["track_total_hits"] != true {
							t.Error("exact totals required")
						}
						batchSize = int(body["size"].(float64))
						from := 0
						if value, ok := body["from"].(float64); ok {
							from = int(value)
						}
						if !tc.scroll {
							if req.URL.Query().Get("scroll") != "" || from != tc.offset || batchSize != tc.limit {
								t.Error("normal from/size path changed")
							}
							response = catalogJSONResponse(t, 200, catalogGeneratedPage(from, batchSize, tc.total, ""))
						} else if req.URL.Query().Get("scroll") == "" || from+batchSize > 10000 {
							response = scopeResponse(400, `{"error":{"reason":"result window is too large"}}`)
						} else {
							if from != 0 || batchSize < 1 || batchSize > 1000 {
								t.Error("scroll must start at zero with a bounded batch")
							}
							latest = "test-scroll-1"
							response = catalogJSONResponse(t, 200, catalogGeneratedPage(0, batchSize, tc.total, latest))
							next = batchSize
						}
					default:
						t.Fatalf("unexpected catalog request: %s", req.Method)
					}
					response.Body = &catalogTrackedBody{response.Body, &closed}
					return response, nil
				})
				rows, total, err := catalogRangeCall(es, method, context.Background(), tc.limit, tc.offset)
				wantCount := min(tc.limit, max(0, tc.total-tc.offset))
				if err != nil || total != tc.total || len(rows) != wantCount || rows == nil {
					t.Fatalf("count=%d total=%d err=%v; want count=%d total=%d", len(rows), total, err, wantCount, tc.total)
				}
				for i, row := range rows {
					if row["blog_id"] != fmt.Sprintf("post-%d", tc.offset+i) || row["title"] != fmt.Sprintf("Title %d", tc.offset+i) {
						t.Fatalf("wrong slice at position %d", i)
					}
				}
				if searches != 1 || closed != requests {
					t.Errorf("searches=%d responses closed=%d requests=%d", searches, closed, requests)
				}
				if tc.scroll && (clears != 1 || (wantCount > 0 && tc.offset > 0 && scrolls == 0)) {
					t.Errorf("scrolls=%d clears=%d", scrolls, clears)
				}
				if !tc.scroll && (scrolls != 0 || clears != 0) {
					t.Error("normal request must not create a scroll")
				}
				if tc.scroll && wantCount == 0 && scrolls != 0 {
					t.Error("empty range should stop after reading exact total")
				}
			})
		}
	}
}

func TestPublicCatalogScrollFailuresAndCleanup(t *testing.T) {
	for _, method := range []string{"published", "tags"} {
		for _, fault := range []string{
			"initial timeout", "initial shards", "initial short", "initial empty", "initial malformed total", "initial http", "continuation timeout", "continuation shards", "continuation total changed", "continuation empty", "continuation short", "continuation missing id", "continuation malformed total", "continuation invalid json", "continuation http", "continuation transport", "canceled during initial", "canceled during continuation", "cleanup http", "cleanup transport", "cleanup unsuccessful",
		} {
			t.Run(method+"/"+fault, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cleared, scrolls := 0, 0
				latest := ""
				es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
					var body map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if req.Method == http.MethodDelete {
						cleared++
						ids, _ := body["scroll_id"].([]interface{})
						if len(ids) != 1 || ids[0] != latest {
							t.Error("must clean up latest known ID, including on failures")
						}
						if req.Context().Err() != nil {
							t.Error("cleanup must outlive caller cancellation")
						}
						if d, ok := req.Context().Deadline(); !ok || time.Until(d) > 5*time.Second {
							t.Error("cleanup deadline missing or unbounded")
						}
						switch fault {
						case "cleanup http":
							return scopeResponse(500, `{"error":"test-scroll-token-must-not-leak"}`), nil
						case "cleanup transport":
							return nil, errors.New("test-scroll-token-must-not-leak")
						case "cleanup unsuccessful":
							return scopeResponse(200, `{"succeeded":false}`), nil
						}
						return scopeResponse(200, `{"succeeded":true,"num_freed":1}`), nil
					}
					initial := strings.HasSuffix(req.URL.Path, "/_search")
					if initial && req.URL.Query().Get("scroll") == "" {
						return scopeResponse(400, `{"error":"result window"}`), nil
					}
					if !initial {
						scrolls++
						if body["scroll_id"] != latest {
							t.Error("wrong continuation ID")
						}
					}
					start := 0
					if !initial {
						start = 1000
					}
					latest = fmt.Sprintf("test-scroll-%d", scrolls+1)
					response := catalogGeneratedPage(start, 1000, 10005, latest)
					apply := strings.HasPrefix(fault, "initial ") && initial || strings.HasPrefix(fault, "continuation ") && !initial
					if apply {
						switch {
						case strings.HasSuffix(fault, "timeout"):
							response["timed_out"] = true
						case strings.HasSuffix(fault, "shards"):
							response["_shards"] = map[string]interface{}{"failed": 1}
						case strings.HasSuffix(fault, "total changed"):
							response["hits"].(map[string]interface{})["total"] = 10006
						case strings.HasSuffix(fault, "malformed total"):
							delete(response["hits"].(map[string]interface{}), "total")
						case strings.HasSuffix(fault, "short"):
							response["hits"].(map[string]interface{})["hits"] = response["hits"].(map[string]interface{})["hits"].([]interface{})[:999]
						case strings.HasSuffix(fault, "empty"):
							response["hits"].(map[string]interface{})["hits"] = []interface{}{}
						case strings.HasSuffix(fault, "missing id"):
							delete(response, "_scroll_id")
							latest = "test-scroll-1"
						case strings.HasSuffix(fault, "invalid json"):
							latest = "test-scroll-1"
							return scopeResponse(200, `{`), nil
						case strings.HasSuffix(fault, "transport"):
							latest = "test-scroll-1"
							return nil, errors.New("test-scroll-token-must-not-leak")
						case strings.HasSuffix(fault, "http"):
							return catalogJSONResponse(t, 500, response), nil
						}
					}
					if fault == "canceled during initial" && initial || fault == "canceled during continuation" && !initial {
						cancel()
					}
					if strings.HasPrefix(fault, "cleanup ") {
						response = catalogGeneratedPage(0, 0, 0, latest)
					}
					return catalogJSONResponse(t, 200, response), nil
				})
				rows, _, err := catalogRangeCall(es, method, ctx, 2, 10000)
				if err == nil || rows != nil {
					t.Fatalf("incomplete or failed request must fail: count=%d err=%v", len(rows), err)
				}
				if strings.Contains(err.Error(), "test-scroll-") {
					t.Error("error exposed a scroll token")
				}
				if strings.HasPrefix(fault, "canceled ") && !errors.Is(err, context.Canceled) {
					t.Errorf("must preserve cancellation: %v", err)
				}
				if cleared != 1 {
					t.Errorf("cleanup attempts=%d want 1", cleared)
				}
			})
		}
	}
}

func TestPublicCatalogCanceledBeforeSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, method := range []string{"published", "tags"} {
		es := scopeStorage(t, func(*http.Request) (*http.Response, error) {
			t.Error("canceled request must not reach transport")
			return nil, context.Canceled
		})
		for _, offset := range []int{0, 10000} {
			if _, _, err := catalogRangeCall(es, method, ctx, 2, offset); !errors.Is(err, context.Canceled) {
				t.Errorf("method=%s offset=%d err=%v", method, offset, err)
			}
		}
	}
}
