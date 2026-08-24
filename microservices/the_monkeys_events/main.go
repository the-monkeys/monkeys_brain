package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/logger"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/services"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// upkeepInterval is how often reminders are swept and finished events are
// archived.
const upkeepInterval = 5 * time.Minute

func printBanner(host, env string) {
	banner := "\n" +
		"┌──────────────────────────────────────────────────────────┐\n" +
		"│   🎟️   The Monkeys Events Service                        │\n" +
		"│   Status   : ONLINE                                      │\n" +
		fmt.Sprintf("│   Host     : %-44s│\n", host) +
		fmt.Sprintf("│   Env      : %-44s│\n", env) +
		"│   Logs     : zap (structured)                            │\n" +
		"│   Tip      : Set LOG_LEVEL=debug for verbose output      │\n" +
		"└──────────────────────────────────────────────────────────┘\n"
	fmt.Print(banner)
}

func main() {
	log := logger.ZapForService("events")
	defer logger.Sync()

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalw("cannot load events service config", "err", err)
	}

	host := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysEvents, cfg.Microservices.EventsPort)
	listenAddr := fmt.Sprintf("0.0.0.0:%d", cfg.Microservices.EventsPort)
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalw("events service cannot listen", "address", listenAddr, "err", err)
	}

	db, err := database.NewEventDB(cfg, log)
	if err != nil {
		log.Fatalw("cannot connect to the events database", "err", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Errorw("failed to close database", "err", err)
		}
	}()

	qConn := rabbitmq.NewConnManager(cfg.RabbitMQ)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	eventService := services.NewEventService(db, log, cfg, qConn)
	eventService.StartScheduler(ctx, upkeepInterval)

	grpcServer := grpc.NewServer()
	pb.RegisterEventServiceServer(grpcServer, eventService)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("EventService", grpc_health_v1.HealthCheckResponse_SERVING)

	go func() {
		<-ctx.Done()
		log.Infow("shutdown signal received, draining events service")
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		grpcServer.GracefulStop()
	}()

	printBanner(host, cfg.AppEnv)
	if err := grpcServer.Serve(lis); err != nil {
		log.Errorw("gRPC events server stopped", "err", err)
		os.Exit(1)
	}
	log.Infow("events service shutdown complete")
}
