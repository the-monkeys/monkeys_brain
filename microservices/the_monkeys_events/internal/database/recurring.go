package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Series lifecycle statuses (mirror event_series.status CHECK).
const (
	seriesActive    = "active"
	seriesPaused    = "paused"
	seriesCompleted = "completed"
	seriesCancelled = "cancelled"
)

// SeriesInput is the DB-layer contract for creating a recurring series. RRULE
// expansion into concrete datetimes is a service-layer concern; GroupID is 0
// for a standalone series and is resolved from a slug by the caller.
type SeriesInput struct {
	OrganizerAccountID   string
	GroupID              int64
	Title                string
	Description          string
	Timezone             string
	RecurrenceRule       string
	RecurrenceStartsAt   time.Time
	RecurrenceEndsAt     *time.Time
	CoverImage           string
	RsvpCloseHoursBefore int32
}

// OccurrenceTemplate carries the event-shaped fields stamped onto every
// generated occurrence. Duration is applied to each occurrence start to derive
// its end_time.
type OccurrenceTemplate struct {
	EventType   string
	Location    string
	MeetingLink string
	Capacity    int32
	CoverImage  string
	Visibility  string
	Duration    time.Duration
	Tags        []string
	Tiers       []*pb.TicketTierInput
	Latitude    float64
	Longitude   float64
}

