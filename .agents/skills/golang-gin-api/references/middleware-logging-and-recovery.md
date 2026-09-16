# Middleware — Logging, Rate Limiting & Request ID

See also: `middleware-core.md`, `middleware-timeout-and-custom.md`

## Request Logging with log/slog

Log each request with structured fields: method, path, status, duration, request ID.

```go
// pkg/middleware/logger.go
package middleware

import (
    "log/slog"
    "time"

    "github.com/gin-gonic/gin"
)

// Logger returns a Gin middleware that logs each request using log/slog.
// Requires RequestID middleware to run first.
func Logger(logger *slog.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        path := c.Request.URL.Path
        query := c.Request.URL.RawQuery

        c.Next()

        duration := time.Since(start)
        status := c.Writer.Status()

        attrs := []slog.Attr{
            slog.String("method", c.Request.Method),
            slog.String("path", path),
            slog.Int("status", status),
            slog.Duration("duration", duration),
            slog.String("ip", c.ClientIP()),
            slog.String("request_id", c.GetString("request_id")),
        }
        if query != "" {
            attrs = append(attrs, slog.String("query", query))
        }
        if len(c.Errors) > 0 {
            attrs = append(attrs, slog.String("errors", c.Errors.String()))
        }

        level := slog.LevelInfo
        if status >= 500 {
            level = slog.LevelError
        } else if status >= 400 {
            level = slog.LevelWarn
        }

        logger.LogAttrs(c.Request.Context(), level, "http request", attrs...)
    }
}
```

Sample JSON output:
```json
{"time":"2026-03-01T10:00:00Z","level":"INFO","msg":"http request","method":"POST","path":"/api/v1/users","status":201,"duration":"2.3ms","ip":"192.168.1.1","request_id":"a1b2c3d4"}
```

---

## Rate Limiting

Basic in-memory rate limiting uses `golang.org/x/time/rate` (token bucket) per client IP:

```go
// Global: 10 req/s, burst of 20
r.Use(middleware.RateLimiter(10, 20))

// Stricter limit on auth endpoints
auth := r.Group("/api/v1/auth")
auth.Use(middleware.RateLimiter(2, 5))
```

For full production patterns (Redis distributed limiting, sliding window, per-user/API-key quotas, tiered limits, response headers), see **[rate-limiting-algorithms.md](rate-limiting-algorithms.md)** and related files.

---

## Request ID Middleware

Inject a unique request ID per request. Propagate it in the response header and store in context for logging.

```go
// pkg/middleware/request_id.go
package middleware

import (
    "github.com/gin-gonic/gin"
    "github.com/google/uuid"
)

const RequestIDKey    = "request_id"
const RequestIDHeader = "X-Request-ID"

// RequestID injects a unique request ID into the context and response headers.
// Reuses the incoming X-Request-ID header if present (for distributed tracing).
func RequestID() gin.HandlerFunc {
    return func(c *gin.Context) {
        id := c.GetHeader(RequestIDHeader)
        if id == "" {
            id = uuid.NewString()
        }

        c.Set(RequestIDKey, id)
        c.Header(RequestIDHeader, id)
        c.Next()
    }
}
```

Retrieve in any handler or downstream service:

```go
requestID := c.GetString(middleware.RequestIDKey)
logger := slog.With("request_id", requestID)
```

See also: `error-handling-recovery.md` for the `Recovery` middleware implementation.
