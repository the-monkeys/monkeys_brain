# Rate Limiting — Per-User & API-Key Limiting

See also: `rate-limiting-algorithms.md`, `rate-limiting-redis.md`, `rate-limiting-tiered.md`, `rate-limiting-headers.md`, `rate-limiting-fallback.md`

## Per-User / API-Key Limiting

Replace the IP key with a user ID from JWT claims or an `X-API-Key` header. This survives IP changes (mobile clients) and is harder to spoof than IP.

```go
// pkg/middleware/rate_limiter_key_extractor.go
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// ClientKeyFunc extracts a rate-limit key from the request.
// Returning "" falls back to ClientIP.
type ClientKeyFunc func(c *gin.Context) string

// APIKeyExtractor reads the X-API-Key header.
func APIKeyExtractor(c *gin.Context) string {
	if key := c.GetHeader("X-API-Key"); key != "" {
		return "apikey:" + key
	}
	return ""
}

// JWTSubjectExtractor reads the "sub" claim from a Bearer token.
// Does NOT validate the token — pair with AuthRequired middleware first.
func JWTSubjectExtractor(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	tokenStr := strings.TrimPrefix(header, "Bearer ")
	claims := jwt.MapClaims{}
	parser := jwt.NewParser()
	token, _, err := parser.ParseUnverified(tokenStr, claims)
	if err != nil {
		return ""
	}
	_ = token
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return ""
	}
	return "user:" + sub
}

// WithKeyExtractor wraps any limiter middleware with a custom key extractor.
// keyFn derives the rate-limit key; if it returns "", c.ClientIP() is used.
func WithKeyExtractor(keyFn ClientKeyFunc, next func(key string) gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := keyFn(c)
		if key == "" {
			key = c.ClientIP()
		}
		next(key)(c)
	}
}
```

**Usage with Redis token bucket:**

```go
// main.go
limiterFn := func(key string) gin.HandlerFunc {
    return middleware.RedisTokenBucketLimiter(middleware.RedisTokenBucketConfig{
        Client:    rdb,
        Capacity:  100,
        Rate:      10,
        KeyPrefix: key + ":",
    })
}

api := r.Group("/api/v1")
api.Use(middleware.WithKeyExtractor(middleware.APIKeyExtractor, limiterFn))
```

**Note:** `APIKeyExtractor` has signature `func(*gin.Context) string` (ClientKeyFunc), not `gin.HandlerFunc` — it cannot be used with `r.Use()` directly. `WithKeyExtractor` composes them into a single `gin.HandlerFunc`.
