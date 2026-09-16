# Routing — Path Parameters, Wildcards & NoRoute

See also: `routing-groups-and-versioning.md`, `routing-validators-and-limits.md`

## Path Parameter Patterns

> **Security note:** In production, never expose raw `err.Error()` to clients. Return generic messages and log the error server-side.

Use `ShouldBindURI` with a struct to bind multiple path parameters at once.

```go
// Single parameter
r.GET("/users/:id", func(c *gin.Context) {
    id := c.Param("id") // returns value without leading slash for :param
})

// Multiple parameters with struct binding
r.GET("/orgs/:orgID/users/:userID", func(c *gin.Context) {
    type params struct {
        OrgID  string `uri:"orgID"  binding:"required"`
        UserID string `uri:"userID" binding:"required"`
    }
    var p params
    if err := c.ShouldBindURI(&p); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    // p.OrgID, p.UserID are bound and validated
})
```

**Critical:** URI parameter names in the `uri:""` tag must match the `:name` in the route path exactly.

---

## Wildcard Routes & NoRoute Handler

### Wildcard Parameters

`*param` matches everything after the prefix, including slashes.

```go
// Matches: /files/docs/report.pdf, /files/images/logo.png
r.GET("/files/*filepath", func(c *gin.Context) {
    filepath := c.Param("filepath") // includes leading slash: "/docs/report.pdf"
})
```

### NoRoute Handler (Custom 404)

```go
r.NoRoute(func(c *gin.Context) {
    c.JSON(http.StatusNotFound, gin.H{
        "error": "route not found",
        "path":  c.Request.URL.Path,
    })
})

// Required — NoMethod handler only fires when this is true (default: false)
r.HandleMethodNotAllowed = true

r.NoMethod(func(c *gin.Context) {
    c.JSON(http.StatusMethodNotAllowed, gin.H{
        "error":  "method not allowed",
        "method": c.Request.Method,
    })
})
```

---

## Multipart File Upload (via Routing)

```go
// internal/handler/upload_handler.go
func (h *UploadHandler) UploadAvatar(c *gin.Context) {
    type uriParams struct {
        UserID string `uri:"id" binding:"required"`
    }
    var p uriParams
    if err := c.ShouldBindURI(&p); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    file, err := c.FormFile("avatar")
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "avatar file required"})
        return
    }

    ext := filepath.Ext(file.Filename)
    allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
    if !allowed[ext] {
        c.JSON(http.StatusBadRequest, gin.H{"error": "only jpg, png, webp files allowed"})
        return
    }

    if file.Size > 2<<20 {
        c.JSON(http.StatusBadRequest, gin.H{"error": "file must be under 2 MB"})
        return
    }

    // Sanitize path: strip directory components to prevent path traversal
    safeID := filepath.Base(p.UserID)
    dst := filepath.Join("uploads/avatars", safeID+ext)
    if err := c.SaveUploadedFile(file, dst); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "upload failed"})
        return
    }

    c.JSON(http.StatusOK, gin.H{"path": dst})
}
```

Route registration:

```go
r.MaxMultipartMemory = 8 << 20

protected := r.Group("/api/v1")
protected.Use(middleware.Auth(tokenCfg, logger))
{
    protected.POST("/users/:id/avatar",    uploadHandler.UploadAvatar)
    protected.POST("/users/:id/documents", uploadHandler.UploadDocuments)
}
```
