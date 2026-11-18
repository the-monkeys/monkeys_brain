package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/the-monkeys/the_monkeys/common/homepage"
)

func homepageSource(id string, words int) map[string]interface{} {
	return map[string]interface{}{"blog_id": id, "owner_account_id": "author", "is_draft": false, "tags": []interface{}{"Science"}, "blog": map[string]interface{}{"blocks": []interface{}{
		map[string]interface{}{"type": "header", "data": map[string]interface{}{"level": 1, "text": "An authored article"}},
		map[string]interface{}{"type": "paragraph", "data": map[string]interface{}{"text": strings.Repeat("readable ", words)}},
	}}}
}

func homepagePage(sources []map[string]interface{}, total int, id string) map[string]interface{} {
	hits := make([]interface{}, 0, len(sources))
	for _, s := range sources {
		hits = append(hits, map[string]interface{}{"_source": s})
	}
	return map[string]interface{}{"_scroll_id": id, "hits": map[string]interface{}{"total": map[string]interface{}{"value": total, "relation": "eq"}, "hits": hits}}
}

func TestHomepageFiltersBeforePaginationAndCleansScroll(t *testing.T) {
	sources := make([]map[string]interface{}, 100)
	for i := range sources {
		sources[i] = homepageSource(fmt.Sprint(i), 249)
	}
	pages := 0
	cleaned := false
	es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			cleaned = true
			return scopeResponse(200, `{"succeeded":true}`), nil
		}
		pages++
		if pages == 1 {
			var q map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&q)
			if q["from"] != nil && q["from"] != float64(0) {
				t.Fatal("candidate offset must be zero")
			}
			if q["_source"] != nil {
				t.Fatal("policy must read full stored source")
			}
			body, _ := json.Marshal(q)
			if strings.Contains(string(body), `"blog_id":"asc"`) {
				t.Error("blog_id is analyzed text in the legacy source index and cannot be sorted")
			}
			for _, f := range []string{"is_draft", "is_archived", "is_scheduled", "group_only"} {
				if !strings.Contains(string(body), f) {
					t.Errorf("missing public filter %s", f)
				}
			}
			return catalogJSONResponse(t, 200, homepagePage(sources, 103, "first")), nil
		}
		return catalogJSONResponse(t, 200, homepagePage([]map[string]interface{}{homepageSource("a", 250), homepageSource("b", 250), homepageSource("c", 250)}, 103, "last")), nil
	})
	rows, more, err := es.GetHomepageBlogs(context.Background(), 1, 1, nil)
	if err != nil || len(rows) != 1 || rows[0]["blog_id"] != "b" || !more || !cleaned || pages != 2 {
		t.Fatalf("rows=%v more=%v err=%v pages=%d cleaned=%v", rows, more, err, pages, cleaned)
	}
	if rows[0]["title"] != "An authored article" {
		t.Fatal("authored title lost")
	}
	if a, ok := rows[0]["homepage_eligibility"].(homepage.Assessment); !ok || !a.Eligible || a.Version != homepage.Version {
		t.Fatalf("assessment=%v", rows[0]["homepage_eligibility"])
	}
}

func TestHomepageTrendingPreservesRankAndSourcePermissions(t *testing.T) {
	sources := []map[string]interface{}{homepageSource("c", 250), homepageSource("b", 250), homepageSource("a", 250)}
	for _, field := range []string{"is_archived", "is_scheduled", "is_draft", "audience", "blog_id"} {
		s := homepageSource("excluded-"+field, 250)
		switch field {
		case "audience":
			s[field] = "group_only"
		case "blog_id":
			s[field] = ""
		default:
			s[field] = true
		}
		sources = append(sources, s)
	}
	es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			return scopeResponse(200, `{"succeeded":true}`), nil
		}
		var q map[string]interface{}
		_ = json.NewDecoder(req.Body).Decode(&q)
		encoded, _ := json.Marshal(q)
		if !strings.Contains(string(encoded), `"ids":{"values":`) {
			t.Error("trending must match exact persisted document IDs, independent of analyzed blog_id mappings")
		}
		return catalogJSONResponse(t, 200, homepagePage(sources, len(sources), "ranked")), nil
	})
	rows, more, err := es.GetHomepageBlogs(context.Background(), 1, 1, []string{"a", "b", "c", "excluded-is_archived", "excluded-is_scheduled", "excluded-is_draft", "excluded-audience"})
	if err != nil || len(rows) != 1 || rows[0]["blog_id"] != "b" || !more {
		t.Fatalf("rows=%v more=%v err=%v", rows, more, err)
	}
}

func TestHomepageScrollFailsClosed(t *testing.T) {
	for _, fault := range []string{"timeout", "shards", "missing context", "changed total", "short batch", "cleanup", "cancel", "transport"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			cleaned := false
			es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					cleaned = true
					if req.Context().Err() != nil {
						t.Error("cleanup inherited cancellation")
					}
					if fault == "cleanup" {
						return scopeResponse(500, `{}`), nil
					}
					return scopeResponse(200, `{"succeeded":true}`), nil
				}
				calls++
				sources := make([]map[string]interface{}, 100)
				for i := range sources {
					sources[i] = homepageSource(fmt.Sprint(i), 249)
				}
				page := homepagePage(sources, 101, "token")
				if calls == 2 {
					page = homepagePage([]map[string]interface{}{homepageSource("valid", 250)}, 101, "last")
				}
				switch fault {
				case "timeout":
					page["timed_out"] = true
				case "shards":
					page["_shards"] = map[string]interface{}{"failed": 1}
				case "missing context":
					delete(page, "_scroll_id")
				case "changed total":
					if calls == 2 {
						page = homepagePage(nil, 102, "last")
					}
				case "short batch":
					page = homepagePage(nil, 101, "token")
				case "cancel":
					cancel()
				case "transport":
					if calls == 2 {
						return nil, errors.New("offline")
					}
				}
				return catalogJSONResponse(t, 200, page), nil
			})
			rows, more, err := es.GetHomepageBlogs(ctx, 1, 0, nil)
			if err == nil || rows != nil || more {
				t.Fatalf("rows=%v more=%v err=%v", rows, more, err)
			}
			if fault != "missing context" && !cleaned {
				t.Error("scroll not cleaned")
			}
		})
	}
}

