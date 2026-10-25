package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newRecurringCoHostDB(t *testing.T) (*eventDB, sqlmock.Sqlmock) {
	t.Helper()
	connection, mock, err := sqlmock.New(sqlmock.ValueConverterOption(cloneValueConverter{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return &eventDB{db: connection, log: zap.NewNop().Sugar()}, mock
}

func recurringCoHostRequest(names ...string) *pb.CreateSeriesReq {
	start := time.Date(2099, 11, 12, 13, 30, 0, 0, time.UTC)
	return &pb.CreateSeriesReq{
		AccountId: "owner-account", Title: "Weekly meetup", Timezone: "UTC", EventType: "virtual",
		StartTime: timestamppb.New(start), EndTime: timestamppb.New(start.Add(time.Hour)),
		CoHostUsernames: names,
	}
}

func expectRecurringPermissions(mock sqlmock.Sqlmock, eventID, userID int64) {
	for _, permission := range []string{"edit_event", "manage_attendees", "manage_tickets", "manage_coupons", "manage_hosts", "manage_questions", "manage_checkins"} {
		mock.ExpectExec(`INSERT INTO event_permissions`).WithArgs(eventID, userID, permission).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func expectRecurringOccurrenceStart(mock sqlmock.Sqlmock, occurrence time.Time, eventID int64) {
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM events WHERE series_id`).WithArgs(int64(9), occurrence).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`INSERT INTO events`).WithArgs(
		"Weekly meetup", "", sqlmock.AnyArg(), occurrence, occurrence.Add(time.Hour), "UTC",
		"virtual", "", "", int32(0), "", int64(1), nil, "public", "published", int64(9), occurrence,
		nil, nil, nil, false,
	).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(eventID))
	expectRecurringPermissions(mock, eventID, 1)
}

func expectRecurringOccurrence(mock sqlmock.Sqlmock, occurrence time.Time, eventID int64, hostIDs []int64) {
	expectRecurringOccurrenceStart(mock, occurrence, eventID)
	for _, hostID := range hostIDs {
		mock.ExpectExec(`INSERT INTO event_co_hosts`).WithArgs(eventID, hostID, "co_host", int64(1)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		expectRecurringPermissions(mock, eventID, hostID)
		mock.ExpectExec(`INSERT INTO event_host_activity_log`).WithArgs(eventID, hostID, "added", int64(1)).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(`DELETE FROM event_tags`).WithArgs(eventID).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`INSERT INTO event_ticket_tiers`).WithArgs(eventID, "General", "", float64(0), "INR", int32(0), int32(0)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(eventID + 100))
}

func TestGenerateSeriesOccurrencesRollsBackAllDatesWhenCoHostGrantFails(t *testing.T) {
	db, mock := newRecurringCoHostDB(t)
	start := recurringCoHostRequest().StartTime.AsTime()
	second := start.Add(7 * 24 * time.Hour)
	expectRecurringSeriesLock(mock)
	expectRecurringOccurrence(mock, start, 11, []int64{2})
	expectRecurringOccurrenceStart(mock, second, 12)
	mock.ExpectExec(`INSERT INTO event_co_hosts`).WithArgs(int64(12), int64(2), "co_host", int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO event_permissions`).WithArgs(int64(12), int64(2), "edit_event").
		WillReturnError(errors.New("permission write failed"))
	mock.ExpectRollback()
	slugs, err := db.GenerateSeriesOccurrences(context.Background(), 9, []time.Time{start, second}, OccurrenceTemplate{
		EventType: "virtual", Duration: time.Hour, CoHostIDs: []int64{2},
	})
	if status.Code(err) != codes.Internal || slugs != nil {
		t.Fatalf("failed generation returned slugs = %v, error = %v", slugs, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectRecurringSeriesLock(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT organizer_id, group_id, title`).WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"organizer_id", "group_id", "title", "description", "timezone", "status", "cover_image", "hours"}).
			AddRow(1, nil, "Weekly meetup", "", "UTC", "active", "", 0))
}

func TestGenerateSeriesOccurrencesGrantsCoHostsOnlyOnNewDates(t *testing.T) {
	for _, withHosts := range []bool{true, false} {
		t.Run(map[bool]string{true: "selected hosts", false: "legacy caller without hosts"}[withHosts], func(t *testing.T) {
			db, mock := newRecurringCoHostDB(t)
			start := recurringCoHostRequest().StartTime.AsTime()
			occs := []time.Time{start, start.Add(7 * 24 * time.Hour), start.Add(14 * 24 * time.Hour)}
			var hosts, selected []int64
			if withHosts {
				hosts, selected = []int64{2, 3}, []int64{2, 2, 1, 3}
			}
			expectRecurringSeriesLock(mock)
			expectRecurringOccurrence(mock, occs[0], 11, hosts)
			mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM events WHERE series_id`).WithArgs(int64(9), occs[1]).
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			expectRecurringOccurrence(mock, occs[2], 12, hosts)
			mock.ExpectCommit()
			slugs, err := db.GenerateSeriesOccurrences(context.Background(), 9, occs, OccurrenceTemplate{
				EventType: "virtual", Duration: time.Hour, CoHostIDs: selected,
			})
			if err != nil || len(slugs) != 2 {
				t.Fatalf("generated dates = %v, error = %v", slugs, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMaterializeSeriesGrantsSelectedCoHostsOnEveryInitialOccurrence(t *testing.T) {
	db, mock := newRecurringCoHostDB(t)
	request := recurringCoHostRequest(" @guest ", "guest", "owner", " @other ", "other")
	start := request.StartTime.AsTime()
	second := start.Add(7 * 24 * time.Hour)
	for _, user := range []struct {
		name string
		id   int64
	}{{"guest", 2}, {"owner", 1}, {"other", 3}} {
		mock.ExpectQuery(`SELECT id FROM user_account WHERE username`).WithArgs(user.name).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(user.id))
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM user_account WHERE account_id`).WithArgs("owner-account").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`INSERT INTO event_series`).WithArgs(
		nil, int64(1), "Weekly meetup", "", "UTC", "FREQ=WEEKLY;COUNT=2", start, nil, "active", "", nil,
	).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectCommit()
	expectRecurringSeriesLock(mock)
	expectRecurringOccurrence(mock, start, 11, []int64{2, 3})
	expectRecurringOccurrence(mock, second, 12, []int64{2, 3})
	mock.ExpectCommit()
	mock.ExpectQuery(`SELECT.*FROM events e JOIN user_account`).WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "title", "description", "slug", "start_time", "end_time", "timezone", "event_type",
			"location", "meeting_link", "capacity", "status", "cover_image", "account_id", "username",
			"created_at", "updated_at", "attendee_count", "group_id", "group_slug", "group_name", "visibility",
			"series_id", "occurrence_at", "rule", "rsvp_closes_at", "close_hours", "latitude", "longitude", "requires_host_review",
		}).AddRow(11, "Weekly meetup", "", "first-occurrence", start, start.Add(time.Hour), "UTC", "virtual", "", "", 0,
			"published", "", "owner-account", "owner", start, start, 0, nil, "", "", "public", 9, start, "FREQ=WEEKLY;COUNT=2", nil, 0, nil, nil, false))
	mock.ExpectQuery(`SELECT event_id, tag_name FROM event_tags`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "tag_name"}))
	mock.ExpectQuery(`FROM event_ticket_tiers t`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "id", "name", "description", "price", "currency", "capacity", "sort_order", "booked"}))
	mock.ExpectQuery(`FROM event_co_hosts c`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "username"}).AddRow(11, "guest").AddRow(11, "other"))
	mock.ExpectQuery(`SELECT event_id, reaction_type, COUNT`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "reaction_type", "count"}))
	mock.ExpectQuery(`SELECT a.status FROM event_attendees`).WithArgs(int64(11), "owner-account").WillReturnRows(sqlmock.NewRows([]string{"status"}))
	mock.ExpectQuery(`SELECT r.reaction_type FROM event_reactions`).WithArgs(int64(11), "owner-account").WillReturnRows(sqlmock.NewRows([]string{"reaction_type"}))
	event, err := db.MaterializeSeries(context.Background(), request, []time.Time{start, second}, "FREQ=WEEKLY;COUNT=2")
	if err != nil {
		t.Fatal(err)
	}
	if event.Slug != "first-occurrence" || len(event.CoHostUsernames) != 2 ||
		event.CoHostUsernames[0] != "guest" || event.CoHostUsernames[1] != "other" {
		t.Fatalf("first occurrence co-hosts = %v", event)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMaterializeSeriesValidatesCoHostsBeforeAnyCreationWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hosts []string
		code  codes.Code
	}{
		{"missing user after valid user", []string{"guest", "missing"}, codes.NotFound},
		{"empty handle", []string{"  "}, codes.InvalidArgument},
		{"empty at handle", []string{" @ "}, codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newRecurringCoHostDB(t)
			if tc.code == codes.NotFound {
				mock.ExpectQuery(`SELECT id FROM user_account WHERE username`).WithArgs("guest").
					WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
				mock.ExpectQuery(`SELECT id FROM user_account WHERE username`).WithArgs("missing").WillReturnError(sql.ErrNoRows)
			}
			request := recurringCoHostRequest(tc.hosts...)
			_, err := db.MaterializeSeries(context.Background(), request, []time.Time{request.StartTime.AsTime()}, "FREQ=WEEKLY;COUNT=1")
			if status.Code(err) != tc.code {
				t.Fatalf("error = %v; want %s before any creation transaction", err, tc.code)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMaterializeSeriesNormalizesAndDeduplicatesCoHostsBeforeCreating(t *testing.T) {
	db, mock := newRecurringCoHostDB(t)
	mock.ExpectQuery(`SELECT id FROM user_account WHERE username`).WithArgs("guest").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
	mock.ExpectQuery(`SELECT id FROM user_account WHERE username`).WithArgs("other").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	// Stop at the first creation boundary; all selected users must already be valid.
	mock.ExpectBegin().WillReturnError(errors.New("creation boundary"))
	request := recurringCoHostRequest(" @guest ", "guest", " @other ", "other")
	_, err := db.MaterializeSeries(context.Background(), request, []time.Time{request.StartTime.AsTime()}, "FREQ=WEEKLY;COUNT=1")
	if status.Code(err) != codes.Internal {
		t.Fatalf("creation boundary error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
