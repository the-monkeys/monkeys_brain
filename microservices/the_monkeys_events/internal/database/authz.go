package database

import (
	"context"
	"database/sql"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// authorizeQuery answers every question the gateway asks about a caller's
// standing on an event in one round trip. Each branch is an indexed lookup
// keyed on the event, and the whole thing collapses to nothing useful for an
// anonymous caller: the viewer CTE is empty, so the organizer comparison is
// NULL (coalesced to false) and every EXISTS is false.
const authorizeQuery = `
WITH viewer AS (
    SELECT id FROM user_account WHERE account_id = $2
)
SELECT
    e.status,
    COALESCE(e.organizer_id = (SELECT id FROM viewer), false),
    EXISTS (SELECT 1 FROM event_co_hosts ch
             WHERE ch.event_id = e.id AND ch.co_host_id = (SELECT id FROM viewer)),
    EXISTS (SELECT 1 FROM event_attendees a
             WHERE a.event_id = e.id AND a.user_id = (SELECT id FROM viewer)
               AND a.status = 'confirmed'),
    array_to_string(ARRAY(
        SELECT p.permission_type FROM event_permissions p
         WHERE p.event_id = e.id AND p.user_id = (SELECT id FROM viewer)), ','),
    CASE WHEN $3 > 0 THEN EXISTS (
        SELECT 1 FROM event_comments c
         WHERE c.id = $3 AND c.event_id = e.id AND c.user_id = (SELECT id FROM viewer))
    ELSE false END
FROM events e
WHERE e.slug = $1`

// Authorize resolves the caller's role, grants and attendance on an event.
//
// This backs the gateway's fast-reject layer. It is advisory only: the write
// paths still call authorize() inside their transaction, so a caller who slips
// past a stale gateway grant is refused at the row.
func (db *eventDB) Authorize(ctx context.Context, req *pb.AuthorizeReq) (*pb.AuthorizeResp, error) {
	var (
		res         pb.AuthorizeResp
		isOrganizer bool
		isCoHost    bool
		granted     string
	)

	err := db.db.QueryRowContext(ctx, authorizeQuery, req.EventSlug, req.AccountId, req.CommentId).
		Scan(&res.EventStatus, &isOrganizer, &isCoHost, &res.IsAttendee, &granted, &res.OwnsComment)
	if err == sql.ErrNoRows {
		// EventExists stays false; the gateway turns this into a 404.
		return &res, nil
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to authorize: %v", err)
	}

	res.EventExists = true
	if granted != "" {
		res.Permissions = strings.Split(granted, ",")
	}

	switch {
	case isOrganizer:
		res.Role = "organizer"
		// The organizer holds every right implicitly and has no rows in
		// event_permissions, so spell the bundle out for the gateway.
		res.Permissions = append([]string(nil), coHostPermissions...)
	case isCoHost:
		res.Role = "co_host"
	case res.IsAttendee:
		res.Role = "attendee"
	default:
		res.Role = "viewer"
	}

	return &res, nil
}
