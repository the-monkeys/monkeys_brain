# Error Handling — Validation Errors & JSON Format

See also: `error-handling-apperror.md`, `error-handling-recovery.md`

## Validation Error Formatting

Gin binding errors (`ShouldBindJSON` failure) return raw `validator/v10` error messages. Format them into field-level errors for API consumers.

```go
// internal/handler/validation.go
package handler

import (
    "errors"
    "fmt"
    "net/http"

    "github.com/gin-gonic/gin"
    "github.com/go-playground/validator/v10"
)

type FieldError struct {
    Field   string `json:"field"`
    Message string `json:"message"`
}

// validationErrors formats validator.ValidationErrors into field-level messages.
func validationErrors(err error) []FieldError {
    var ve validator.ValidationErrors
    if !errors.As(err, &ve) {
        return []FieldError{{Field: "request", Message: err.Error()}}
    }

    out := make([]FieldError, 0, len(ve))
    for _, fe := range ve {
        out = append(out, FieldError{
            Field:   fe.Field(),
            Message: fieldMessage(fe),
        })
    }
    return out
}

func fieldMessage(fe validator.FieldError) string {
    switch fe.Tag() {
    case "required":
        return "field is required"
    case "email":
        return "must be a valid email address"
    case "min":
        return fmt.Sprintf("must be at least %s characters", fe.Param())
    case "max":
        return fmt.Sprintf("must be at most %s characters", fe.Param())
    case "oneof":
        return fmt.Sprintf("must be one of: %s", fe.Param())
    default:
        return fmt.Sprintf("failed validation: %s", fe.Tag())
    }
}
```

Usage in handler:

```go
func (h *UserHandler) Create(c *gin.Context) {
    var req domain.CreateUserRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{
            "error":  "validation failed",
            "fields": validationErrors(err),
        })
        return
    }
    // ...
}
```

Response body:
```json
{
  "error": "validation failed",
  "fields": [
    {"field": "Email", "message": "must be a valid email address"},
    {"field": "Password", "message": "must be at least 8 characters"}
  ]
}
```

---

## Consistent JSON Error Format

All error responses must follow the same shape so clients can parse them predictably.

```go
// ErrorResponse is the canonical error body shape.
type ErrorResponse struct {
    Error  string       `json:"error"`
    Fields []FieldError `json:"fields,omitempty"` // only for validation errors
}
```

Examples:

```json
// 400 binding error
{"error": "invalid JSON body"}

// 400 validation error (with fields)
{"error": "validation failed", "fields": [{"field": "Email", "message": "must be a valid email address"}]}

// 401 unauthorized
{"error": "unauthorized"}

// 404 not found
{"error": "resource not found"}

// 409 conflict
{"error": "resource already exists"}

// 500 internal
{"error": "internal server error"}
```

**Critical:** Never include internal error messages, stack traces, or database errors in 5xx responses. Log internally, return generic message to client.
