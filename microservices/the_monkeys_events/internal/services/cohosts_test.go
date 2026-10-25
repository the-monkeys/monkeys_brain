package services

import (
	"context"
	"encoding/json"
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

type coHostDB struct {
	database.EventDB
	event   *pb.Event
	actor   string
	failure error
	target  string
}

func (d *coHostDB) GetCalendarEvent(context.Context, string, string) (*pb.Event, string, error) {
	return d.event, "", nil
}
func (d *coHostDB) UsernameByAccountID(context.Context, string) (string, error) { return d.actor, nil }
func (d *coHostDB) AddCoHost(_ context.Context, r *pb.CoHostReq) error {
	d.target = r.CohostUsername
	return d.failure
}
func (d *coHostDB) RemoveCoHost(_ context.Context, r *pb.CoHostReq) error {
	d.target = r.CohostUsername
	return d.failure
}
func (d *coHostDB) CreateEvent(context.Context, *pb.CreateEventReq) (*pb.Event, error) {
	return d.event, d.failure
}

type notificationCapture struct{ messages chan eventNotification }

func (p *notificationCapture) PublishMessage(_, _ string, b []byte) error {
	var n eventNotification
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	p.messages <- n
	return nil
}
func (p *notificationCapture) PublishReliable(_, _ string, _ []byte, _ int) error { return nil }
func coHostTestService(d *coHostDB) (*EventService, *notificationCapture) {
	p := &notificationCapture{messages: make(chan eventNotification, 10)}
	cfg := &config.Config{}
	cfg.RabbitMQ.RoutingKeys = []string{"storage", "one", "two", "three", "notifications"}
	return &EventService{db: d, log: zap.NewNop().Sugar(), cfg: cfg, qConn: p}, p
}
func receiveCoHostNotification(t *testing.T, p *notificationCapture) eventNotification {
	t.Helper()
	select {
	case n := <-p.messages:
		return n
	case <-time.After(time.Second):
		t.Fatal("no co-host in-app notification published after successful change")
		return eventNotification{}
	}
}

func TestCoHostAddNotifiesCanonicalRecipient(t *testing.T) {
	d := &coHostDB{event: &pb.Event{Slug: "local-event", Title: "Local event", OrganizerUsername: "owner"}, actor: "owner"}
	s, p := coHostTestService(d)
	_, err := s.AddCoHost(context.Background(), &pb.CoHostReq{EventSlug: "local-event", AccountId: "owner-account", CohostUsername: " @guest "})
	if err != nil {
		t.Fatal(err)
	}
	n := receiveCoHostNotification(t, p)
	if n.Action != "event_cohost_added" || n.NewUsername != "guest" || n.Username != "owner" || n.EventSlug != "local-event" || n.EventTitle != "Local event" {
		t.Fatalf("wrong recipient/action/identity: %+v", n)
	}
	if d.target != "guest" {
		t.Fatalf("lookup target=%q", d.target)
	}
}

func TestCoHostRemoveAndLeaveHaveDifferentRecipients(t *testing.T) {
	for _, tc := range []struct{ actor, action, recipient, message string }{
		{"owner", "event_cohost_removed", "guest", "co-host removed"},
		{"guest", "event_cohost_left", "owner", "left as co-host"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			d := &coHostDB{event: &pb.Event{Slug: "local-event", Title: "Local event", OrganizerUsername: "owner"}, actor: tc.actor}
			s, p := coHostTestService(d)
			res, err := s.RemoveCoHost(context.Background(), &pb.CoHostReq{EventSlug: "local-event", AccountId: "actor", CohostUsername: "guest"})
			if err != nil {
				t.Fatal(err)
			}
			n := receiveCoHostNotification(t, p)
			if n.Action != tc.action || n.NewUsername != tc.recipient || n.Username != tc.actor {
				t.Fatalf("wrong removal notification: %+v", n)
			}
			if res.Message != tc.message {
				t.Fatalf("message=%q", res.Message)
			}
		})
	}
}

func TestCreateEventNotifiesEachDistinctCoHost(t *testing.T) {
	d := &coHostDB{event: &pb.Event{Slug: "local-event", Title: "Local event", OrganizerUsername: "owner", CoHostUsernames: []string{"guest", "other", "guest", "owner"}}}
	s, p := coHostTestService(d)
	start := time.Date(2027, 1, 12, 12, 0, 0, 0, time.UTC)
	_, err := s.CreateEvent(context.Background(), &pb.CreateEventReq{Title: "Local event", Timezone: "UTC", StartTime: timestamppb.New(start), EndTime: timestamppb.New(start.Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for range 2 {
		n := receiveCoHostNotification(t, p)
		if n.Action != "event_cohost_added" || n.Username != "owner" {
			t.Fatalf("bad create notification: %+v", n)
		}
		seen[n.NewUsername] = true
	}
	if !seen["guest"] || !seen["other"] {
		t.Fatalf("recipients=%v", seen)
	}
	select {
	case n := <-p.messages:
		t.Fatalf("duplicate/self notification: %+v", n)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestFailedCoHostChangeNeverNotifies(t *testing.T) {
	d := &coHostDB{event: &pb.Event{Slug: "local-event", OrganizerUsername: "owner"}, actor: "owner", failure: status.Error(codes.AlreadyExists, "already a co-host")}
	s, p := coHostTestService(d)
	if _, err := s.AddCoHost(context.Background(), &pb.CoHostReq{EventSlug: "local-event", AccountId: "owner", CohostUsername: "guest"}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("err=%v", err)
	}
	select {
	case n := <-p.messages:
		t.Fatalf("notification after failed change: %+v", n)
	case <-time.After(20 * time.Millisecond):
	}
}
