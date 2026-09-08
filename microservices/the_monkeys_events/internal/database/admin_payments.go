package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func adminLimit(n int32) int32 {
	if n <= 0 || n > 100 {
		return 20
	}
	return n
}

func moneyINR(paise int64) *pb.MoneyINR {
	return &pb.MoneyINR{Paise: paise, Inr: money.FormatINR(paise)}
}

func errNothingToSettle(open int64) error {
	if open <= 0 {
		return status.Error(codes.FailedPrecondition, "nothing to settle")
	}
	return nil
}

func (db *eventDB) AdminListEventPayments(ctx context.Context, req *pb.AdminListEventPaymentsReq) (*pb.AdminListEventPaymentsResp, error) {
	limit := adminLimit(req.GetLimit())
	offset := req.GetOffset()
	if offset < 0 {
		offset = 0
	}
	q := strings.TrimSpace(req.GetQuery())
	st := strings.TrimSpace(req.GetStatus())

	args := []any{q, st}
	where := `
		FROM events e
		JOIN user_account u ON u.id = e.organizer_id
		JOIN (
			SELECT event_id,
				COALESCE(SUM(gross_paise) FILTER (WHERE status = 'captured'), 0) AS gross,
				COALESCE(SUM(platform_fee_paise) FILTER (WHERE status = 'captured'), 0) AS fee,
				COALESCE(SUM(gst_paise) FILTER (WHERE status = 'captured'), 0) AS gst,
				COALESCE(SUM(host_payable_paise) FILTER (WHERE status = 'captured'), 0) AS host,
				COALESCE(SUM(gross_paise) FILTER (WHERE status = 'refunded'), 0) AS refunded
			FROM event_payments
			GROUP BY event_id
		) pay ON pay.event_id = e.id
		LEFT JOIN (
			SELECT event_id, COALESCE(SUM(payable_paise), 0) AS settled
			FROM event_host_settlements
			WHERE status IN ('pending', 'paid')
			GROUP BY event_id
		) setl ON setl.event_id = e.id
		WHERE ($1 = '' OR e.title ILIKE '%' || $1 || '%' OR e.slug ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR e.status = $2)`

	var total int32
	if err := db.db.QueryRowContext(ctx, "SELECT COUNT(1) "+where, args...).Scan(&total); err != nil {
		return nil, status.Error(codes.Internal, "failed to count event payments")
	}

	args = append(args, limit, offset)
	rows, err := db.db.QueryContext(ctx, `
		SELECT e.id, e.slug, e.title, u.username, e.start_time,
			pay.gross, pay.fee, pay.gst, pay.host, pay.refunded, COALESCE(setl.settled, 0)
		`+where+`
		ORDER BY e.start_time DESC
		LIMIT $3 OFFSET $4`, args...)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list event payments")
	}
	defer rows.Close()

	out := &pb.AdminListEventPaymentsResp{Total: total}
	for rows.Next() {
		sum, err := scanPaymentSummary(rows)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to scan event payments")
		}
		out.Events = append(out.Events, sum)
	}
	return out, rows.Err()
}

func scanPaymentSummary(row rowScanner) (*pb.AdminEventPaymentSummary, error) {
	var start sql.NullTime
	var gross, fee, gst, host, refunded, settled int64
	s := &pb.AdminEventPaymentSummary{}
	if err := row.Scan(&s.EventId, &s.Slug, &s.Title, &s.OrganizerUsername, &start,
		&gross, &fee, &gst, &host, &refunded, &settled); err != nil {
		return nil, err
	}
	if start.Valid {
		s.StartTime = timestamppb.New(start.Time)
	}
	s.GrossCaptured = moneyINR(gross)
	s.PlatformFee = moneyINR(fee)
	s.Gst = moneyINR(gst)
	s.HostPayableCaptured = moneyINR(host)
	s.RefundedGross = moneyINR(refunded)
	s.Settled = moneyINR(settled)
	s.OpenPayable = moneyINR(money.OpenPayable(host, settled))
	return s, nil
}

