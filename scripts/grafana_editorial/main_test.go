package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/the-monkeys/the_monkeys/searchdoc"
)

const originalTestTitle = "The Ultimate Guide to Grafana: What It Is, Why It’s Used, and How to Build a Complete Monitoring Stack with Prometheus, Node exporter and Docker"

func originalExport() map[string]interface{} {
	return map[string]interface{}{"_id": "1uvwek", "_seq_no": json.Number("9007199254740993"), "_primary_term": 2, "_source": map[string]interface{}{
		"blog_id": "1uvwek", "owner_account_id": "9e48b1a5979fe25c187f2e20f024c20b", "is_draft": false, "audience": "public", "published_time": "2024-06-01T00:00:00Z",
		"slug": "full-original-title-unrelated-to-canonical-1uvwek", "likes": json.Number("9007199254740993"), "unknown": map[string]interface{}{"keep": "unchanged"}, "tags": []interface{}{"Grafana", "Linux"},
		"blog": map[string]interface{}{"blocks": []interface{}{map[string]interface{}{"type": "header", "data": map[string]interface{}{"text": originalTestTitle, "level": 1}}, map[string]interface{}{"type": "paragraph", "data": map[string]interface{}{"text": "Reviewed original content"}}}},
	}}
}

func digestBlog(source map[string]interface{}) string {
	b, _ := json.Marshal(source["blog"])
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

func prepareExport(t *testing.T, export map[string]interface{}, digest string) (map[string]interface{}, error) {
	t.Helper()
	raw, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	return prepare(raw, originalTestTitle, digest, time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC))
}

func TestPrepareAllowsOnlyEditorialAndDerivedSearchFields(t *testing.T) {
	export := originalExport()
	source := export["_source"].(map[string]interface{})
	before, _ := json.Marshal(export)
	plan, err := prepareExport(t, export, digestBlog(source))
	if err != nil {
		t.Fatal(err)
	}
	if plan["blog_id"] != "1uvwek" || plan["if_seq_no"] != int64(9007199254740993) || plan["if_primary_term"] != int64(2) {
		t.Fatalf("identity or CAS lost: %v", plan)
	}
	doc := plan["update"].(map[string]interface{})["doc"].(map[string]interface{})
	if doc["rss_guid"] != "https://monkeys.com.co/blog/the-ultimate-guide-to-grafana-what-it-is-why-it-used-and-how-to-build-a-complete-monitoring-1uvwek" {
		t.Fatal("RSS identity changed")
	}
	allowed := map[string]bool{"blog": true, "canonical_slug": true, "seo_description": true, "content_modified_at": true, "rss_guid": true, "title": true, "body": true, "summary": true, "tags": true}
	for k := range doc {
		if !allowed[k] {
			t.Errorf("unexpected write to %s", k)
		}
	}
	if doc["canonical_slug"] != "the-ultimate-guide-to-grafana-what-it-is-why-it-used-and-how-to-build-a-complete-monitoring-1uvwek" || doc["title"] != "Grafana Tutorial: Monitor Linux with Prometheus and Docker" || doc["content_modified_at"] != "2026-11-22T00:00:00Z" {
		t.Fatalf("incorrect revision: %v", doc)
	}
	if doc["seo_description"] != "Learn how to use Grafana to monitor a Linux server with Prometheus, Node Exporter and Docker Compose, with setup checks and fixes for missing data." {
		t.Fatal("wrong description")
	}
	merged := map[string]interface{}{}
	for k, v := range source {
		merged[k] = v
	}
	for k, v := range doc {
		merged[k] = v
	}
	derived := searchdoc.Build(merged)
	if doc["body"] != derived.Body || doc["summary"] != derived.Summary || !reflect.DeepEqual(doc["tags"], []string{"grafana", "linux"}) {
		t.Fatal("not derived with searchdoc")
	}
	for k, v := range source {
		if !allowed[k] && !reflect.DeepEqual(merged[k], v) {
			t.Errorf("changed %s", k)
		}
	}
	after, _ := json.Marshal(export)
	if string(after) != string(before) {
		t.Fatal("input mutated")
	}
	export["_source"] = merged
	again, err := prepareExport(t, export, digestBlog(source))
	if err != nil || again["already_applied"] != true || again["update"] != nil {
		t.Fatalf("rerun is not a no-op: %v %v", again, err)
	}
}

