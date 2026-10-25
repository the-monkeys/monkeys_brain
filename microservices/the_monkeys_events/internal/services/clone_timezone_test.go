package services

import (
	"context"
	"testing"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/database"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type cloneTimezoneDB struct {
	database.EventDB
	source      *pb.Event
	getErr      error
	cloned      *pb.CloneEventReq
	reads       int
	detailReads int
	cloneCalls  int
}

func (d *cloneTimezoneDB) GetEvent(_ context.Context, slug, accountID string) (*pb.Event, string, error) {
	d.reads++
	d.detailReads++
	return d.source, "", d.getErr
}

func (d *cloneTimezoneDB) GetEventTimezone(_ context.Context, slug string) (string, error) {
	d.reads++
	return d.source.GetTimezone(), d.getErr
}

func (d *cloneTimezoneDB) CloneEvent(_ context.Context, req *pb.CloneEventReq) (*pb.Event, error) {
	d.cloneCalls++
	d.cloned = req
	return &pb.Event{Slug: "new-draft", Status: "draft", Timezone: req.Timezone, StartTime: req.StartTime, EndTime: req.EndTime}, nil
}

func TestCloneTimezoneSelection(t *testing.T) {
	start, err := time.Parse(time.RFC3339Nano, "2026-11-12T19:00:12.123+05:30")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source, choice, want string
		reads                      int
	}{
		{"override invalid legacy zone", "India", " Asia/Kolkata ", "Asia/Kolkata", 0},
		{"override valid source", "Europe/Berlin", "Asia/Kolkata", "Asia/Kolkata", 0},
		{"inherit source", "Europe/Berlin", "", "Europe/Berlin", 1},
		{"default empty source", "", "", "UTC", 1},
		{"normalize inherited source", " Europe/Berlin ", "", "Europe/Berlin", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &pb.Event{Slug: "source", Timezone: tc.source}
			db := &cloneTimezoneDB{source: source}
			svc := &EventService{db: db}
			resp, err := svc.CloneEvent(context.Background(), &pb.CloneEventReq{
				Slug: "source", AccountId: "host", Timezone: tc.choice,
				StartTime: timestamppb.New(start), EndTime: timestamppb.New(start.Add(time.Hour)),
			})
			if err != nil {
				t.Fatal(err)
			}
			if db.cloned == nil || db.cloneCalls != 1 || db.reads != tc.reads {
				t.Fatalf("reads = %d, clone calls = %d, request = %v", db.reads, db.cloneCalls, db.cloned)
			}
			if db.detailReads != 0 {
				t.Errorf("clone inheritance fetched a hydrated source %d times; hydration can update source coordinates", db.detailReads)
			}
			if resp.Event.Timezone != tc.want || db.cloned.Timezone != tc.want {
				t.Errorf("new draft timezone = %q, persisted choice = %q; want %q", resp.Event.Timezone, db.cloned.Timezone, tc.want)
			}
			if got := db.cloned.StartTime.AsTime().Format(time.RFC3339Nano); got != "2026-11-12T13:30:12.123Z" {
				t.Errorf("start instant changed: %s", got)
			}
			if got := db.cloned.EndTime.AsTime().Format(time.RFC3339Nano); got != "2026-11-12T14:30:12.123Z" {
				t.Errorf("end instant changed: %s", got)
			}
			if source.Timezone != tc.source {
				t.Errorf("source timezone changed to %q", source.Timezone)
			}
		})
	}
}

func TestCloneRejectsInvalidTimezoneAndScheduleBeforeClone(t *testing.T) {
	start := timestamppb.New(time.Date(2026, 11, 12, 13, 30, 0, 0, time.UTC))
	end := timestamppb.New(start.AsTime().Add(time.Hour))
	for _, tc := range []struct {
		name, source, choice string
		start, end           *timestamppb.Timestamp
	}{
		{"invalid override", "Europe/Berlin", "India", start, end},
		{"invalid inherited label", "India", "", start, end},
		{"missing start", "UTC", "UTC", nil, end},
		{"invalid timestamp", "UTC", "UTC", &timestamppb.Timestamp{Nanos: -1}, end},
		{"inverted range", "UTC", "UTC", end, start},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &cloneTimezoneDB{source: &pb.Event{Timezone: tc.source}}
			svc := &EventService{db: db}
			_, err := svc.CloneEvent(context.Background(), &pb.CloneEventReq{Slug: "source", Timezone: tc.choice, StartTime: tc.start, EndTime: tc.end})
			if status.Code(err) != codes.InvalidArgument || db.cloneCalls != 0 {
				t.Fatalf("error = %v, clone calls = %d; want InvalidArgument without a clone", err, db.cloneCalls)
			}
		})
	}
}

func TestClonePropagatesSourceLookupError(t *testing.T) {
	db := &cloneTimezoneDB{getErr: status.Error(codes.NotFound, "source not found")}
	svc := &EventService{db: db}
	_, err := svc.CloneEvent(context.Background(), &pb.CloneEventReq{Slug: "missing"})
	if status.Code(err) != codes.NotFound || db.cloneCalls != 0 {
		t.Fatalf("error = %v, clone calls = %d", err, db.cloneCalls)
	}
}
