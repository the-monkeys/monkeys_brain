# Middleware — Timeout & Custom Middleware Template

See also: `middleware-core.md`, `middleware-logging-and-recovery.md`

## Timeout Middleware

Cancel the request context after a deadline. Handlers must respect `c.Request.Context()` for this to work.

```go
// pkg/middleware/timeout.go
package middleware

import (
    "context"
    "net/http"
    "time"

    "github.com/gin-gonic/gin"
)

// Timeout returns middleware that cancels the request context after d.
// Handlers must pass c.Request.Context() to all blocking calls for cancellation to work.
//
// The context deadline propagates to every downstream blocking call that respects it
// (database queries, HTTP clients, gRPC calls). This does NOT forcibly interrupt
// handlers that ignore context — it is cooperative cancellation only.
func Timeout(d time.Duration) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx, cancel := context.WithTimeout(c.Request.Context(), d)
        defer cancel()

        c.Request = c.Request.WithContext(ctx)
        c.Next()

        if ctx.Err() == context.DeadlineExceeded && !c.Writer.Written() {
            c.AbortWithStatusJSON(http.StatusGatewayTimeout, gin.H{
                "error": "request timeout",
            })
        }
    }
}
```

Apply per route group, not globally (health check should not time out):

```go
api := r.Group("/api/v1")
api.Use(middleware.Timeout(30 * time.Second))
```

**How this works:** The deadline is embedded in `c.Request.Context()`. Any handler that passes `c.Request.Context()` to a database query, HTTP client, or gRPC call will have that call cancelled when the deadline fires.

**Important:** This is cooperative cancellation. Handlers that do not check `ctx.Done()` or pass context to blocking calls will not be interrupted.

**Do NOT call `c.Next()` in a goroutine** for timeout middleware. Gin's `ResponseWriter` is not thread-safe. Calling `c.Next()` in a goroutine creates a data race when both the goroutine and the timeout branch attempt to write to `c.Writer` concurrently.

---

## Custom Middleware Template

Use this as a starting point for any new middleware.

```go
// pkg/middleware/my_middleware.go
package middleware

import (
    "log/slog"
    "net/http"

    "github.com/gin-gonic/gin"
)

// MyMiddleware does X. It requires Y to run before it.
// Apply at: r.Use() for global, group.Use() for group-scoped, or inline for per-route.
func MyMiddleware(logger *slog.Logger) gin.HandlerFunc {
    // One-time setup (runs at registration, not per request)

    return func(c *gin.Context) {
        // --- PRE-HANDLER ---
        // Short-circuit with c.AbortWithStatusJSON() if validation fails
        val := c.GetHeader("X-My-Header")
        if val == "" {
            c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
                "error": "X-My-Header is required",
            })
            return
        }

        // Store data for downstream handlers
        c.Set("my_value", val)

        // --- CALL NEXT ---
        c.Next()

        // --- POST-HANDLER ---
        // Read response data: c.Writer.Status(), c.Writer.Size()
        status := c.Writer.Status()
        logger.InfoContext(c.Request.Context(), "my middleware post",
            "status", status,
            "value", val,
        )
    }
}
```
