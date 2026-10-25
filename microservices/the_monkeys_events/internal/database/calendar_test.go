package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type calendarReader interface {
	GetCalendarEvent(context.Context, string, string) (*pb.Event, string, error)
	CalendarOccurrenceSlugs(context.Context, *pb.Event) ([]string, error)
}

func TestCalendarReadsWithoutHydrationOrGeocodingWrites(t *testing.T) {
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	reader, ok := any(&eventDB{db: sqldb}).(calendarReader)
	if !ok {
		t.Fatal("calendar export needs a read-only lookup, not GetEvent's geocoding write path")
	}
	start := time.Date(2026, 11, 12, 13, 30, 0, 0, time.UTC)
	mock.ExpectQuery(`FROM events e`).WithArgs("local", "viewer").WillReturnRows(sqlmock.NewRows([]string{
		"id", "title", "description", "slug", "start", "end", "timezone", "event_type", "location", "meeting_link", "organizer_account", "organizer", "status", "series_id", "occurrence", "updated", "visibility", "group_id", "viewer_status", "is_host", "is_member", "group_visibility",
	}).AddRow(7, "Local", "Description", "local", start, start.Add(time.Hour), "India", "in_person", "New venue", "private-link", "organizer", "host", "published", 8, start, start, "public", nil, "confirmed", false, false, "public"))
	event, attendance, err := reader.GetCalendarEvent(context.Background(), "local", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	if event.Slug != "local" || event.Location != "New venue" || event.Timezone != "India" || attendance != "confirmed" || !event.StartTime.AsTime().Equal(start) {
		t.Fatalf("event=%v, attendance=%q", event, attendance)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCalendarLookupEnforcesEachOccurrencesVisibility(t *testing.T) {
	for _, tc := range []struct {
		visibility, groupVisibility, status, attendance string
		host, member, allowed                           bool
	}{
		{"public", "public", "published", "", false, false, true},
		{"private", "public", "published", "", false, false, false},
		{"private", "public", "published", "confirmed", false, false, true},
		{"private", "public", "published", "", true, false, true},
		{"group_members", "public", "published", "", false, true, true},
		{"group_members", "public", "published", "", false, false, false},
		{"public", "private", "published", "", false, false, false},
		{"public", "private", "published", "", false, true, true},
		{"public", "public", "draft", "confirmed", false, true, false},
		{"unknown", "public", "published", "", false, false, false},
	} {
		t.Run(tc.visibility+"/"+tc.status+"/"+tc.attendance, func(t *testing.T) {
			sqldb, mock, _ := sqlmock.New()
			defer sqldb.Close()
			reader, ok := any(&eventDB{db: sqldb}).(calendarReader)
			if !ok {
				t.Fatal("calendar lookup missing")
			}
			start := time.Date(2026, 11, 12, 13, 30, 0, 0, time.UTC)
			columns := []string{"id", "title", "description", "slug", "start", "end", "timezone", "event_type", "location", "meeting_link", "organizer_account", "organizer", "status", "series_id", "occurrence", "updated", "visibility", "group_id", "viewer_status", "is_host", "is_member", "group_visibility"}
			mock.ExpectQuery(`FROM events e`).WithArgs("local", "viewer").WillReturnRows(sqlmock.NewRows(columns).AddRow(7, "Local", "", "local", start, start.Add(time.Hour), "UTC", "virtual", "", "secret", "organizer", "host", tc.status, nil, nil, start, tc.visibility, 2, tc.attendance, tc.host, tc.member, tc.groupVisibility))
			_, _, err := reader.GetCalendarEvent(context.Background(), "local", "viewer")
			if tc.allowed && err != nil {
				t.Fatal(err)
			}
			if !tc.allowed && status.Code(err) != codes.NotFound {
				t.Fatalf("expected concealed event, got %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCalendarSeriesUsesOriginalPositionNotEditedTime(t *testing.T) {
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	reader, ok := any(&eventDB{db: sqldb}).(calendarReader)
	if !ok {
		t.Fatal("series export needs persisted occurrence selection")
	}
	original := time.Date(2026, 10, 23, 17, 0, 0, 0, time.UTC)
	selected := &pb.Event{SeriesId: 8, StartTime: timestamppb.New(original.AddDate(0, 0, 10)), SeriesOccurrenceAt: timestamppb.New(original)}
	mock.ExpectQuery(`WHERE e.series_id = \$1 AND COALESCE\(e.series_occurrence_at, e.start_time\) >= \$2`).WithArgs(int64(8), original).WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("edited-date").AddRow("next-date"))
	slugs, err := reader.CalendarOccurrenceSlugs(context.Background(), selected)
	if err != nil || len(slugs) != 2 || slugs[0] != "edited-date" || slugs[1] != "next-date" {
		t.Fatalf("slugs=%v err=%v", slugs, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCalendarMissingEventDoesNotBecomeAnEmptyExport(t *testing.T) {
	sqldb, mock, _ := sqlmock.New()
	defer sqldb.Close()
	reader, ok := any(&eventDB{db: sqldb}).(calendarReader)
	if !ok {
		t.Fatal("calendar lookup missing")
	}
	mock.ExpectQuery(`FROM events e`).WithArgs("missing", "").WillReturnError(sql.ErrNoRows)
	_, _, err := reader.GetCalendarEvent(context.Background(), "missing", "")
	if status.Code(err) != codes.NotFound {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
