package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/database"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type calendarExportDB struct {
	database.EventDB
	events        map[string]*pb.Event
	slugs         []string
	attendance    map[string]string
	failures      map[string]error
	hydratedReads int
}

func (d *calendarExportDB) GetEvent(_ context.Context, slug, _ string) (*pb.Event, string, error) {
	d.hydratedReads++
	return proto.Clone(d.events[slug]).(*pb.Event), d.attendance[slug], nil
}
func (d *calendarExportDB) GetCalendarEvent(_ context.Context, slug, _ string) (*pb.Event, string, error) {
	if err := d.failures[slug]; err != nil {
		return nil, "", err
	}
	if d.events[slug] == nil {
		return nil, "", status.Error(codes.NotFound, "event not found")
	}
	return proto.Clone(d.events[slug]).(*pb.Event), d.attendance[slug], nil
}
func (d *calendarExportDB) CalendarOccurrenceSlugs(context.Context, *pb.Event) ([]string, error) {
	return d.slugs, nil
}
func (d *calendarExportDB) ViewerIsHost(context.Context, int64, string, string) (bool, error) {
	return false, nil
}
func calendarFixture(id int64, slug, start string) *pb.Event {
	t, _ := time.Parse(time.RFC3339, start)
	return &pb.Event{Id: id, Slug: slug, Title: "Weekly", Status: "published", Visibility: "public", SeriesId: 8,
		StartTime: timestamppb.New(t), EndTime: timestamppb.New(t.Add(time.Hour)), Timezone: "Europe/Berlin", MeetingLink: "https://meeting.example.test/" + slug}
}
func TestCalendarDefaultDoesNotHydrateOrShiftLegacyInstant(t *testing.T) {
	source := calendarFixture(7, "source", "2026-11-12T19:00:00+05:30")
	source.Timezone = "India"
	db := &calendarExportDB{events: map[string]*pb.Event{"source": source}, attendance: map[string]string{"source": "confirmed"}}
	res, err := (&EventService{db: db}).GetCalendarFile(context.Background(), &pb.CalendarReq{EventSlug: "source", AccountId: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	if db.hydratedReads != 0 {
		t.Fatal("calendar read used the geocoding/hydration path")
	}
	if res.IncludesSeries || strings.Count(string(res.IcsData), "BEGIN:VEVENT") != 1 || !strings.Contains(string(res.IcsData), "UID:the-monkeys-event-7@themonkeys.live") || !strings.Contains(string(res.IcsData), "DTSTART:20261112T133000Z") {
		t.Fatalf("legacy export changed: %s", res.IcsData)
	}
}
func TestCalendarSeriesExportsActualDSTInstantsAndRedactsEachOccurrence(t *testing.T) {
	first := calendarFixture(7, "source", "2026-10-23T19:00:00+02:00")
	next := calendarFixture(8, "next", "2026-10-30T20:30:00+01:00")
	next.Location = "Updated venue"
	cancelled := calendarFixture(9, "cancelled", "2026-11-06T19:00:00+01:00")
	cancelled.Status = "cancelled"
	unlisted := calendarFixture(10, "hidden-link", "2026-11-13T19:00:00+01:00")
	unlisted.Visibility = "unlisted"
	db := &calendarExportDB{events: map[string]*pb.Event{"source": first, "next": next, "cancelled": cancelled, "hidden-link": unlisted}, slugs: []string{"source", "next", "cancelled", "private", "hidden-link"}, attendance: map[string]string{"source": "confirmed"}, failures: map[string]error{"private": status.Error(codes.NotFound, "private")}}
	res, err := (&EventService{db: db}).GetCalendarFile(context.Background(), &pb.CalendarReq{EventSlug: "source", AccountId: "viewer", IncludeSeries: true})
	if err != nil {
		t.Fatal(err)
	}
	ics := strings.ReplaceAll(string(res.IcsData), "\r\n ", "")
	if !res.IncludesSeries || strings.Count(ics, "BEGIN:VEVENT") != 2 {
		t.Fatalf("series export = %s", ics)
	}
	for _, expected := range []string{"DTSTART:20261023T170000Z", "DTSTART:20261030T193000Z", "LOCATION:Updated venue", "https://meeting.example.test/source"} {
		if !strings.Contains(ics, expected) {
			t.Errorf("missing %q", expected)
		}
	}
	if strings.Contains(ics, "meeting.example.test/next") || strings.Contains(ics, "event-9@") || strings.Contains(ics, "event-10@") || strings.Contains(ics, "RRULE:") {
		t.Fatal("export leaked an unauthorized link, hidden date or invented repeat rule")
	}
	if db.hydratedReads != 0 {
		t.Fatal("calendar export hydrated an event")
	}
}
func TestCalendarRejectsBrokenDatesBeforeProducingAFile(t *testing.T) {
	event := calendarFixture(7, "source", "2026-11-12T13:30:00Z")
	event.EndTime = nil
	db := &calendarExportDB{events: map[string]*pb.Event{"source": event}}
	res, err := (&EventService{db: db}).GetCalendarFile(context.Background(), &pb.CalendarReq{EventSlug: "source"})
	if status.Code(err) != codes.FailedPrecondition || res != nil {
		t.Fatalf("res=%v err=%v", res, err)
	}
}
func TestCalendarSeriesDoesNotHideInfrastructureFailures(t *testing.T) {
	db := &calendarExportDB{events: map[string]*pb.Event{"source": calendarFixture(7, "source", "2026-11-12T13:30:00Z")}, slugs: []string{"source", "broken"}, failures: map[string]error{"broken": status.Error(codes.Internal, "read failed")}}
	res, err := (&EventService{db: db}).GetCalendarFile(context.Background(), &pb.CalendarReq{EventSlug: "source", IncludeSeries: true})
	if status.Code(err) != codes.Internal || res != nil {
		t.Fatalf("res=%v err=%v", res, err)
	}
}