// CreateSeries records a recurring-event definition and returns its id. The
// concrete occurrences are materialised separately via GenerateSeriesOccurrences
// so the caller controls the generation horizon.
func (db *eventDB) CreateSeries(ctx context.Context, in SeriesInput) (int64, error) {
	if strings.TrimSpace(in.Title) == "" {
		return 0, status.Error(codes.InvalidArgument, "series title is required")
	}
	if strings.TrimSpace(in.RecurrenceRule) == "" {
		return 0, status.Error(codes.InvalidArgument, "recurrence_rule is required")
	}
	if in.RecurrenceStartsAt.IsZero() {
		return 0, status.Error(codes.InvalidArgument, "recurrence_starts_at is required")
	}

	var seriesID int64
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		organizerID, err := resolveAccount(ctx, tx, in.OrganizerAccountID)
		if err != nil {
			return err
		}

		var groupID any
		if in.GroupID > 0 {
			groupID = in.GroupID
		}
		var endsAt any
		if in.RecurrenceEndsAt != nil {
			endsAt = *in.RecurrenceEndsAt
		}

		if err := tx.QueryRowContext(ctx, `
			INSERT INTO event_series (
				group_id, organizer_id, title, description, timezone,
				recurrence_rule, recurrence_starts_at, recurrence_ends_at, status,
				cover_image, rsvp_close_hours_before
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			RETURNING id`,
			groupID, organizerID, strings.TrimSpace(in.Title), in.Description,
			defaultTimezone(in.Timezone), in.RecurrenceRule, in.RecurrenceStartsAt,
			endsAt, seriesActive, strings.TrimSpace(in.CoverImage),
			nullHours(in.RsvpCloseHoursBefore),
		).Scan(&seriesID); err != nil {
			return status.Errorf(codes.Internal, "failed to create series: %v", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return seriesID, nil
}

// GenerateSeriesOccurrences materialises one published event row per supplied
// occurrence time. It is idempotent: an occurrence that already exists for the
// series (matched on series_occurrence_at) is skipped, so the generator can be
// re-run to extend the horizon without duplicating rows. Returns the slugs of
// the newly created events.
func (db *eventDB) GenerateSeriesOccurrences(ctx context.Context, seriesID int64, occurrences []time.Time, tmpl OccurrenceTemplate) ([]string, error) {
	if len(occurrences) == 0 {
		return nil, nil
	}
	if tmpl.Duration <= 0 {
		tmpl.Duration = time.Hour
	}
	visibility := tmpl.Visibility
	if visibility == "" {
		visibility = "public"
	}

	var created []string
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		// Lock the series row so two concurrent generators cannot both decide an
		// occurrence is missing and insert it twice.
		var organizerID int64
		var groupID sql.NullInt64
		var title, description, timezone, seriesStatus, seriesCover string
		var rsvpCloseHours int32
		if err := tx.QueryRowContext(ctx, `
			SELECT organizer_id, group_id, title, COALESCE(description, ''), timezone, status,
			       COALESCE(cover_image, ''), COALESCE(rsvp_close_hours_before, 0)
			FROM event_series WHERE id = $1 FOR UPDATE`, seriesID,
		).Scan(&organizerID, &groupID, &title, &description, &timezone, &seriesStatus, &seriesCover, &rsvpCloseHours); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "series not found")
			}
			return status.Error(codes.Internal, "failed to load series")
		}
		if seriesStatus == seriesCancelled || seriesStatus == seriesCompleted {
			return status.Errorf(codes.FailedPrecondition, "series is %s", seriesStatus)
		}
		if strings.TrimSpace(tmpl.CoverImage) == "" {
			tmpl.CoverImage = seriesCover
		}

		var groupCol any
		if groupID.Valid {
			groupCol = groupID.Int64
		}

		for _, occ := range occurrences {
			var exists bool
			if err := tx.QueryRowContext(ctx,
				"SELECT EXISTS (SELECT 1 FROM events WHERE series_id = $1 AND series_occurrence_at = $2)",
				seriesID, occ).Scan(&exists); err != nil {
				return status.Error(codes.Internal, "failed to check occurrence")
			}
			if exists {
				continue
			}

			slug := slugify(title)
			var eventID int64
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO events (
					title, description, slug, start_time, end_time, timezone,
					event_type, location, meeting_link, capacity, cover_image,
					organizer_id, group_id, visibility, status,
					series_id, series_occurrence_at, latitude, longitude, rsvp_closes_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
				RETURNING id`,
				title, description, slug, occ, occ.Add(tmpl.Duration), defaultTimezone(timezone),
				tmpl.EventType, tmpl.Location, tmpl.MeetingLink, tmpl.Capacity, tmpl.CoverImage,
				organizerID, groupCol, visibility, StatusPublished, seriesID, occ,
				nullCoord(tmpl.Latitude), nullCoord(tmpl.Longitude),
				rsvpClosesValue(occ, rsvpCloseHours),
			).Scan(&eventID); err != nil {
				return status.Errorf(codes.Internal, "failed to create occurrence: %v", err)
			}

			if err := grantPermissions(ctx, tx, eventID, organizerID); err != nil {
				return err
			}
			if err := replaceTags(ctx, tx, eventID, tmpl.Tags); err != nil {
				return err
			}
			if len(tmpl.Tiers) == 0 {
				if _, err := insertTier(ctx, tx, eventID,
					&pb.TicketTierInput{Name: "General", Currency: defaultCurrency}, 0); err != nil {
					return err
				}
			} else {
				for i, tier := range tmpl.Tiers {
					if _, err := insertTier(ctx, tx, eventID, tier, int32(i)); err != nil {
						return err
					}
				}
			}
			created = append(created, slug)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// CancelSeriesOccurrence cancels a single generated occurrence without touching
// the series or its siblings. Outstanding RSVPs on that occurrence are released
// exactly as a standalone cancel would.
func (db *eventDB) CancelSeriesOccurrence(ctx context.Context, slug, accountID string) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, _, err := authorize(ctx, tx, slug, accountID, permEditEvent)
		if err != nil {
			return err
		}

		var seriesID sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			"SELECT series_id FROM events WHERE id = $1", eventID).Scan(&seriesID); err != nil {
			return status.Error(codes.Internal, "failed to load event")
		}
		if !seriesID.Valid {
			return status.Error(codes.FailedPrecondition, "event is not part of a series")
		}

		if _, err := tx.ExecContext(ctx,
			"UPDATE events SET status = $1, updated_at = NOW() WHERE id = $2",
			StatusCancelled, eventID); err != nil {
			return status.Errorf(codes.Internal, "failed to cancel occurrence: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE event_attendees SET status = 'cancelled', updated_at = NOW()
			WHERE event_id = $1 AND status IN ('confirmed', 'waitlisted', 'pending_payment')`,
			eventID); err != nil {
			return status.Errorf(codes.Internal, "failed to release rsvps: %v", err)
		}
		return nil
	})
}

