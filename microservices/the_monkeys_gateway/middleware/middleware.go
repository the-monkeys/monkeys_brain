package middleware

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/idempotency"
	"github.com/ulule/limiter/v3"
	limiterGin "github.com/ulule/limiter/v3/drivers/middleware/gin"
	"github.com/ulule/limiter/v3/drivers/store/memory"
	"go.uber.org/zap"
)

func SetMiddlewareJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next(w, r)
	}
}

func CORSMiddleware(allowedOriginExp string) gin.HandlerFunc {
	lg := zap.S().With("middleware", "cors")
	return func(c *gin.Context) {
		requestOrigin := c.Request.Header.Get("Origin")
		if match, _ := regexp.Match(allowedOriginExp, []byte(requestOrigin)); match {
			c.Writer.Header().Set("Access-Control-Allow-Origin", requestOrigin)
			lg.Debugw("cors request allowed", "origin", requestOrigin, "method", c.Request.Method)
		} else {
			lg.Warnw("cors request blocked", "origin", requestOrigin, "method", c.Request.Method)
		}
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, IP, Client, OS, User-Agent, Accept-Language, DNT, Sec-CH-UA, Sec-CH-UA-Mobile, Sec-CH-UA-Platform, Sec-Fetch-Site, Sec-Fetch-Mode, Sec-Fetch-User, Sec-Fetch-Dest, Referer, X-Forwarded-For, X-Real-IP, X-Forwarded-Proto, X-Forwarded-Host, X-Original-URL, CF-Connecting-IP, CF-IPCountry, CF-Ray, CF-Visitor, X-UTM-Source, X-UTM-Medium, X-UTM-Campaign, X-UTM-Term, X-UTM-Content, X-Client-ID, X-Session-ID, X-Request-ID, X-Correlation-ID, Idempotency-Key")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, PATCH, DELETE")
		c.Header("Access-Control-Expose-Headers", "Idempotency-Replayed, Retry-After")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

func TmpCORSMiddleware() gin.HandlerFunc {
	cfg := cors.Config{AllowOrigins: []string{"*"}, AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"}, AllowHeaders: []string{"*"}, ExposeHeaders: []string{"Idempotency-Replayed", "Retry-After"}, AllowCredentials: true}
	corsMiddleware := cors.New(cfg)
	lg := zap.S().With("middleware", "tmp_cors")
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" {
			lg.Debugw("temporary cors allow all", "origin", origin, "method", c.Request.Method)
		}
		corsMiddleware(c)
	}
}

func NewCorsMiddleware() gin.HandlerFunc {
	return cors.New(cors.Config{AllowOrigins: []string{"*"}, AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}, AllowHeaders: []string{"Origin", "Content-Type", "Authorization", "IP", "Client", "Content-Length", "Accept-Encoding", "X-CSRF-Token", "accept", "Cache-Control", "X-Requested-With", "User-Agent", "Accept-Language", "DNT", "Sec-CH-UA", "Sec-CH-UA-Mobile", "Sec-CH-UA-Platform", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-User", "Sec-Fetch-Dest", "Referer", "X-Forwarded-For", "X-Real-IP", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Original-URL", "CF-Connecting-IP", "CF-IPCountry", "CF-Ray", "CF-Visitor", "X-UTM-Source", "X-UTM-Medium", "X-UTM-Campaign", "X-UTM-Term", "X-UTM-Content", "X-Client-ID", "X-Session-ID", "X-Request-ID", "X-Correlation-ID", "Idempotency-Key"}, ExposeHeaders: []string{"Content-Length", "Idempotency-Replayed", "Retry-After"}, AllowCredentials: true})
}

func LogRequestBody() gin.HandlerFunc {
	lg := zap.S().With("middleware", "req_body")
	return func(c *gin.Context) {
		// Keyed creation bodies must be bounded and read only after authentication.
		if _, keyed := c.Request.Header[http.CanonicalHeaderKey("Idempotency-Key")]; keyed && idempotency.IsCreation(c.Request.Method, c.FullPath()) {
			c.Next()
			return
		}
		// Skip logging for multipart form data (large files/videos)
		contentType := c.Request.Header.Get("Content-Type")
		if strings.Contains(contentType, "multipart/form-data") {
			c.Next()
			return
		}

		// Skip logging if Content-Length is too large (> 1MB)
		if c.Request.ContentLength > 1024*1024 {
			c.Next()
			return
		}

		var bodyBuffer bytes.Buffer
		if _, err := io.Copy(&bodyBuffer, c.Request.Body); err != nil {
			lg.Errorw("copy body failed", "err", err)
			if err := c.AbortWithError(http.StatusBadRequest, err); err != nil {
				lg.Errorw("abort request failed", "err", err)
			}
			return
		}
		if err := c.Request.Body.Close(); err != nil {
			lg.Errorw("close body failed", "err", err)
		}
		c.Request.Body = io.NopCloser(&bodyBuffer)
		c.Next()
	}
}

func RateLimiterMiddleware(limit string) gin.HandlerFunc {
	rate, err := limiter.NewRateFromFormatted(limit)
	if err != nil {
		panic(err)
	}
	store := memory.NewStore()
	instance := limiter.New(store, rate)
	return limiterGin.NewMiddleware(instance)
}

// ZapRequestLogger logs every HTTP request at debug level via zap.
// Zero cost in production when LOG_LEVEL >= info because zap skips the message entirely.
func ZapRequestLogger() gin.HandlerFunc {
	lg := zap.S().With("middleware", "access_log")
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		lg.Debugw("request",
			"method", c.Request.Method,
			"path", path,
			"query", query,
			"status", status,
			"latency_ms", latency.Milliseconds(),
			"client_ip", c.ClientIP(),
			"user", c.GetString("userName"),
		)
	}
}
