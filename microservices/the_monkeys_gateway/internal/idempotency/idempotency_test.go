package idempotency

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type memoryEntry struct {
	record  record
	expires time.Time
}
type memoryStore struct {
	mu                                  sync.Mutex
	entries                             map[string]memoryEntry
	now                                 time.Time
	failReserve, failLoad, failComplete bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{entries: map[string]memoryEntry{}, now: time.Now()}
}
func (s *memoryStore) reserve(_ context.Context, key string, value record, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failReserve {
		return false, errors.New("redis unavailable")
	}
	if old, ok := s.entries[key]; ok && s.now.Before(old.expires) {
		return false, nil
	}
	s.entries[key] = memoryEntry{value, s.now.Add(ttl)}
	return true, nil
}
func (s *memoryStore) load(_ context.Context, key string) (record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failLoad {
		return record{}, errors.New("redis unavailable")
	}
	old, ok := s.entries[key]
	if !ok || !s.now.Before(old.expires) {
		return record{}, errors.New("expired")
	}
	return old.record, nil
}
func (s *memoryStore) complete(ctx context.Context, key string, before, after record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failComplete {
		return errors.New("redis unavailable")
	}
	old, ok := s.entries[key]
	if !ok || !s.now.Before(old.expires) || old.record.Owner != before.Owner {
		return errors.New("lost reservation")
	}
	old.record = after
	s.entries[key] = old
	return nil
}
func testRouter(g *Guard, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if id := c.GetHeader("Test-Account"); id != "" {
			c.Set("accountId", id)
		}
		c.Next()
	})
	r.POST("/api/v1/events", g.Handler(), handler)
	r.POST("/api/v1/events/series", g.Handler(), handler)
	r.POST("/api/v1/events/:slug/clone", g.Handler(), handler)
	r.PUT("/api/v1/events", g.Handler(), handler)
	return r
}
func request(r http.Handler, method, path, account, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	req.Header.Set("Test-Account", account)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func assertCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("got %d %s; want %d %s", w.Code, w.Body.String(), status, code)
	}
}
func TestSequentialReplayPreservesResourceAndOnlySafeHeaders(t *testing.T) {
	s := newMemoryStore()
	var calls atomic.Int32
	r := testRouter(&Guard{store: s}, func(c *gin.Context) {
		calls.Add(1)
		c.Header("Set-Cookie", "secret=token")
		c.Header("Authorization", "Bearer secret")
		c.Header("X-Private", "secret")
		c.JSON(201, gin.H{"id": "created-once", "existing_field": true})
	})
	first := request(r, "POST", "/api/v1/events", "account-private", "key-private", `{"title":"private body"}`)
	second := request(r, "POST", "/api/v1/events", "account-private", "key-private", `{"title":"private body"}`)
	if first.Code != 201 || second.Code != 201 || first.Body.String() != second.Body.String() || calls.Load() != 1 {
		t.Fatalf("first=%d %s second=%d %s creates=%d", first.Code, first.Body, second.Code, second.Body, calls.Load())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" || second.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing replay/privacy headers: %v", second.Header())
	}
	for _, header := range []string{"Set-Cookie", "Authorization", "X-Private"} {
		if second.Header().Get(header) != "" {
			t.Fatalf("replayed secret header %s", header)
		}
	}
	for key, entry := range s.entries {
		for _, raw := range []string{"account-private", "key-private", "private body"} {
			if strings.Contains(key, raw) || strings.Contains(entry.record.Fingerprint, raw) {
				t.Fatal("unhashed identity or payload")
			}
		}
		if entry.expires.Sub(s.now) != 24*time.Hour {
			t.Fatal("reservation must expire after 24 hours")
		}
	}
}
func TestConcurrentRequestsAcrossGatewayInstancesCreateOnce(t *testing.T) {
	s := newMemoryStore()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var calls atomic.Int32
	handler := func(c *gin.Context) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		c.JSON(201, gin.H{"id": "once"})
	}
	first, second := testRouter(&Guard{store: s}, handler), testRouter(&Guard{store: s}, handler)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(first, "POST", "/api/v1/events", "alice", "same", `{}`) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first request did not enter")
	}
	w := request(second, "POST", "/api/v1/events", "alice", "same", `{}`)
	assertCode(t, w, 409, "idempotency_in_progress")
	if w.Header().Get("Retry-After") != "2" || calls.Load() != 1 {
		t.Fatal("concurrent request executed or lacks retry guidance")
	}
	unblock()
	select {
	case firstResult := <-done:
		if firstResult.Code != 201 {
			t.Fatalf("first create failed: %d", firstResult.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first create hung")
	}
	replay := request(second, "POST", "/api/v1/events", "alice", "same", `{}`)
	if replay.Code != 201 || replay.Header().Get("Idempotency-Replayed") != "true" || calls.Load() != 1 {
		t.Fatalf("cross-instance replay: %d %s count=%d", replay.Code, replay.Body, calls.Load())
	}
}
func TestPayloadMismatchAndRequestScope(t *testing.T) {
	s := newMemoryStore()
	var calls atomic.Int32
	r := testRouter(&Guard{store: s}, func(c *gin.Context) { calls.Add(1); c.JSON(201, gin.H{"id": "created"}) })
	request(r, "POST", "/api/v1/events", "alice", "same", `{"title":"one"}`)
	assertCode(t, request(r, "POST", "/api/v1/events", "alice", "same", `{"title":"two"}`), 409, "idempotency_key_reused")
	assertCode(t, request(r, "POST", "/api/v1/events?mode=changed", "alice", "same", `{"title":"one"}`), 409, "idempotency_key_reused")
	for _, tc := range []struct{ method, path, account string }{{"POST", "/api/v1/events", "bob"}, {"POST", "/api/v1/events/series", "alice"}, {"POST", "/api/v1/events/first/clone", "alice"}, {"POST", "/api/v1/events/second/clone", "alice"}, {"PUT", "/api/v1/events", "alice"}} {
		if w := request(r, tc.method, tc.path, tc.account, "same", `{"title":"two"}`); w.Code != 201 {
			t.Fatalf("scope isolation: %d %s", w.Code, w.Body)
		}
	}
	if calls.Load() != 6 {
		t.Fatalf("creation count %d", calls.Load())
	}
}
func TestExpiryAllowsNewIntent(t *testing.T) {
	s := newMemoryStore()
	var calls atomic.Int32
	r := testRouter(&Guard{store: s}, func(c *gin.Context) { calls.Add(1); c.JSON(201, gin.H{"id": "created"}) })
	request(r, "POST", "/api/v1/events", "alice", "same", `{}`)
	s.now = s.now.Add(24*time.Hour + time.Second)
	w := request(r, "POST", "/api/v1/events", "alice", "same", `{"new":true}`)
	if w.Code != 201 || calls.Load() != 2 {
		t.Fatalf("expired key blocked: %d %s count=%d", w.Code, w.Body, calls.Load())
	}
}
func TestRedisErrorsFailClosedAndLegacyRequestsBypass(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    store
	}{{"unconfigured", nil}, {"reserve failure", &memoryStore{failReserve: true}}, {"read failure", func() store { s := newMemoryStore(); s.failLoad = true; return s }()}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			r := testRouter(&Guard{store: tc.s}, func(c *gin.Context) { calls.Add(1); c.JSON(201, gin.H{"id": "legacy"}) })
			if tc.name == "read failure" {
				request(r, "POST", "/api/v1/events", "alice", "same", `{}`)
				calls.Store(0)
			}
			assertCode(t, request(r, "POST", "/api/v1/events", "alice", "same", `{}`), 503, "idempotency_unavailable")
			if calls.Load() != 0 {
				t.Fatal("creation proceeded without reservation")
			}
			for i := 0; i < 2; i++ {
				w := request(r, "POST", "/api/v1/events", "alice", "", `{}`)
				if w.Code != 201 {
					t.Fatal("legacy flow changed")
				}
			}
			if calls.Load() != 2 {
				t.Fatal("legacy requests deduplicated")
			}
		})
	}
}
func TestUncertainOutcomesRetainReservation(t *testing.T) {
	for _, outcome := range []string{"5xx", "cancel", "panic", "completion failure", "non-json", "oversize", "4xx"} {
		t.Run(outcome, func(t *testing.T) {
			s := newMemoryStore()
			var calls atomic.Int32
			r := testRouter(&Guard{store: s}, func(c *gin.Context) {
				calls.Add(1)
				switch outcome {
				case "5xx":
					c.JSON(504, gin.H{"error": "timeout after write"})
				case "4xx":
					c.JSON(400, gin.H{"error": "validation"})
				case "cancel":
					ctx, cancel := context.WithCancel(c.Request.Context())
					cancel()
					c.Request = c.Request.WithContext(ctx)
					c.JSON(201, gin.H{"id": "created"})
				case "panic":
					panic("write may have committed")
				case "completion failure":
					s.failComplete = true
					c.JSON(201, gin.H{"id": "created"})
				case "non-json":
					c.String(201, "created")
				case "oversize":
					c.JSON(201, gin.H{"value": strings.Repeat("x", 256*1024)})
				}
			})
			request(r, "POST", "/api/v1/events", "alice", "same", `{}`)
			again := request(r, "POST", "/api/v1/events", "alice", "same", `{}`)
			// Completion uses an independent bounded context so cancellation can still save success.
			if outcome == "cancel" {
				if again.Code != 201 || again.Header().Get("Idempotency-Replayed") != "true" {
					t.Fatalf("canceled response was not saved: %d %s", again.Code, again.Body)
				}
			} else {
				assertCode(t, again, 409, "idempotency_in_progress")
			}
			if calls.Load() != 1 {
				t.Fatal("uncertain creation executed twice")
			}
		})
	}
}
func TestKeyBodyAndIdentityBounds(t *testing.T) {
	var calls atomic.Int32
	r := testRouter(&Guard{store: newMemoryStore()}, func(c *gin.Context) {
		calls.Add(1)
		body, _ := io.ReadAll(c.Request.Body)
		if !bytes.Equal(body, []byte(`{}`)) {
			t.Fatal("body not restored")
		}
		c.JSON(201, gin.H{"id": "one"})
	})
	for _, key := range []string{strings.Repeat("x", 201), " spaces ", "é"} {
		assertCode(t, request(r, "POST", "/api/v1/events", "alice", key, `{}`), 400, "idempotency_key_invalid")
	}
	req := httptest.NewRequest("POST", "/api/v1/events", strings.NewReader(`{}`))
	req.Header["Idempotency-Key"] = []string{"one", "two"}
	req.Header.Set("Test-Account", "alice")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertCode(t, w, 400, "idempotency_key_invalid")
	assertCode(t, request(r, "POST", "/api/v1/events", "alice", "same", strings.Repeat("x", 1024*1024+1)), 413, "idempotency_body_too_large")
	req = httptest.NewRequest("POST", "/api/v1/events", strings.NewReader(strings.Repeat("x", 1024*1024+1)))
	req.ContentLength = -1
	req.Header.Set("Idempotency-Key", "chunked")
	req.Header.Set("Test-Account", "alice")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertCode(t, w, 413, "idempotency_body_too_large")
	assertCode(t, request(r, "POST", "/api/v1/events", "", "same", `{}`), 401, "idempotency_identity_required")
	if calls.Load() != 0 {
		t.Fatal("invalid keyed request reached handler")
	}
	if w := request(r, "POST", "/api/v1/events", "alice", strings.Repeat("x", 200), `{}`); w.Code != 201 {
		t.Fatalf("valid boundary key rejected: %d %s", w.Code, w.Body)
	}
}
