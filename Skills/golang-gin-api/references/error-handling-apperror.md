# Error Handling — AppError, Sentinels & handleServiceError

See also: `error-handling-validation.md`, `error-handling-recovery.md`

## AppError Struct

`AppError` carries an HTTP status code alongside the message, so the transport layer never decides status codes — the domain does.

> **Note:** This `AppError` is a simplified version. For the canonical pattern with `Detail` field and 5xx guard, see the **golang-gin-architect** skill (`references/clean-architecture-layers-di.md`).

```go
// internal/domain/errors.go
package domain

import "errors"

// AppError is a domain error that maps directly to an HTTP response.
type AppError struct {
    Code    int    // HTTP status code
    Message string // User-facing message (safe to expose)
    Err     error  // Wrapped cause (for logging, not exposed to client)
}

func (e *AppError) Error() string { return e.Message }
func (e *AppError) Unwrap() error  { return e.Err }
func (e AppError) Is(target error) bool {
    switch t := target.(type) {
    case *AppError:
        return e.Code == t.Code
    case AppError:
        return e.Code == t.Code
    }
    return false
}

// New wraps an underlying cause with a domain error.
func (e *AppError) New(cause error) *AppError {
    return &AppError{Code: e.Code, Message: e.Message, Err: cause}
}
```

---

## Sentinel Errors

```go
// internal/domain/errors.go (continued)
var (
    ErrNotFound     = &AppError{Code: 404, Message: "resource not found"}
    ErrUnauthorized = &AppError{Code: 401, Message: "unauthorized"}
    ErrForbidden    = &AppError{Code: 403, Message: "forbidden"}
    ErrConflict     = &AppError{Code: 409, Message: "resource already exists"}
    ErrValidation   = &AppError{Code: 422, Message: "validation failed"}
    ErrInternal     = &AppError{Code: 500, Message: "internal server error"}
)
```

Usage in a service:

```go
func (s *userService) GetByID(ctx context.Context, id string) (*domain.User, error) {
    user, err := s.repo.GetByID(ctx, id)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, domain.ErrNotFound.New(err)
        }
        return nil, domain.ErrInternal.New(err)
    }
    return user, nil
}

func (s *userService) Create(ctx context.Context, req domain.CreateUserRequest) (*domain.User, error) {
    existing, err := s.repo.GetByEmail(ctx, req.Email)
    if err != nil && !errors.Is(err, sql.ErrNoRows) {
        return nil, domain.ErrInternal.New(err)
    }
    if existing != nil {
        return nil, domain.ErrConflict.New(fmt.Errorf("email %s already registered", req.Email))
    }
    // ...
}
```

---

## handleServiceError Function

Single translation point between domain errors and HTTP responses.

```go
// internal/handler/errors.go
package handler

import (
    "errors"
    "log/slog"
    "net/http"

    "github.com/gin-gonic/gin"
    "myapp/internal/domain"
)

func handleServiceError(c *gin.Context, err error, logger *slog.Logger) {
    var appErr *domain.AppError
    if errors.As(err, &appErr) {
        if appErr.Code >= 500 {
            logger.ErrorContext(c.Request.Context(), "service error",
                "error", appErr.Unwrap(),
                "path", c.FullPath(),
            )
        }
        c.JSON(appErr.Code, gin.H{"error": appErr.Message})
        return
    }

    logger.ErrorContext(c.Request.Context(), "unexpected error",
        "error", err,
        "path", c.FullPath(),
    )
    c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
```

## Error Wrapping & Unwrapping

```go
// Wrapping — preserves the chain
return nil, fmt.Errorf("userService.Create: %w", domain.ErrConflict.New(err))

// Unwrapping — errors.As walks the chain
var appErr *domain.AppError
if errors.As(err, &appErr) {
    // appErr is the first *AppError in the chain
}

// errors.Is for sentinel comparison (AppError implements Is() by Code)
if errors.Is(err, domain.ErrNotFound) {
    // true if ErrNotFound appears anywhere in the chain
}
```

**Note:** `AppError` implements `Is()` matching by `Code`, so `errors.Is(err, domain.ErrNotFound)` works correctly through the chain even when `.New(cause)` creates a new pointer. Use `errors.As` when you need access to the full `AppError` value.
