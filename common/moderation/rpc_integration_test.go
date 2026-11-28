package moderation

import (
	"context"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"testing"
	"time"
)

func TestRPCRealStoreReportRevealAndAgeChange(t *testing.T) {
	store, db := storeFixture(t)
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	pb.RegisterModerationServiceServer(server, NewServer(store))
	go server.Serve(lis)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	client := pb.NewModerationServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := targetStore("blog")
	report, err := client.ReportContent(ctx, &pb.ActionRequest{Actor: &pb.Actor{AccountId: "support"}, Target: target, Reason: "nsfw"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := client.ReportContent(ctx, &pb.ActionRequest{Actor: &pb.Actor{AccountId: "support"}, Target: target, Reason: "nsfw"})
	if err != nil || report.Report.Id != again.Report.Id {
		t.Fatal("retry", again, err)
	}
	anonymous, err := client.Check(ctx, &pb.CheckRequest{Targets: []*pb.Target{target}})
	if err != nil || !anonymous.Items[0].IsNsfw || anonymous.Items[0].Revealed || anonymous.Items[0].Reason != "login_required" {
		t.Fatal(anonymous, err)
	}
	adult, err := client.Reveal(ctx, &pb.ActionRequest{Actor: &pb.Actor{AccountId: "adult"}, Target: target})
	if err != nil || !adult.Access.Revealed {
		t.Fatal(adult, err)
	}
	execStore(t, db, `UPDATE user_account SET date_of_birth='2020-01-01' WHERE account_id='adult'`)
	changed, err := client.Check(ctx, &pb.CheckRequest{AccountId: "adult", Targets: []*pb.Target{target}})
	if err != nil || changed.Items[0].Revealed || changed.Items[0].Reason != "underage" {
		t.Fatal(changed, err)
	}
}
