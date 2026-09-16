# Routing — Custom Validators & Request Size Limits

See also: `routing-groups-and-versioning.md`, `routing-params-and-wildcards.md`

## Custom Validators

Register custom validators via the `go-playground/validator/v10` engine. Do this once at startup.

```go
// pkg/validation/validators.go
package validation

import (
    "fmt"
    "time"

    "github.com/gin-gonic/gin/binding"
    "github.com/go-playground/validator/v10"
    "github.com/google/uuid"
)

func Register() error {
    v, ok := binding.Validator.Engine().(*validator.Validate)
    if !ok {
        return fmt.Errorf("unexpected validator type")
    }

    if err := v.RegisterValidation("uuid", validateUUID); err != nil {
        return fmt.Errorf("register uuid validator: %w", err)
    }

    if err := v.RegisterValidation("future_ts", validateFutureTimestamp); err != nil {
        return fmt.Errorf("register future_ts validator: %w", err)
    }

    return nil
}

func validateUUID(fl validator.FieldLevel) bool {
    val := fl.Field().String()
    _, err := uuid.Parse(val)
    return err == nil
}

func validateFutureTimestamp(fl validator.FieldLevel) bool {
    ts := fl.Field().Int()
    return time.Unix(ts, 0).After(time.Now())
}
```

Register in main.go before starting the server:

```go
if err := validation.Register(); err != nil {
    logger.Error("failed to register validators", "error", err)
    os.Exit(1)
}
```

Usage in request struct:

```go
type ScheduleRequest struct {
    UserID    string `json:"user_id"    binding:"required,uuid"`
    StartTime int64  `json:"start_time" binding:"required,future_ts"`
}
```

---

## Request Size Limits

Limit request body size to protect against large payload attacks.

```go
// pkg/middleware/limits.go
package middleware

import (
    "net/http"
    "github.com/gin-gonic/gin"
)

// RequestSizeLimit returns middleware that limits request body size.
func RequestSizeLimit(maxBytes int64) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
        c.Next()
    }
}
```

```go
// Apply globally (1 MB limit for all routes)
r.Use(middleware.RequestSizeLimit(1 << 20))

// Or per-route (10 MB for file upload route)
r.POST("/upload", middleware.RequestSizeLimit(10<<20), uploadHandler)
```

For multipart forms, also configure `engine.MaxMultipartMemory`:

```go
r := gin.New()
r.MaxMultipartMemory = 8 << 20 // 8 MB in-memory; rest spills to disk
```
