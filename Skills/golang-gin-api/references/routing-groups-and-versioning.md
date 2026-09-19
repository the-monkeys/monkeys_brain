# Routing — Groups, Nesting & API Versioning

See also: `routing-params-and-wildcards.md`, `routing-validators-and-limits.md`

## Route Groups & Nesting

Groups share a path prefix and optional middleware. Use `{}` blocks for readability — they are cosmetic only (Go scope, not Gin scope).

```go
func registerRoutes(r *gin.Engine, h *handler.Handlers) {
    r.GET("/health", h.Health.Check)

    api := r.Group("/api")
    {
        v1 := api.Group("/v1")
        {
            auth := v1.Group("/auth")
            {
                auth.POST("/login", h.Auth.Login)
                auth.POST("/register", h.Auth.Register)
                auth.POST("/refresh", h.Auth.Refresh)
            }

            users := v1.Group("/users")
            users.Use(middleware.Auth(tokenCfg, logger))
            {
                users.GET("", h.User.List)
                users.POST("", h.User.Create)
                users.GET("/:id", h.User.GetByID)
                users.PUT("/:id", h.User.Update)
                users.DELETE("/:id", h.User.Delete)
            }

            admin := v1.Group("/admin")
            admin.Use(middleware.Auth(tokenCfg, logger), middleware.RequireRole("admin"))
            {
                admin.GET("/users", h.Admin.ListAllUsers)
                admin.DELETE("/users/:id", h.Admin.DeleteUser)
            }
        }
    }
}
```

**Why group nesting:** Each level applies its middleware to all routes below. This avoids repeating middleware on every route and makes the security model explicit in structure.

---

## API Versioning

### URL Path Versioning (recommended)

Path versioning is explicit, cache-friendly, and easy to route at the load-balancer level.

```go
v1 := r.Group("/api/v1")
v2 := r.Group("/api/v2")

v1.GET("/users", listUsersV1)
v2.GET("/users", listUsersV2)
```

### Header-Based Versioning

Use when you want a stable URL but need to evolve the API contract.

```go
// pkg/middleware/version.go
func APIVersion() gin.HandlerFunc {
    return func(c *gin.Context) {
        version := c.GetHeader("API-Version")
        if version == "" {
            version = "v1"
        }
        c.Set("api_version", version)
        c.Next()
    }
}

func listUsers(c *gin.Context) {
    version := c.GetString("api_version")
    switch version {
    case "v2":
        // return v2 shape
    default:
        // return v1 shape
    }
}
```

```go
r.Use(middleware.APIVersion())
r.GET("/api/users", listUsers)
```

---

## Query Parameter Binding with Pagination

Bind all query parameters at once using `ShouldBindQuery`. Define defaults via struct field initialisation before binding.

```go
// internal/domain/pagination.go
type ListOptions struct {
    Page   int    `form:"page"   binding:"min=1"`
    Limit  int    `form:"limit"  binding:"min=1,max=100"`
    Sort   string `form:"sort"   binding:"omitempty,oneof=created_at updated_at name"`
    Order  string `form:"order"  binding:"omitempty,oneof=asc desc"`
    Search string `form:"search" binding:"omitempty,max=100"`
    Role   string `form:"role"   binding:"omitempty,oneof=admin user"`
}

func (o *ListOptions) SetDefaults() {
    if o.Page == 0  { o.Page = 1 }
    if o.Limit == 0 { o.Limit = 20 }
    if o.Sort == ""  { o.Sort = "created_at" }
    if o.Order == "" { o.Order = "desc" }
}
```

```go
func (h *UserHandler) List(c *gin.Context) {
    var opts domain.ListOptions
    opts.SetDefaults()

    if err := c.ShouldBindQuery(&opts); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    users, total, err := h.svc.List(c.Request.Context(), opts)
    if err != nil {
        handleServiceError(c, err, h.logger)
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "data":  users,
        "total": total,
        "page":  opts.Page,
        "limit": opts.Limit,
    })
}
```
