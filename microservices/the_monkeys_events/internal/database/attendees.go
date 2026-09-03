package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RSVP statuses stored in event_attendees.status.
const (
	RSVPPendingPayment = "pending_payment"
	RSVPConfirmed      = "confirmed"
	RSVPWaitlisted     = "waitlisted"
	RSVPCancelled      = "cancelled"
)

// RSVPResult is the outcome of a reservation attempt. AmountDue is non-zero
// only when the caller must drive the attendee through checkout.
type RSVPResult struct {
	AttendeeID        int64
	Status            string
	AmountDue         float64
	Currency          string
	EventTitle        string
	EventSlug         string
	Username          string
	OrganizerUsername string
	// DatesSaved is how many occurrences this request wrote (confirmed or
	// waitlisted). 0/1 keeps the single-date copy; >1 is an all-upcoming RSVP.
	DatesSaved      int
	WaitlistedDates int
}

// CancelResult carries what the caller needs to issue a refund and to notify
// anyone promoted off the waitlist.
type CancelResult struct {
	EventTitle       string
	PaymentID        string
	AmountPaid       float64
	Username         string
	PromotedUsername string
	PromotedStatus   string
}

// PaymentResult identifies the attendee a webhook resolved.
type PaymentResult struct {
	Username   string
	EventTitle string
	EventSlug  string
}

