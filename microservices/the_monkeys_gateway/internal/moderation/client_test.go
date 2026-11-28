package moderation

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"google.golang.org/grpc"
)

type userHostedModeration struct {
	pb.UnimplementedModerationServiceServer
}

func (userHostedModeration) Check(context.Context, *pb.CheckRequest) (*pb.CheckResponse, error) {
	return &pb.CheckResponse{Items: []*pb.Access{{Reason: "user-service"}}}, nil
}

func TestNewClientUsesUserServiceEndpoint(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterModerationServiceServer(server, userHostedModeration{})
	go server.Serve(lis)
	t.Cleanup(server.Stop)
	host, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Microservices.TheMonkeysUser = host
	cfg.Microservices.UserPort = n
	// An invalid events endpoint proves moderation does not depend on events.
	cfg.Microservices.TheMonkeysEvents = "127.0.0.1"
	cfg.Microservices.EventsPort = 0
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := client.Check(ctx, &pb.CheckRequest{})
	if err != nil {
		t.Fatalf("moderation must use the user-service endpoint: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Reason != "user-service" {
		t.Fatalf("unexpected moderation response: %v", got)
	}
}
