package storage_v2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
)

type profileFixture struct {
	sync.Mutex
	etag, body   string
	status, gets int
}

func profileCacheFixture(t *testing.T) (*Service, *profileFixture) {
	t.Helper()
	f := &profileFixture{etag: "old", body: "old-avatar", status: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.Lock()
		defer f.Unlock()
		if r.Method != http.MethodHead && r.Method != http.MethodGet {
			t.Errorf("avatar read attempted %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status := f.status
		if r.URL.Path != "/public-avatars/profiles/alice/profile.png" {
			status = http.StatusNotFound
		}
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(status)
			fmt.Fprint(w, "<Error><Code>NoSuchKey</Code></Error>")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprint(len(f.body)))
		w.Header().Set("ETag", `"`+f.etag+`"`)
		w.Header().Set("Last-Modified", "Sun, 18 Oct 2026 20:00:00 GMT")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			f.gets++
			fmt.Fprint(w, f.body)
		}
	}))
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	mc, err := minio.New(u.Host, &minio.Options{Secure: false, Region: "us-east-1", Creds: credentials.NewStaticV4("fixture", "fixture", "")})
	if err != nil {
		t.Fatal(err)
	}
	return &Service{mc: mc, bucket: "public-avatars", log: zap.NewNop().Sugar()}, f
}

func profileCacheRead(s *Service, method, validator, modified string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/v2/storage/profiles/alice/profile", nil)
	c.Request.Header.Set("If-None-Match", validator)
	c.Request.Header.Set("If-Modified-Since", modified)
	c.Params = gin.Params{{Key: "user_id", Value: "alice"}}
	if method == http.MethodHead {
		s.HeadProfileImage(c)
	} else {
		s.GetProfileImage(c)
	}
	c.Writer.WriteHeaderNow()
	return w
}

func TestProfileImageValidatorsAndReplacement(t *testing.T) {
	s, f := profileCacheFixture(t)
	w := profileCacheRead(s, http.MethodGet, "", "")
	if w.Code != 200 || w.Body.String() != "old-avatar" {
		t.Fatalf("initial image: %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get("ETag") != `"old"` {
		t.Fatalf("ETag must be quoted: %q", w.Header().Get("ETag"))
	}
	if w.Header().Get("Cache-Control") != "public, no-cache, max-age=0, must-revalidate" {
		t.Fatalf("mutable avatar must revalidate: %q", w.Header().Get("Cache-Control"))
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, validator := range []string{`"old"`, `W/"old"`, `"other", W/"old"`, "*"} {
			w = profileCacheRead(s, method, validator, "")
			if w.Code != 304 || w.Body.Len() != 0 {
				t.Fatalf("%s %s: got %d body %q", method, validator, w.Code, w.Body.String())
			}
		}
	}
	f.Lock()
	gets := f.gets
	f.etag = "new"
	f.body = "new-avatar"
	f.Unlock()
	if gets != 1 {
		t.Fatalf("conditional requests downloaded image %d times", gets)
	}
	w = profileCacheRead(s, http.MethodGet, `"old"`, "Sun, 18 Oct 2026 20:00:00 GMT")
	if w.Code != 200 || w.Body.String() != "new-avatar" || w.Header().Get("ETag") != `"new"` {
		t.Fatalf("old validator after upload must get new image: %d %q %q", w.Code, w.Body.String(), w.Header().Get("ETag"))
	}
}

func TestProfileImageModifiedSinceAndMissing(t *testing.T) {
	s, f := profileCacheFixture(t)
	w := profileCacheRead(s, http.MethodGet, "", "Sun, 18 Oct 2026 20:00:00 GMT")
	if w.Code != 304 {
		t.Fatalf("unchanged image: %d", w.Code)
	}
	w = profileCacheRead(s, http.MethodHead, "", "invalid")
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD invalid date: %d %q", w.Code, w.Body.String())
	}
	f.Lock()
	f.status = http.StatusNotFound
	f.Unlock()
	w = profileCacheRead(s, http.MethodGet, "*", "")
	if w.Code != 404 {
		t.Fatalf("missing must stay 404: %d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing must not persist in HTTP caches: %q", w.Header().Get("Cache-Control"))
	}
}

func TestProfileImageStorageFailureIsNotMissing(t *testing.T) {
	s, f := profileCacheFixture(t)
	f.Lock()
	f.status = http.StatusForbidden
	f.Unlock()
	w := profileCacheRead(s, http.MethodGet, "", "")
	if w.Code != 500 {
		t.Fatalf("storage failure must not be cached as missing: %d", w.Code)
	}
	if strings.Contains(w.Header().Get("Cache-Control"), "public") {
		t.Fatal("storage failure must not receive public caching")
	}
}
