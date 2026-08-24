package database

import (
	"context"
	"database/sql"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Attendance states (mirror event_attendees.attendance_status CHECK).
const (
	attendanceRegistered = "registered"
	attendanceCheckedIn  = "checked_in"
	attendanceNoShow     = "no_show"
	attendanceNotComing  = "not_coming"
)

var validAttendanceStatuses = map[string]struct{}{
	attendanceRegistered: {},
	attendanceCheckedIn:  {},
	attendanceNoShow:     {},
	attendanceNotComing:  {},
}

// UpdateAttendance sets an attendee's attendance status and check-in state,
// keeping the legacy checked_in boolean consistent with checked_in_at/by. The
// actor must hold manage_checkins. Checking someone in implies attendance
// status 'checked_in'; clearing check-in nulls the audit columns.
func (db *eventDB) UpdateAttendance(ctx context.Context, req *pb.UpdateAttendanceReq) error {
	status_ := req.AttendanceStatus
	checkedIn := req.CheckedIn

	// A check-in and the 'checked_in' status are two views of one fact; reconcile
	// them so the row is never internally contradictory regardless of which the
	// caller set.
	if checkedIn {
		status_ = attendanceCheckedIn
	} else if status_ == attendanceCheckedIn {
		checkedIn = true
	}
	if status_ == "" {
		status_ = attendanceRegistered
	}
	if _, ok := validAttendanceStatuses[status_]; !ok {
		return status.Errorf(codes.InvalidArgument, "invalid attendance_status %q", status_)
	}

	return db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, actorID, err := authorize(ctx, tx, req.EventSlug, req.AccountId, permManageCheckins)
		if err != nil {
			return err
		}

		// Scope the attendee to this event so an id from another event cannot be
		// mutated through a slug the actor happens to control.
		var exists bool
		if err := tx.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM event_attendees WHERE id = $1 AND event_id = $2)",
			req.AttendeeId, eventID).Scan(&exists); err != nil {
			return status.Error(codes.Internal, "failed to verify attendee")
		}
		if !exists {
			return status.Error(codes.NotFound, "attendee not found for this event")
		}

		if checkedIn {
			_, err = tx.ExecContext(ctx, `
				UPDATE event_attendees
				SET attendance_status = $1, checked_in = TRUE,
				    checked_in_at = NOW(), checked_in_by = $2, updated_at = NOW()
				WHERE id = $3 AND event_id = $4`,
				status_, actorID, req.AttendeeId, eventID)
		} else {
			_, err = tx.ExecContext(ctx, `
				UPDATE event_attendees
				SET attendance_status = $1, checked_in = FALSE,
				    checked_in_at = NULL, checked_in_by = NULL, updated_at = NOW()
				WHERE id = $2 AND event_id = $3`,
				status_, req.AttendeeId, eventID)
		}
		if err != nil {
			return status.Errorf(codes.Internal, "failed to update attendance: %v", err)
		}
		return nil
	})
}