func TestHomepageEmptyAndBounds(t *testing.T) {
	for _, tc := range []struct {
		limit, offset int
		ids           []string
	}{{0, 0, nil}, {51, 0, nil}, {1, -1, nil}, {1, 1001, nil}, {1, 0, []string{" "}}} {
		es := scopeStorage(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid request reached ES"); return nil, nil })
		if _, _, err := es.GetHomepageBlogs(context.Background(), tc.limit, tc.offset, tc.ids); err == nil {
			t.Fatal("invalid input accepted", tc)
		}
	}
	es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			return scopeResponse(200, `{"succeeded":true}`), nil
		}
		return catalogJSONResponse(t, 200, homepagePage(nil, 0, "empty")), nil
	})
	rows, more, err := es.GetHomepageBlogs(context.Background(), 12, 0, nil)
	if err != nil || more || !reflect.DeepEqual(rows, []map[string]interface{}{}) {
		t.Fatalf("rows=%v more=%v err=%v", rows, more, err)
	}
}

func TestHomepageUsesFreshAssessmentForLegacyFields(t *testing.T) {
	source := homepageSource("legacy", 250)
	source["title"] = "Legacy authored title"
	source["body"] = strings.Repeat("readable ", 250)
	source["first_image"] = "https://example.com/cover.png"
	source["homepage_eligibility"] = map[string]interface{}{"eligible": false, "version": "old"}
	source["blog"] = map[string]interface{}{"tags": []interface{}{"Science"}}
	delete(source, "tags")
	es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			return scopeResponse(200, `{"succeeded":true}`), nil
		}
		return catalogJSONResponse(t, 200, homepagePage([]map[string]interface{}{source}, 1, "legacy")), nil
	})
	rows, more, err := es.GetHomepageBlogs(context.Background(), 12, 0, nil)
	if err != nil || more || len(rows) != 1 {
		t.Fatalf("rows=%v more=%v err=%v", rows, more, err)
	}
	if rows[0]["title"] != "Legacy authored title" || rows[0]["first_image"] != "https://example.com/cover.png" || !reflect.DeepEqual(rows[0]["tags"], []string{"Science"}) {
		t.Fatal(rows[0])
	}
	a := rows[0]["homepage_eligibility"].(homepage.Assessment)
	if !a.Eligible || !a.LeadEligible || a.Version != homepage.Version {
		t.Fatal(a)
	}
}

func TestHomepageTrendingRanksAcrossScrollBatches(t *testing.T) {
	ids := make([]string, 202)
	sources := make([]map[string]interface{}, 202)
	for i := range ids {
		ids[i] = fmt.Sprint(i)
		sources[len(ids)-1-i] = homepageSource(ids[i], 250)
	}
	cursor := 0
	es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			return scopeResponse(200, `{"succeeded":true}`), nil
		}
		end := min(cursor+100, len(sources))
		page := homepagePage(sources[cursor:end], len(sources), fmt.Sprint(end))
		cursor = end
		return catalogJSONResponse(t, 200, page), nil
	})
	rows, more, err := es.GetHomepageBlogs(context.Background(), 2, 1, ids)
	if err != nil || !more || len(rows) != 2 || rows[0]["blog_id"] != "1" || rows[1]["blog_id"] != "2" || cursor != 202 {
		t.Fatalf("rows=%v more=%v cursor=%d err=%v", rows, more, cursor, err)
	}
}

func TestHomepageDiscardsPartialPageAndCleansLatestContext(t *testing.T) {
	sources := make([]map[string]interface{}, 100)
	for i := range sources {
		sources[i] = homepageSource(fmt.Sprint(i), 249)
	}
	sources[0] = homepageSource("eligible", 250)
	calls := 0
	cleaned := false
	es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			var body map[string][]string
			_ = json.NewDecoder(req.Body).Decode(&body)
			if !reflect.DeepEqual(body["scroll_id"], []string{"latest-token"}) {
				t.Errorf("wrong cleanup: %v", body)
			}
			cleaned = true
			return scopeResponse(200, `{"succeeded":true}`), nil
		}
		calls++
		if calls == 1 {
			return catalogJSONResponse(t, 200, homepagePage(sources, 101, "first-token")), nil
		}
		page := homepagePage([]map[string]interface{}{homepageSource("second", 250)}, 101, "latest-token")
		page["timed_out"] = true
		return catalogJSONResponse(t, 200, page), nil
	})
	rows, more, err := es.GetHomepageBlogs(context.Background(), 2, 0, nil)
	if err == nil || rows != nil || more || !cleaned {
		t.Fatalf("rows=%v more=%v cleaned=%v err=%v", rows, more, cleaned, err)
	}
}

func TestHomepageCanceledBeforeSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	es := scopeStorage(t, func(*http.Request) (*http.Response, error) { t.Fatal("canceled request reached ES"); return nil, nil })
	rows, more, err := es.GetHomepageBlogs(ctx, 12, 0, nil)
	if !errors.Is(err, context.Canceled) || rows != nil || more {
		t.Fatalf("rows=%v more=%v err=%v", rows, more, err)
	}
}
