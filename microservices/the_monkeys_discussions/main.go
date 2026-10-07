package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_discussion/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/logger"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/services"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func printBanner(host, env string) {
	banner := "\n" +
		"┌──────────────────────────────────────────────────────────┐\n" +
		"│   The Monkeys Discussions Service                        │\n" +
		"│   Status   : ONLINE                                      │\n" +
		fmt.Sprintf("│   Host     : %-44s│\n", host) +
		fmt.Sprintf("│   Env      : %-44s│\n", env) +
		"│   Logs     : zap (structured)                            │\n" +
		"│   Tip      : Set LOG_LEVEL=debug for verbose output      │\n" +
		"└──────────────────────────────────────────────────────────┘\n"
	fmt.Print(banner)
}

func main() {
	log := logger.ZapForService("discussions")
	defer logger.Sync()

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalw("cannot load discussions service config", "err", err)
	}

	port := cfg.Microservices.DiscussionsPort
	if port == 0 {
		port = 50064
	}
	host := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysDiscussions, port)
	listenAddr := fmt.Sprintf("0.0.0.0:%d", port)
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalw("discussions service cannot listen", "address", listenAddr, "err", err)
	}

	db, err := database.NewDiscussionDB(cfg, log)
	if err != nil {
		log.Fatalw("cannot connect to the discussions database", "err", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Errorw("failed to close database", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	grpcServer := grpc.NewServer()
	pb.RegisterDiscussionServiceServer(grpcServer, services.NewDiscussionService(db))
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("DiscussionService", grpc_health_v1.HealthCheckResponse_SERVING)

	go func() {
		<-ctx.Done()
		log.Infow("shutdown signal received, draining discussions service")
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		grpcServer.GracefulStop()
	}()

	printBanner(host, cfg.AppEnv)
	if err := grpcServer.Serve(lis); err != nil {
		log.Errorw("gRPC discussions server stopped", "err", err)
		os.Exit(1)
	}
	log.Infow("discussions service shutdown complete")
}
