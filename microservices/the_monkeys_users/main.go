package main

import (
	"fmt"
	"net"

	eventpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/logger"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/consumer"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/services"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func printBanner(cfg *config.Config) {
	banner := `
┌────────────────────────────────────────────────────────────┐
│   🐒  The Monkeys User Service                             │
│   Status   : ONLINE                                         │
│   Service  : ` + cfg.Microservices.TheMonkeysUser + `
│   Port     : ` + fmt.Sprintf("%d", cfg.Microservices.UserPort) + `
│   Env      : ` + cfg.AppEnv + `
│   Logs     : zap (structured)                               │
│   Tip      : set LOG_LEVEL=debug for verbose logs           │
└────────────────────────────────────────────────────────────┘`
	fmt.Printf("%s\nEnvironment: %s\nService: %s\nPort: %d\n", banner, cfg.AppEnv, cfg.Microservices.TheMonkeysUser, cfg.Microservices.UserPort)
}

func main() {
	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Printf("failed to load user config, error: %+v\n", err)
	}
	log := logger.ZapForService("tm_users")

	db, err := database.NewUserDbHandler(cfg, log)
	if err != nil {
		log.Fatalf("failed to connect to the database: %v", err)
	}

	// Bind to all interfaces for health checks to work
	listenAddr := fmt.Sprintf("0.0.0.0:%d", cfg.Microservices.UserPort)
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Errorf("failed to listen at port %v, error: %+v", listenAddr, err)
	}

	printBanner(cfg)

	qConn := rabbitmq.NewConnManager(cfg.RabbitMQ)
	go consumer.ConsumeFromQueue(qConn, cfg, log, db)

	eventsAddr := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysEvents, cfg.Microservices.EventsPort)
	eventsConn, err := grpc.NewClient(eventsAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Errorf("failed to dial events service at %s: %v", eventsAddr, err)
	}
	groupsAddr := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysGroups, cfg.Microservices.GroupsPort)
	groupsConn, err := grpc.NewClient(groupsAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Errorf("failed to dial groups service at %s: %v", groupsAddr, err)
	}

	var eventsClient eventpb.EventServiceClient
	var groupsClient grouppb.GroupServiceClient
	if eventsConn != nil {
		eventsClient = eventpb.NewEventServiceClient(eventsConn)
	}
	if groupsConn != nil {
		groupsClient = grouppb.NewGroupServiceClient(groupsConn)
	}

	userService := services.NewUserSvc(db, log, cfg, qConn, eventsClient, groupsClient)

	grpcServer := grpc.NewServer()
	pb.RegisterUserServiceServer(grpcServer, userService)

	// Register health check service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Set the service as serving (healthy)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("UserService", grpc_health_v1.HealthCheckResponse_SERVING)

	log.Debugf("✅ the user service started at: %v", cfg.Microservices.TheMonkeysUser+":"+fmt.Sprint(cfg.Microservices.UserPort))
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}

func BlogServiceConn(addr string) (*grpc.ClientConn, error) {
	log := logger.ZapForService("tm_users")
	log.Debugf("gRPC dialing to the blog server: %v", addr)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return conn, nil
}
