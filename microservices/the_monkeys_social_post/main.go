package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_social_post/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/logger"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_social_post/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_social_post/internal/services"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	log := logger.ZapForService("social-post")
	defer logger.Sync()
	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalw("load social post config", "err", err)
	}
	store, err := database.New(cfg)
	if err != nil {
		log.Fatalw("connect social post database", "err", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Errorw("close social post database", "err", err)
		}
	}()
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", cfg.Microservices.SocialPostPort))
	if err != nil {
		log.Fatalw("listen social post service", "err", err)
	}
	svc := services.New(store)
	server := grpc.NewServer()
	pb.RegisterSocialPostServiceServer(server, svc)
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("SocialPostService", grpc_health_v1.HealthCheckResponse_SERVING)

	workerID := fmt.Sprintf("social-post-worker-%d", os.Getpid())
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	go services.NewWorker(svc, workerID, log).Run(workerCtx)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		cancelWorker()
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		server.GracefulStop()
	}()
	if err := server.Serve(lis); err != nil {
		log.Fatalw("serve social post service", "err", err)
	}
}
