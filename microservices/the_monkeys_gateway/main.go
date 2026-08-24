package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/secure"
	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/logger"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/admin"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/blog"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/blogsearch"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/cache/searchcache"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/events"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/groups"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/notification"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/snapshot"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/storage"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/storage_v2"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/systems"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/user_service"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/middleware"

	"go.uber.org/zap"
)

type Server struct {
	router *gin.Engine
}

func newServer() *Server {
	return &Server{router: gin.New()}
}

func printBanner(cfg *config.Config) {
	banner := "\n" +
		"┌────────────────────────────────────────────────────────────┐\n" +
		"│   🐒  The Monkeys API Gateway                              │\n" +
		"│   Status   : ONLINE                                        │\n" +
		"│   HTTP     : http://" + cfg.TheMonkeysGateway.HTTP + "\n" +
		"│   HTTPS    : https://" + cfg.TheMonkeysGateway.HTTPS + "\n" +
		"│   Env      : " + cfg.AppEnv + "\n" +
		"│   Logs     : zap (structured)                              │\n" +
		"│   Tip      : export LOG_LEVEL=debug for verbose logs       │\n" +
		"└────────────────────────────────────────────────────────────┘\n"
	fmt.Print(banner)
}

func main() {
	// Load API Gateway configuration
	cfg, err := config.GetConfig()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}
	log := logger.ZapForService("gateway")
	defer logger.Sync()

	// Search v2 cache (Phase 0 plumbing). Safe-to-fail: returns a usable
	// instance even if Redis is down so search still serves traffic.
	searchCache := searchcache.New(cfg, log)
	defer func() {
		if err := searchCache.Close(); err != nil {
			log.Warnw("searchcache close failed", "err", err)
		}
	}()
	_ = searchCache // additional consumers wired in later phases
	// Set Gin to Release mode
	gin.SetMode(gin.ReleaseMode)

	// Create a gin router and add the Recovery middleware to recover from panics
	server := newServer()
	server.router.Use(gin.Recovery())
	// Structured request logger via zap — logs at debug level, zero cost in production
	server.router.Use(middleware.ZapRequestLogger())
	server.router.MaxMultipartMemory = 100 << 20

	// Apply security middleware
	server.router.Use(secure.New(secure.Config{
		FrameDeny:             false,
		ContentTypeNosniff:    true,
		BrowserXssFilter:      true,
		ContentSecurityPolicy: "default-src 'self';", // Customize as needed
		ReferrerPolicy:        "strict-origin-when-cross-origin",
	}))

	// Enable CORS - conditionally use temp CORS or strict CORS based on config
	if cfg.Cors.UseTempCors {
		log.Debug("using temporary CORS middleware (allow all origins)")
		server.router.Use(middleware.TmpCORSMiddleware())
	} else {
		log.Debug("using strict CORS middleware (regex)")
		server.router.Use(middleware.CORSMiddleware(cfg.Cors.AllowedOriginExp))
	}

	// Log request body
	server.router.Use(middleware.LogRequestBody())

	// Register REST routes for all the microservices
	authClient := auth.RegisterAuthRouter(server.router, cfg, log)
	userClient := user_service.RegisterUserRouter(server.router, cfg, authClient, log, searchCache)
	blog.RegisterBlogRouter(server.router, cfg, authClient, userClient, log)
	// Phase 3: search-v2 blog & autocomplete endpoints. Hits ES via the
	// the_monkeys_blogs alias, so it follows whichever physical index
	// the Phase 2 alias swap is currently pointing at.
	blogsearch.RegisterRoutes(server.router, cfg, searchCache, log)
	storageClient := storage.RegisterFileStorageRouter(server.router, cfg, authClient, log)
	storageV2Svc := storage_v2.RegisterRoutes(server.router, cfg, storageClient.Client, authClient, log)
	notification.RegisterNotificationRoute(server.router, cfg, authClient, log)
	eventClient := events.RegisterEventRouter(server.router, cfg, authClient, storageV2Svc, log)
	groups.RegisterGroupRouter(server.router, cfg, authClient, eventClient.Client, storageV2Svc, log)
	// monkeys_ai.RegisterRecommendationRoute(server.router, cfg, authClient, log)
	admin.RegisterAdminRouter(server.router, cfg, log)
	systems.RegisterSystemRouter(server.router, cfg, log)
	snapshot.RegisterRoutes(server.router, cfg, log)

	// Health check endpoint
	server.router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "healthy",
		})
	})

	printBanner(cfg)
	server.start(context.Background(), cfg, log)
}

