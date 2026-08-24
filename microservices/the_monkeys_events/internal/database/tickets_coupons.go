package database

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultCurrency = "INR"

// -----------------------------------------------------------------------------
// Ticket tiers
// -----------------------------------------------------------------------------

// insertTier writes one tier. Shared by CreateEvent's inline tiers and the
// standalone CreateTicketTier RPC.
func insertTier(ctx context.Context, q querier, eventID int64, in *pb.TicketTierInput, sortOrder int32) (*pb.TicketTier, error) {
	if in == nil || strings.TrimSpace(in.Name) == "" {
		return nil, status.Error(codes.InvalidArgument, "ticket tier name is required")
	}
	if in.Price < 0 {
		return nil, status.Error(codes.InvalidArgument, "ticket price cannot be negative")
	}
	if in.SortOrder > 0 {
		sortOrder = in.SortOrder
	}

	currency := in.Currency
	if currency == "" {
		currency = defaultCurrency
	}

	tier := &pb.TicketTier{
		EventId:     eventID,
		Name:        in.Name,
		Description: in.Description,
		Price:       in.Price,
		Currency:    currency,
		Capacity:    in.Capacity,
		SortOrder:   sortOrder,
	}
	if err := q.QueryRowContext(ctx, `
		INSERT INTO event_ticket_tiers (event_id, name, description, price, currency, capacity, sort_order)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		eventID, tier.Name, tier.Description, tier.Price, tier.Currency, tier.Capacity, tier.SortOrder,
	).Scan(&tier.Id); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create ticket tier: %v", err)
	}
	return tier, nil
}

func (db *eventDB) CreateTicketTier(ctx context.Context, req *pb.CreateTicketTierReq) (*pb.TicketTier, error) {
	var tier *pb.TicketTier
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, actorID, err := authorize(ctx, tx, req.EventSlug, req.AccountId, permManageTickets)
		if err != nil {
			return err
		}
		if req.Tier.GetPrice() > 0 {
			if err := requireVerified(ctx, tx, actorID); err != nil {
				return err
			}
		}

		var next int32
		if err := tx.QueryRowContext(ctx,
			"SELECT COALESCE(MAX(sort_order), -1) + 1 FROM event_ticket_tiers WHERE event_id = $1",
			eventID).Scan(&next); err != nil {
			return status.Error(codes.Internal, "failed to compute tier order")
		}

		tier, err = insertTier(ctx, tx, eventID, req.Tier, next)
		return err
	})
	return tier, err
}

func (db *eventDB) UpdateTicketTier(ctx context.Context, req *pb.UpdateTicketTierReq) (*pb.TicketTier, error) {
	var tier *pb.TicketTier
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, actorID, err := authorize(ctx, tx, req.EventSlug, req.AccountId, permManageTickets)
		if err != nil {
			return err
		}
		if req.Tier == nil {
			return status.Error(codes.InvalidArgument, "tier payload is required")
		}
		if req.Tier.Price > 0 {
			if err := requireVerified(ctx, tx, actorID); err != nil {
				return err
			}
		}

		// Capacity may not drop below what has already been taken.
		var booked int32
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM event_attendees
			WHERE ticket_tier_id = $1 AND status IN ('confirmed', 'pending_payment')`,
			req.TierId).Scan(&booked); err != nil {
			return status.Error(codes.Internal, "failed to count tier bookings")
		}
		if req.Tier.Capacity > 0 && req.Tier.Capacity < booked {
			return status.Errorf(codes.FailedPrecondition,
				"capacity cannot be below the %d tickets already taken", booked)
		}

		currency := req.Tier.Currency
		if currency == "" {
			currency = defaultCurrency
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE event_ticket_tiers
			SET name = $1, description = $2, price = $3, currency = $4, capacity = $5, sort_order = $6
			WHERE id = $7 AND event_id = $8`,
			req.Tier.Name, req.Tier.Description, req.Tier.Price, currency,
			req.Tier.Capacity, req.Tier.SortOrder, req.TierId, eventID)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to update ticket tier: %v", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return status.Error(codes.NotFound, "ticket tier not found")
		}

		tier = &pb.TicketTier{
			Id: req.TierId, EventId: eventID, Name: req.Tier.Name,
			Description: req.Tier.Description, Price: req.Tier.Price, Currency: currency,
			Capacity: req.Tier.Capacity, SortOrder: req.Tier.SortOrder, Booked: booked,
		}
		return nil
	})
	return tier, err
}

func (db *eventDB) DeleteTicketTier(ctx context.Context, req *pb.TierActionReq) error {
	return db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, _, err := authorize(ctx, tx, req.EventSlug, req.AccountId, permManageTickets)
		if err != nil {
			return err
		}

		var booked int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM event_attendees
			WHERE ticket_tier_id = $1 AND status IN ('confirmed', 'pending_payment', 'waitlisted')`,
			req.TierId).Scan(&booked); err != nil {
			return status.Error(codes.Internal, "failed to count tier bookings")
		}
		if booked > 0 {
			return status.Error(codes.FailedPrecondition, "ticket tier already has attendees")
		}

		res, err := tx.ExecContext(ctx,
			"DELETE FROM event_ticket_tiers WHERE id = $1 AND event_id = $2", req.TierId, eventID)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to delete ticket tier: %v", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return status.Error(codes.NotFound, "ticket tier not found")
		}
		return nil
	})
}

// -----------------------------------------------------------------------------
// Coupons
// -----------------------------------------------------------------------------

