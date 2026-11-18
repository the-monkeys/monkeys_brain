package middleware

import (
	"github.com/gin-gonic/gin"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdempotencyHeaderAllowedByCors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, handler := range map[string]gin.HandlerFunc{"strict": CORSMiddleware(`^https://ui\.example$`), "library": NewCorsMiddleware(), "temporary": TmpCORSMiddleware()} {
		t.Run(name, func(t *testing.T) {
			r := gin.New()
			r.Use(handler)
			r.POST("/api/v1/events", func(c *gin.Context) { c.Status(201) })
			req := httptest.NewRequest("OPTIONS", "/api/v1/events", nil)
			req.Header.Set("Origin", "https://ui.example")
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "content-type,idempotency-key")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			allowed := strings.ToLower(w.Header().Get("Access-Control-Allow-Headers"))
			if w.Code != 204 || (!strings.Contains(allowed, "idempotency-key") && allowed != "*") {
				t.Fatalf("preflight rejected keyed creation: %d %v", w.Code, w.Header())
			}
		})
	}
}

// keyedBodyReader detects an accidental global read before authentication.
type keyedBodyReader struct{ reads int }

func (b *keyedBodyReader) Read(p []byte) (int, error) { b.reads++; return 0, io.EOF }
func (b *keyedBodyReader) Close() error               { return nil }

func TestKeyedCreationBodyIsNotReadBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/api/v1/events", "/api/v1/events/series", "/api/v1/events/:slug/clone", "/api/v1/groups", "/api/v1/groups/:slug/events", "/api/v1/user/verification"} {
		r := gin.New()
		r.Use(LogRequestBody())
		r.POST(path, func(c *gin.Context) { c.AbortWithStatus(401) })
		actual := strings.ReplaceAll(path, ":slug", "resource")
		body := &keyedBodyReader{}
		req := httptest.NewRequest("POST", actual, nil)
		req.Body = body
		req.ContentLength = -1
		req.Header.Set("Idempotency-Key", "same")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 401 || body.reads != 0 {
			t.Fatalf("%s body read before auth: status=%d reads=%d", path, w.Code, body.reads)
		}
	}
}
