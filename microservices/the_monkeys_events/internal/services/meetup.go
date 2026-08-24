package services

import (
	"context"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

// -----------------------------------------------------------------------------
// Meetup-parity attendee & save RPCs
//
// These are thin orchestration wrappers: the database layer owns the state
// reconciliation (keeping checked_in / checked_in_at / attendance_status
// consistent) and the final permission checks. Callers are identified by
// account_id, the immutable cross-service user id.
// -----------------------------------------------------------------------------

// UpdateAttendance sets an attendee's attendance status and/or check-in state.
func (s *EventService) UpdateAttendance(ctx context.Context, req *pb.UpdateAttendanceReq) (*pb.BasicResp, error) {
	if err := s.db.UpdateAttendance(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "attendance updated", Success: true}, nil
}

// SaveEvent bookmarks an event for the caller. It is idempotent.
func (s *EventService) SaveEvent(ctx context.Context, req *pb.SaveEventReq) (*pb.BasicResp, error) {
	if err := s.db.SaveEvent(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "event saved", Success: true}, nil
}

// UnsaveEvent removes a bookmark for the caller. It is idempotent.
func (s *EventService) UnsaveEvent(ctx context.Context, req *pb.SaveEventReq) (*pb.BasicResp, error) {
	if err := s.db.UnsaveEvent(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "event removed from saved", Success: true}, nil
}