func (db *eventDB) CreateCoupon(ctx context.Context, req *pb.CreateCouponReq) (*pb.Coupon, error) {
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		return nil, status.Error(codes.InvalidArgument, "coupon code is required")
	}
	if req.DiscountPercent <= 0 || req.DiscountPercent > 100 {
		return nil, status.Error(codes.InvalidArgument, "discount_percent must be between 1 and 100")
	}

	coupon := &pb.Coupon{
		Code:            code,
		DiscountPercent: req.DiscountPercent,
		MaxUses:         req.MaxUses,
		ValidFrom:       req.ValidFrom,
		ValidTo:         req.ValidTo,
	}

	err := db.inTx(ctx, func(tx *sql.Tx) error {
		eventID, _, err := authorize(ctx, tx, req.EventSlug, req.AccountId, permManageCoupons)
		if err != nil {
			return err
		}
		coupon.EventId = eventID

		if err := tx.QueryRowContext(ctx, `
			INSERT INTO event_coupons (event_id, code, discount_percent, max_uses, valid_from, valid_to)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			eventID, code, req.DiscountPercent, req.MaxUses,
			nullableTime(req.ValidFrom), nullableTime(req.ValidTo),
		).Scan(&coupon.Id); err != nil {
			return status.Errorf(codes.AlreadyExists, "failed to create coupon: %v", err)
		}
		return nil
	})
	return coupon, err
}

func (db *eventDB) ListCoupons(ctx context.Context, req *pb.ListCouponsReq) ([]*pb.Coupon, error) {
	eventID, _, err := authorize(ctx, db.db, req.EventSlug, req.AccountId, permManageCoupons)
	if err != nil {
		return nil, err
	}

	rows, err := db.db.QueryContext(ctx, `
		SELECT id, code, discount_percent, max_uses, current_uses, valid_from, valid_to
		FROM event_coupons WHERE event_id = $1 ORDER BY id`, eventID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list coupons: %v", err)
	}
	defer rows.Close()

	out := make([]*pb.Coupon, 0)
	for rows.Next() {
		c := &pb.Coupon{EventId: eventID}
		var from, to sql.NullTime
		if err := rows.Scan(&c.Id, &c.Code, &c.DiscountPercent, &c.MaxUses, &c.CurrentUses, &from, &to); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan coupon: %v", err)
		}
		c.ValidFrom, c.ValidTo = fromNullTime(from), fromNullTime(to)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (db *eventDB) DeleteCoupon(ctx context.Context, req *pb.CouponActionReq) error {
	eventID, _, err := authorize(ctx, db.db, req.EventSlug, req.AccountId, permManageCoupons)
	if err != nil {
		return err
	}
	res, err := db.db.ExecContext(ctx,
		"DELETE FROM event_coupons WHERE id = $1 AND event_id = $2", req.CouponId, eventID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to delete coupon: %v", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return status.Error(codes.NotFound, "coupon not found")
	}
	return nil
}

// validateCoupon checks a code is live and not exhausted, returning the row so
// the caller can apply the discount and increment usage in the same
// transaction.
func validateCoupon(ctx context.Context, q querier, eventID int64, code string) (*pb.Coupon, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	c := &pb.Coupon{EventId: eventID, Code: code}

	var from, to sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT id, discount_percent, max_uses, current_uses, valid_from, valid_to
		FROM event_coupons WHERE event_id = $1 AND code = $2`,
		eventID, code).Scan(&c.Id, &c.DiscountPercent, &c.MaxUses, &c.CurrentUses, &from, &to)
	if err == sql.ErrNoRows {
		return nil, status.Error(codes.NotFound, "invalid coupon code")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load coupon")
	}

	now := time.Now()
	if from.Valid && now.Before(from.Time) {
		return nil, status.Error(codes.FailedPrecondition, "coupon is not yet valid")
	}
	if to.Valid && now.After(to.Time) {
		return nil, status.Error(codes.FailedPrecondition, "coupon has expired")
	}
	if c.MaxUses > 0 && c.CurrentUses >= c.MaxUses {
		return nil, status.Error(codes.ResourceExhausted, "coupon has reached its usage limit")
	}

	c.ValidFrom, c.ValidTo = fromNullTime(from), fromNullTime(to)
	return c, nil
}

// ValidateCoupon exposes coupon validation to the API, optionally quoting the
// discounted price for a specific tier.
func (db *eventDB) ValidateCoupon(ctx context.Context, req *pb.ValidateCouponReq) (*pb.Coupon, float64, error) {
	eventID, _, err := resolveEvent(ctx, db.db, req.EventSlug)
	if err != nil {
		return nil, 0, err
	}
	coupon, err := validateCoupon(ctx, db.db, eventID, req.Code)
	if err != nil {
		return nil, 0, err
	}

	if req.TicketTierId == 0 {
		return coupon, 0, nil
	}
	var price float64
	if err := db.db.QueryRowContext(ctx,
		"SELECT price FROM event_ticket_tiers WHERE id = $1 AND event_id = $2",
		req.TicketTierId, eventID).Scan(&price); err != nil {
		return nil, 0, status.Error(codes.NotFound, "ticket tier not found")
	}
	return coupon, applyDiscount(price, coupon.DiscountPercent), nil
}

// applyDiscount returns the payable amount rounded to two decimals.
func applyDiscount(price, discountPercent float64) float64 {
	if price <= 0 || discountPercent <= 0 {
		return price
	}
	amount := price * (1 - discountPercent/100)
	if amount < 0 {
		amount = 0
	}
	return float64(int64(amount*100+0.5)) / 100
}

func nullableTime(ts *timestamppb.Timestamp) any {
	if ts == nil || !ts.IsValid() || ts.AsTime().IsZero() {
		return nil
	}
	return ts.AsTime()
}

func fromNullTime(t sql.NullTime) *timestamppb.Timestamp {
	if !t.Valid {
		return nil
	}
	return timestamppb.New(t.Time)
}