// UpdateSeriesFutureOccurrences propagates template changes to every future
// occurrence that is still open (draft or published) from cutoff onward.
// Cancelled and completed occurrences are left untouched so past history and
// deliberately-cancelled dates are preserved. The actor must own the series.
func (db *eventDB) UpdateSeriesFutureOccurrences(ctx context.Context, seriesID int64, actorAccountID string, cutoff time.Time, tmpl OccurrenceTemplate) (int64, error) {
	var affected int64
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		actorID, err := resolveAccount(ctx, tx, actorAccountID)
		if err != nil {
			return err
		}

		var organizerID int64
		var seriesCover string
		if err := tx.QueryRowContext(ctx,
			"SELECT organizer_id, COALESCE(cover_image, '') FROM event_series WHERE id = $1", seriesID).Scan(&organizerID, &seriesCover); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "series not found")
			}
			return status.Error(codes.Internal, "failed to load series")
		}
		if organizerID != actorID {
			return status.Error(codes.PermissionDenied, "only the series organizer can edit the series")
		}
		if strings.TrimSpace(tmpl.CoverImage) == "" {
			tmpl.CoverImage = seriesCover
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE events SET
				event_type = $1, location = $2, meeting_link = $3,
				capacity = $4, cover_image = $5, visibility = $6, updated_at = NOW()
			WHERE series_id = $7 AND series_occurrence_at >= $8
			  AND status IN ($9, $10)`,
			tmpl.EventType, tmpl.Location, tmpl.MeetingLink, tmpl.Capacity,
			tmpl.CoverImage, defaultVisibility(tmpl.Visibility),
			seriesID, cutoff, StatusDraft, StatusPublished,
		)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to update future occurrences: %v", err)
		}
		affected, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}

func defaultVisibility(v string) string {
	if v == "" {
		return "public"
	}
	return v
}

// applySeriesCover writes the same cover URL onto the series and every
// occurrence. The URL points at the one v2 object that was uploaded; siblings
// do not get their own MinIO copies.
func applySeriesCover(ctx context.Context, tx *sql.Tx, seriesID int64, url string) error {
	url = strings.TrimSpace(url)
	if seriesID == 0 || url == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE event_series SET cover_image = $1, updated_at = NOW() WHERE id = $2",
		url, seriesID); err != nil {
		return status.Error(codes.Internal, "failed to update series cover")
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE events SET cover_image = $1, updated_at = NOW() WHERE series_id = $2",
		url, seriesID); err != nil {
		return status.Error(codes.Internal, "failed to update series cover")
	}
	return nil
}

func (db *eventDB) MaterializeSeries(ctx context.Context, req *pb.CreateSeriesReq, occs []time.Time, rule string) (*pb.Event, error) {
	if strings.TrimSpace(req.Title) == "" {
		return nil, status.Error(codes.InvalidArgument, "title is required")
	}
	if len(occs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "recurrence produced no dates")
	}
	if req.StartTime == nil || req.EndTime == nil || !req.EndTime.AsTime().After(req.StartTime.AsTime()) {
		return nil, status.Error(codes.InvalidArgument, "end_time must be after start_time")
	}

	var endsAt *time.Time
	if req.Recurrence != nil && req.Recurrence.Until != nil {
		t := req.Recurrence.Until.AsTime()
		endsAt = &t
	}

	var groupID int64
	if strings.TrimSpace(req.GroupSlug) != "" {
		err := db.inTx(ctx, func(tx *sql.Tx) error {
			organizerID, err := resolveAccount(ctx, tx, req.AccountId)
			if err != nil {
				return err
			}
			gid, err := resolveGroupForOrganizer(ctx, tx, req.GroupSlug, organizerID)
			if err != nil {
				return err
			}
			groupID = gid
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	hours := int32(0)
	if req.Recurrence != nil {
		hours = req.Recurrence.GetRsvpCloseHoursBefore()
	}
	hours, err := normalizeRsvpCloseHours(hours)
	if err != nil {
		return nil, err
	}

	seriesID, err := db.CreateSeries(ctx, SeriesInput{
		OrganizerAccountID:   req.AccountId,
		GroupID:              groupID,
		Title:                req.Title,
		Description:          req.Description,
		Timezone:             req.Timezone,
		RecurrenceRule:       rule,
		RecurrenceStartsAt:   req.StartTime.AsTime(),
		RecurrenceEndsAt:     endsAt,
		CoverImage:           req.CoverImage,
		RsvpCloseHoursBefore: hours,
	})
	if err != nil {
		return nil, err
	}

	lat, lng := Geocode(req.Location)
	slugs, err := db.GenerateSeriesOccurrences(ctx, seriesID, occs, OccurrenceTemplate{
		EventType:   req.EventType,
		Location:    req.Location,
		MeetingLink: req.MeetingLink,
		Capacity:    req.Capacity,
		CoverImage:  req.CoverImage,
		Visibility:  req.Visibility,
		Duration:    req.EndTime.AsTime().Sub(req.StartTime.AsTime()),
		Tags:        req.Tags,
		Tiers:       req.TicketTiers,
		Latitude:    lat,
		Longitude:   lng,
	})
	if err != nil {
		return nil, err
	}
	if len(slugs) == 0 {
		return nil, status.Error(codes.Internal, "failed to create series occurrences")
	}
	event, _, err := db.GetEvent(ctx, slugs[0], req.AccountId)
	return event, err
}

// recurrenceText turns a stored RRULE into a short card label. Called only when
// the event belongs to a series; unknown/empty rules still get a generic line.
func recurrenceText(rule string) string {
	freq, interval, byDay := parseRRULE(rule)
	if freq == "" {
		return "Part of a recurring series"
	}
	n := interval
	if n < 1 {
		n = 1
	}
	unit := map[string][2]string{
		"DAILY":   {"day", "days"},
		"WEEKLY":  {"week", "weeks"},
		"MONTHLY": {"month", "months"},
		"YEARLY":  {"year", "years"},
	}[freq]
	if unit[0] == "" {
		return "Part of a recurring series"
	}
	var b strings.Builder
	if n == 1 {
		b.WriteString("Every ")
		b.WriteString(unit[0])
	} else {
		fmt.Fprintf(&b, "Every %d %s", n, unit[1])
	}
	if freq == "WEEKLY" && len(byDay) > 0 {
		b.WriteString(" on ")
		b.WriteString(strings.Join(byDay, ", "))
	}
	return b.String()
}

func parseRRULE(rule string) (freq string, interval int, byDay []string) {
	interval = 1
	dayName := map[string]string{
		"MO": "Mon", "TU": "Tue", "WE": "Wed", "TH": "Thu",
		"FR": "Fri", "SA": "Sat", "SU": "Sun",
	}
	for _, part := range strings.Split(rule, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || v == "" {
			continue
		}
		switch strings.ToUpper(k) {
		case "FREQ":
			freq = strings.ToUpper(v)
		case "INTERVAL":
			fmt.Sscanf(v, "%d", &interval)
		case "BYDAY":
			for _, d := range strings.Split(v, ",") {
				d = strings.ToUpper(strings.TrimSpace(d))
				if n, ok := dayName[d]; ok {
					byDay = append(byDay, n)
				}
			}
		}
	}
	return freq, interval, byDay
}
