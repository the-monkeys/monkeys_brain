package services

import (
	"context"
	"testing"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/database"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type recurringCoHostDB struct {
	database.EventDB
	event   *pb.Event
	failure error
	calls   int
}

func (d *recurringCoHostDB) MaterializeSeries(context.Context, *pb.CreateSeriesReq, []time.Time, string) (*pb.Event, error) {
	d.calls++
	return d.event, d.failure
}

func TestCreateSeriesNotifiesDistinctCoHostsAfterMaterialization(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			db := &recurringCoHostDB{event: &pb.Event{
				Slug: "first-occurrence", Title: "Weekly meetup", OrganizerUsername: "owner",
				CoHostUsernames: []string{"guest", "other", "guest", "owner", ""},
			}}
			if fail {
				db.failure = status.Error(codes.Internal, "materialization failed")
			}
			publisher := &notificationCapture{messages: make(chan eventNotification, 10)}
			cfg := &config.Config{}
			cfg.RabbitMQ.RoutingKeys = []string{"storage", "one", "two", "three", "notifications"}
			service := &EventService{db: db, cfg: cfg, log: zap.NewNop().Sugar(), qConn: publisher}
			start := time.Date(2099, 1, 12, 12, 0, 0, 0, time.UTC)
			response, err := service.CreateSeries(context.Background(), &pb.CreateSeriesReq{
				Title: "Weekly meetup", Timezone: "UTC", EventType: "virtual",
				StartTime: timestamppb.New(start), EndTime: timestamppb.New(start.Add(time.Hour)),
				Recurrence: &pb.Recurrence{Freq: "weekly", Count: 3},
			})
			if db.calls != 1 {
				t.Fatalf("materialization calls = %d; want 1", db.calls)
			}
			if fail {
				if status.Code(err) != codes.Internal || response != nil {
					t.Fatalf("failed creation = %v, %v", response, err)
				}
			} else {
				if err != nil || response.Event != db.event {
					t.Fatalf("creation = %v, %v", response, err)
				}
				seen := map[string]bool{}
				for range 2 {
					notification := receiveCoHostNotification(t, publisher)
					if notification.Action != "event_cohost_added" || notification.Username != "owner" ||
						notification.EventSlug != "first-occurrence" || notification.EventTitle != "Weekly meetup" || seen[notification.NewUsername] {
						t.Fatalf("unexpected series notification: %+v", notification)
					}
					seen[notification.NewUsername] = true
				}
				if !seen["guest"] || !seen["other"] {
					t.Fatalf("notified co-hosts = %v", seen)
				}
			}
			select {
			case notification := <-publisher.messages:
				t.Fatalf("unexpected duplicate/self/failed-series notification: %+v", notification)
			case <-time.After(20 * time.Millisecond):
			}
		})
	}
}