func TestPrepareRejectsUnsafeOrNewerDocuments(t *testing.T) {
	mutations := map[string]func(map[string]interface{}, map[string]interface{}){
		"wrong prefixed owner":       func(e, s map[string]interface{}) { s["owner_account_id"] = "owner9e48b1a5979fe25c187f2e20f024c20b" },
		"wrong owner":                func(e, s map[string]interface{}) { s["owner_account_id"] = "someone-else" },
		"wrong blog id":              func(e, s map[string]interface{}) { s["blog_id"] = "other" },
		"wrong ES id":                func(e, s map[string]interface{}) { e["_id"] = "other" },
		"draft":                      func(e, s map[string]interface{}) { s["is_draft"] = true },
		"missing draft flag":         func(e, s map[string]interface{}) { delete(s, "is_draft") },
		"archived":                   func(e, s map[string]interface{}) { s["is_archived"] = true },
		"scheduled":                  func(e, s map[string]interface{}) { s["is_scheduled"] = true },
		"restricted":                 func(e, s map[string]interface{}) { s["audience"] = "group_only" },
		"private group":              func(e, s map[string]interface{}) { s["group_visibility"] = "private" },
		"unlisted":                   func(e, s map[string]interface{}) { s["visibility"] = "unlisted" },
		"bad scope type":             func(e, s map[string]interface{}) { s["audience"] = true },
		"unpublished":                func(e, s map[string]interface{}) { delete(s, "published_time") },
		"future publication":         func(e, s map[string]interface{}) { s["published_time"] = "2099-01-01T00:00:00Z" },
		"newer timestamp":            func(e, s map[string]interface{}) { s["content_modified_at"] = "2026-11-23T00:00:00Z" },
		"invalid modified timestamp": func(e, s map[string]interface{}) { s["content_modified_at"] = "bad" },
		"newer content same title":   func(e, s map[string]interface{}) { s["blog"].(map[string]interface{})["new_content"] = "later edit" },
		"new title": func(e, s map[string]interface{}) {
			s["blog"].(map[string]interface{})["blocks"].([]interface{})[0].(map[string]interface{})["data"].(map[string]interface{})["text"] = "A later title"
		},
		"missing CAS":  func(e, s map[string]interface{}) { delete(e, "_seq_no") },
		"negative CAS": func(e, s map[string]interface{}) { e["_seq_no"] = -1 },
		"zero term":    func(e, s map[string]interface{}) { e["_primary_term"] = 0 },
		"bad CAS type": func(e, s map[string]interface{}) { e["_seq_no"] = "4" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			e := originalExport()
			s := e["_source"].(map[string]interface{})
			digest := digestBlog(s)
			mutate(e, s)
			if plan, err := prepareExport(t, e, digest); err == nil {
				t.Fatalf("unsafe input accepted: %v", plan)
			}
		})
	}
}

func TestPrepareRequiresReviewedBaselineAndSingleExport(t *testing.T) {
	e := originalExport()
	s := e["_source"].(map[string]interface{})
	raw, _ := json.Marshal(e)
	modified := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw           []byte
		title, digest string
		date          time.Time
	}{
		{raw, "", digestBlog(s), modified}, {raw, originalTestTitle, "", modified}, {raw, originalTestTitle, "bad", modified}, {append(raw, raw...), originalTestTitle, digestBlog(s), modified}, {[]byte(`{}`), originalTestTitle, digestBlog(s), modified}, {raw, originalTestTitle, digestBlog(s), time.Time{}},
	} {
		if _, err := prepare(tc.raw, tc.title, tc.digest, tc.date); err == nil {
			t.Fatal("accepted invalid baseline/export")
		}
	}
}

