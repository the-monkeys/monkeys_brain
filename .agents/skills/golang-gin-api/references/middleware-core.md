# Middleware — Chain Execution, CORS & Security Headers

See also: `middleware-logging-and-recovery.md`, `middleware-timeout-and-custom.md`

## Middleware Signature & Chain Execution

All handlers and middleware share the same signature: `func(*gin.Context)`.

```go
// Execution order with two middleware and one handler:
// Middleware1-before → Middleware2-before → Handler → Middleware2-after → Middleware1-after

func Middleware1(c *gin.Context) {
    // runs BEFORE handler
    c.Next()
    // runs AFTER handler (and all subsequent middleware)
    status := c.Writer.Status()
    _ = status
}
```

Registration order matters. Register middleware before routes:

```go
r := gin.New()                     // no middleware
r.Use(middleware.RequestID())      // 1st: inject request ID (used by logger)
r.Use(middleware.Logger(logger))   // 2nd: logs with request ID available
r.Use(middleware.Recovery(logger)) // 3rd: catches panics from any handler
r.GET("/health", healthHandler)
```

**Why `gin.New()` over `gin.Default()`:** `gin.Default()` adds Logger and Recovery with default configuration. Use `gin.New()` + explicit middleware so you control format, destination, and behaviour — critical for structured logging and custom error responses.

---

## CORS Configuration

Use `github.com/gin-contrib/cors`. Configure per environment.

```go
// pkg/middleware/cors.go
package middleware

import (
    "os"
    "strings"
    "time"

    "github.com/gin-contrib/cors"
    "github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
    env := os.Getenv("APP_ENV")

    if env == "development" {
        return cors.New(cors.Config{
            AllowOrigins:     []string{"http://localhost:3000", "http://localhost:5173"},
            AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
            AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-ID"},
            AllowCredentials: true,
            MaxAge:           12 * time.Hour,
        })
    }

    origins := os.Getenv("ALLOWED_ORIGINS")
    allowedOrigins := []string{}
    if origins != "" {
        for _, o := range strings.Split(origins, ",") {
            allowedOrigins = append(allowedOrigins, strings.TrimSpace(o))
        }
    }

    return cors.New(cors.Config{
        AllowOrigins:     allowedOrigins,
        AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE"},
        AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-ID"},
        ExposeHeaders:    []string{"X-Request-ID"},
        AllowCredentials: true,
        MaxAge:           1 * time.Hour,
    })
}
```

**Warning:** `AllowAllOrigins: true` with `AllowCredentials: true` is rejected by browsers. Use one or the other, never both in production.

---

## Security Headers Middleware

Set HTTP security headers on every response. Register early in the chain so they are always applied, even when a later middleware aborts.

```go
// pkg/middleware/security_headers.go
package middleware

import "github.com/gin-gonic/gin"

func SecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Header("X-Content-Type-Options", "nosniff")
        c.Header("X-Frame-Options", "DENY")
        c.Header("X-XSS-Protection", "0") // disabled — CSP supersedes it
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        c.Header("Content-Security-Policy", "default-src 'self'")
        c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
        c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
        c.Next()
    }
}
```

Registration order:

```go
r := gin.New()
r.Use(middleware.CORS())
r.Use(middleware.SecurityHeaders())
r.Use(middleware.RequestID())
r.Use(middleware.Logger(logger))
r.Use(middleware.Recovery(logger))
```

**Header notes:**
- `X-XSS-Protection: 0` — disables the legacy XSS auditor; `Content-Security-Policy` is the correct control.
- `Strict-Transport-Security` — only effective over HTTPS; set `preload` after submitting to the HSTS preload list.
- `Content-Security-Policy` — `default-src 'self'` is a safe starting point; tighten per route group if the API serves HTML.
