package database

import (
	"context"
	"database/sql"
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
	OrganizerAccountID string
	GroupID            int64
	Title              string
	Description        string
	Timezone           string
	RecurrenceRule     string
	RecurrenceStartsAt time.Time
	RecurrenceEndsAt   *time.Time
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
				recurrence_rule, recurrence_starts_at, recurrence_ends_at, status
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			RETURNING id`,
			groupID, organizerID, strings.TrimSpace(in.Title), in.Description,
			defaultTimezone(in.Timezone), in.RecurrenceRule, in.RecurrenceStartsAt,
			endsAt, seriesActive,
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
		var title, description, timezone, seriesStatus string
		if err := tx.QueryRowContext(ctx, `
			SELECT organizer_id, group_id, title, COALESCE(description, ''), timezone, status
			FROM event_series WHERE id = $1 FOR UPDATE`, seriesID,
		).Scan(&organizerID, &groupID, &title, &description, &timezone, &seriesStatus); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "series not found")
			}
			return status.Error(codes.Internal, "failed to load series")
		}
		if seriesStatus == seriesCancelled || seriesStatus == seriesCompleted {
			return status.Errorf(codes.FailedPrecondition, "series is %s", seriesStatus)
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
					series_id, series_occurrence_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
				RETURNING id`,
				title, description, slug, occ, occ.Add(tmpl.Duration), defaultTimezone(timezone),
				tmpl.EventType, tmpl.Location, tmpl.MeetingLink, tmpl.Capacity, tmpl.CoverImage,
				organizerID, groupCol, visibility, StatusPublished, seriesID, occ,
			).Scan(&eventID); err != nil {
				return status.Errorf(codes.Internal, "failed to create occurrence: %v", err)
			}

			if err := grantPermissions(ctx, tx, eventID, organizerID); err != nil {
				return err
			}
			// A published event needs a tier for RSVP to attach to; mirror the
			// default tier the publish path creates for free events.
			if _, err := insertTier(ctx, tx, eventID,
				&pb.TicketTierInput{Name: "General", Currency: defaultCurrency}, 0); err != nil {
				return err
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
		if err := tx.QueryRowContext(ctx,
			"SELECT organizer_id FROM event_series WHERE id = $1", seriesID).Scan(&organizerID); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "series not found")
			}
			return status.Error(codes.Internal, "failed to load series")
		}
		if organizerID != actorID {
			return status.Error(codes.PermissionDenied, "only the series organizer can edit the series")
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
