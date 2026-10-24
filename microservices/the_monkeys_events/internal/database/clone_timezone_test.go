package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCloneTimezoneLookupReadsOnlySourceTimezone(t *testing.T) {
	for _, tc := range []struct {
		name, stored, want string
		missing            bool
	}{
		{name: "valid source", stored: "Europe/Berlin", want: "Europe/Berlin"},
		{name: "empty legacy source", stored: "", want: ""},
		{name: "invalid label stays invalid", stored: "India", want: "India"},
		{name: "missing source", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqldb, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqldb.Close()
			db := &eventDB{db: sqldb}
			lookup, ok := any(db).(interface {
				GetEventTimezone(context.Context, string) (string, error)
			})
			if !ok {
				t.Fatal("clone inheritance needs a timezone-only lookup without event hydration or source writes")
			}
			expect := mock.ExpectQuery(`SELECT COALESCE\(timezone, 'UTC'\) FROM events WHERE slug = \$1`).WithArgs("source")
			if tc.missing {
				expect.WillReturnError(sql.ErrNoRows)
			} else {
				expect.WillReturnRows(sqlmock.NewRows([]string{"timezone"}).AddRow(tc.stored))
			}
			got, err := lookup.GetEventTimezone(context.Background(), "source")
			if tc.missing {
				if status.Code(err) != codes.NotFound {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("timezone = %q, error = %v; want %q", got, err, tc.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// sqlmock's default converter does not support the []int64 arrays accepted
// by the production pgx driver during detail hydration.
type cloneValueConverter struct{}

func (cloneValueConverter) ConvertValue(value any) (driver.Value, error) {
	if ids, ok := value.([]int64); ok {
		return pq.Array(ids).Value()
	}
	return driver.DefaultParameterConverter.ConvertValue(value)
}

func TestClonePersistsTimezoneOnlyOnNewDraft(t *testing.T) {
	for _, tc := range []struct {
		name, source, choice, want string
	}{
		{"override legacy label", "India", "Asia/Kolkata", "Asia/Kolkata"},
		{"inherit valid source", "Europe/Berlin", "", "Europe/Berlin"},
		{"default empty legacy source", "", "", "UTC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqldb, mock, err := sqlmock.New(sqlmock.ValueConverterOption(cloneValueConverter{}))
			if err != nil {
				t.Fatal(err)
			}
			defer sqldb.Close()
			db := &eventDB{db: sqldb, log: zap.NewNop().Sugar()}
			start := time.Date(2026, 11, 12, 13, 30, 12, 0, time.UTC)
			end := start.Add(time.Hour)
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT id FROM user_account`).WithArgs("host").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))
			mock.ExpectQuery(`SELECT id, organizer_id FROM events`).WithArgs("source").
				WillReturnRows(sqlmock.NewRows([]string{"id", "organizer_id"}).AddRow(7, 11))
			mock.ExpectQuery(`SELECT title, COALESCE\(description, ''\), COALESCE\(timezone, 'UTC'\)`).WithArgs(int64(7)).
				WillReturnRows(sqlmock.NewRows([]string{
					"title", "description", "timezone", "event_type", "location", "meeting_link", "capacity",
					"cover_image", "visibility", "group_id", "latitude", "longitude", "requires_host_review",
				}).AddRow("Meeting", "Description", tc.source, "virtual", "", "", 10, "", "public", nil, nil, nil, false))
			mock.ExpectQuery(`INSERT INTO events`).WithArgs(
				"Meeting", "Description", sqlmock.AnyArg(), start, end, tc.want,
				"virtual", "", "", int32(10), "", int64(11), nil, "public", nil, nil, false,
			).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
			for _, permission := range coHostPermissions {
				mock.ExpectExec(`INSERT INTO event_permissions`).WithArgs(int64(8), int64(11), permission).
					WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectQuery(`SELECT tag_name FROM event_tags`).WithArgs(int64(7)).
				WillReturnRows(sqlmock.NewRows([]string{"tag_name"}))
			mock.ExpectExec(`DELETE FROM event_tags`).WithArgs(int64(8)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT name, COALESCE\(description, ''\), price`).WithArgs(int64(7), "INR").
				WillReturnRows(sqlmock.NewRows([]string{"name", "description", "price", "currency", "capacity", "sort_order"}).
					AddRow("General", "", 0, "INR", 0, 0))
			mock.ExpectQuery(`INSERT INTO event_ticket_tiers`).WithArgs(int64(8), "General", "", float64(0), "INR", int32(0), int32(0)).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
			mock.ExpectCommit()
			mock.ExpectQuery(`SELECT.*FROM events e JOIN user_account`).WithArgs(sqlmock.AnyArg()).
				WillReturnRows(sqlmock.NewRows([]string{
					"id", "title", "description", "slug", "start_time", "end_time", "timezone", "event_type",
					"location", "meeting_link", "capacity", "status", "cover_image", "account_id", "username",
					"created_at", "updated_at", "attendee_count", "group_id", "group_slug", "group_name", "visibility",
					"series_id", "occurrence_at", "rule", "rsvp_closes_at", "close_hours", "latitude", "longitude", "requires_host_review",
				}).AddRow(8, "Meeting", "Description", "new-draft", start, end, tc.want, "virtual", "", "", 10,
					"draft", "", "host", "organizer", start, start, 0, nil, "", "", "public", nil, nil, "", nil, 0, nil, nil, false))
			mock.ExpectQuery(`SELECT event_id, tag_name FROM event_tags`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "tag_name"}))
			mock.ExpectQuery(`FROM event_ticket_tiers t`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "id", "name", "description", "price", "currency", "capacity", "sort_order", "booked"}))
			mock.ExpectQuery(`FROM event_co_hosts c`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "username"}))
			mock.ExpectQuery(`SELECT event_id, reaction_type, COUNT`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "reaction_type", "count"}))
			mock.ExpectQuery(`SELECT a.status FROM event_attendees`).WithArgs(int64(8), "host").WillReturnRows(sqlmock.NewRows([]string{"status"}))
			mock.ExpectQuery(`SELECT r.reaction_type FROM event_reactions`).WithArgs(int64(8), "host").WillReturnRows(sqlmock.NewRows([]string{"reaction_type"}))

			// The strict SQL expectations permit writes only to the new event and
			// its tags/permissions/tiers; an UPDATE of the source cannot pass.
			got, err := db.CloneEvent(context.Background(), &pb.CloneEventReq{
				Slug: "source", AccountId: "host", Timezone: tc.choice,
				StartTime: timestamppb.New(start), EndTime: timestamppb.New(end),
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Id != 8 || got.Status != "draft" || got.Timezone != tc.want || !got.StartTime.AsTime().Equal(start) || !got.EndTime.AsTime().Equal(end) {
				t.Fatalf("new draft = %v", got)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
