package services

import (
	"context"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/database"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type eventListDB struct {
	database.EventDB
	list func(context.Context, *pb.ListEventsReq) ([]*pb.Event, int32, error)
}

func (d *eventListDB) ListEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error) {
	return d.list(ctx, req)
}

func (d *eventListDB) GetUserEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error) {
	return d.list(ctx, req)
}

func (d *eventListDB) GetGroupEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error) {
	return d.list(ctx, req)
}

func (d *eventListDB) GetUserAttendingEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error) {
	return d.list(ctx, req)
}

func TestEventListsRedactOnlyAnonymousMeetingLinks(t *testing.T) {
	for _, method := range []struct {
		name string
		call func(*EventService, context.Context, *pb.ListEventsReq) (*pb.ListEventsResp, error)
	}{
		{"discovery", (*EventService).ListEvents},
		{"user", (*EventService).GetUserEvents},
		{"group", (*EventService).GetGroupEvents},
		{"attending", (*EventService).GetUserAttendingEvents},
	} {
		t.Run(method.name, func(t *testing.T) {
			for _, accountID := range []string{"", "authenticated-viewer"} {
				t.Run("viewer="+accountID, func(t *testing.T) {
					req := &pb.ListEventsReq{AccountId: accountID, Username: "organizer", GroupSlug: "community", Limit: 2, Offset: 4, Tags: []string{"go"}, Status: "published", EventType: "online", Query: "weekly", Location: "test city", DateFilter: "past", SortBy: "newest", UserLat: 12, UserLng: 77, Radius: 10}
					originalReq := proto.Clone(req)
					events := []*pb.Event{
						{Id: 1, Slug: "online-event", Title: "Online event", Status: "published", Visibility: "public", EventType: "online", MeetingLink: "https://meeting.example.test/attendees-only", OrganizerAccountId: "organizer", Capacity: 20},
						{Id: 2, Slug: "in-person-event", Title: "In-person event", Status: "published", EventType: "in_person", Location: "Test venue"},
					}
					want := proto.Clone(&pb.ListEventsResp{Events: events, Total: 17}).(*pb.ListEventsResp)
					if accountID == "" {
						want.Events[0].MeetingLink = ""
					}
					calls := 0
					db := &eventListDB{list: func(_ context.Context, got *pb.ListEventsReq) ([]*pb.Event, int32, error) {
						calls++
						if !proto.Equal(got, originalReq) {
							t.Fatal("list filters changed before database read")
						}
						return events, 17, nil
					}}
					got, err := method.call(&EventService{db: db}, context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					if !proto.Equal(got, want) {
						t.Fatal("list must redact anonymous meeting links and preserve all other fields and totals")
					}
					if calls != 1 || !proto.Equal(req, originalReq) {
						t.Fatal("list must preserve filters and use one database list read")
					}
				})
			}
			t.Run("database-error", func(t *testing.T) {
				wantErr := status.Error(codes.Unavailable, "list unavailable")
				db := &eventListDB{list: func(context.Context, *pb.ListEventsReq) ([]*pb.Event, int32, error) {
					return nil, 0, wantErr
				}}
				got, err := method.call(&EventService{db: db}, context.Background(), &pb.ListEventsReq{})
				if got != nil || err != wantErr {
					t.Fatal("database errors must propagate unchanged")
				}
			})
			t.Run("empty-list", func(t *testing.T) {
				db := &eventListDB{list: func(context.Context, *pb.ListEventsReq) ([]*pb.Event, int32, error) {
					return nil, 0, nil
				}}
				got, err := method.call(&EventService{db: db}, context.Background(), &pb.ListEventsReq{})
				if err != nil || got == nil || len(got.Events) != 0 || got.Total != 0 {
					t.Fatal("empty list response changed")
				}
			})
		})
	}
}

type eventDetailPrivacyDB struct {
	database.EventDB
	isHost     bool
	attendance string
}

func (d *eventDetailPrivacyDB) GetEvent(context.Context, string, string) (*pb.Event, string, error) {
	return &pb.Event{Id: 1, Slug: "online-event", Status: "published", MeetingLink: "https://meeting.example.test/attendees-only"}, d.attendance, nil
}

func (d *eventDetailPrivacyDB) ViewerIsHost(context.Context, int64, string, string) (bool, error) {
	return d.isHost, nil
}

func TestEventDetailMeetingLinkAccessUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name       string
		accountID  string
		isHost     bool
		attendance string
		wantLink   bool
	}{
		{name: "anonymous"},
		{name: "organizer", accountID: "organizer", isHost: true, wantLink: true},
		{name: "confirmed-attendee", accountID: "attendee", attendance: "confirmed", wantLink: true},
		{name: "waitlisted-attendee", accountID: "attendee", attendance: "waitlisted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &eventDetailPrivacyDB{isHost: tc.isHost, attendance: tc.attendance}
			got, err := (&EventService{db: db}).GetEvent(context.Background(), &pb.GetEventReq{Slug: "online-event", AccountId: tc.accountID})
			if err != nil {
				t.Fatal(err)
			}
			if (got.Event.MeetingLink != "") != tc.wantLink || got.ViewerRsvpStatus != tc.attendance {
				t.Fatal("detail meeting-link or RSVP access changed")
			}
		})
	}
}
