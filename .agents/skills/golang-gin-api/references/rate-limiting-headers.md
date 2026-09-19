# Rate Limiting — Response Headers

See also: `rate-limiting-algorithms.md`, `rate-limiting-redis.md`, `rate-limiting-peruser.md`, `rate-limiting-tiered.md`, `rate-limiting-fallback.md`

## Response Headers

Set these on every response — not only on 429 — so clients can self-throttle before hitting the limit.

| Header | Value | When |
|---|---|---|
| `X-RateLimit-Limit` | Total requests allowed in window | Always |
| `X-RateLimit-Remaining` | Requests remaining this window | Always |
| `X-RateLimit-Reset` | Unix epoch when window resets | Always (where applicable) |
| `Retry-After` | Seconds until client may retry | 429 responses only |

```go
// pkg/middleware/rate_limit_headers.go
package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// SetRateLimitHeaders writes standard rate limit headers.
// resetAt: time when the current window expires (zero value omits X-RateLimit-Reset).
func SetRateLimitHeaders(c *gin.Context, limit, remaining int, resetAt time.Time) {
	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
	if !resetAt.IsZero() {
		c.Header("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
	}
}

// AbortRateLimited sends a 429 with Retry-After and standard rate limit headers.
func AbortRateLimited(c *gin.Context, limit int, retryAfterSec int) {
	SetRateLimitHeaders(c, limit, 0, time.Now().Add(time.Duration(retryAfterSec)*time.Second))
	c.Header("Retry-After", strconv.Itoa(retryAfterSec))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error":       "rate limit exceeded",
		"retry_after": retryAfterSec,
	})
}
```

**Why always set headers:** Clients and API gateways use `X-RateLimit-Remaining` to implement proactive back-off. If headers only appear on 429, clients have no signal until they are already blocked.
