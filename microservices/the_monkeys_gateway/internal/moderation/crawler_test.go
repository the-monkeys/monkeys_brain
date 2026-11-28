package moderation

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type crawlerFixture struct{ allow bool }

func (f crawlerFixture) Enabled() bool             { return true }
func (f crawlerFixture) Verify(*http.Request) bool { return f.allow }

const crawlerBlog = `{"blog_id":"restricted","audience":"public","is_draft":false,"published_time":"2026-01-01T00:00:00Z","title":"original secret","canonical_slug":"original","blog":{"blocks":[{"type":"paragraph","data":{"text":"original secret body"}}]},"private_email":"private@example.test","extra_private":"do not expose"}`

func crawl(t *testing.T, path, raw string, f *fakePolicy, allow bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := New(f)
	s.crawler = crawlerFixture{allow}
	r.Use(s.Middleware(nil))
	r.GET(path, func(c *gin.Context) { c.Data(200, "application/json", []byte(raw)) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}
func TestCrawlerGetsOriginalPublicArticleWithoutHumanReveal(t *testing.T) {
	f := &fakePolicy{}
	w := crawl(t, "/api/v2/blog/restricted", crawlerBlog, f, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "original secret body") || !strings.Contains(w.Body.String(), `"seo_crawler":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private@example.test") || strings.Contains(w.Body.String(), "do not expose") || strings.Contains(w.Body.String(), `"revealed":true`) {
		t.Fatal("crawler inherited private or human grant", w.Body.String())
	}
	if f.lastAccount != "" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("identity/cache regression")
	}
}
func TestCrawlerDoesNotBypassPublicPolicyOrLists(t *testing.T) {
	cases := []struct {
		path, raw string
		policy    *fakePolicy
	}{
		{"/api/v2/blog/restricted", crawlerBlog, &fakePolicy{denied: true}},
		{"/api/v2/blog/restricted", strings.Replace(crawlerBlog, `"public"`, `"group_only"`, 1), &fakePolicy{}},
		{"/api/v2/blog/restricted", strings.Replace(crawlerBlog, `"is_draft":false`, `"is_draft":true`, 1), &fakePolicy{}},
		{"/api/v2/blog/restricted", strings.Replace(crawlerBlog, "2026-01-01", "2099-01-01", 1), &fakePolicy{}},
		{"/api/v2/blog/meta-feed", `{"blogs":[` + crawlerBlog + `]}`, &fakePolicy{}},
		{"/api/v2/blog/other", crawlerBlog, &fakePolicy{}},
		{"/api/v2/blog/restricted", crawlerBlog, &fakePolicy{fail: true}},
	}
	for _, tc := range cases {
		w := crawl(t, tc.path, tc.raw, tc.policy, true)
		if strings.Contains(w.Body.String(), "original secret") || strings.Contains(w.Body.String(), `"seo_crawler":true`) {
			t.Fatal(tc.path, w.Body.String())
		}
	}
}
func TestUnverifiedReaderKeepsRedactionAndOptionalCapability(t *testing.T) {
	w := crawl(t, "/api/v2/blog/restricted", crawlerBlog, &fakePolicy{}, false)
	if strings.Contains(w.Body.String(), "original secret") || strings.Contains(w.Body.String(), `"seo_crawler":true`) {
		t.Fatal(w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"seo_indexable":true`) {
		t.Fatal("missing optional rollout capability", w.Body.String())
	}
}
func TestCrawlerNeverTrustsContentSuppliedCapability(t *testing.T) {
	raw := strings.TrimSuffix(crawlerBlog, "}") + `,"seo_crawler":true,"seo_indexable":true}`
	w := serve(t, "/api/v2/blog/restricted", raw, &fakePolicy{})
	if strings.Contains(w.Body.String(), `"seo_crawler":true`) || strings.Contains(w.Body.String(), `"seo_indexable":true`) {
		t.Fatal(w.Body.String())
	}
}

func TestLegacyPublicMetadataUsesAnonymousPolicyInsteadOfOptionalAudience(t *testing.T) {
	raw := strings.Replace(crawlerBlog, `"audience":"public",`, "", 1)
	feed := crawl(t, "/api/v2/blog/meta-feed", `{"blogs":[`+raw+`]}`, &fakePolicy{}, false)
	if !strings.Contains(feed.Body.String(), `"seo_indexable":true`) || !strings.Contains(feed.Body.String(), `"audience":"public"`) || strings.Contains(feed.Body.String(), "original secret") {
		t.Fatal(feed.Body.String())
	}
	detail := crawl(t, "/api/v2/blog/restricted", raw, &fakePolicy{}, true)
	if !strings.Contains(detail.Body.String(), "original secret body") {
		t.Fatal(detail.Body.String())
	}
	denied := crawl(t, "/api/v2/blog/restricted", raw, &fakePolicy{denied: true}, true)
	if strings.Contains(denied.Body.String(), "original secret") || strings.Contains(denied.Body.String(), `"seo_indexable":true`) {
		t.Fatal(denied.Body.String())
	}
}
