# Rate Limiting — Redis Token Bucket & Redis Sliding Window

See also: `rate-limiting-algorithms.md`, `rate-limiting-peruser.md`, `rate-limiting-tiered.md`, `rate-limiting-headers.md`, `rate-limiting-fallback.md`

## Redis Token Bucket (Lua)

A Lua script executes atomically on the Redis instance — no race between read and write. Uses two keys per client: `tokens` (current count) and `ts` (last refill timestamp).

```go
// pkg/middleware/rate_limiter_redis.go
package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// tokenBucketScript atomically checks and decrements a token bucket stored in Redis.
// KEYS[1]: token count key, KEYS[2]: last-refill timestamp key
// ARGV[1]: capacity, ARGV[2]: refill rate (tokens/sec), ARGV[3]: current unix timestamp (float)
// Returns: remaining tokens after the request (-1 means denied).
var tokenBucketScript = redis.NewScript(`
local tokens_key = KEYS[1]
local ts_key     = KEYS[2]
local capacity   = tonumber(ARGV[1])
local rate       = tonumber(ARGV[2])
local now        = tonumber(ARGV[3])
local ttl        = math.ceil(capacity / rate) + 1

local last_ts = tonumber(redis.call("GET", ts_key))
local tokens

if last_ts == nil then
    tokens = capacity
else
    local elapsed = now - last_ts
    tokens = math.min(capacity, tonumber(redis.call("GET", tokens_key) or 0) + elapsed * rate)
end

if tokens < 1 then
    return -1
end

tokens = tokens - 1
redis.call("SETEX", tokens_key, ttl, tokens)
redis.call("SETEX", ts_key, ttl, now)
return tokens
`)

type RedisTokenBucketConfig struct {
	Client    *redis.Client
	Capacity  int
	Rate      float64
	KeyPrefix string
}

func RedisTokenBucketLimiter(cfg RedisTokenBucketConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		ip := c.ClientIP()
		base := cfg.KeyPrefix + ip
		tokensKey := base + ":tokens"
		tsKey := base + ":ts"
		now := float64(time.Now().UnixNano()) / 1e9

		remaining, err := tokenBucketScript.Run(ctx, cfg.Client,
			[]string{tokensKey, tsKey},
			cfg.Capacity, cfg.Rate, now,
		).Int()

		if err != nil && !errors.Is(err, redis.Nil) {
			slog.ErrorContext(ctx, "redis rate limiter error", "err", err)
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", strconv.Itoa(cfg.Capacity))

		if remaining < 0 {
			c.Header("X-RateLimit-Remaining", "0")
			c.Header("Retry-After", strconv.Itoa(int(1.0/cfg.Rate)+1))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}

		c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
		c.Next()
	}
}
```

**Why Lua:** `GET` + conditional `SET` in application code has a TOCTOU race under concurrent requests. A Lua script is executed atomically by the Redis server — no other command runs between the read and write.

See also: `rate-limiting-redis-sliding.md` for the Redis sorted-set sliding window implementation.