func (db *eventDB) AdminGetEventPayments(ctx context.Context, req *pb.AdminGetEventPaymentsReq) (*pb.AdminGetEventPaymentsResp, error) {
	slug := strings.TrimSpace(req.GetSlug())
	if slug == "" {
		return nil, status.Error(codes.InvalidArgument, "slug is required")
	}

	sumRow := db.db.QueryRowContext(ctx, `
		SELECT e.id, e.slug, e.title, u.username, e.start_time,
			COALESCE(pay.gross, 0), COALESCE(pay.fee, 0), COALESCE(pay.gst, 0),
			COALESCE(pay.host, 0), COALESCE(pay.refunded, 0), COALESCE(setl.settled, 0)
		FROM events e
		JOIN user_account u ON u.id = e.organizer_id
		LEFT JOIN (
			SELECT event_id,
				COALESCE(SUM(gross_paise) FILTER (WHERE status = 'captured'), 0) AS gross,
				COALESCE(SUM(platform_fee_paise) FILTER (WHERE status = 'captured'), 0) AS fee,
				COALESCE(SUM(gst_paise) FILTER (WHERE status = 'captured'), 0) AS gst,
				COALESCE(SUM(host_payable_paise) FILTER (WHERE status = 'captured'), 0) AS host,
				COALESCE(SUM(gross_paise) FILTER (WHERE status = 'refunded'), 0) AS refunded
			FROM event_payments
			GROUP BY event_id
		) pay ON pay.event_id = e.id
		LEFT JOIN (
			SELECT event_id, COALESCE(SUM(payable_paise), 0) AS settled
			FROM event_host_settlements
			WHERE status IN ('pending', 'paid')
			GROUP BY event_id
		) setl ON setl.event_id = e.id
		WHERE e.slug = $1`, slug)
	sum, err := scanPaymentSummary(sumRow)
	if err == sql.ErrNoRows {
		return nil, status.Error(codes.NotFound, "event not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load event payments")
	}

	payRows, err := db.db.QueryContext(ctx, `
		SELECT COALESCE(u.username, ''), p.razorpay_order_id, p.razorpay_payment_id, p.status,
			p.gross_paise, p.platform_fee_paise, p.gst_paise, p.host_payable_paise
		FROM event_payments p
		JOIN event_attendees a ON a.id = p.attendee_id
		JOIN user_account u ON u.id = a.user_id
		WHERE p.event_id = $1
		ORDER BY p.captured_at DESC`, sum.EventId)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list payments")
	}
	defer payRows.Close()

	resp := &pb.AdminGetEventPaymentsResp{Event: sum}
	for payRows.Next() {
		line := &pb.AdminPaymentLine{}
		var gross, fee, gst, host int64
		if err := payRows.Scan(&line.AttendeeUsername, &line.RazorpayOrderId, &line.RazorpayPaymentId, &line.Status,
			&gross, &fee, &gst, &host); err != nil {
			return nil, status.Error(codes.Internal, "failed to scan payment")
		}
		line.Gross = moneyINR(gross)
		line.PlatformFee = moneyINR(fee)
		line.Gst = moneyINR(gst)
		line.HostPayable = moneyINR(host)
		resp.Payments = append(resp.Payments, line)
	}
	if err := payRows.Err(); err != nil {
		return nil, status.Error(codes.Internal, "failed to read payments")
	}

	setRows, err := db.db.QueryContext(ctx, `
		SELECT id, payable_paise, status, COALESCE(note, '')
		FROM event_host_settlements
		WHERE event_id = $1
		ORDER BY created_at DESC`, sum.EventId)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list settlements")
	}
	defer setRows.Close()
	for setRows.Next() {
		line := &pb.AdminSettlementLine{}
		var payable int64
		if err := setRows.Scan(&line.Id, &payable, &line.Status, &line.Note); err != nil {
			return nil, status.Error(codes.Internal, "failed to scan settlement")
		}
		line.Payable = moneyINR(payable)
		resp.Settlements = append(resp.Settlements, line)
	}
	return resp, setRows.Err()
}

