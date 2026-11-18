package idempotency

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPrivateJSONStoresOnlyReferenceAndResolvesOnReplay(t *testing.T) {
	s := newMemoryStore()
	g := &Guard{store: s}
	var creates, resolves atomic.Int32
	reference := func(body []byte) ([]byte, error) {
		var v struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, err
		}
		return json.Marshal(v)
	}
	failResolve := false
	resolve := func(c *gin.Context, ref []byte) ([]byte, error) {
		resolves.Add(1)
		if failResolve {
			return nil, errors.New("private lookup failed")
		}
		if c.GetString("accountId") != "alice" || string(ref) != `{"id":"private-request"}` {
			t.Error("replay was not scoped to original account and resource")
		}
		return []byte(`{"id":"private-request","proof":"private-document"}`), nil
	}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("accountId", "alice"); c.Next() })
	r.POST("/api/v1/events", g.PrivateJSON(reference, resolve), func(c *gin.Context) {
		creates.Add(1)
		c.JSON(201, gin.H{"id": "private-request", "proof": "private-document"})
	})
	first := request(r, "POST", "/api/v1/events", "alice", "same", `{"proof":"private-document"}`)
	for _, entry := range s.entries {
		if string(entry.record.Body) != `{"id":"private-request"}` {
			t.Fatalf("private result stored: %s", entry.record.Body)
		}
	}
	again := request(r, "POST", "/api/v1/events", "alice", "same", `{"proof":"private-document"}`)
	if first.Code != 201 || again.Code != 201 || first.Body.String() != again.Body.String() || creates.Load() != 1 || resolves.Load() != 1 {
		t.Fatalf("private replay changed response or recreated resource: first=%d %s again=%d %s creates=%d resolves=%d", first.Code, first.Body, again.Code, again.Body, creates.Load(), resolves.Load())
	}
	failResolve = true
	assertCode(t, request(r, "POST", "/api/v1/events", "alice", "same", `{"proof":"private-document"}`), 503, "idempotency_unavailable")
	if creates.Load() != 1 {
		t.Fatal("private lookup failure re-executed creation")
	}
}

func TestAuthenticationAndPermissionRecheckedBeforeReplay(t *testing.T) {
	s := newMemoryStore()
	var creates atomic.Int32
	authorized := true
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if !authorized {
			c.AbortWithStatusJSON(403, gin.H{"error": "permission denied"})
			return
		}
		c.Set("accountId", "alice")
		c.Next()
	})
	r.POST("/api/v1/events", (&Guard{store: s}).Handler(), func(c *gin.Context) { creates.Add(1); c.JSON(201, gin.H{"id": "private-created"}) })
	request(r, "POST", "/api/v1/events", "alice", "same", `{}`)
	authorized = false
	w := request(r, "POST", "/api/v1/events", "alice", "same", `{}`)
	if w.Code != 403 || strings.Contains(w.Body.String(), "private-created") || creates.Load() != 1 {
		t.Fatal("replay bypassed existing security guard")
	}
}

func TestMalformedReservationNeverAllowsCreation(t *testing.T) {
	s := newMemoryStore()
	r := testRouter(&Guard{store: s}, func(c *gin.Context) { c.JSON(201, gin.H{"id": "first"}) })
	request(r, "POST", "/api/v1/events", "alice", "same", `{}`)
	for key, entry := range s.entries {
		entry.record.Body = []byte(`not-json`)
		s.entries[key] = entry
	}
	assertCode(t, request(r, "POST", "/api/v1/events", "alice", "same", `{}`), 503, "idempotency_unavailable")
}

func TestEmptyKeyHeaderDoesNotBypassProtection(t *testing.T) {
	r := testRouter(New(nil), func(c *gin.Context) { t.Fatal("empty keyed header reached creation") })
	req := httptest.NewRequest("POST", "/api/v1/events", strings.NewReader(`{}`))
	req.Header.Set("Test-Account", "alice")
	req.Header.Set("Idempotency-Key", "")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertCode(t, w, 400, "idempotency_key_invalid")
}

func TestFingerprintDistinguishesExactBodyBytes(t *testing.T) {
	s := newMemoryStore()
	var creates atomic.Int32
	r := testRouter(&Guard{store: s}, func(c *gin.Context) { creates.Add(1); c.JSON(201, gin.H{"id": "once"}) })
	// encoding/json replaces different invalid UTF-8 bytes when marshaling a
	// string; request fingerprints must hash bytes without that normalization.
	first := string([]byte{'{', '"', 'v', '"', ':', '"', 0xff, '"', '}'})
	changed := string([]byte{'{', '"', 'v', '"', ':', '"', 0xfe, '"', '}'})
	request(r, "POST", "/api/v1/events", "alice", "same", first)
	assertCode(t, request(r, "POST", "/api/v1/events", "alice", "same", changed), 409, "idempotency_key_reused")
	if creates.Load() != 1 {
		t.Fatal("changed body created twice")
	}
}
