# Safe Context Extraction — Preventing Nil Pointer Panics

See also: `server-handlers-and-errors.md`, `middleware-core.md`

## The Problem

Gin's `c.Get()` returns `(any, bool)`. Doing a raw type assertion without checking causes a **panic** if the middleware that sets the value fails or is misconfigured:

```go
// ❌ DANGEROUS — panics if "user_id" is missing or nil
userID, _ := c.Get("user_id")
uid := userID.(int64) // panic: interface conversion: interface is nil, not int64

// ❌ DANGEROUS — panics if pointer is nil
empresaID, _ := c.Get("empresa_id")
eid := empresaID.(*int64) // panic: nil pointer dereference on *eid
```

This is one of the most common runtime crashes in Gin APIs. Every handler that reads from context is vulnerable.

## The Solution: Type-Safe Extraction Helpers

Create helper functions that handle nil/missing values safely. Place them in `internal/handler/` alongside your error helpers.

```go
// internal/handler/context_helpers.go
package handler

import "github.com/gin-gonic/gin"

// getContextInt64 safely extracts an int64 from gin context.
func getContextInt64(c *gin.Context, key string) (int64, bool) {
    val, exists := c.Get(key)
    if !exists {
        return 0, false
    }
    v, ok := val.(int64)
    return v, ok
}

// getContextInt64Ptr safely extracts a *int64 from gin context.
func getContextInt64Ptr(c *gin.Context, key string) (*int64, bool) {
    val, exists := c.Get(key)
    if !exists || val == nil {
        return nil, false
    }
    v, ok := val.(*int64)
    return v, ok && v != nil
}

// getContextString safely extracts a string from gin context.
func getContextString(c *gin.Context, key string) (string, bool) {
    val, exists := c.Get(key)
    if !exists {
        return "", false
    }
    v, ok := val.(string)
    return v, ok
}

// getContextBool safely extracts a bool from gin context (defaults to false).
func getContextBool(c *gin.Context, key string) bool {
    val, _ := c.Get(key)
    v, _ := val.(bool)
    return v
}
```

## Usage in Handlers

```go
func (h *Handler) ListVendas(c *gin.Context) {
    role, _ := getContextString(c, "role")

    switch role {
    case domain.RoleHolding:
        // ✅ SAFE — returns error instead of panicking
        hid, ok := getContextInt64Ptr(c, "holding_id")
        if !ok {
            apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "missing context")
            return
        }
        vendas, total, err = h.vendas.ListByHolding(*hid, params)

    case domain.RoleAdminEmpresa:
        eid, ok := getContextInt64Ptr(c, "empresa_id")
        if !ok {
            apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "missing context")
            return
        }
        vendas, total, err = h.vendas.ListByEmpresa(*eid, params)

    case domain.RoleFuncionario:
        uid, ok := getContextInt64(c, "user_id")
        if !ok {
            apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "missing context")
            return
        }
        vendas, total, err = h.vendas.ListByUser(uid, params)
    }
}
```

## Rules

1. **NEVER** do raw type assertions on `c.Get()` values — always use helper functions or check the `ok` return
2. **NEVER** dereference a `*int64` (or any pointer) from context without nil check
3. When extraction fails, return HTTP 500 — this indicates a middleware misconfiguration, not a user error
4. Create helpers once, reuse everywhere — consistency prevents one-off mistakes
5. For bool context values (feature flags, permissions), default to `false` (deny) on missing

## Common Patterns

### Access check helpers (same pattern)

```go
func (h *Handler) sameEmpresa(c *gin.Context, ownerID int64) bool {
    // ✅ SAFE — fails closed (returns false) if context is missing
    eid, ok := getContextInt64Ptr(c, "empresa_id")
    if !ok {
        return false
    }
    owner, err := h.users.GetByID(ownerID)
    if err != nil {
        return false
    }
    return owner.EmpresaID != nil && *owner.EmpresaID == *eid
}
```

### Audit log helpers

```go
func (h *Handler) logAudit(c *gin.Context, action, resource string, resourceID *int64, details domain.JSONB) {
    var userID *int64
    // ✅ SAFE — no panic if user_id is missing (e.g. public endpoints)
    if uid, ok := getContextInt64(c, "user_id"); ok {
        userID = &uid
    }
    h.auditRepo.Log(domain.AuditLog{UserID: userID, Action: action, ...})
}
```