func (s *Server) start(ctx context.Context, cfg *config.Config, log *zap.SugaredLogger) {
	// TLS certificate and key
	var tlsCert, tlsKey string
	if os.Getenv("NO_TLS") != "1" {
		tlsCert = os.Getenv("TLS_CERT")
		if tlsCert == "" {
			tlsCert = "config/certs/openssl/server.crt"
		}
		tlsKey = os.Getenv("TLS_KEY")
		if tlsKey == "" {
			tlsKey = "config/certs/openssl/server.key"
		}
	}
	// Launch the server (this is a blocking call)
	s.launchServer(ctx, cfg, tlsCert, tlsKey, log)
}

// Start the server
func (s *Server) launchServer(ctx context.Context, cfg *config.Config, tlsCert, tlsKey string, log *zap.SugaredLogger) {
	// If we don't have a TLS certificate, don't enable TLS
	enableTLS := (tlsCert != "" && tlsKey != "")

	common := func(srv *http.Server, name string) {
		log.Infow(name+" listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorw(name+" server start failed", "err", err)
			panic(err)
		}
	}

	// HTTP server (no TLS)
	httpAddr := cfg.TheMonkeysGateway.HTTP
	if httpAddr != "" && httpAddr[0] != ':' {
		httpAddr = ":" + httpAddr
	}
	httpSrv := &http.Server{
		Addr:           httpAddr,
		Handler:        s.router,
		MaxHeaderBytes: 5 << 20,
		ReadTimeout:    1 * time.Hour,
		WriteTimeout:   1 * time.Hour,
	}

	// HTTPS server (with TLS)
	httpsAddr := cfg.TheMonkeysGateway.HTTPS
	if httpsAddr != "" && httpsAddr[0] != ':' {
		httpsAddr = ":" + httpsAddr
	}
	httpsSrv := &http.Server{
		Addr:           httpsAddr,
		Handler:        s.router,
		MaxHeaderBytes: 5 << 20,
		ReadTimeout:    1 * time.Hour,
		WriteTimeout:   1 * time.Hour,
	}

	// Start the HTTP server in a background goroutine
	go common(httpSrv, "http")

	// Start the HTTPS server in a background goroutine
	if enableTLS {
		// go func() {
		// 	log.Infow("https listening", "addr", httpsSrv.Addr)
		// 	if err := httpsSrv.ListenAndServeTLS(tlsCert, tlsKey); err != nil && err != http.ErrServerClosed {
		// 		log.Errorw("https server start failed", "err", err)
		// 		panic(err)
		// 	}
		// }()
	}

	// Listen to SIGINT and SIGTERM signals
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)

	// Block until we either get a termination signal, or until the context is canceled
	select {
	case <-ctx.Done():
	case <-ch:
	}

	// We received an interrupt signal, shut down both servers
	var errHttp, errHttps error
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	errHttp = httpSrv.Shutdown(shutdownCtx)
	if enableTLS {
		errHttps = httpsSrv.Shutdown(shutdownCtx)
	}
	shutdownCancel()
	// Log the errors (could be context canceled)
	if errHttp != nil {
		log.Errorw("http shutdown error", "err", errHttp)
	}
	if errHttps != nil {
		log.Errorw("https shutdown error", "err", errHttps)
	}
	log.Infow("gateway shutdown complete")
}