func TestPrepareRejectsFuturePublicationAndRevisionAgainstCurrentClock(t *testing.T) {
	future := time.Date(2099, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, bothFuture := range []bool{false, true} {
		t.Run(map[bool]string{false: "future revision", true: "both dates future"}[bothFuture], func(t *testing.T) {
			e := originalExport()
			s := e["_source"].(map[string]interface{})
			if bothFuture {
				s["published_time"] = "2099-01-01T00:00:00Z"
			}
			raw, _ := json.Marshal(e)
			if _, err := prepare(raw, originalTestTitle, digestBlog(s), future); err == nil {
				t.Fatal("future dates accepted")
			}
		})
	}
}

func TestOfflineCommandInspectionAndPreparation(t *testing.T) {
	e := originalExport()
	raw, _ := json.Marshal(e)
	path := filepath.Join(t.TempDir(), "export.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"-input", path, "-inspect"}, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	var inspection map[string]interface{}
	if err := json.Unmarshal(output.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	if inspection["original_title"] != originalTestTitle || inspection["original_blog_sha256"] != digestBlog(e["_source"].(map[string]interface{})) {
		t.Fatalf("inspection=%v", inspection)
	}
	output.Reset()
	if err := run([]string{"-input", path, "-original-title", originalTestTitle, "-original-blog-sha256", inspection["original_blog_sha256"].(string), "-modified-at", "2026-11-22T00:00:00Z"}, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"if_seq_no": 9007199254740993`)) || !bytes.Contains(output.Bytes(), []byte(`"doc"`)) {
		t.Fatalf("bad command output: %s", output.Bytes())
	}
	output.Reset()
	if err := run([]string{"-input", path, "-original-title", "wrong", "-original-blog-sha256", inspection["original_blog_sha256"].(string), "-modified-at", "2026-11-22T00:00:00Z"}, &output, io.Discard); err == nil || output.Len() != 0 {
		t.Fatal("unsafe command emitted update")
	}
}

func TestRevisionHasStableAnchorsAndMatchingMonitoringFiles(t *testing.T) {
	var revision map[string]interface{}
	if err := json.Unmarshal(revisionJSON, &revision); err != nil {
		t.Fatal(err)
	}
	blocks := revision["blog"].(map[string]interface{})["blocks"].([]interface{})
	headerIDs := map[string]bool{}
	codes := map[string]string{}
	toc := ""
	for i, raw := range blocks {
		block := raw.(map[string]interface{})
		id := block["id"].(string)
		data := block["data"].(map[string]interface{})
		if block["type"] == "header" {
			if data["anchor"] != id {
				t.Errorf("header %s lacks matching anchor", id)
			}
			if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(id) || headerIDs[id] {
				t.Fatalf("unsafe/duplicate header ID %s", id)
			}
			headerIDs[id] = true
			level := data["level"].(float64)
			if i == 0 && level != 1 || i > 0 && level != 2 && level != 3 {
				t.Fatalf("invalid heading hierarchy at %s", id)
			}
		}
		if i == 0 && block["type"] != "header" {
			t.Fatal("first block is not title header")
		}
		if block["type"] == "code" {
			codes[id] = data["code"].(string)
		}
		if id == "toc" {
			toc = data["text"].(string)
		}
	}
	links := regexp.MustCompile(`href="#([a-z0-9-]+)"`).FindAllStringSubmatch(toc, -1)
	if len(links) < 8 {
		t.Fatal("missing table of contents")
	}
	for _, link := range links {
		if !headerIDs[link[1]] {
			t.Errorf("broken anchor %s", link[1])
		}
	}
	for blockID, path := range map[string]string{"compose": "compose.yml", "prometheus": "prometheus.yml", "datasource": "datasource.yml", "dashboard-provider": "dashboards.yml"} {
		file, err := os.ReadFile(filepath.Join("monitoring", path))
		if err != nil {
			t.Fatal(err)
		}
		if codes[blockID] != strings.ReplaceAll(string(file), "\r\n", "\n") {
			t.Errorf("article and companion %s diverged", path)
		}
	}
	file, err := os.ReadFile(filepath.Join("monitoring", "linux-dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dashboard map[string]interface{}
	if err := json.Unmarshal(file, &dashboard); err != nil {
		t.Fatal(err)
	}
	panels := dashboard["panels"].([]interface{})
	if len(panels) != 3 {
		t.Fatal("expected three configured metrics panels")
	}
	for i, id := range []string{"cpu-query", "memory-query", "disk-query"} {
		panel := panels[i].(map[string]interface{})
		target := panel["targets"].([]interface{})[0].(map[string]interface{})
		if target["expr"] != codes[id] {
			t.Errorf("dashboard and article %s query differ", id)
		}
	}
}
