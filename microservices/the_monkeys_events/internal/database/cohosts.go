package database

import (
	"context"
	"database/sql"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Host permission types (mirror event_permissions.permission_type).
const (
	permEditEvent       = "edit_event"
	permManageAttendees = "manage_attendees"
	permManageTickets   = "manage_tickets"
	permManageCoupons   = "manage_coupons"
	permManageHosts     = "manage_hosts"
	// Meetup-parity additive permissions.
	permManageQuestions = "manage_questions"
	permManageCheckins  = "manage_checkins"
)

const defaultCoHostRole = "co_host"

// coHostPermissions is the rights bundle granted to an active co-host.
var coHostPermissions = []string{
	permEditEvent, permManageAttendees, permManageTickets, permManageCoupons, permManageHosts,
	permManageQuestions, permManageCheckins,
}

// grantPermissions writes the co-host rights bundle for a user on an event.
func grantPermissions(ctx context.Context, q querier, eventID, userID int64) error {
	for _, p := range coHostPermissions {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO event_permissions (event_id, user_id, permission_type)
			 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			eventID, userID, p); err != nil {
			return status.Errorf(codes.Internal, "failed to grant permission %s: %v", p, err)
		}
	}
	return nil
}

// auditHostAction appends to the host activity log.
func auditHostAction(ctx context.Context, q querier, eventID, hostID int64, action string, performedBy int64) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO event_host_activity_log (event_id, host_id, action, performed_by)
		 VALUES ($1, $2, $3, $4)`,
		eventID, hostID, action, performedBy)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to audit host action: %v", err)
	}
	return nil
}

// authorize resolves the actor, loads the event and verifies the actor holds
// the permission. The organizer implicitly holds every permission. It returns
// the event id and the actor's numeric id so callers can proceed directly.
func authorize(ctx context.Context, q querier, slug, accountID, perm string) (eventID, actorID int64, err error) {
	actorID, err = resolveAccount(ctx, q, accountID)
	if err != nil {
		return 0, 0, err
	}
	eventID, organizerID, err := resolveEvent(ctx, q, slug)
	if err != nil {
		return 0, 0, err
	}
	if organizerID == actorID {
		return eventID, actorID, nil
	}

	var n int
	if err = q.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM event_permissions
		 WHERE event_id = $1 AND user_id = $2 AND permission_type = $3`,
		eventID, actorID, perm).Scan(&n); err != nil {
		return 0, 0, status.Error(codes.Internal, "failed to check permission")
	}
	if n == 0 {
		return 0, 0, status.Errorf(codes.PermissionDenied, "requires %s permission", perm)
	}
	return eventID, actorID, nil
}

// addCoHost inserts the host row, grants rights and audits, all on the given
// querier so it can join a caller's transaction (used by CreateEvent too).
func addCoHost(ctx context.Context, q querier, eventID, hostID, actorID int64) error {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO event_co_hosts (event_id, co_host_id, role, added_by)
		 VALUES ($1, $2, $3, $4) ON CONFLICT (event_id, co_host_id) DO NOTHING`,
		eventID, hostID, defaultCoHostRole, actorID); err != nil {
		return status.Errorf(codes.Internal, "failed to add host: %v", err)
	}
	if err := grantPermissions(ctx, q, eventID, hostID); err != nil {
		return err
	}
	return auditHostAction(ctx, q, eventID, hostID, "added", actorID)
}

// AddCoHost grants event hosting rights. Only the organizer, or a co-host with
// manage_hosts, may add hosts.
func (db *eventDB) AddCoHost(ctx context.Context, req *pb.CoHostReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, actorID, err := authorize(ctx, tx, req.EventSlug, req.AccountId, permManageHosts)
		if err != nil {
			return err
		}
		targetID, err := resolveUsername(ctx, tx, req.CohostUsername)
		if err != nil {
			return err
		}

		var organizerID int64
		if err = tx.QueryRowContext(ctx,
			"SELECT organizer_id FROM events WHERE id = $1", eventID).Scan(&organizerID); err != nil {
			return status.Error(codes.Internal, "failed to load organizer")
		}
		if targetID == organizerID {
			return status.Error(codes.InvalidArgument, "the organizer is already a host")
		}

		var exists int
		if err = tx.QueryRowContext(ctx,
			"SELECT COUNT(1) FROM event_co_hosts WHERE event_id = $1 AND co_host_id = $2",
			eventID, targetID).Scan(&exists); err != nil {
			return status.Error(codes.Internal, "failed to check existing host")
		}
		if exists > 0 {
			return status.Error(codes.AlreadyExists, "user is already a host")
		}

		return addCoHost(ctx, tx, eventID, targetID, actorID)
	})
}

// RemoveCoHost revokes hosting rights. The organizer can never be removed.
func (db *eventDB) RemoveCoHost(ctx context.Context, req *pb.CoHostReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, actorID, err := authorize(ctx, tx, req.EventSlug, req.AccountId, permManageHosts)
		if err != nil {
			return err
		}
		targetID, err := resolveUsername(ctx, tx, req.CohostUsername)
		if err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx,
			"DELETE FROM event_co_hosts WHERE event_id = $1 AND co_host_id = $2", eventID, targetID)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to remove host: %v", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return status.Error(codes.NotFound, "user is not a host of this event")
		}

		if _, err = tx.ExecContext(ctx,
			"DELETE FROM event_permissions WHERE event_id = $1 AND user_id = $2",
			eventID, targetID); err != nil {
			return status.Errorf(codes.Internal, "failed to revoke permissions: %v", err)
		}

		return auditHostAction(ctx, tx, eventID, targetID, "removed", actorID)
	})
}