// CreateRSVP reserves a seat. Capacity is evaluated under a row lock on the
// event so concurrent requests cannot oversell. Paid tiers land in
// pending_payment until the Razorpay webhook confirms them; when the event or
// tier is already full the attendee is waitlisted instead.
func (db *eventDB) CreateRSVP(ctx context.Context, req *pb.RSVPReq) (*RSVPResult, error) {
	out := &RSVPResult{}

	err := db.inTx(ctx, func(tx *sql.Tx) error {
		userID, err := resolveAccount(ctx, tx, req.AccountId)
		if err != nil {
			return err
		}
		series, err := parseRSVPScope(req.GetScope())
		if err != nil {
			return err
		}
		if series {
			return rsvpSeriesUpcoming(ctx, tx, req, userID, out)
		}

		// Locking the event row serialises capacity arithmetic for this event.
		var eventID, organizerID int64
		var eventStatus string
		var capacity int32
		var endTime time.Time
		var rsvpCloses sql.NullTime
		if err := tx.QueryRowContext(ctx, `
			SELECT e.id, e.organizer_id, e.status, e.capacity, e.title, e.slug, e.end_time, e.rsvp_closes_at
			FROM events e WHERE e.slug = $1 FOR UPDATE`, req.EventSlug,
		).Scan(&eventID, &organizerID, &eventStatus, &capacity, &out.EventTitle, &out.EventSlug, &endTime, &rsvpCloses); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "event not found")
			}
			return status.Error(codes.Internal, "failed to load event")
		}
		if eventStatus != StatusPublished && eventStatus != StatusLive {
			return status.Errorf(codes.FailedPrecondition, "event is not open for rsvp (%s)", eventStatus)
		}
		if eventHasEnded(eventStatus, endTime) {
			return status.Error(codes.FailedPrecondition, "event is not open for rsvp (ended)")
		}
		if rsvpHasClosed(rsvpCloses) {
			return status.Error(codes.FailedPrecondition, "RSVP for this meetup has closed.")
		}
		if userID == organizerID {
			return status.Error(codes.FailedPrecondition, "the organizer is already attending")
		}

		if err := tx.QueryRowContext(ctx,
			"SELECT username FROM user_account WHERE id = $1", userID).Scan(&out.Username); err != nil {
			return status.Error(codes.Internal, "failed to load attendee")
		}
		if err := tx.QueryRowContext(ctx,
			"SELECT username FROM user_account WHERE id = $1", organizerID).Scan(&out.OrganizerUsername); err != nil {
			return status.Error(codes.Internal, "failed to load organizer")
		}

		var price float64
		var tierCapacity int32
		if err := tx.QueryRowContext(ctx, `
			SELECT price, COALESCE(currency, $1), capacity FROM event_ticket_tiers
			WHERE id = $2 AND event_id = $3`,
			defaultCurrency, req.TicketTierId, eventID,
		).Scan(&price, &out.Currency, &tierCapacity); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "ticket tier not found for this event")
			}
			return status.Error(codes.Internal, "failed to load ticket tier")
		}

		seat, err := applyRSVPSeat(ctx, tx, userID, eventID, req.TicketTierId, capacity, tierCapacity, price, out.Currency, req.CouponCode)
		if err != nil {
			return err
		}
		if seat.Already {
			return status.Error(codes.AlreadyExists, "you have already responded to this event")
		}
		out.AttendeeID = seat.AttendeeID
		out.Status = seat.Status
		out.AmountDue = seat.AmountDue
		out.DatesSaved = 1
		if seat.Status == RSVPWaitlisted {
			out.WaitlistedDates = 1
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type rsvpSeatResult struct {
	AttendeeID int64
	Status     string
	AmountDue  float64
	Already    bool
}

// applyRSVPSeat writes one attendee row. Callers hold the event row lock.
// Coupon codes only apply when amount starts from a paid price; waitlisted
// seats do not consume coupon budget.
func applyRSVPSeat(ctx context.Context, tx *sql.Tx, userID, eventID, tierID int64, eventCap, tierCap int32, price float64, currency, couponCode string) (*rsvpSeatResult, error) {
	var existingID int64
	var existingStatus string
	switch err := tx.QueryRowContext(ctx,
		"SELECT id, status FROM event_attendees WHERE event_id = $1 AND user_id = $2",
		eventID, userID).Scan(&existingID, &existingStatus); {
	case err == sql.ErrNoRows:
	case err != nil:
		return nil, status.Error(codes.Internal, "failed to check existing rsvp")
	case existingStatus == RSVPConfirmed || existingStatus == RSVPWaitlisted:
		return &rsvpSeatResult{AttendeeID: existingID, Status: existingStatus, Already: true}, nil
	}

	if existingID != 0 {
		if err := releaseCoupon(ctx, tx, eventID, existingID); err != nil {
			return nil, err
		}
	}

	amount := price
	usedCode := ""
	var couponID int64
	if couponCode != "" && price > 0 {
		coupon, err := validateCoupon(ctx, tx, eventID, couponCode)
		if err != nil {
			return nil, err
		}
		amount = applyDiscount(price, coupon.DiscountPercent)
		usedCode, couponID = coupon.Code, coupon.Id
	}

	full, err := seatsExhausted(ctx, tx, eventID, tierID, eventCap, tierCap, existingID)
	if err != nil {
		return nil, err
	}

	out := &rsvpSeatResult{}
	switch {
	case full:
		out.Status, out.AmountDue = RSVPWaitlisted, 0
	case amount > 0:
		out.Status, out.AmountDue = RSVPPendingPayment, amount
	default:
		out.Status, out.AmountDue = RSVPConfirmed, 0
	}

	if existingID != 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE event_attendees
			SET ticket_tier_id = $1, status = $2, coupon_used = $3, amount_paid = 0,
				payment_order_id = NULL, payment_id = NULL, updated_at = NOW()
			WHERE id = $4`,
			tierID, out.Status, nullableString(usedCode), existingID); err != nil {
			return nil, status.Error(codes.Internal, "failed to update rsvp")
		}
		out.AttendeeID = existingID
	} else {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO event_attendees (event_id, user_id, ticket_tier_id, status, coupon_used, currency)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			eventID, userID, tierID, out.Status, nullableString(usedCode), currency,
		).Scan(&out.AttendeeID); err != nil {
			return nil, status.Error(codes.Internal, "failed to create rsvp")
		}
	}

	if couponID != 0 && out.Status != RSVPWaitlisted {
		if _, err := tx.ExecContext(ctx,
			"UPDATE event_coupons SET current_uses = current_uses + 1 WHERE id = $1", couponID); err != nil {
			return nil, status.Error(codes.Internal, "failed to record coupon use")
		}
	}
	return out, nil
}

// releaseCoupon returns the discount allowance an attendee row is holding.
// Clearing coupon_used makes the release idempotent, so a seat that is
// cancelled and later re-booked cannot decrement the counter twice. Callers
// hold the event row lock, which serialises the read-modify-write.
func releaseCoupon(ctx context.Context, q querier, eventID, attendeeID int64) error {
	var code sql.NullString
	if err := q.QueryRowContext(ctx,
		"SELECT coupon_used FROM event_attendees WHERE id = $1", attendeeID).Scan(&code); err != nil {
		return status.Error(codes.Internal, "failed to read coupon hold")
	}
	if !code.Valid || code.String == "" {
		return nil
	}

	if _, err := q.ExecContext(ctx,
		"UPDATE event_attendees SET coupon_used = NULL WHERE id = $1", attendeeID); err != nil {
		return status.Error(codes.Internal, "failed to clear coupon hold")
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE event_coupons SET current_uses = GREATEST(current_uses - 1, 0)
		WHERE event_id = $1 AND code = $2`, eventID, code.String); err != nil {
		return status.Error(codes.Internal, "failed to release coupon")
	}
	return nil
}

