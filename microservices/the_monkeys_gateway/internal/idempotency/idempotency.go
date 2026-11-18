// Package idempotency guards authenticated creation requests with Redis-backed
// reservations. No request payloads or response headers are persisted.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	reservationTTL = 24 * time.Hour
	maxKeyBytes    = 200
	maxBodyBytes   = 1 << 20
	maxReplayBytes = 256 << 10
	storeTimeout   = 2 * time.Second
)

type record struct {
	Fingerprint string `json:"fingerprint"`
	Owner       string `json:"owner"`
	Status      int    `json:"status,omitempty"`
	Body        []byte `json:"body,omitempty"`
}
type store interface {
	reserve(context.Context, string, record, time.Duration) (bool, error)
	load(context.Context, string) (record, error)
	complete(context.Context, string, record, record) error
}

// Guard shares a distributed reservation store across route middleware.
// A nil store fails closed only when a caller supplies an Idempotency-Key.
type Guard struct{ store store }

func New(client redis.UniversalClient) *Guard {
	g := &Guard{}
	if client != nil {
		g.store = &redisStore{client: client}
	}
	return g
}

// Optional keeps existing router callers source-compatible. In an unwired
// router, legacy requests work and keyed requests fail closed.
func Optional(guards []*Guard) *Guard {
	if len(guards) > 0 && guards[0] != nil {
		return guards[0]
	}
	return New(nil)
}

func (g *Guard) Handler() gin.HandlerFunc { return g.handler(nil, nil) }

// PrivateJSON stores only a resource reference, then resolves an account-private
// response on replay. The resolver must verify ownership before returning JSON.
func (g *Guard) PrivateJSON(reference func([]byte) ([]byte, error), resolve func(*gin.Context, []byte) ([]byte, error)) gin.HandlerFunc {
	return g.handler(reference, resolve)
}

func (g *Guard) handler(reference func([]byte) ([]byte, error), resolve func(*gin.Context, []byte) ([]byte, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		values, present := c.Request.Header[http.CanonicalHeaderKey("Idempotency-Key")]
		if !present {
			c.Next()
			return
		}
		c.Header("Cache-Control", "no-store")
		if len(values) != 1 || !validKey(values[0]) {
			fail(c, 400, "idempotency_key_invalid", "Idempotency-Key must contain 1 to 200 visible ASCII characters without spaces.")
			return
		}
		account := c.GetString("accountId")
		if account == "" {
			fail(c, 401, "idempotency_identity_required", "An authenticated account is required.")
			return
		}
		if c.Request.ContentLength > maxBodyBytes {
			fail(c, 413, "idempotency_body_too_large", "Keyed request body exceeds 1 MiB.")
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes+1))
		if err != nil {
			fail(c, 400, "idempotency_body_invalid", "Could not read keyed request body.")
			return
		}
		if len(body) > maxBodyBytes {
			fail(c, 413, "idempotency_body_too_large", "Keyed request body exceeds 1 MiB.")
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		if g == nil || g.store == nil {
			unavailable(c)
			return
		}

		key := "idempotency:v1:" + digest([]string{account, c.Request.Method, c.FullPath(), c.Request.URL.Path, values[0]})
		pending := record{Fingerprint: digest([]string{c.Request.URL.RawQuery, c.GetHeader("Content-Type"), string(body)}), Owner: uuid.NewString()}
		ctx, cancel := context.WithTimeout(c.Request.Context(), storeTimeout)
		reserved, err := g.store.reserve(ctx, key, pending, reservationTTL)
		cancel()
		if err != nil {
			unavailable(c)
			return
		}
		if !reserved {
			ctx, cancel = context.WithTimeout(c.Request.Context(), storeTimeout)
			previous, loadErr := g.store.load(ctx, key)
			cancel()
			if loadErr != nil {
				unavailable(c)
				return
			}
			if previous.Fingerprint != pending.Fingerprint {
				fail(c, 409, "idempotency_key_reused", "Idempotency-Key was already used with a different request.")
				return
			}
			if previous.Status < 200 || previous.Status >= 300 || len(previous.Body) == 0 {
				c.Header("Retry-After", "2")
				fail(c, 409, "idempotency_in_progress", "This request is still in progress or its outcome is uncertain. Retry with the same key.")
				return
			}
			replay := previous.Body
			if resolve != nil {
				replay, err = resolve(c, replay)
				if err != nil {
					unavailable(c)
					return
				}
			}
			if len(replay) > maxReplayBytes || !json.Valid(replay) {
				unavailable(c)
				return
			}
			c.Header("Idempotency-Replayed", "true")
			c.Data(previous.Status, "application/json; charset=utf-8", replay)
			c.Abort()
			return
		}

		original := c.Writer
		capture := &responseCapture{ResponseWriter: original}
		c.Writer = capture
		// Restore even on panic; the reservation deliberately survives any uncertain
		// result, handler panic, disconnect, timeout, or replay persistence failure.
		defer func() { c.Writer = original }()
		c.Next()
		mediaType, _, _ := mime.ParseMediaType(capture.Header().Get("Content-Type"))
		if capture.Status() < 200 || capture.Status() >= 300 || mediaType != "application/json" || capture.overflow || !json.Valid(capture.body.Bytes()) {
			return
		}
		replay := capture.body.Bytes()
		if reference != nil {
			replay, err = reference(replay)
			if err != nil || len(replay) > maxReplayBytes || !json.Valid(replay) {
				return
			}
		}
		completed := pending
		completed.Status = capture.Status()
		completed.Body = replay
		// Persist a completed result even if the browser disconnected after the
		// mutation. Never delete or re-acquire a reservation when this write fails.
		ctx, cancel = context.WithTimeout(context.WithoutCancel(c.Request.Context()), storeTimeout)
		defer cancel()
		_ = g.store.complete(ctx, key, pending, completed)
	}
}

