package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"google.golang.org/grpc"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePolicy struct {
	resolved bool
	pb.ModerationServiceClient
	fail        bool
	calls       int
	media       bool
	revealed    bool
	denied      bool
	lastAccount string
}

func (f *fakePolicy) Check(_ context.Context, r *pb.CheckRequest, _ ...grpc.CallOption) (*pb.CheckResponse, error) {
	f.calls++
	f.lastAccount = r.AccountId
	if f.denied {
		out := &pb.CheckResponse{}
		for _, t := range r.Targets {
			out.Items = append(out.Items, &pb.Access{Target: t, Reason: "not_found"})
		}
		return out, nil
	}
	if f.fail {
		return nil, errors.New("unavailable")
	}
	out := &pb.CheckResponse{}
	for _, target := range r.Targets {
		out.Items = append(out.Items, &pb.Access{Target: target, IsNsfw: target.Id == "restricted", Reason: "login_required", Revealed: f.revealed, CanReveal: f.revealed, Revision: 1, ScanStatus: "unscanned"})
	}
	return out, nil
}
func (f *fakePolicy) CheckMedia(context.Context, *pb.MediaRequest, ...grpc.CallOption) (*pb.MediaResponse, error) {
	if f.fail {
		return nil, errors.New("unavailable")
	}
	return &pb.MediaResponse{Allowed: f.media, Restricted: !f.media, Resolved: f.resolved}, nil
}
func serve(t *testing.T, path, body string, f *fakePolicy) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := New(f)
	r.Use(s.Middleware(nil))
	r.GET(path, func(c *gin.Context) { c.Data(200, "application/json", []byte(body)) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}
func TestRestrictedDetailRemovesAllRawContent(t *testing.T) {
	raw := `{"blog_id":"restricted","title":"secret title","owner_account_id":"owner","blog":{"blocks":[{"data":{"text":"secret body","url":"secret.jpg"}}]},"first_image":"secret.jpg","first_paragraph":"secret excerpt","extra":{"url":"secret.jpg"}}`
	w := serve(t, "/api/v2/blog/restricted", raw, &fakePolicy{})
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("leaked restricted article: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"is_nsfw":true`) {
		t.Fatal(w.Body.String())
	}
}
func TestKnownEnvelopesKeepPaginationAndSafeContent(t *testing.T) {
	raw := `{"blogs":[{"blog_id":"safe","title":"Keep me","blog":{"blocks":[{"data":{"blog_id":"restricted","text":"ordinary author JSON"}}]}},{"blog_id":"restricted","title":"secret"}],"total_blogs":22,"has_more":true}`
	w := serve(t, "/api/v2/blog/homepage", raw, &fakePolicy{})
	if !strings.Contains(w.Body.String(), "Keep me") || !strings.Contains(w.Body.String(), "ordinary author JSON") || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), `"total_blogs":22`) {
		t.Fatal(w.Body.String())
	}
}
func TestFilterFailsClosedOnResolverFailure(t *testing.T) {
	w := serve(t, "/api/v2/blog/restricted", `{"blog_id":"restricted","title":"secret"}`, &fakePolicy{fail: true})
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestEventAndGroupAndDiscussionProjection(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/api/v1/events/restricted", `{"event":{"slug":"restricted","title":"secret","description":"secret","cover_image":"secret.jpg"}}`},
		{"/api/v1/groups/restricted", `{"group":{"slug":"restricted","name":"secret","description":"secret","logo_image":"secret.jpg"}}`},
		{"/api/v1/discussions/restricted", `{"public_id":"restricted","body":"secret","files":[{"storage_key":"secret"}],"replies":[{"body":"secret"}]}`},
	} {
		w := serve(t, tc.path, tc.body, &fakePolicy{})
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal(tc.path, w.Body.String())
		}
	}
}
func TestMediaBothAliasesDenyBeforeHandler(t *testing.T) {
	for _, prefix := range []string{"/api/v2/storage", "/api/storage"} {
		for _, method := range []string{"GET", "HEAD"} {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			f := &fakePolicy{}
			r.Use(New(f).Middleware(nil))
			called := false
			path := prefix + "/posts/restricted/image.png"
			r.Handle(method, path, func(c *gin.Context) { called = true; c.String(200, "secret image") })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if called || w.Code != http.StatusNotFound {
				t.Fatal(path, method, w.Code, called)
			}
		}
	}
}
func TestRevealedAdultDetailAllowedButShareStaysSafe(t *testing.T) {
	f := &fakePolicy{revealed: true}
	detail := serve(t, "/api/v2/blog/restricted", `{"blog_id":"restricted","title":"secret"}`, f)
	if !strings.Contains(detail.Body.String(), "secret") {
		t.Fatal(detail.Body.String())
	}
	share := serve(t, "/api/v1/events/restricted/share", `{"og_title":"secret","og_image":"secret.jpg"}`, f)
	if strings.Contains(share.Body.String(), "secret") {
		t.Fatal(share.Body.String())
	}
}
func TestUnrelatedRoutesAreUntouched(t *testing.T) {
	f := &fakePolicy{fail: true}
	w := serve(t, "/healthz", `{"status":"healthy"}`, f)
	if w.Code != 200 || f.calls != 0 {
		t.Fatal(w.Code, f.calls)
	}
}

