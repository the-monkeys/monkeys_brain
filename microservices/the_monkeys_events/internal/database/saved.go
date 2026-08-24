package database

import (
	"context"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SaveEvent bookmarks an event for the caller. It is idempotent: saving an
// already-saved event is a no-op rather than an error, so the UI can fire it
// without first reading state.
func (db *eventDB) SaveEvent(ctx context.Context, req *pb.SaveEventReq) error {
	userID, err := resolveAccount(ctx, db.db, req.AccountId)
	if err != nil {
		return err
	}
	eventID, _, err := resolveEvent(ctx, db.db, req.EventSlug)
	if err != nil {
		return err
	}

	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO saved_events (user_id, event_id)
		VALUES ($1, $2) ON CONFLICT (user_id, event_id) DO NOTHING`,
		userID, eventID); err != nil {
		return status.Errorf(codes.Internal, "failed to save event: %v", err)
	}
	return nil
}

// UnsaveEvent removes a bookmark. Idempotent: unsaving what was never saved
// succeeds silently.
func (db *eventDB) UnsaveEvent(ctx context.Context, req *pb.SaveEventReq) error {
	userID, err := resolveAccount(ctx, db.db, req.AccountId)
	if err != nil {
		return err
	}
	eventID, _, err := resolveEvent(ctx, db.db, req.EventSlug)
	if err != nil {
		return err
	}

	if _, err := db.db.ExecContext(ctx,
		"DELETE FROM saved_events WHERE user_id = $1 AND event_id = $2",
		userID, eventID); err != nil {
		return status.Errorf(codes.Internal, "failed to unsave event: %v", err)
	}
	return nil
}