func validKey(value string) bool {
	if len(value) == 0 || len(value) > maxKeyBytes {
		return false
	}
	for _, b := range []byte(value) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
func digest(parts []string) string {
	// Length-prefixed raw bytes avoid delimiter ambiguity and preserve invalid
	// UTF-8 exactly instead of normalizing it through JSON string encoding.
	hash := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = io.WriteString(hash, part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"code": code, "error": message, "message": message})
}
func unavailable(c *gin.Context) {
	c.Header("Retry-After", "2")
	fail(c, 503, "idempotency_unavailable", "Idempotency protection is unavailable. Retry with the same key.")
}

type responseCapture struct {
	gin.ResponseWriter
	body     bytes.Buffer
	overflow bool
}

func (w *responseCapture) Write(body []byte) (int, error) {
	if !w.overflow {
		if w.body.Len()+len(body) > maxReplayBytes {
			w.overflow = true
			w.body.Reset()
		} else {
			_, _ = w.body.Write(body)
		}
	}
	return w.ResponseWriter.Write(body)
}
func (w *responseCapture) WriteString(body string) (int, error) { return w.Write([]byte(body)) }

type redisStore struct{ client redis.UniversalClient }

func (s *redisStore) reserve(ctx context.Context, key string, value record, ttl time.Duration) (bool, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	return s.client.SetNX(ctx, key, encoded, ttl).Result()
}
func (s *redisStore) load(ctx context.Context, key string) (record, error) {
	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		return record{}, err
	}
	// Values in this namespace are bounded by gateway writers. Reject unexpected
	// data rather than ever treating a malformed reservation as permission to run.
	if len(data) > 2*maxReplayBytes {
		return record{}, errors.New("invalid reservation")
	}
	var value record
	if err = json.Unmarshal(data, &value); err != nil {
		return record{}, err
	}
	if value.Owner == "" || value.Fingerprint == "" {
		return record{}, errors.New("invalid reservation")
	}
	return value, nil
}

// Compare the entire original reservation in Redis, atomically preserving its
// remaining TTL. A late worker cannot overwrite a new owner after expiration.
const completeScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'XX', 'KEEPTTL')
return 1
`

func (s *redisStore) complete(ctx context.Context, key string, before, after record) error {
	old, err := json.Marshal(before)
	if err != nil {
		return err
	}
	updated, err := json.Marshal(after)
	if err != nil {
		return err
	}
	result, err := s.client.Eval(ctx, completeScript, []string{key}, string(old), string(updated)).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return errors.New("idempotency reservation lost")
	}
	return nil
}

// IsCreation identifies the deliberately narrow surface for global middleware
// that must leave keyed request bodies bounded and unread until after auth.
func IsCreation(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/api/v1/events", "/api/v1/events/series", "/api/v1/events/:slug/clone", "/api/v1/groups", "/api/v1/groups/:slug/events", "/api/v1/user/verification":
		return true
	}
	return false
}
