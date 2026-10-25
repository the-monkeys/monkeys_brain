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

type timezoneDB struct {
	database.EventDB
	created *pb.CreateEventReq
	occs    []time.Time
	calls   int
}

func (d *timezoneDB) CreateEvent(_ context.Context, req *pb.CreateEventReq) (*pb.Event, error) {
	d.calls++
	d.created = req
	return &pb.Event{Status: "draft"}, nil
}
func (d *timezoneDB) GetEvent(context.Context, string, string) (*pb.Event, string, error) {
	d.calls++
	return &pb.Event{Status: "draft"}, "", nil
}
func (d *timezoneDB) UpdateEvent(context.Context, *pb.UpdateEventReq) (*pb.Event, error) {
	d.calls++
	return &pb.Event{Status: "draft"}, nil
}
func (d *timezoneDB) MaterializeSeries(_ context.Context, _ *pb.CreateSeriesReq, occs []time.Time, _ string) (*pb.Event, error) {
	d.calls++
	d.occs = occs
	return &pb.Event{Status: "draft"}, nil
}

func TestRejectInvalidTimezoneBeforeDatabaseWork(t *testing.T) {
	start := timestamppb.New(time.Date(2026, 11, 12, 13, 30, 0, 0, time.UTC))
	end := timestamppb.New(start.AsTime().Add(time.Hour))
	for _, zone := range []string{"India", "Local", "Not/AZone", "+05:30"} {
		for _, operation := range []string{"create", "update", "series"} {
			t.Run(zone+"/"+operation, func(t *testing.T) {
				db := &timezoneDB{}
				svc := &EventService{db: db}
				var err error
				switch operation {
				case "create":
					_, err = svc.CreateEvent(context.Background(), &pb.CreateEventReq{Timezone: zone, StartTime: start, EndTime: end})
				case "update":
					_, err = svc.UpdateEvent(context.Background(), &pb.UpdateEventReq{Timezone: zone, StartTime: start, EndTime: end})
				case "series":
					_, err = svc.CreateSeries(context.Background(), &pb.CreateSeriesReq{Timezone: zone, StartTime: start, EndTime: end, Recurrence: &pb.Recurrence{Freq: "weekly", Count: 2}})
				}
				if status.Code(err) != codes.InvalidArgument || db.calls != 0 {
					t.Fatalf("got error %v, database calls %d; want InvalidArgument before database work", err, db.calls)
				}
			})
		}
	}
}

func TestScheduleValidationPreservesInstantsAndDefaultsMissingZone(t *testing.T) {
	instant, _ := time.Parse(time.RFC3339, "2026-11-12T19:00:00+05:30")
	for _, zone := range []string{"Asia/Kolkata", "Europe/Berlin", ""} {
		db := &timezoneDB{}
		svc := &EventService{db: db}
		_, err := svc.CreateEvent(context.Background(), &pb.CreateEventReq{Timezone: zone, StartTime: timestamppb.New(instant), EndTime: timestamppb.New(instant.Add(time.Hour))})
		if err != nil {
			t.Fatal(err)
		}
		if got := db.created.StartTime.AsTime().Format(time.RFC3339); got != "2026-11-12T13:30:00Z" {
			t.Fatalf("instant shifted: %s", got)
		}
		wantZone := zone
		if wantZone == "" {
			wantZone = "UTC"
		}
		if db.created.Timezone != wantZone {
			t.Fatalf("zone = %q, want %q", db.created.Timezone, wantZone)
		}
	}
}

func TestSeriesKeepsEventWallTimeAcrossDST(t *testing.T) {
	for _, interval := range []int32{1, 2} {
		db := &timezoneDB{}
		svc := &EventService{db: db}
		start := time.Date(2026, 10, 18, 17, 0, 0, 0, time.UTC) // 19:00 Berlin before DST ends.
		_, err := svc.CreateSeries(context.Background(), &pb.CreateSeriesReq{Timezone: "Europe/Berlin", StartTime: timestamppb.New(start), EndTime: timestamppb.New(start.Add(time.Hour)), Recurrence: &pb.Recurrence{Freq: "weekly", Interval: interval, ByDay: []string{"SU"}, Count: 3}})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"2026-10-18T17:00:00Z", "2026-10-25T18:00:00Z", "2026-11-01T18:00:00Z"}
		if interval == 2 {
			want = []string{"2026-10-18T17:00:00Z", "2026-11-01T18:00:00Z", "2026-11-15T18:00:00Z"}
		}
		if len(db.occs) != len(want) {
			t.Fatalf("occurrences = %v", db.occs)
		}
		for i, occurrence := range db.occs {
			if occurrence.Location() != time.UTC {
				t.Fatalf("database boundary received non-UTC occurrence: %v", occurrence)
			}
			if got := occurrence.UTC().Format(time.RFC3339); got != want[i] {
				t.Fatalf("interval %d occurrence %d = %s, want %s", interval, i, got, want[i])
			}
		}
	}
}