func TestLargePagesAreCheckedInBoundedBatches(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"blogs":[`)
	for i := 0; i < 150; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"blog_id":"safe","title":"ok"}`)
	}
	b.WriteString(`]}`)
	f := &fakePolicy{}
	w := serve(t, "/api/v2/blog/feed", b.String(), f)
	if w.Code != 200 || f.calls != 2 {
		t.Fatal(w.Code, f.calls)
	}
}
func TestWebsocketUpgradeNeedsRevealBeforeHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(New(&fakePolicy{}).Middleware(nil))
	called := false
	r.GET("/api/v2/blog/draft/:blog_id", func(c *gin.Context) { called = true; c.String(200, "secret") })
	req := httptest.NewRequest("GET", "/api/v2/blog/draft/restricted", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if called || w.Code != 403 {
		t.Fatal(called, w.Code, w.Body.String())
	}
}
func TestMediaCannotRestorePublicCacheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(New(&fakePolicy{media: true}).Middleware(nil))
	r.GET("/api/v2/storage/posts/safe/image.png", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000")
		c.Writer.WriteHeaderNow()
		c.String(200, "image")
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v2/storage/posts/safe/image.png", nil))
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Header())
	}
}

func TestReportEndpointUsesAuthenticatedActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := &reportPolicy{}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("accountId", "trusted"); c.Next() })
	r.POST("/api/v1/moderation/:kind/:id/report", New(f).Report)
	req := httptest.NewRequest("POST", "/api/v1/moderation/blog/post/report", strings.NewReader(`{"reason":"nsfw","details":"review","actor":{"account_id":"admin"},"role":"Admin"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || f.request.Actor.AccountId != "trusted" || f.request.Target.Kind != "blog" || f.request.Reason != "nsfw" {
		t.Fatal(w.Code, f.request)
	}
}

type reportPolicy struct {
	pb.ModerationServiceClient
	request *pb.ActionRequest
}

func (f *reportPolicy) ReportContent(_ context.Context, r *pb.ActionRequest, _ ...grpc.CallOption) (*pb.ActionResponse, error) {
	f.request = r
	return &pb.ActionResponse{Report: &pb.Report{Id: 1, Status: "pending"}}, nil
}
func TestReportErrorsDoNotEchoPrivateDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/error", func(c *gin.Context) { fail(c, errors.New("secret database password or DOB")) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/error", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDeniedPolicyCannotExposeStaleRawDocuments(t *testing.T) {
	w := serve(t, "/api/v2/blog/restricted", `{"blog_id":"restricted","title":"secret","blog":{"blocks":[{"data":{"text":"secret"}}]}}`, &fakePolicy{denied: true})
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Body.String())
	}
}

func TestAccessEndpointUsesVerifiedSessionAndNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := &fakePolicy{}
	r := gin.New()
	r.GET("/api/v1/moderation/:kind/:id", func(c *gin.Context) { c.Set("accountId", "trusted"); c.Next() }, New(f).Access)
	req := httptest.NewRequest("GET", "/api/v1/moderation/blog/restricted?account_id=admin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || f.lastAccount != "trusted" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, f.lastAccount, w.Header())
	}
}

func TestMediaPolicyReadRequiresResolvedReferenceAndHidesInternalState(t *testing.T) {
	for _, tc := range []struct{ resolved, want bool }{{false, false}, {true, true}} {
		gin.SetMode(gin.TestMode)
		f := &fakePolicy{media: true, resolved: tc.resolved}
		r := gin.New()
		r.GET("/media", New(f).Media)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/media?path=%2Fapi%2Fv2%2Fstorage%2Fposts%2Fpost%2Fimage.png", nil))
		expected := `"allowed":false`
		if tc.want {
			expected = `"allowed":true`
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), expected) || strings.Contains(w.Body.String(), "resolved") || strings.Contains(w.Body.String(), "restricted") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestMediaPolicyReadRejectsUnrelatedPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/media", New(&fakePolicy{media: true, resolved: true}).Media)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/media?path=http%3A%2F%2Fevil.test%2Fprivate", nil))
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSafeViewsCarryPolicyRevisionWithoutChangingBody(t *testing.T) {
	w := serve(t, "/api/v2/blog/safe", `{"blog_id":"safe","title":"Original title","blog":{"blocks":[{"data":{"text":"Original body"}}]}}`, &fakePolicy{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"content_rating"`) || !strings.Contains(w.Body.String(), "Original body") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestHomepageRestrictionPreservesAssessmentAndCardOrder(t *testing.T) {
	raw := `{"blogs":[{"blog_id":"restricted","title":"secret headline","first_image":"secret.jpg","homepage_eligibility":{"eligible":true,"lead_eligible":true,"word_count":351,"minimum_words":250,"reasons":[],"version":"homepage-v1","extra":"secret"}},{"blog_id":"safe","title":"Keep me"}],"has_more":false,"homepage_policy_version":"homepage-v1"}`
	w := serve(t, "/api/v2/blog/homepage", raw, &fakePolicy{})
	var response struct {
		Blogs   []map[string]any `json:"blogs"`
		Version string           `json:"homepage_policy_version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(response.Blogs) != 2 || response.Blogs[0]["blog_id"] != "restricted" || response.Blogs[1]["blog_id"] != "safe" || response.Version != "homepage-v1" {
		t.Fatal(w.Code, w.Body.String())
	}
	assessment, ok := response.Blogs[0]["homepage_eligibility"].(map[string]any)
	if !ok || len(assessment) != 6 || assessment["eligible"] != true || assessment["lead_eligible"] != true || assessment["word_count"] != float64(351) || assessment["minimum_words"] != float64(250) || assessment["version"] != "homepage-v1" {
		t.Fatalf("homepage client cannot retain concealed card: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("raw content survived concealment", w.Body.String())
	}
}
