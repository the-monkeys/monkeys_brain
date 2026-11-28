package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPrivateNoStorePreservesVary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		vary []string
	}{
		{"empty", nil},
		{"cors", []string{"Origin"}},
		{"multiple lines and existing credentials", []string{"Origin, cookie", "Accept-Encoding, AUTHORIZATION"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Header("Cache-Control", "public, max-age=600")
				for _, value := range tc.vary {
					c.Writer.Header().Add("Vary", value)
				}
				c.Next()
			}, PrivateNoStore(), PrivateNoStore())
			router.GET("/private", func(c *gin.Context) { c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"}) })
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/private", nil))
			// Result snapshots the headers actually sent, catching headers set too late.
			header := w.Result().Header
			if got := header.Get("Cache-Control"); got != "private, no-store" {
				t.Errorf("Cache-Control = %q", got)
			}
			counts := map[string]int{}
			for _, value := range header.Values("Vary") {
				for _, field := range strings.Split(value, ",") {
					counts[strings.ToLower(strings.TrimSpace(field))]++
				}
			}
			for _, field := range []string{"cookie", "authorization"} {
				if counts[field] != 1 {
					t.Errorf("Vary field %s count = %d, want 1", field, counts[field])
				}
			}
			for _, value := range tc.vary {
				for _, field := range strings.Split(value, ",") {
					if counts[strings.ToLower(strings.TrimSpace(field))] != 1 {
						t.Errorf("existing Vary field lost or duplicated: %s", field)
					}
				}
			}
		})
	}
}
