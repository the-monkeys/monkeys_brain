package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/logger"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_groups/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_groups/internal/services"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func printBanner(host, env string) {
	banner := "\n" +
		"┌──────────────────────────────────────────────────────────┐\n" +
		"│   👥   The Monkeys Groups Service                        │\n" +
		"│   Status   : ONLINE                                      │\n" +
		fmt.Sprintf("│   Host     : %-44s│\n", host) +
		fmt.Sprintf("│   Env      : %-44s│\n", env) +
		"│   Logs     : zap (structured)                            │\n" +
		"│   Tip      : Set LOG_LEVEL=debug for verbose output      │\n" +
		"└──────────────────────────────────────────────────────────┘\n"
	fmt.Print(banner)
}

func main() {
	log := logger.ZapForService("groups")
	defer logger.Sync()

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalw("cannot load groups service config", "err", err)
	}

	host := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysGroups, cfg.Microservices.GroupsPort)
	listenAddr := fmt.Sprintf("0.0.0.0:%d", cfg.Microservices.GroupsPort)
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalw("groups service cannot listen", "address", listenAddr, "err", err)
	}

	db, err := database.NewGroupDB(cfg, log)
	if err != nil {
		log.Fatalw("cannot connect to the groups database", "err", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Errorw("failed to close database", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	groupService := services.NewGroupService(db, log, cfg)

	grpcServer := grpc.NewServer()
	pb.RegisterGroupServiceServer(grpcServer, groupService)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("GroupService", grpc_health_v1.HealthCheckResponse_SERVING)

	go func() {
		<-ctx.Done()
		log.Infow("shutdown signal received, draining groups service")
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		grpcServer.GracefulStop()
	}()

	printBanner(host, cfg.AppEnv)
	if err := grpcServer.Serve(lis); err != nil {
		log.Errorw("gRPC groups server stopped", "err", err)
		os.Exit(1)
	}
	log.Infow("groups service shutdown complete")
}
