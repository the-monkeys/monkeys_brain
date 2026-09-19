# Rate Limiting — Graceful Degradation (Redis Fallback)

See also: `rate-limiting-algorithms.md`, `rate-limiting-redis.md`, `rate-limiting-peruser.md`, `rate-limiting-tiered.md`, `rate-limiting-headers.md`

## Graceful Degradation

When Redis is unavailable, fall back to an in-memory limiter instead of blocking all requests or panicking.

```go
// pkg/middleware/rate_limiter_fallback.go
package middleware

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// FallbackRateLimiter tries Redis first; on error falls back to in-memory.
// Ensures availability over strict accuracy when Redis is down.
func FallbackRateLimiter(redisCfg RedisTokenBucketConfig, memCfg MemoryRateLimiterConfig, done <-chan struct{}) gin.HandlerFunc {
	redisLimiter := RedisTokenBucketLimiter(redisCfg)
	memLimiter := MemoryRateLimiter(memCfg, done)

	var (
		mu           sync.RWMutex
		redisHealthy = true
		lastCheck    time.Time
		checkEvery   = 10 * time.Second
	)

	checkRedis := func(ctx context.Context) bool {
		mu.RLock()
		healthy := redisHealthy
		since := time.Since(lastCheck)
		mu.RUnlock()

		if since < checkEvery {
			return healthy
		}

		mu.Lock()
		defer mu.Unlock()
		err := redisCfg.Client.Ping(ctx).Err()
		redisHealthy = err == nil
		lastCheck = time.Now()
		if err != nil {
			slog.Warn("redis unhealthy, using in-memory rate limiter", "err", err)
		} else if !healthy {
			slog.Info("redis recovered, resuming redis rate limiter")
		}
		return redisHealthy
	}

	return func(c *gin.Context) {
		if checkRedis(c.Request.Context()) {
			redisLimiter(c)
		} else {
			memLimiter(c)
		}
	}
}
```

**Why fail open (allow) on Redis errors:** Blocking all traffic because a cache is down is worse than briefly allowing excess traffic. Log the error, alert on it, but keep the API serving. If strict enforcement is required, fail closed — change `c.Next()` to `c.AbortWithStatus(503)` in the Redis error paths.

**Registration example:**

```go
// main.go
done := make(chan struct{})
defer close(done)

r.Use(middleware.FallbackRateLimiter(
    middleware.RedisTokenBucketConfig{
        Client:    rdb,
        Capacity:  100,
        Rate:      10,
        KeyPrefix: "rl:api:",
    },
    middleware.MemoryRateLimiterConfig{
        Rate:  10,
        Burst: 20,
    },
    done,
))
```
