# Rate Limiting — Redis Sliding Window (Sorted Set)

See also: `rate-limiting-redis.md`, `rate-limiting-algorithms.md`

## Redis Sliding Window

Uses a sorted set where each member is a unique request ID and the score is the Unix timestamp (nanoseconds). `ZREMRANGEBYSCORE` prunes old entries; `ZCARD` counts the current window.

```go
// pkg/middleware/rate_limiter_redis_sliding.go
package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

var redisSlidingScript = redis.NewScript(`
local key    = KEYS[1]
local window = tonumber(ARGV[1])
local now    = tonumber(ARGV[2])
local limit  = tonumber(ARGV[3])
local req_id = ARGV[4]
local cutoff = now - window

redis.call("ZREMRANGEBYSCORE", key, "-inf", cutoff)
local count = redis.call("ZCARD", key)

if count >= limit then
    return 1
end

redis.call("ZADD", key, now, req_id)
redis.call("PEXPIRE", key, math.ceil(window / 1e6))
return 0
`)

type RedisSlidingWindowConfig struct {
	Client    *redis.Client
	Window    time.Duration
	Limit     int
	KeyPrefix string
}

func RedisSlidingWindowLimiter(cfg RedisSlidingWindowConfig) gin.HandlerFunc {
	windowNs := cfg.Window.Nanoseconds()

	return func(c *gin.Context) {
		ctx := c.Request.Context()
		ip := c.ClientIP()
		key := cfg.KeyPrefix + ip
		now := time.Now().UnixNano()
		reqID := strconv.FormatInt(now, 36) + ip

		denied, err := redisSlidingScript.Run(ctx, cfg.Client,
			[]string{key},
			windowNs, now, cfg.Limit, reqID,
		).Int()

		if err != nil && !errors.Is(err, redis.Nil) {
			slog.ErrorContext(ctx, "redis sliding window error", "err", err)
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", strconv.Itoa(cfg.Limit))

		if denied == 1 {
			c.Header("X-RateLimit-Remaining", "0")
			c.Header("Retry-After", strconv.FormatFloat(cfg.Window.Seconds(), 'f', 0, 64))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}

		c.Next()
	}
}
```

**Memory trade-off:** Each request is a sorted set entry. For 1000 req/min limit × 10 000 clients = ~10M entries. At ~100 bytes/entry this is ~1 GB. Use the sliding window **counter** approach (two INCR keys) if memory is a concern at scale.
