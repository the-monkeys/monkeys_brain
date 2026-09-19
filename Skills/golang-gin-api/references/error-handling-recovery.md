# Error Handling — Panic Recovery Middleware

See also: `error-handling-apperror.md`, `error-handling-validation.md`

## Panic Recovery Middleware

Use custom recovery to return JSON instead of Gin's default plain-text 500.

```go
// pkg/middleware/recovery.go
package middleware

import (
    "log/slog"
    "net/http"

    "github.com/gin-gonic/gin"
)

// Recovery returns a Gin middleware that recovers from panics and logs them.
// Register with r.Use(middleware.Recovery(logger)) instead of gin.Recovery().
func Recovery(logger *slog.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        defer func() {
            if rec := recover(); rec != nil {
                logger.ErrorContext(c.Request.Context(), "panic recovered",
                    "panic", rec,
                    "path", c.FullPath(),
                    "method", c.Request.Method,
                )
                c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
                    "error": "internal server error",
                })
            }
        }()
        c.Next()
    }
}
```

Register in main.go:

```go
r := gin.New()
r.Use(middleware.Logger(logger))
r.Use(middleware.Recovery(logger))
```

**Why custom recovery over `gin.Recovery()`:** `gin.Recovery()` writes a plain-text response. Custom recovery returns JSON, which API clients can parse consistently. It also integrates with your structured logger (`log/slog`) and request ID.

**What panics to expect:**
- Nil pointer dereferences in handlers (most common)
- Index out of range errors
- Type assertion failures without the two-value form

**Best practice:** Fix panics at their source — don't rely on recovery as a general error handler. Recovery is a last-resort safety net, not a substitute for proper error handling with `AppError` and `handleServiceError`.
