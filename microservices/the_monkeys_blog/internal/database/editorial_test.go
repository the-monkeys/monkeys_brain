package database

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

var editorialExample = map[string]interface{}{
	"canonical_slug": "canonical-post1", "seo_description": "Authored description", "content_modified_at": "2026-11-22T00:00:00Z",
	"rss_guid": "https://monkeys.com.co/blog/original-post1",
}

func TestEditorialMetadataProjections(t *testing.T) {
	calls := map[string]catalogCall{
		"published": catalogCalls["published"], "tags": catalogCalls["tags"],
		"query": func(es *elasticsearchStorage) ([]map[string]interface{}, int, error) {
			return es.GetBlogsMetadataByQuery(context.Background(), []string{"grafana"}, false, 2, 0)
		},
		"owner": func(es *elasticsearchStorage) ([]map[string]interface{}, int, error) {
			return es.GetBlogsMetaByAccountId(context.Background(), "owner1", false, false, false, 2, 0)
		},
		"ids": func(es *elasticsearchStorage) ([]map[string]interface{}, int, error) {
			return es.GetBlogsMetaByBlogIdsV2(context.Background(), []string{"post1"}, false, 2, 0)
		},
	}
	for name, call := range calls {
		for _, present := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/legacy", true: "/editorial"}[present], func(t *testing.T) {
				source := map[string]interface{}{"blog_id": "post1", "owner_account_id": "owner1", "slug": "different-stored-slug", "blog": map[string]interface{}{"blocks": []interface{}{map[string]interface{}{"type": "header", "data": map[string]interface{}{"text": "Title", "level": 1}}}}}
				if present {
					for k, v := range editorialExample {
						source[k] = v
					}
				}
				body, _ := json.Marshal(map[string]interface{}{"hits": map[string]interface{}{"total": map[string]interface{}{"value": 1}, "hits": []interface{}{map[string]interface{}{"_source": source}}}})
				es := catalogStorage(t, string(body), func(q map[string]interface{}) {
					includes := q["_source"].([]interface{})
					for field := range editorialExample {
						found := false
						for _, v := range includes {
							if v == field {
								found = true
							}
						}
						if !found {
							t.Errorf("query omits %s", field)
						}
					}
				})
				rows, _, err := call(es)
				if err != nil || len(rows) != 1 {
					t.Fatalf("rows=%v err=%v", rows, err)
				}
				for k, v := range editorialExample {
					got, exists := rows[0][k]
					if exists != present || present && got != v {
						t.Errorf("%s=%v present=%t", k, got, exists)
					}
				}
				if _, exists := rows[0]["slug"]; exists {
					t.Fatal("legacy stored slug leaked into projection")
				}
			})
		}
	}
}

func TestSaveBlogEditorialPreservation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted", true: "explicit"}[explicit], func(t *testing.T) {
			incoming := map[string]interface{}{"blog_id": "draft1", "group_slug": "", "audience": "public", "owner_account_id": "owner1", "is_draft": true}
			if explicit {
				incoming["canonical_slug"] = ""
				incoming["seo_description"] = nil
				incoming["content_modified_at"] = "2026-11-23T00:00:00Z"
				incoming["rss_guid"] = "https://monkeys.com.co/blog/explicit-draft1"
			}
			before, _ := json.Marshal(incoming)
			var saved map[string]interface{}
			es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					includes := req.URL.Query().Get("_source_includes")
					for field := range editorialExample {
						if !strings.Contains(includes, field) {
							t.Errorf("read omits %s", field)
						}
					}
					body, _ := json.Marshal(map[string]interface{}{"_source": editorialExample, "_seq_no": 4, "_primary_term": 2})
					return scopeResponse(200, string(body)), nil
				}
				if !explicit && (req.URL.Query().Get("if_seq_no") != "4" || req.URL.Query().Get("if_primary_term") != "2") {
					t.Error("missing CAS for editorial preservation")
				}
				if err := json.NewDecoder(req.Body).Decode(&saved); err != nil {
					t.Fatal(err)
				}
				return scopeResponse(201, `{"result":"updated"}`), nil
			})
			res, err := es.SaveBlog(context.Background(), incoming)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			for field, value := range editorialExample {
				if explicit {
					value = incoming[field]
				}
				got, exists := saved[field]
				if !exists || !reflect.DeepEqual(got, value) {
					t.Errorf("%s=%v want %v", field, got, value)
				}
			}
			after, _ := json.Marshal(incoming)
			if string(before) != string(after) {
				t.Fatal("mutated caller")
			}
			if saved["owner_account_id"] != "owner1" || saved["group_slug"] != "" || saved["audience"] != "public" {
				t.Fatal("unrelated fields changed")
			}
		})
	}
}

func TestSaveBlogEditorialCASConflict(t *testing.T) {
	es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return scopeResponse(200, `{"_source":{"seo_description":"new"},"_seq_no":4,"_primary_term":2}`), nil
		}
		if req.URL.Query().Get("if_seq_no") != "4" || req.URL.Query().Get("if_primary_term") != "2" {
			t.Error("missing editorial CAS")
		}
		return scopeResponse(409, `{"error":{"type":"version_conflict_engine_exception"}}`), nil
	})
	res, err := es.SaveBlog(context.Background(), map[string]interface{}{"blog_id": "draft1", "group_slug": "", "audience": "public"})
	if res != nil {
		defer res.Body.Close()
	}
	if err == nil || res == nil || res.StatusCode != 409 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestSaveBlogPartialEditorialEditKeepsOmittedFields(t *testing.T) {
	var saved map[string]interface{}
	es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return scopeResponse(200, `{"_source":{"canonical_slug":"canonical-draft1","seo_description":"stored","content_modified_at":"2026-11-22T00:00:00Z"},"_seq_no":4,"_primary_term":2}`), nil
		}
		if req.URL.Query().Get("if_seq_no") != "4" {
			t.Error("missing CAS")
		}
		if err := json.NewDecoder(req.Body).Decode(&saved); err != nil {
			t.Fatal(err)
		}
		return scopeResponse(201, `{"result":"updated"}`), nil
	})
	res, err := es.SaveBlog(context.Background(), map[string]interface{}{"blog_id": "draft1", "group_slug": "", "audience": "public", "seo_description": nil})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	value, present := saved["seo_description"]
	if !present || value != nil || saved["canonical_slug"] != "canonical-draft1" || saved["content_modified_at"] != "2026-11-22T00:00:00Z" {
		t.Fatalf("partial edit lost optional values: %v", saved)
	}
}
