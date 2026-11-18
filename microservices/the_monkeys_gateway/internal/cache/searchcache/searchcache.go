// Package searchcache provides a small, Redis-backed cache used by the
// search-v2 endpoints in the API gateway.
//
// Design notes:
//   - Every key is namespaced with `search:v2:` to keep search data isolated
//     from any other cached entities and to make targeted invalidation easy
//     (`FLUSHDB` is never required).
//   - All public methods are safe to call when Redis is unreachable: read
//     misses simply return ErrCacheMiss and writes silently no-op. This keeps
//     search itself functional during a cache outage. We trade hit-rate for
//     availability — search is in the user-visible hot path.
//   - We store *string payloads only*. Callers are expected to JSON-encode
//     their data. Keeping the cache schema-agnostic makes it reusable from
//     both blog and user search handlers without a generics gymnastic.
package searchcache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/the-monkeys/the_monkeys/config"
	"go.uber.org/zap"
)

// KeyPrefix is prepended to every cache key written by this package.
// Centralised so dashboards / cache-busting scripts have a single source of
// truth.
const KeyPrefix = "search:v2:"

// ErrCacheMiss is returned when the requested key does not exist in Redis.
// Callers should treat this as a normal control-flow signal, not an error.
var ErrCacheMiss = errors.New("searchcache: miss")

// Cache wraps a Redis client with a small, opinionated API.
// The zero value is unusable; always construct via New.
type Cache struct {
	rdb *redis.Client
	log *zap.SugaredLogger
}

// New builds a Cache from the central application config. It dials Redis
// eagerly with a 2-second timeout so misconfiguration surfaces at boot rather
// than on the first user request. If Redis is unreachable we still return a
// usable Cache instance — every call will be a miss / no-op — and log a
// warning so the operator can fix it without taking the gateway down.
func New(cfg *config.Config, log *zap.SugaredLogger) *Cache {
	addr := cfg.Redis.Host
	// Config sometimes stores host as "host:port"; respect that. If the port
	// is missing we fall back to the explicit Port field.
	if cfg.Redis.Port != 0 && !hasPort(addr) {
		addr = addr + ":" + itoa(cfg.Redis.Port)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		PoolSize:     orDefault(cfg.Redis.PoolSize, 10),
		MinIdleConns: orDefault(cfg.Redis.MaxIdle, 2),
		DialTimeout:  2 * time.Second,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
	})

	pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		// Do NOT fail boot. Search must still serve traffic without cache.
		log.Warnw("searchcache: redis unreachable at boot, continuing without cache",
			"addr", addr, "err", err)
	} else {
		log.Infow("searchcache: connected", "addr", addr, "prefix", KeyPrefix)
	}

	return &Cache{rdb: rdb, log: log}
}

// Get reads a string value for the given key. Returns ErrCacheMiss when the
// key is absent. Any other error is logged and reported as a miss so callers
// can fall through to the source of truth without special-casing transport
// errors.
func (c *Cache) Get(ctx context.Context, key string) (string, error) {
	if c == nil || c.rdb == nil {
		return "", ErrCacheMiss
	}

	v, err := c.rdb.Get(ctx, KeyPrefix+key).Result()
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, redis.Nil):
		return "", ErrCacheMiss
	default:
		c.log.Debugw("searchcache: get failed", "key", key, "err", err)
		return "", ErrCacheMiss
	}
}

// Set writes a string value with a TTL. Errors are logged and swallowed so a
// flaky Redis never propagates into a user-visible 5xx.
func (c *Cache) Set(ctx context.Context, key, value string, ttl time.Duration) {
	if c == nil || c.rdb == nil {
		return
	}
	if err := c.rdb.Set(ctx, KeyPrefix+key, value, ttl).Err(); err != nil {
		c.log.Debugw("searchcache: set failed", "key", key, "err", err)
	}
}

// Del removes one or more keys. Used by invalidation hooks (e.g. when a
// blog is published the suggestion cache for that title should be cleared).
// Errors are best-effort.
func (c *Cache) Del(ctx context.Context, keys ...string) {
	if c == nil || c.rdb == nil || len(keys) == 0 {
		return
	}
	prefixed := make([]string, len(keys))
	for i, k := range keys {
		prefixed[i] = KeyPrefix + k
	}
	if err := c.rdb.Del(ctx, prefixed...).Err(); err != nil {
		c.log.Debugw("searchcache: del failed", "keys", keys, "err", err)
	}
}

// Close releases the underlying Redis connection pool. Wire this into the
// gateway's shutdown sequence.
func (c *Cache) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// hasPort reports whether s already contains a colon (i.e. host:port).
// We avoid net.SplitHostPort here because the input may be IPv6 literals and
// the only thing we care about is whether to append a port.
func hasPort(s string) bool {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	// Tiny inline; avoids pulling strconv just for one digit count.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func orDefault(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

// RedisClient exposes the shared pool for fail-closed coordination. Unlike the
// best-effort search cache API, callers must handle every Redis error.
func (c *Cache) RedisClient() redis.UniversalClient {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb
}
