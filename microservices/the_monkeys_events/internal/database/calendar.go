package database

import (
	"context"
	"database/sql"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

// GetCalendarEvent is deliberately separate from GetEvent: exporting must not
// hydrate discovery data, geocode an address, or write missing coordinates.
func (db *eventDB) GetCalendarEvent(ctx context.Context, slug, accountID string) (*pb.Event, string, error) {
	var e pb.Event
	var start, end, updated time.Time
	var seriesID, groupID sql.NullInt64
	var occurrence sql.NullTime
	var attendance, groupVisibility string
	var host, member bool
	err := db.db.QueryRowContext(ctx, `
        WITH viewer AS (SELECT id FROM user_account WHERE account_id = $2)
        SELECT e.id,e.title,COALESCE(e.description,''),e.slug,e.start_time,e.end_time,
          COALESCE(e.timezone,'UTC'),e.event_type,COALESCE(e.location,''),COALESCE(e.meeting_link,''),
          u.account_id,u.username,e.status,e.series_id,e.series_occurrence_at,e.updated_at,
          COALESCE(e.visibility,'public'),e.group_id,
          COALESCE((SELECT a.status FROM event_attendees a WHERE a.event_id=e.id AND a.user_id=(SELECT id FROM viewer)),''),
          COALESCE(e.organizer_id=(SELECT id FROM viewer),false) OR EXISTS (
            SELECT 1 FROM event_co_hosts ch WHERE ch.event_id=e.id AND ch.co_host_id=(SELECT id FROM viewer)),
          EXISTS (SELECT 1 FROM group_members gm WHERE gm.group_id=e.group_id AND gm.user_id=(SELECT id FROM viewer) AND gm.status='active'),
          COALESCE(g.visibility,'public')
        FROM events e JOIN user_account u ON u.id=e.organizer_id
        LEFT JOIN groups g ON g.id=e.group_id WHERE e.slug=$1`, slug, accountID).Scan(
		&e.Id, &e.Title, &e.Description, &e.Slug, &start, &end, &e.Timezone, &e.EventType, &e.Location, &e.MeetingLink,
		&e.OrganizerAccountId, &e.OrganizerUsername, &e.Status, &seriesID, &occurrence, &updated, &e.Visibility, &groupID,
		&attendance, &host, &member, &groupVisibility)
	if err == sql.ErrNoRows {
		return nil, "", status.Error(codes.NotFound, "event not found")
	}
	if err != nil {
		return nil, "", status.Error(codes.Internal, "failed to read calendar event")
	}
	if !host {
		visible := e.Visibility == "public" || e.Visibility == "unlisted" || (e.Visibility == "group_members" && member)
		if attendance == "confirmed" {
			visible = true
		}
		if e.Status == StatusDraft || !visible || (groupVisibility != "public" && !member && attendance != "confirmed") {
			return nil, "", status.Error(codes.NotFound, "event not found")
		}
	}
	e.StartTime = timestamppb.New(start)
	e.EndTime = timestamppb.New(end)
	e.UpdatedAt = timestamppb.New(updated)
	if seriesID.Valid {
		e.SeriesId = seriesID.Int64
	}
	if groupID.Valid {
		e.GroupId = groupID.Int64
	}
	if occurrence.Valid {
		e.SeriesOccurrenceAt = timestamppb.New(occurrence.Time)
	}
	return &e, attendance, nil
}

// Select the materialized dates, not an inferred RRULE. Original positions
// retain rescheduled exceptions whose current start was moved earlier/later.
func (db *eventDB) CalendarOccurrenceSlugs(ctx context.Context, event *pb.Event) ([]string, error) {
	if event.GetSeriesId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "event is not part of a series")
	}
	start := event.GetSeriesOccurrenceAt()
	if start == nil {
		start = event.GetStartTime()
	}
	if start == nil || start.CheckValid() != nil {
		return nil, status.Error(codes.FailedPrecondition, "series date is unavailable")
	}
	rows, err := db.db.QueryContext(ctx, `
        SELECT e.slug FROM events e
        WHERE e.series_id = $1 AND COALESCE(e.series_occurrence_at, e.start_time) >= $2
          AND e.status IN ('published','live','completed')
        ORDER BY COALESCE(e.series_occurrence_at,e.start_time),e.id LIMIT 1001`, event.SeriesId, start.AsTime())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to read calendar dates")
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, status.Error(codes.Internal, "failed to read calendar date")
		}
		slugs = append(slugs, slug)
	}
	if err := rows.Err(); err != nil {
		return nil, status.Error(codes.Internal, "failed to read calendar dates")
	}
	if len(slugs) > 1000 {
		return nil, status.Error(codes.ResourceExhausted, "too many dates; add individual occurrences instead")
	}
	return slugs, nil
}
