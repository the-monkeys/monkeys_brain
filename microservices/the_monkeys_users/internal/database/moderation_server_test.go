package database

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestRegisterModerationServerUsesUserDatabasePool(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	server := grpc.NewServer()
	if err := RegisterModerationServer(server, &uDBHandler{db: db}); err != nil {
		t.Fatal(err)
	}
	lis := bufconn.Listen(1024 * 1024)
	go server.Serve(lis)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///user-moderation-test", grpc.WithNoProxy(),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	client := pb.NewModerationServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, kind := range []string{"blog", "event", "group", "discussion"} {
		mock.ExpectBegin()
		// An invalid authenticated actor fails closed, using this exact pool.
		mock.ExpectQuery("SELECT u.id,u.username").WithArgs("missing-account").
			WillReturnRows(sqlmock.NewRows([]string{"id", "username", "role", "date_of_birth"}))
		mock.ExpectRollback()
		_, err := client.ReportContent(ctx, &pb.ActionRequest{
			Actor:  &pb.Actor{AccountId: "missing-account"},
			Target: &pb.Target{Kind: kind, Id: "existing-public-id"}, Reason: "nsfw",
		})
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("%s: expected unauthenticated, got %v", kind, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
	mock.ExpectBegin()
	mock.ExpectCommit()
	got, err := client.Check(ctx, &pb.CheckRequest{})
	if err != nil || len(got.Items) != 0 {
		t.Fatalf("shared pool policy check: %v, %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterModerationServerRejectsInvalidUserDatabase(t *testing.T) {
	var typedNil *uDBHandler
	for _, db := range []UserDb{nil, typedNil, &uDBHandler{}} {
		server := grpc.NewServer()
		if err := RegisterModerationServer(server, db); err == nil {
			t.Fatal("registration must fail before startup with no primary user SQL pool")
		}
		if len(server.GetServiceInfo()) != 0 {
			t.Fatal("invalid pool must not register a moderation handler")
		}
		server.Stop()
	}
}