func (db *eventDB) AdminCreateSettlement(ctx context.Context, req *pb.AdminCreateSettlementReq) (*pb.AdminSettlementResp, error) {
	slug := strings.TrimSpace(req.GetSlug())
	if slug == "" {
		return nil, status.Error(codes.InvalidArgument, "slug is required")
	}

	out := &pb.AdminSettlementResp{}
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		var eventID, organizerID int64
		if err := tx.QueryRowContext(ctx,
			"SELECT id, organizer_id FROM events WHERE slug = $1 FOR UPDATE", slug).
			Scan(&eventID, &organizerID); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "event not found")
			}
			return status.Error(codes.Internal, "failed to load event")
		}

		var captured, settled int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(host_payable_paise), 0) FROM event_payments WHERE event_id = $1 AND status = 'captured'`,
			eventID).Scan(&captured); err != nil {
			return status.Error(codes.Internal, "failed to sum host payable")
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(payable_paise), 0) FROM event_host_settlements WHERE event_id = $1 AND status IN ('pending', 'paid')`,
			eventID).Scan(&settled); err != nil {
			return status.Error(codes.Internal, "failed to sum settlements")
		}
		open := money.OpenPayable(captured, settled)
		if err := errNothingToSettle(open); err != nil {
			return err
		}

		line := &pb.AdminSettlementLine{Status: "pending", Note: strings.TrimSpace(req.GetNote())}
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO event_host_settlements (event_id, organizer_user_id, currency, payable_paise, status, note)
			VALUES ($1, $2, $3, $4, 'pending', $5)
			RETURNING id`,
			eventID, organizerID, money.CurrencyINR, open, line.Note).Scan(&line.Id); err != nil {
			return status.Error(codes.Internal, "failed to create settlement")
		}
		line.Payable = moneyINR(open)
		out.Settlement = line
		return writeAudit(ctx, tx, req.GetActor(), "settlement.create", "event", slug, map[string]any{
			"settlement_id": line.Id, "payable_paise": open, "note": line.Note,
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (db *eventDB) AdminMarkSettlementPaid(ctx context.Context, req *pb.AdminMarkSettlementPaidReq) (*pb.AdminSettlementResp, error) {
	if req.GetSettlementId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "settlement id is required")
	}
	out := &pb.AdminSettlementResp{}
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		note := strings.TrimSpace(req.GetNote())
		res, err := tx.ExecContext(ctx, `
			UPDATE event_host_settlements
			SET status = 'paid', marked_paid_at = NOW(), marked_paid_by = $2,
			    note = CASE WHEN $3 = '' THEN note ELSE $3 END
			WHERE id = $1 AND status = 'pending'`,
			req.SettlementId, actorLabel(req.GetActor()), note)
		if err != nil {
			return status.Error(codes.Internal, "failed to mark settlement paid")
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return status.Error(codes.FailedPrecondition, "settlement is not pending")
		}
		line := &pb.AdminSettlementLine{Id: req.SettlementId, Status: "paid", Note: note}
		var payable int64
		var slug string
		if err := tx.QueryRowContext(ctx, `
			SELECT s.payable_paise, COALESCE(s.note, ''), e.slug
			FROM event_host_settlements s
			JOIN events e ON e.id = s.event_id
			WHERE s.id = $1`, req.SettlementId).Scan(&payable, &line.Note, &slug); err != nil {
			return status.Error(codes.Internal, "failed to load settlement")
		}
		line.Payable = moneyINR(payable)
		out.Settlement = line
		return writeAudit(ctx, tx, req.GetActor(), "settlement.paid", "event", slug, map[string]any{
			"settlement_id": req.SettlementId, "note": line.Note,
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func actorLabel(a *pb.AdminActor) string {
	if a == nil {
		return ""
	}
	return strings.TrimSpace(a.Username + " " + a.AccountId + " " + a.Role)
}

func writeAudit(ctx context.Context, tx *sql.Tx, actor *pb.AdminActor, action, entityType, entityID string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	if actor != nil {
		payload["actor_username"] = actor.Username
		payload["actor_account_id"] = actor.AccountId
		payload["actor_role"] = actor.Role
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return status.Error(codes.Internal, "failed to encode audit")
	}
	ip := ""
	if actor != nil {
		ip = actor.Ip
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_audit_log (actor_ip, action, entity_type, entity_id, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)`,
		ip, action, entityType, entityID, string(raw)); err != nil {
		return status.Error(codes.Internal, "failed to write audit")
	}
	return nil
}
