# Rate Limiting — Tiered Limits

See also: `rate-limiting-algorithms.md`, `rate-limiting-redis.md`, `rate-limiting-peruser.md`, `rate-limiting-headers.md`, `rate-limiting-fallback.md`

## Tiered Limits

Different limits per user role or subscription plan. Load from config; avoid hardcoding.

```go
// pkg/middleware/rate_limiter_tiered.go
package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// TierConfig holds rate limit parameters for one tier.
type TierConfig struct {
	Rate  rate.Limit
	Burst int
}

// DefaultTiers maps role names to limits. Override from env/config.
var DefaultTiers = map[string]TierConfig{
	"anonymous":     {Rate: 2, Burst: 5},
	"authenticated": {Rate: 20, Burst: 40},
	"premium":       {Rate: 100, Burst: 200},
}

// TieredRateLimiter applies per-role limits using the Redis token bucket.
// Reads "role" from the Gin context (set by AuthRequired middleware).
func TieredRateLimiter(rdb *redis.Client, tiers map[string]TierConfig, keyPrefix string) gin.HandlerFunc {
	fallback := tiers["anonymous"]

	return func(c *gin.Context) {
		ctx := c.Request.Context()

		role, _ := c.Get("role")
		roleStr, _ := role.(string)
		cfg, ok := tiers[roleStr]
		if !ok {
			cfg = fallback
		}

		sub, _ := c.Get("sub")
		subStr, _ := sub.(string)
		if subStr == "" {
			subStr = c.ClientIP()
		}
		base := keyPrefix + roleStr + ":" + subStr

		now := float64(time.Now().UnixNano()) / 1e9
		remaining, err := tokenBucketScript.Run(ctx, rdb,
			[]string{base + ":tokens", base + ":ts"},
			cfg.Burst, float64(cfg.Rate), now,
		).Int()

		if err != nil && err != redis.Nil {
			slog.ErrorContext(ctx, "tiered limiter redis error", "err", err, "role", roleStr)
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", strconv.Itoa(cfg.Burst))

		if remaining < 0 {
			c.Header("X-RateLimit-Remaining", "0")
			retryAfter := int(1.0/float64(cfg.Rate)) + 1
			c.Header("Retry-After", strconv.Itoa(retryAfter))
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

**Loading tiers from environment:**

```go
// config/rate_limits.go
package config

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/time/rate"
	"myapp/pkg/middleware"
)

func LoadTiers() map[string]middleware.TierConfig {
	tiers := make(map[string]middleware.TierConfig)
	for _, role := range []string{"anonymous", "authenticated", "premium"} {
		rateKey := "RATE_" + strings.ToUpper(role) + "_RPS"
		burstKey := "RATE_" + strings.ToUpper(role) + "_BURST"
		r, err := strconv.ParseFloat(os.Getenv(rateKey), 64)
		if err != nil {
			r = float64(middleware.DefaultTiers[role].Rate)
		}
		b, err := strconv.Atoi(os.Getenv(burstKey))
		if err != nil {
			b = middleware.DefaultTiers[role].Burst
		}
		tiers[role] = middleware.TierConfig{Rate: rate.Limit(r), Burst: b}
	}
	return tiers
}
```
