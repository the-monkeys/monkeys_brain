# Server and Handlers — Thin Handlers, Sanitization & Error Handling

See also: `server-setup-and-routes.md`

## Domain Model

> **Architecture note:** Domain entities should not carry `json` or `binding` tags. Use separate request/response DTOs in the delivery layer.

```go
// internal/domain/user.go
package domain

import "time"

type User struct {
    ID           string    `json:"id"`
    Name         string    `json:"name"`
    Email        string    `json:"email"`
    PasswordHash string    `json:"-"` // never exposed via API
    Role         string    `json:"role"`
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}

type CreateUserRequest struct {
    Name     string `json:"name"     binding:"required,min=2,max=100"`
    Email    string `json:"email"    binding:"required,email"`
    Password string `json:"password" binding:"required,min=8"`
    Role     string `json:"role"     binding:"omitempty,oneof=admin user"`
}
```

## Thin Handler Pattern

Handlers bind input, call a service, and format the response. No business logic.

```go
// internal/handler/user_handler.go
package handler

import (
    "log/slog"
    "net/http"

    "github.com/gin-gonic/gin"
    "myapp/internal/domain"
    "myapp/internal/service"
)

type UserHandler struct {
    svc    service.UserService
    logger *slog.Logger
}

func NewUserHandler(svc service.UserService, logger *slog.Logger) *UserHandler {
    return &UserHandler{svc: svc, logger: logger}
}

func (h *UserHandler) Create(c *gin.Context) {
    var req domain.CreateUserRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{
            "error":  "validation failed",
            "fields": validationErrors(err),
        })
        return
    }

    user, err := h.svc.Create(c.Request.Context(), req)
    if err != nil {
        handleServiceError(c, err, h.logger)
        return
    }

    c.JSON(http.StatusCreated, user)
}

func (h *UserHandler) GetByID(c *gin.Context) {
    type uriParams struct {
        ID string `uri:"id" binding:"required"`
    }
    var params uriParams
    if err := c.ShouldBindURI(&params); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
        return
    }

    user, err := h.svc.GetByID(c.Request.Context(), params.ID)
    if err != nil {
        handleServiceError(c, err, h.logger)
        return
    }

    c.JSON(http.StatusOK, user)
}
```

## Input Sanitization

Sanitize string fields **after** binding to neutralize injection payloads before they reach services or storage.

```go
func (h *UserHandler) Create(c *gin.Context) {
    var req domain.CreateUserRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    req.Name  = strings.TrimSpace(req.Name)
    req.Name  = html.EscapeString(req.Name)
    req.Email = strings.TrimSpace(req.Email)
    req.Email = strings.ToLower(req.Email)

    user, err := h.svc.Create(c.Request.Context(), req)
    // ...
}
```

For file uploads, always strip directory components from client-supplied filenames:

```go
safeName := filepath.Base(file.Filename)
dst := filepath.Join("uploads", safeName)
```

## Goroutine Safety

**Always call `c.Copy()` before passing `*gin.Context` to a goroutine.** The original context is reused by the pool after the request ends.

```go
cCopy := c.Copy()
go func() {
    h.notifier.SendWelcome(cCopy.Request.Context(), user)
}()
```

See also: `background-jobs-goroutine-and-pool.md` for full goroutine + worker pool examples.