// seatsExhausted reports whether either the event-wide or tier capacity is
// already taken. Zero capacity means unlimited. An attendee's own existing row
// is excluded so a retry does not count against them twice.
func seatsExhausted(ctx context.Context, q querier, eventID, tierID int64, eventCap, tierCap int32, excludeID int64) (bool, error) {
	const q1 = `
		SELECT
			(SELECT COUNT(1) FROM event_attendees
			 WHERE event_id = $1 AND id <> $3 AND status IN ('confirmed', 'pending_payment')),
			(SELECT COUNT(1) FROM event_attendees
			 WHERE ticket_tier_id = $2 AND id <> $3 AND status IN ('confirmed', 'pending_payment'))`

	var eventTaken, tierTaken int32
	if err := q.QueryRowContext(ctx, q1, eventID, tierID, excludeID).Scan(&eventTaken, &tierTaken); err != nil {
		return false, status.Error(codes.Internal, "failed to evaluate capacity")
	}
	return (eventCap > 0 && eventTaken >= eventCap) || (tierCap > 0 && tierTaken >= tierCap), nil
}

// AttachPaymentOrder records the Razorpay order id created for a reservation.
func (db *eventDB) AttachPaymentOrder(ctx context.Context, attendeeID int64, orderID string, amount float64) error {
	_, err := db.db.ExecContext(ctx,
		`UPDATE event_attendees SET payment_order_id = $1, amount_paid = $2, updated_at = NOW() WHERE id = $3`,
		orderID, amount, attendeeID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to attach payment order: %v", err)
	}
	return nil
}

