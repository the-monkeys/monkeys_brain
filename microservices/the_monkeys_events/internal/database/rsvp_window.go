package database

import (
	"context"
	"database/sql"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Off, 12 hours, 1 day, 3 days, 1 week before each occurrence start.
var allowedRsvpCloseHours = map[int32]struct{}{
	0: {}, 12: {}, 24: {}, 72: {}, 168: {},
}

func normalizeRsvpCloseHours(hours int32) (int32, error) {
	if hours < 0 {
		return 0, status.Error(codes.InvalidArgument, "invalid RSVP close window")
	}
	if _, ok := allowedRsvpCloseHours[hours]; !ok {
		return 0, status.Error(codes.InvalidArgument,
			"RSVP close must be off, 12 hours, 1 day, 3 days, or 1 week before the start")
	}
	return hours, nil
}

func rsvpClosesValue(start time.Time, hours int32) any {
	if hours <= 0 {
		return nil
	}
	return start.Add(-time.Duration(hours) * time.Hour)
}

func validateRsvpClosesAt(start time.Time, closes *timestamppb.Timestamp) error {
	if closes == nil || !closes.IsValid() {
		return nil
	}
	t := closes.AsTime()
	if t.IsZero() {
		return nil
	}
	if !t.Before(start) {
		return status.Error(codes.InvalidArgument, "RSVP must close before the meetup starts")
	}
	return nil
}

func nullHours(hours int32) any {
	if hours <= 0 {
		return nil
	}
	return hours
}

func rsvpHasClosed(closes sql.NullTime) bool {
	return closes.Valid && time.Now().After(closes.Time)
}

// applySeriesRsvpClose stores the relative offset on the series and stamps
// rsvp_closes_at on every open future occurrence. Past/cancelled dates keep
// their historical close.
func applySeriesRsvpClose(ctx context.Context, tx *sql.Tx, seriesID int64, hours int32) error {
	hours, err := normalizeRsvpCloseHours(hours)
	if err != nil {
		return err
	}
	hoursCol := nullHours(hours)
	if _, err := tx.ExecContext(ctx,
		`UPDATE event_series SET rsvp_close_hours_before = $1, updated_at = NOW() WHERE id = $2`,
		hoursCol, seriesID); err != nil {
		return status.Error(codes.Internal, "failed to update series rsvp close")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE events SET
			rsvp_closes_at = CASE
				WHEN $1::int IS NULL OR $1::int <= 0 THEN NULL
				ELSE start_time - ($1::int * INTERVAL '1 hour')
			END,
			updated_at = NOW()
		WHERE series_id = $2
		  AND status IN ('draft', 'published', 'live')
		  AND end_time >= NOW()`,
		hoursCol, seriesID); err != nil {
		return status.Error(codes.Internal, "failed to update series rsvp close")
	}
	return nil
}
