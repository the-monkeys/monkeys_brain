# Defensive Handler Patterns — Input Validation, Fail-Closed, Bounds

See also: `safe-context-extraction.md`, `routing-validators-and-limits.md`

## Validate Input Format Before DB Lookup

Always validate the format of path parameters before hitting the database. This prevents unnecessary DB queries and returns clearer error messages.

```go
import gouid "github.com/google/uuid"

func (h *Handler) CreateVenda(c *gin.Context) {
    uuid := c.Param("uuid")

    // ✅ Validate format first — returns 400, not 404
    if _, err := gouid.Parse(uuid); err != nil {
        apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid uuid format")
        return
    }

    // Only hit DB after format is valid
    user, err := h.users.GetByUUID(uuid)
    if err != nil {
        apiError(c, http.StatusNotFound, "NOT_FOUND", "uuid not found")
        return
    }
    // ...
}
```

**Rule:** For every path parameter:
- UUID → validate with `uuid.Parse()`
- Numeric ID → validate with `strconv.ParseInt()` (already standard practice)
- Slug → validate with regex if format is constrained
- Return 400 for bad format, 404 for valid format but not found

## Fail-Closed Security Principle

When parsing security-related data (schedules, permissions, configs), **deny access on parse failure**. Never fail-open.

```go
// ❌ DANGEROUS — corrupt JSON = allow access
if err := json.Unmarshal([]byte(*scheduleJSON), &schedule); err != nil {
    return true // attacker can corrupt data to bypass restrictions
}

// ✅ SAFE — corrupt JSON = deny access + log warning
if err := json.Unmarshal([]byte(*scheduleJSON), &schedule); err != nil {
    slog.Warn("invalid access schedule JSON", "error", err)
    return false // fail-closed: deny on corruption
}
```

**Rule:** When in doubt, deny. It's easier to debug "why am I blocked" than "how did they get in."

## Pagination Bounds

Always cap pagination parameters to prevent:
- Integer overflow in offset calculation (theoretical on 32-bit)
- Excessive DB scans from absurdly large page numbers
- Memory exhaustion from large `per_page` values

```go
func ParsePaginationParams(c *gin.Context) PaginationParams {
    page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
    if page < 1 {
        page = 1
    }

    perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "15"))
    if perPage < 1 || perPage > 100 {
        perPage = 15
    }

    // ✅ Cap page to prevent excessive offset
    if page > 10000 {
        page = 10000
    }

    return PaginationParams{Page: page, PerPage: perPage}
}
```

**Rule:** Always enforce: `1 <= page <= 10000`, `1 <= per_page <= 100`.

## Background Goroutine Lifecycle

Any goroutine started in middleware or server setup MUST be cancellable via context or done channel. Otherwise it leaks on shutdown or in tests.

```go
// ❌ LEAKS — goroutine runs forever, even after server shuts down
go func() {
    for {
        time.Sleep(time.Minute)
        cleanup()
    }
}()

// ✅ SAFE — stops cleanly on context cancellation
go func() {
    ticker := time.NewTicker(time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            cleanup()
        }
    }
}()
```

**Rule:** Never use bare `for { time.Sleep(...) }` in production code. Always use `ticker + select + ctx.Done()`.

## Summary Checklist

Before marking a handler "done," verify:

- [ ] All `c.Get()` values extracted via safe helpers (no raw type assertions)
- [ ] Path parameters validated for format before DB lookup
- [ ] Security-related parsing fails closed (deny on error)
- [ ] Pagination capped with upper bounds
- [ ] Any background goroutines are cancellable
- [ ] Sensitive data (passwords, tokens) never logged or returned in errors
