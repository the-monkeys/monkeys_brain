# Server and Handlers — Setup, Routes & Request Binding

See also: `server-handlers-and-errors.md`

## Project Structure

```
myapp/
├── cmd/api/main.go          # Entry point, wiring
├── internal/
│   ├── handler/             # HTTP handlers (thin layer)
│   ├── service/             # Business logic
│   ├── repository/          # Data access
│   └── domain/              # Entities, interfaces, errors
├── pkg/middleware/          # Shared middleware
└── go.mod
```

Use `internal/` for code that must not be imported by other modules. Use `pkg/` for reusable middleware and utilities.

## Server Setup with Graceful Shutdown

```go
package main

import (
    "context"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/gin-gonic/gin"
    "myapp/internal/handler"
    "myapp/internal/service"
    "myapp/internal/repository"
    "myapp/pkg/middleware"
)

func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

    r := gin.New()
    r.SetTrustedProxies([]string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"})
    r.Use(middleware.Logger(logger))
    r.Use(middleware.Recovery(logger))

    var db *gorm.DB // see gin-database skill
    userRepo := repository.NewUserRepository(db)
    userSvc := service.NewUserService(userRepo)
    userHandler := handler.NewUserHandler(userSvc, logger)
    authHandler := handler.NewAuthHandler(userSvc, logger)
    tokenCfg := auth.TokenConfig{Secret: os.Getenv("JWT_SECRET")}

    registerRoutes(r, userHandler, authHandler, tokenCfg, logger)

    srv := &http.Server{
        Addr:              ":" + os.Getenv("PORT"),
        Handler:           r,
        ReadHeaderTimeout: 10 * time.Second, // guards against Slowloris (CWE-400)
        ReadTimeout:       30 * time.Second,
        WriteTimeout:      30 * time.Second,
        IdleTimeout:       120 * time.Second,
    }

    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            logger.Error("server failed", "error", err)
            os.Exit(1)
        }
    }()

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    if err := srv.Shutdown(ctx); err != nil {
        logger.Error("graceful shutdown failed", "error", err)
    }
}
```

## Route Registration

```go
func registerRoutes(r *gin.Engine, userHandler *handler.UserHandler, authHandler *handler.AuthHandler, tokenCfg auth.TokenConfig, logger *slog.Logger) {
    r.GET("/health", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{"status": "ok"})
    })

    api := r.Group("/api/v1")

    public := api.Group("")
    {
        public.POST("/users", userHandler.Create)
        public.POST("/auth/login", authHandler.Login)
    }

    protected := api.Group("")
    protected.Use(middleware.Auth(tokenCfg, logger))
    {
        protected.GET("/users/:id", userHandler.GetByID)
        protected.GET("/users", userHandler.List)
        protected.PUT("/users/:id", userHandler.Update)
        protected.DELETE("/users/:id", userHandler.Delete)
    }
}
```

## Request Binding Patterns

```go
// JSON body
var req domain.CreateUserRequest
if err := c.ShouldBindJSON(&req); err != nil { ... }

// Query string: GET /users?page=1&limit=20&role=admin
type ListQuery struct {
    Page  int    `form:"page"  binding:"min=1"`
    Limit int    `form:"limit" binding:"min=1,max=100"`
    Role  string `form:"role"  binding:"omitempty,oneof=admin user"`
}
var q ListQuery
if err := c.ShouldBindQuery(&q); err != nil { ... }

// URI parameters: GET /users/:id
type URIParams struct {
    ID string `uri:"id" binding:"required"`
}
var params URIParams
if err := c.ShouldBindURI(&params); err != nil { ... }
```

**Critical:** Always use `ShouldBind*` — `Bind*` auto-aborts with 400 and prevents custom error responses.
