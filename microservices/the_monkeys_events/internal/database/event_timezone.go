package database

import (
	"context"
	"database/sql"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetEventTimezone reads only scheduling metadata. GetEvent hydrates a detail
// response and may backfill source coordinates, which clone inheritance must
// not do. CloneEvent still enforces edit permission inside its write transaction.
func (db *eventDB) GetEventTimezone(ctx context.Context, slug string) (string, error) {
	var zone string
	err := db.db.QueryRowContext(ctx,
		"SELECT COALESCE(timezone, 'UTC') FROM events WHERE slug = $1", slug).Scan(&zone)
	if err == sql.ErrNoRows {
		return "", status.Error(codes.NotFound, "event not found")
	}
	if err != nil {
		return "", status.Error(codes.Internal, "failed to load event timezone")
	}
	return zone, nil
}
