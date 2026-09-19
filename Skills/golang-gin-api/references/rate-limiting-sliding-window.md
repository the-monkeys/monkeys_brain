# Rate Limiting — Sliding Window Counter

See also: `rate-limiting-algorithms.md`, `rate-limiting-redis.md`, `rate-limiting-peruser.md`, `rate-limiting-tiered.md`, `rate-limiting-headers.md`, `rate-limiting-fallback.md`

## Sliding Window Counter

Blends two consecutive fixed-window buckets weighted by elapsed time. More accurate than a plain fixed window (no boundary burst) without storing per-request timestamps.

```go
// pkg/middleware/rate_limiter_sliding.go
package middleware

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type windowBucket struct {
	count    int
	windowAt time.Time
}

// SlidingWindowLimiter limits requests using a sliding window counter.
// windowSize: duration of one window (e.g. time.Minute).
// limit: max requests per window.
func SlidingWindowLimiter(windowSize time.Duration, limit int) gin.HandlerFunc {
	var mu sync.Mutex
	buckets := make(map[string][2]windowBucket) // [prev, curr]

	return func(c *gin.Context) {
		key := c.ClientIP()
		now := time.Now()
		windowStart := now.Truncate(windowSize)

		mu.Lock()
		pair := buckets[key]

		// Advance windows if necessary.
		if pair[1].windowAt != windowStart {
			if pair[1].windowAt.Add(windowSize).Equal(windowStart) {
				pair[0] = pair[1] // shift: current → previous
			} else {
				pair[0] = windowBucket{} // gap larger than one window — reset both
			}
			pair[1] = windowBucket{windowAt: windowStart}
		}

		// Weighted count: fraction of previous window that overlaps current.
		elapsed := now.Sub(windowStart)
		overlap := 1.0 - elapsed.Seconds()/windowSize.Seconds()
		count := int(float64(pair[0].count)*overlap) + pair[1].count

		if count >= limit {
			mu.Unlock()
			slog.WarnContext(c.Request.Context(), "sliding window limit exceeded", "ip", key)
			c.Header("Retry-After", windowSize.String())
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}

		pair[1].count++
		buckets[key] = pair
		mu.Unlock()

		c.Next()
	}
}
```

**Why weighted overlap:** If a client sent 90 requests in the last window and the current window is 30% complete, the effective count is `90 * 0.7 + current`. This prevents the burst that occurs at fixed-window boundaries.
