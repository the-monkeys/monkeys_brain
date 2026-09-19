# Rate Limiting — Algorithm Overview & In-Memory Token Bucket

See also: `rate-limiting-redis.md`, `rate-limiting-peruser.md`, `rate-limiting-tiered.md`, `rate-limiting-headers.md`, `rate-limiting-fallback.md`

## Algorithm Overview

| Algorithm | Accuracy | Memory | Distributed | Best For |
|---|---|---|---|---|
| Fixed window | Low (boundary burst) | O(1) | Yes (INCR) | Simple global caps |
| Token bucket | High | O(clients) | Yes (Lua) | Smooth traffic shaping |
| Sliding window counter | Medium-high | O(1) | Yes (INCR×2) | Accurate without per-request storage |
| Sliding window log | Exact | O(requests) | Yes (ZADD) | High-value APIs, low traffic |

**Token bucket** (via `golang.org/x/time/rate`) is best for single-node deployments — it allows short bursts while enforcing a long-term rate. **Sliding window counter** blends two fixed-window buckets for near-exact counts without storing individual timestamps. **Redis Lua** scripts ensure atomic check-and-decrement across instances.

Use **in-memory** when: single binary, simplicity matters, losing counts on restart is acceptable.
Use **Redis** when: multiple instances, counts must survive restarts, per-user billing accuracy required.

---

## In-Memory Token Bucket

`golang.org/x/time/rate` wraps the token bucket algorithm. Accepts a configurable cleanup interval and a done channel for clean shutdown.

```go
// pkg/middleware/rate_limiter_memory.go
package middleware

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// MemoryRateLimiterConfig configures the in-memory rate limiter.
type MemoryRateLimiterConfig struct {
	Rate            rate.Limit
	Burst           int
	CleanupInterval time.Duration
	TTL             time.Duration
}

// MemoryRateLimiter limits requests per client IP using a token bucket.
// NOT suitable for multi-instance deployments — use RedisRateLimiter instead.
func MemoryRateLimiter(cfg MemoryRateLimiterConfig, done <-chan struct{}) gin.HandlerFunc {
	if cfg.CleanupInterval == 0 {
		cfg.CleanupInterval = time.Minute
	}
	if cfg.TTL == 0 {
		cfg.TTL = 3 * time.Minute
	}

	var mu sync.Mutex
	limiters := make(map[string]*clientLimiter)

	go func() {
		ticker := time.NewTicker(cfg.CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				for ip, cl := range limiters {
					if time.Since(cl.lastSeen) > cfg.TTL {
						delete(limiters, ip)
					}
				}
				mu.Unlock()
			case <-done:
				return
			}
		}
	}()

	getLimiter := func(ip string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		cl, ok := limiters[ip]
		if !ok {
			cl = &clientLimiter{limiter: rate.NewLimiter(cfg.Rate, cfg.Burst)}
			limiters[ip] = cl
		}
		cl.lastSeen = time.Now()
		return cl.limiter
	}

	return func(c *gin.Context) {
		ip := c.ClientIP()
		lim := getLimiter(ip)
		if !lim.Allow() {
			slog.WarnContext(c.Request.Context(), "rate limit exceeded", "ip", ip)
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}
```

**Usage:**

```go
done := make(chan struct{})
defer close(done)

r.Use(middleware.MemoryRateLimiter(middleware.MemoryRateLimiterConfig{
    Rate:  10,
    Burst: 20,
}, done))
```

**Why `done` channel:** The goroutine would leak if the server restarts or the router is rebuilt in tests. Closing `done` stops the ticker cleanly.

See also: `rate-limiting-sliding-window.md` for the sliding window counter implementation.