// ConfirmPayment settles a successful checkout and returns the attendee, or
// nil when the order is unknown or was already confirmed (webhooks retry).
func (db *eventDB) ConfirmPayment(ctx context.Context, orderID, paymentID string) (*PaymentResult, error) {
	out := &PaymentResult{}

	err := db.inTx(ctx, func(tx *sql.Tx) error {
		var attendeeID int64
		err := tx.QueryRowContext(ctx, `
			SELECT a.id, u.username, e.title, e.slug
			FROM event_attendees a
			JOIN user_account u ON u.id = a.user_id
			JOIN events e ON e.id = a.event_id
			WHERE a.payment_order_id = $1 AND a.status = $2
			FOR UPDATE OF a`,
			orderID, RSVPPendingPayment).Scan(&attendeeID, &out.Username, &out.EventTitle, &out.EventSlug)
		if err == sql.ErrNoRows {
			out = nil
			return nil
		}
		if err != nil {
			return status.Error(codes.Internal, "failed to load payment order")
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE event_attendees SET status = $1, payment_id = $2, updated_at = NOW() WHERE id = $3`,
			RSVPConfirmed, paymentID, attendeeID); err != nil {
			return status.Errorf(codes.Internal, "failed to confirm payment: %v", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FailPayment releases a reservation whose checkout failed.
func (db *eventDB) FailPayment(ctx context.Context, orderID string) error {
	_, err := db.db.ExecContext(ctx,
		`UPDATE event_attendees SET status = $1, updated_at = NOW()
		 WHERE payment_order_id = $2 AND status = $3`,
		RSVPCancelled, orderID, RSVPPendingPayment)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to release reservation: %v", err)
	}
	return nil
}

// CancelRSVP releases a seat and promotes the longest-waiting attendee into
// it. Promotion goes straight to confirmed for free tiers; paid tiers move to
// pending_payment so the promoted attendee can check out.
func (db *eventDB) CancelRSVP(ctx context.Context, req *pb.CancelRSVPReq) (*CancelResult, error) {
	out := &CancelResult{}

	err := db.inTx(ctx, func(tx *sql.Tx) error {
		userID, err := resolveAccount(ctx, tx, req.AccountId)
		if err != nil {
			return err
		}

		var eventID int64
		var endTime time.Time
		var eventStatus string
		if err := tx.QueryRowContext(ctx,
			"SELECT id, title, end_time, status FROM events WHERE slug = $1 FOR UPDATE", req.EventSlug).
			Scan(&eventID, &out.EventTitle, &endTime, &eventStatus); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "event not found")
			}
			return status.Error(codes.Internal, "failed to load event")
		}
		if eventHasEnded(eventStatus, endTime) {
			return status.Error(codes.FailedPrecondition, "event has ended")
		}

		var attendeeID int64
		var current string
		var paymentID sql.NullString
		if err := tx.QueryRowContext(ctx, `
			SELECT a.id, a.status, a.payment_id, COALESCE(a.amount_paid, 0), u.username
			FROM event_attendees a JOIN user_account u ON u.id = a.user_id
			WHERE a.event_id = $1 AND a.user_id = $2`,
			eventID, userID).Scan(&attendeeID, &current, &paymentID, &out.AmountPaid, &out.Username); err != nil {
			if err == sql.ErrNoRows {
				return status.Error(codes.NotFound, "you have not responded to this event")
			}
			return status.Error(codes.Internal, "failed to load rsvp")
		}
		if current == RSVPCancelled {
			return status.Error(codes.FailedPrecondition, "your rsvp is already cancelled")
		}

		// Only a settled payment is refundable.
		if current == RSVPConfirmed && paymentID.Valid {
			out.PaymentID = paymentID.String
		}

		if _, err := tx.ExecContext(ctx,
			"UPDATE event_attendees SET status = $1, updated_at = NOW() WHERE id = $2",
			RSVPCancelled, attendeeID); err != nil {
			return status.Errorf(codes.Internal, "failed to cancel rsvp: %v", err)
		}
		if err := releaseCoupon(ctx, tx, eventID, attendeeID); err != nil {
			return err
		}

		// A waitlisted seat was never occupied, so nothing frees up.
		if current == RSVPWaitlisted {
			return nil
		}
		return promoteFromWaitlist(ctx, tx, eventID, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// promoteFromWaitlist moves the oldest waitlisted attendee into the freed seat.
func promoteFromWaitlist(ctx context.Context, q querier, eventID int64, out *CancelResult) error {
	var promotedID, tierID int64
	var price float64
	err := q.QueryRowContext(ctx, `
		SELECT a.id, a.ticket_tier_id, t.price, u.username
		FROM event_attendees a
		JOIN event_ticket_tiers t ON t.id = a.ticket_tier_id
		JOIN user_account u ON u.id = a.user_id
		WHERE a.event_id = $1 AND a.status = $2
		ORDER BY a.created_at ASC
		LIMIT 1
		FOR UPDATE OF a SKIP LOCKED`, eventID, RSVPWaitlisted).
		Scan(&promotedID, &tierID, &price, &out.PromotedUsername)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return status.Error(codes.Internal, "failed to read waitlist")
	}

	out.PromotedStatus = RSVPConfirmed
	if price > 0 {
		out.PromotedStatus = RSVPPendingPayment
	}

	if _, err := q.ExecContext(ctx,
		"UPDATE event_attendees SET status = $1, updated_at = NOW() WHERE id = $2",
		out.PromotedStatus, promotedID); err != nil {
		return status.Errorf(codes.Internal, "failed to promote from waitlist: %v", err)
	}
	return nil
}

// ReleaseReservation cancels a hold that never reached checkout, for example
// when opening the payment order failed.
func (db *eventDB) ReleaseReservation(ctx context.Context, attendeeID int64) error {
	_, err := db.db.ExecContext(ctx,
		"UPDATE event_attendees SET status = $1, updated_at = NOW() WHERE id = $2 AND status = $3",
		RSVPCancelled, attendeeID, RSVPPendingPayment)
	return err
}

// Refund is one outstanding refund owed to an attendee.
type Refund struct {
	PaymentID string
	Amount    float64
	Username  string
}

// PendingRefunds lists cancelled attendees who paid but have not been
// refunded yet. Driving refunds off this query makes the payout retryable.
func (db *eventDB) PendingRefunds(ctx context.Context, slug string) ([]Refund, error) {
	rows, err := db.db.QueryContext(ctx, `
		SELECT a.payment_id, COALESCE(a.amount_paid, 0), u.username
		FROM event_attendees a
		JOIN user_account u ON u.id = a.user_id
		JOIN events e ON e.id = a.event_id
		WHERE e.slug = $1 AND a.status = $2
		  AND a.payment_id IS NOT NULL AND a.refund_id IS NULL
		  AND COALESCE(a.amount_paid, 0) > 0`, slug, RSVPCancelled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Refund
	for rows.Next() {
		var r Refund
		if err := rows.Scan(&r.PaymentID, &r.Amount, &r.Username); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkRefunded records the refund reference against a cancelled attendee.
func (db *eventDB) MarkRefunded(ctx context.Context, paymentID, refundID string) error {
	_, err := db.db.ExecContext(ctx,
		"UPDATE event_attendees SET refund_id = $1, updated_at = NOW() WHERE payment_id = $2",
		refundID, paymentID)
	return err
}

// ListAttendees returns the attendee roster for organizers and co-hosts.
func (db *eventDB) ListAttendees(ctx context.Context, req *pb.ListAttendeesReq) ([]*pb.Attendee, int32, error) {
	eventID, _, err := authorize(ctx, db.db, req.EventSlug, req.AccountId, permManageAttendees)
	if err != nil {
		return nil, 0, err
	}

	f := &filter{}
	f.add("a.event_id = $%d", eventID)
	if req.Status != "" {
		f.add("a.status = $%d", req.Status)
	}
	where := f.where()

	const from = `
		FROM event_attendees a
		JOIN user_account u ON u.id = a.user_id
		LEFT JOIN event_ticket_tiers t ON t.id = a.ticket_tier_id`

	var total int32
	if err := db.db.QueryRowContext(ctx, "SELECT COUNT(1)"+from+where, f.args...).Scan(&total); err != nil {
		return nil, 0, status.Errorf(codes.Internal, "failed to count attendees: %v", err)
	}

	limit := int32(50)
	if req.Limit > 0 && req.Limit <= 500 {
		limit = req.Limit
	}
	args := append(append([]any{}, f.args...), limit, req.Offset)

	query := fmt.Sprintf(`
		SELECT a.id, a.event_id, u.account_id, u.username, u.email, a.ticket_tier_id,
			COALESCE(t.name, ''), a.status, COALESCE(a.payment_id, ''),
			COALESCE(a.coupon_used, ''), a.checked_in, a.created_at
		%s%s ORDER BY a.created_at ASC LIMIT $%d OFFSET $%d`,
		from, where, len(args)-1, len(args))

	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, status.Errorf(codes.Internal, "failed to list attendees: %v", err)
	}
	defer rows.Close()

	out := make([]*pb.Attendee, 0, limit)
	for rows.Next() {
		var a pb.Attendee
		var created time.Time
		if err := rows.Scan(&a.Id, &a.EventId, &a.AccountId, &a.UserName, &a.UserEmail,
			&a.TicketTierId, &a.TicketTierName, &a.Status, &a.PaymentId,
			&a.CouponUsed, &a.CheckedIn, &created); err != nil {
			return nil, 0, status.Errorf(codes.Internal, "failed to scan attendee: %v", err)
		}
		a.CreatedAt = timestamppb.New(created)
		out = append(out, &a)
	}
	return out, total, rows.Err()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
