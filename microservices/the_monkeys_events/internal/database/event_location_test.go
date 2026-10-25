package database

import (
	"context"
	"database/sql/driver"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type locationRoundTripper func(*http.Request) (*http.Response, error)

func (f locationRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateEventDoesNotKeepAnotherAddressPin(t *testing.T) {
	const oldPlace = "Old venue Hubli"
	const newPlace = "HSR Layout Bengaluru"
	const oldLat, oldLng = 15.3915717, 75.0815271
	const newLat, newLng = 12.9101, 77.6402
	for _, tc := range []struct {
		name, place, eventType string
		pinLat, pinLng         float64
		wantLat, wantLng       driver.Value
		geocoded, lookupFails  bool
	}{
		{"changed address with stale client pin", newPlace, "in_person", oldLat, oldLng, newLat, newLng, true, false},
		{"unchanged address keeps manual pin", oldPlace, "in_person", oldLat, oldLng, oldLat, oldLng, false, false},
		{"cosmetic address edit keeps manual pin", "  OLD  VENUE HUBLI  ", "in_person", oldLat, oldLng, oldLat, oldLng, false, false},
		{"changed address with new manual pin", newPlace, "in_person", 12.92, 77.65, 12.92, 77.65, false, false},
		{"removed address discards stale pin", "", "in_person", oldLat, oldLng, nil, nil, false, false},
		{"online event drops physical pin", newPlace, "virtual", oldLat, oldLng, nil, nil, false, false},
		{"failed lookup never restores stale pin", newPlace, "in_person", oldLat, oldLng, nil, nil, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			originalTransport := http.DefaultTransport
			http.DefaultTransport = locationRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "nominatim.openstreetmap.org" || !strings.Contains(r.URL.Query().Get("q"), "HSR") {
					t.Errorf("unexpected geocode destination/address: %s %s", r.URL.Host, r.URL.Query().Get("q"))
				}
				code, body := http.StatusOK, `[{"lat":"12.9101","lon":"77.6402"}]`
				if tc.lookupFails {
					code, body = http.StatusNotFound, `[]`
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			sqldb, mock, err := sqlmock.New(sqlmock.ValueConverterOption(cloneValueConverter{}))
			if err != nil {
				t.Fatal(err)
			}
			defer sqldb.Close()
			db := &eventDB{db: sqldb, log: zap.NewNop().Sugar()}
			start := time.Date(2099, 11, 12, 13, 30, 0, 0, time.UTC)
			end := start.Add(90 * time.Minute)
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT id FROM user_account`).WithArgs("host").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			mock.ExpectQuery(`SELECT id, organizer_id FROM events`).WithArgs("moving-event").
				WillReturnRows(sqlmock.NewRows([]string{"id", "organizer_id"}).AddRow(11, 1))
			mock.ExpectQuery(`SELECT status, start_time, end_time, event_type`).WithArgs(int64(11)).
				WillReturnRows(sqlmock.NewRows([]string{"status", "start_time", "end_time", "event_type", "location", "meeting_link", "capacity", "visibility", "series_id", "rsvp_closes_at", "latitude", "longitude"}).
					AddRow("published", start, end, "in_person", oldPlace, "", 0, "public", nil, nil, oldLat, oldLng))
			mock.ExpectExec(`UPDATE events SET`).WithArgs(
				"Meetup", "", start, end, "UTC", tc.eventType, tc.place, "", int32(0), "", "", tc.wantLat, tc.wantLng, nil, nil, int64(11),
			).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`DELETE FROM event_tags`).WithArgs(int64(11)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()
			mock.ExpectQuery(`SELECT.*FROM events e JOIN user_account`).WithArgs("moving-event").
				WillReturnRows(sqlmock.NewRows([]string{
					"id", "title", "description", "slug", "start_time", "end_time", "timezone", "event_type", "location", "meeting_link", "capacity", "status", "cover_image", "account_id", "username", "created_at", "updated_at", "attendee_count", "group_id", "group_slug", "group_name", "visibility", "series_id", "occurrence_at", "rule", "rsvp_closes_at", "close_hours", "latitude", "longitude", "requires_host_review",
				}).AddRow(11, "Meetup", "", "moving-event", start, end, "UTC", tc.eventType, tc.place, "", 0, "published", "", "host", "host", start, start, 0, nil, "", "", "public", nil, nil, "", nil, 0, tc.wantLat, tc.wantLng, false))
			mock.ExpectQuery(`SELECT event_id, tag_name FROM event_tags`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "tag_name"}))
			mock.ExpectQuery(`FROM event_ticket_tiers t`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "id", "name", "description", "price", "currency", "capacity", "sort_order", "booked"}))
			mock.ExpectQuery(`FROM event_co_hosts c`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "username"}))
			mock.ExpectQuery(`SELECT event_id, reaction_type, COUNT`).WillReturnRows(sqlmock.NewRows([]string{"event_id", "reaction_type", "count"}))
			mock.ExpectQuery(`SELECT a.status FROM event_attendees`).WillReturnRows(sqlmock.NewRows([]string{"status"}))
			mock.ExpectQuery(`SELECT r.reaction_type FROM event_reactions`).WillReturnRows(sqlmock.NewRows([]string{"reaction_type"}))
			got, err := db.UpdateEvent(context.Background(), &pb.UpdateEventReq{
				Slug: "moving-event", AccountId: "host", Title: "Meetup", EventType: tc.eventType, Location: tc.place,
				StartTime: timestamppb.New(start), EndTime: timestamppb.New(end), Timezone: "UTC", Latitude: tc.pinLat, Longitude: tc.pinLng,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Location != tc.place {
				t.Fatalf("location = %q; want %q", got.Location, tc.place)
			}
			if tc.geocoded != (calls > 0) {
				t.Fatalf("geocode calls = %d; expected lookup %v", calls, tc.geocoded)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFillMissingCoordsOnlyPinsTheAddressItLookedUp(t *testing.T) {
	for _, tc := range []struct {
		name    string
		updated int64
	}{
		{"unchanged address accepts the lookup", 1},
		{"address changed while lookup ran", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalTransport := http.DefaultTransport
			http.DefaultTransport = locationRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "nominatim.openstreetmap.org" {
					t.Fatalf("unexpected lookup host: %s", r.URL.Host)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[{"lat":"15.3915717","lon":"75.0815271"}]`)), Header: make(http.Header)}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			sqldb, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqldb.Close()
			db := &eventDB{db: sqldb, log: zap.NewNop().Sugar()}
			event := &pb.Event{Slug: "moving-event", EventType: "in_person", Location: "Old venue Hubli"}
			mock.ExpectExec(regexp.QuoteMeta(`UPDATE events SET latitude = $1, longitude = $2, updated_at = NOW()
				WHERE slug = $3 AND (latitude IS NULL OR longitude IS NULL)
				AND COALESCE(location, '') = $4 AND event_type = $5`)).
				WithArgs(15.3915717, 75.0815271, event.Slug, event.Location, event.EventType).
				WillReturnResult(sqlmock.NewResult(0, tc.updated))
			db.fillMissingCoords(context.Background(), event)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
			if tc.updated == 0 && event.Venue != nil {
				t.Fatal("a stale lookup must not attach the old address's pin")
			}
			if tc.updated == 1 && (event.Venue == nil || event.Venue.Latitude != 15.3915717 || event.Venue.Longitude != 75.0815271) {
				t.Fatalf("unchanged address did not receive its pin: %v", event.Venue)
			}
		})
	}
}
