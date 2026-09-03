package database

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const paidSeriesRSVP = "RSVP each date separately for paid meetups."

func parseRSVPScope(scope string) (series bool, err error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "this":
		return false, nil
	case "series":
		return true, nil
	default:
		return false, status.Error(codes.InvalidArgument, "scope must be this or series")
	}
}

func SeriesRSVPMessage(saved, waitlisted int) string {
	if saved <= 1 {
		return ""
	}
	confirmed := saved - waitlisted
	switch {
	case waitlisted == saved:
		return "Those dates are full. You're on the waitlist for " + strconv.Itoa(saved) + " dates."
	case waitlisted > 0 && confirmed > 0:
		return "You're in for " + strconv.Itoa(confirmed) + " dates; " + strconv.Itoa(waitlisted) + " are waitlisted."
	default:
		return "You're in for " + strconv.Itoa(saved) + " upcoming dates."
	}
}

// rsvpSeriesUpcoming confirms a free seat on every open future occurrence in
// the clicked event's series. Paid tiers are refused. Full dates are
// waitlisted independently; already-going / closed / ended dates are skipped.
func rsvpSeriesUpcoming(ctx context.Context, tx *sql.Tx, req *pb.RSVPReq, userID int64, out *RSVPResult) error {
	var clickedID, organizerID int64
	var seriesID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT id, organizer_id, series_id, title, slug
		FROM events WHERE slug = $1`, req.EventSlug,
	).Scan(&clickedID, &organizerID, &seriesID, &out.EventTitle, &out.EventSlug); err != nil {
		if err == sql.ErrNoRows {
			return status.Error(codes.NotFound, "event not found")
		}
		return status.Error(codes.Internal, "failed to load event")
	}
	if !seriesID.Valid {
		return status.Error(codes.InvalidArgument, "this meetup is not part of a series")
	}
	if userID == organizerID {
		return status.Error(codes.FailedPrecondition, "the organizer is already attending")
	}

	var tierName string
	var price float64
	if err := tx.QueryRowContext(ctx, `
		SELECT name, price, COALESCE(currency, $1)
		FROM event_ticket_tiers WHERE id = $2 AND event_id = $3`,
		defaultCurrency, req.TicketTierId, clickedID,
	).Scan(&tierName, &price, &out.Currency); err != nil {
		if err == sql.ErrNoRows {
			return status.Error(codes.NotFound, "ticket tier not found for this event")
		}
		return status.Error(codes.Internal, "failed to load ticket tier")
	}
	if price > 0 {
		return status.Error(codes.FailedPrecondition, paidSeriesRSVP)
	}

	if err := tx.QueryRowContext(ctx,
		"SELECT username FROM user_account WHERE id = $1", userID).Scan(&out.Username); err != nil {
		return status.Error(codes.Internal, "failed to load attendee")
	}
	if err := tx.QueryRowContext(ctx,
		"SELECT username FROM user_account WHERE id = $1", organizerID).Scan(&out.OrganizerUsername); err != nil {
		return status.Error(codes.Internal, "failed to load organizer")
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT e.id, e.status, e.capacity, e.end_time, e.rsvp_closes_at
		FROM events e
		WHERE e.series_id = $1
		  AND e.status IN ($2, $3)
		  AND e.end_time >= NOW()
		ORDER BY e.id
		FOR UPDATE`,
		seriesID.Int64, StatusPublished, StatusLive)
	if err != nil {
		return status.Error(codes.Internal, "failed to load series dates")
	}
	defer rows.Close()

	type occRow struct {
		id         int64
		status     string
		capacity   int32
		endTime    time.Time
		rsvpCloses sql.NullTime
	}
	var list []occRow
	clickedSeen := false
	for rows.Next() {
		var o occRow
		if err := rows.Scan(&o.id, &o.status, &o.capacity, &o.endTime, &o.rsvpCloses); err != nil {
			return status.Error(codes.Internal, "failed to load series dates")
		}
		if o.id == clickedID {
			clickedSeen = true
		}
		list = append(list, o)
	}
	if err := rows.Err(); err != nil {
		return status.Error(codes.Internal, "failed to load series dates")
	}
	if !clickedSeen {
		return status.Error(codes.FailedPrecondition, "no upcoming dates left to RSVP")
	}

	var clickedAlready bool
	for _, o := range list {
		if eventHasEnded(o.status, o.endTime) || rsvpHasClosed(o.rsvpCloses) {
			continue
		}
		tierID := req.TicketTierId
		var tierCap int32
		if o.id != clickedID {
			id, cap, err := siblingFreeTier(ctx, tx, o.id, tierName)
			if err != nil {
				return err
			}
			if id == 0 {
				continue
			}
			tierID, tierCap = id, cap
		} else if err := tx.QueryRowContext(ctx,
			"SELECT capacity FROM event_ticket_tiers WHERE id = $1 AND event_id = $2",
			req.TicketTierId, o.id).Scan(&tierCap); err != nil {
			return status.Error(codes.Internal, "failed to load ticket tier")
		}

		seat, err := applyRSVPSeat(ctx, tx, userID, o.id, tierID, o.capacity, tierCap, 0, out.Currency, "")
		if err != nil {
			return err
		}
		if o.id == clickedID {
			out.AttendeeID = seat.AttendeeID
			out.Status = seat.Status
			out.AmountDue = 0
			clickedAlready = seat.Already
		}
		if seat.Already {
			continue
		}
		out.DatesSaved++
		if seat.Status == RSVPWaitlisted {
			out.WaitlistedDates++
		}
	}

	if out.DatesSaved == 0 {
		if clickedAlready {
			return status.Error(codes.AlreadyExists, "you have already responded to this event")
		}
		return status.Error(codes.FailedPrecondition, "no upcoming dates left to RSVP")
	}
	if out.Status == "" {
		if out.WaitlistedDates == out.DatesSaved {
			out.Status = RSVPWaitlisted
		} else {
			out.Status = RSVPConfirmed
		}
	}
	return nil
}

func siblingFreeTier(ctx context.Context, tx *sql.Tx, eventID int64, name string) (id int64, cap int32, err error) {
	var price float64
	err = tx.QueryRowContext(ctx, `
		SELECT id, capacity, price FROM event_ticket_tiers
		WHERE event_id = $1 AND lower(btrim(name)) = lower(btrim($2))
		ORDER BY sort_order ASC, id ASC
		LIMIT 1`, eventID, name).Scan(&id, &cap, &price)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, status.Error(codes.Internal, "failed to load ticket tier")
	}
	if price > 0 {
		return 0, 0, nil
	}
	return id, cap, nil
}
