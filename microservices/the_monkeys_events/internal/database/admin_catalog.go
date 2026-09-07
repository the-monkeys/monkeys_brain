package database

import (
	"context"
	"database/sql"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (db *eventDB) AdminListEvents(ctx context.Context, req *pb.AdminListEventsReq) (*pb.AdminListEventsResp, error) {
	limit := adminLimit(req.GetLimit())
	offset := req.GetOffset()
	if offset < 0 {
		offset = 0
	}
	q := strings.TrimSpace(req.GetQuery())
	st := strings.TrimSpace(req.GetStatus())
	groupSlug := strings.TrimSpace(req.GetGroupSlug())

	var total int32
	if err := db.db.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM events e
		LEFT JOIN groups g ON g.id = e.group_id
		WHERE ($1 = '' OR e.status = $1)
		  AND ($2 = '' OR e.title ILIKE '%' || $2 || '%' OR e.slug ILIKE '%' || $2 || '%')
		  AND ($3 = '' OR g.slug = $3)`, st, q, groupSlug).Scan(&total); err != nil {
		return nil, status.Error(codes.Internal, "failed to count events")
	}
	rows, err := db.db.QueryContext(ctx, `
		SELECT e.slug, e.title, e.status, u.username, COALESCE(g.slug, ''), e.start_time
		FROM events e
		JOIN user_account u ON u.id = e.organizer_id
		LEFT JOIN groups g ON g.id = e.group_id
		WHERE ($1 = '' OR e.status = $1)
		  AND ($2 = '' OR e.title ILIKE '%' || $2 || '%' OR e.slug ILIKE '%' || $2 || '%')
		  AND ($3 = '' OR g.slug = $3)
		ORDER BY e.start_time DESC NULLS LAST
		LIMIT $4 OFFSET $5`, st, q, groupSlug, limit, offset)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list events")
	}
	defer rows.Close()
	out := &pb.AdminListEventsResp{Total: total}
	for rows.Next() {
		row := &pb.AdminEventRow{}
		var start sql.NullTime
		if err := rows.Scan(&row.Slug, &row.Title, &row.Status, &row.OrganizerUsername, &row.GroupSlug, &start); err != nil {
			return nil, status.Error(codes.Internal, "failed to scan event")
		}
		if start.Valid {
			row.StartTime = timestamppb.New(start.Time)
		}
		out.Events = append(out.Events, row)
	}
	return out, rows.Err()
}

func (db *eventDB) AdminEventStats(ctx context.Context) (*pb.AdminEventStatsResp, error) {
	out := &pb.AdminEventStatsResp{}
	err := db.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'draft')::int,
			COUNT(*) FILTER (WHERE status = 'published')::int,
			COUNT(*) FILTER (WHERE status = 'live')::int,
			COUNT(*) FILTER (WHERE status = 'completed')::int,
			COUNT(*) FILTER (WHERE status = 'cancelled')::int
		FROM events`).Scan(&out.Draft, &out.Published, &out.Live, &out.Completed, &out.Cancelled)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count events")
	}
	return out, nil
}

func (db *eventDB) AdminPaymentStats(ctx context.Context) (*pb.AdminPaymentStatsResp, error) {
	out := &pb.AdminPaymentStatsResp{}
	var capturedHost, settled int64
	err := db.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(gross_paise) FILTER (WHERE status = 'captured'), 0),
			COALESCE(SUM(platform_fee_paise) FILTER (WHERE status = 'captured'), 0),
			COALESCE(SUM(gst_paise) FILTER (WHERE status = 'captured'), 0),
			COALESCE(SUM(host_payable_paise) FILTER (WHERE status = 'captured'), 0),
			COALESCE(SUM(gross_paise) FILTER (WHERE status = 'refunded'), 0)
		FROM event_payments`).Scan(&out.CapturedGrossPaise, &out.PlatformFeePaise, &out.GstPaise, &capturedHost, &out.RefundedGrossPaise)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to sum payments")
	}
	if err := db.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(payable_paise), 0) FROM event_host_settlements
		WHERE status IN ('pending', 'paid')`).Scan(&settled); err != nil {
		return nil, status.Error(codes.Internal, "failed to sum settlements")
	}
	out.HostPayableOpenPaise = money.OpenPayable(capturedHost, settled)
	return out, nil
}

func (db *eventDB) AdminCancelEvent(ctx context.Context, req *pb.AdminEventActionReq) (*pb.Event, error) {
	slug := strings.TrimSpace(req.GetSlug())
	if slug == "" {
		return nil, status.Error(codes.InvalidArgument, "slug is required")
	}
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		var eventID int64
		var current string
		if err := tx.QueryRowContext(ctx, "SELECT id, status FROM events WHERE slug = $1 FOR UPDATE", slug).
			Scan(&eventID, &current); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "event not found")
			}
			return status.Error(codes.Internal, "failed to load event")
		}
		if current == StatusCancelled {
			return status.Error(codes.FailedPrecondition, "event is already cancelled")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE events SET status = $1, updated_at = NOW() WHERE id = $2",
			StatusCancelled, eventID); err != nil {
			return status.Error(codes.Internal, "failed to cancel event")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE event_attendees SET status = 'cancelled', updated_at = NOW()
			WHERE event_id = $1 AND status IN ('confirmed', 'waitlisted', 'pending_payment')`, eventID); err != nil {
			return status.Error(codes.Internal, "failed to release rsvps")
		}
		return writeAudit(ctx, tx, req.GetActor(), "event.cancel", "event", slug, nil)
	})
	if err != nil {
		return nil, err
	}
	event, _, err := db.GetEvent(ctx, slug, "")
	return event, err
}

func cannotUnpublishWithPaid(confirmedPaid int64) bool {
	return confirmedPaid > 0
}

func (db *eventDB) AdminUnpublishEvent(ctx context.Context, req *pb.AdminEventActionReq) (*pb.Event, error) {
	slug := strings.TrimSpace(req.GetSlug())
	if slug == "" {
		return nil, status.Error(codes.InvalidArgument, "slug is required")
	}
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		var eventID int64
		var current string
		if err := tx.QueryRowContext(ctx, "SELECT id, status FROM events WHERE slug = $1 FOR UPDATE", slug).
			Scan(&eventID, &current); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "event not found")
			}
			return status.Error(codes.Internal, "failed to load event")
		}
		if current != StatusPublished && current != StatusLive {
			return status.Error(codes.FailedPrecondition, "only published or live events can be unpublished")
		}
		var paid int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM event_attendees
			WHERE event_id = $1 AND status = 'confirmed'
			  AND (COALESCE(amount_captured_paise, 0) > 0 OR COALESCE(amount_paid, 0) > 0)`,
			eventID).Scan(&paid); err != nil {
			return status.Error(codes.Internal, "failed to count paid attendees")
		}
		if cannotUnpublishWithPaid(paid) {
			return status.Error(codes.FailedPrecondition, "event has confirmed paid attendees")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE events SET status = $1, updated_at = NOW() WHERE id = $2",
			StatusDraft, eventID); err != nil {
			return status.Error(codes.Internal, "failed to unpublish event")
		}
		return writeAudit(ctx, tx, req.GetActor(), "event.unpublish", "event", slug, nil)
	})
	if err != nil {
		return nil, err
	}
	event, _, err := db.GetEvent(ctx, slug, "")
	return event, err
}
