package services

import (
	"context"
	"fmt"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/money"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// notificationRoutingKey is the index of the notification consumer's key in
// the shared RabbitMQ routing key list.
const notificationRoutingKey = 4

// storageRoutingKey is queue/key 0 — the storage consumer (same as account delete).
const storageRoutingKey = 0

type EventService struct {
	pb.UnimplementedEventServiceServer
	db    database.EventDB
	log   *zap.SugaredLogger
	cfg   *config.Config
	qConn *rabbitmq.ConnManager
	pay   *razorpay
}

const paidTicketsDisabled = "Paid tickets aren't enabled yet. Add a free ticket, or use ₹0 until payments are turned on."

func (s *EventService) requirePayments(price float64) error {
	if price <= 0 || s.pay.enabled() {
		return nil
	}
	s.log.Warnw("razorpay disabled")
	return status.Error(codes.FailedPrecondition, paidTicketsDisabled)
}

func (s *EventService) rejectIfPaymentsDisabled() error {
	return s.requirePayments(1)
}

func hasPaidTier(tiers []*pb.TicketTierInput) bool {
	for _, t := range tiers {
		if t != nil && t.Price > 0 {
			return true
		}
	}
	return false
}

func NewEventService(db database.EventDB, log *zap.SugaredLogger, cfg *config.Config, qConn *rabbitmq.ConnManager) *EventService {
	return &EventService{
		db:    db,
		log:   log,
		cfg:   cfg,
		qConn: qConn,
		pay:   newRazorpay(cfg.Keys.RazorpayKeyID, cfg.Keys.RazorpaySecret, cfg.Keys.RazorpayWebhookSecret),
	}
}

// -----------------------------------------------------------------------------
// Event CRUD
// -----------------------------------------------------------------------------

func (s *EventService) CreateEvent(ctx context.Context, req *pb.CreateEventReq) (*pb.EventResp, error) {
	if hasPaidTier(req.TicketTiers) {
		if err := s.rejectIfPaymentsDisabled(); err != nil {
			return nil, err
		}
	}
	event, err := s.db.CreateEvent(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.EventResp{Message: "event created as draft", Event: event}, nil
}

func (s *EventService) UpdateEvent(ctx context.Context, req *pb.UpdateEventReq) (*pb.EventResp, error) {
	event, err := s.db.UpdateEvent(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.EventResp{Message: "event updated", Event: event}, nil
}

func (s *EventService) DeleteEvent(ctx context.Context, req *pb.EventActionReq) (*pb.BasicResp, error) {
	if err := s.db.DeleteEvent(ctx, req); err != nil {
		return nil, err
	}
	s.publishStorageDelete(constants.EVENT_DELETE, req.GetSlug(), "")
	return &pb.BasicResp{Message: "event deleted", Success: true}, nil
}

func (s *EventService) AdminDeleteEvent(ctx context.Context, req *pb.AdminEventActionReq) (*pb.BasicResp, error) {
	if err := s.db.AdminDeleteEvent(ctx, req); err != nil {
		return nil, err
	}
	s.publishStorageDelete(constants.EVENT_DELETE, req.GetSlug(), "")
	return &pb.BasicResp{Message: "event deleted", Success: true}, nil
}

func (s *EventService) CheckUserEventRemoval(ctx context.Context, req *pb.AccountIdReq) (*pb.UserRemovalCheckResp, error) {
	slugs, err := s.db.CheckUserEventRemoval(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	if len(slugs) == 0 {
		return &pb.UserRemovalCheckResp{Allowed: true}, nil
	}
	return &pb.UserRemovalCheckResp{
		Allowed:       false,
		Reason:        "account still has captured or pending event payments; cancel or settle them first",
		BlockingSlugs: slugs,
	}, nil
}

func (s *EventService) RemoveUserFromEvents(ctx context.Context, req *pb.AccountIdReq) (*pb.BasicResp, error) {
	slugs, err := s.db.RemoveUserFromEvents(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	for _, slug := range slugs {
		s.publishStorageDelete(constants.EVENT_DELETE, slug, "")
	}
	return &pb.BasicResp{Message: "user removed from events", Success: true}, nil
}

// PublishEvent opens the event for RSVPs and tells the organizer's followers.
func (s *EventService) PublishEvent(ctx context.Context, req *pb.EventActionReq) (*pb.EventResp, error) {
	event, err := s.db.SetEventStatus(ctx, req, database.StatusPublished)
	if err != nil {
		return nil, err
	}

	followers, err := s.db.FollowerUsernames(ctx, req.Slug)
	if err != nil {
		s.log.Warnw("failed to load followers for event announcement", "slug", req.Slug, "err", err)
	} else {
		s.notifyAll(followers, eventNotification{
			Username:     event.OrganizerUsername,
			Action:       constants.EVENT_NEW_BY_FOLLOWED,
			Notification: fmt.Sprintf("%s scheduled a new event: %s", event.OrganizerUsername, event.Title),
			EventSlug:    event.Slug,
			EventTitle:   event.Title,
		})
	}

	return &pb.EventResp{Message: "event published", Event: event}, nil
}

// CancelEvent releases every RSVP, refunds paid attendees and notifies them.
func (s *EventService) CancelEvent(ctx context.Context, req *pb.EventActionReq) (*pb.EventResp, error) {
	attendees, err := s.db.AttendeeUsernames(ctx, req.Slug)
	if err != nil {
		s.log.Warnw("failed to load attendees before cancellation", "slug", req.Slug, "err", err)
	}

	event, err := s.db.SetEventStatus(ctx, req, database.StatusCancelled)
	if err != nil {
		return nil, err
	}

	s.notifyAll(attendees, eventNotification{
		Username:     event.OrganizerUsername,
		Action:       constants.EVENT_CANCELLED,
		Notification: fmt.Sprintf("%s has been cancelled", event.Title),
		EventSlug:    event.Slug,
		EventTitle:   event.Title,
	})

	go s.refundAll(context.WithoutCancel(ctx), event.Slug, event.Title)

	return &pb.EventResp{Message: "event cancelled", Event: event}, nil
}

func (s *EventService) GetEvent(ctx context.Context, req *pb.GetEventReq) (*pb.EventResp, error) {
	event, viewerStatus, err := s.db.GetEvent(ctx, req.Slug, req.AccountId)
	if err != nil {
		return nil, err
	}
	if err := s.redactForViewer(ctx, event, req.AccountId, viewerStatus); err != nil {
		return nil, err
	}
	return &pb.EventResp{Event: event, ViewerRsvpStatus: viewerStatus}, nil
}

func applyEventRedaction(event *pb.Event, isHost bool, viewerStatus string) error {
	if event.Status == "draft" && !isHost {
		return status.Error(codes.NotFound, "event not found")
	}
	if !isHost && viewerStatus != "confirmed" {
		event.MeetingLink = ""
	}
	return nil
}

// redactForViewer enforces the two things a raw event row does not know about
// the person reading it: a draft belongs to its hosts, and the join link
// belongs to people holding a ticket. Handing the link to every visitor on the
// detail page would make the ticket optional.
//
// Host standing is organizer match plus a single co-host EXISTS — not the
// full Authorize RPC used on write paths.
func (s *EventService) redactForViewer(ctx context.Context, event *pb.Event, accountID, viewerStatus string) error {
	isHost, err := s.db.ViewerIsHost(ctx, event.Id, event.OrganizerAccountId, accountID)
	if err != nil {
		return err
	}
	return applyEventRedaction(event, isHost, viewerStatus)
}

func (s *EventService) ListEvents(ctx context.Context, req *pb.ListEventsReq) (*pb.ListEventsResp, error) {
	return listResp(s.db.ListEvents(ctx, req))
}

func (s *EventService) GetUserEvents(ctx context.Context, req *pb.ListEventsReq) (*pb.ListEventsResp, error) {
	return listResp(s.db.GetUserEvents(ctx, req))
}

func (s *EventService) GetUserAttendingEvents(ctx context.Context, req *pb.ListEventsReq) (*pb.ListEventsResp, error) {
	return listResp(s.db.GetUserAttendingEvents(ctx, req))
}

func (s *EventService) GetGroupEvents(ctx context.Context, req *pb.ListEventsReq) (*pb.ListEventsResp, error) {
	return listResp(s.db.GetGroupEvents(ctx, req))
}

func listResp(events []*pb.Event, total int32, err error) (*pb.ListEventsResp, error) {
	if err != nil {
		return nil, err
	}
	return &pb.ListEventsResp{Events: events, Total: total}, nil
}

// -----------------------------------------------------------------------------
// Ticket tiers & coupons
// -----------------------------------------------------------------------------

func (s *EventService) CreateTicketTier(ctx context.Context, req *pb.CreateTicketTierReq) (*pb.TicketTierResp, error) {
	if err := s.requirePayments(req.Tier.GetPrice()); err != nil {
		return nil, err
	}
	tier, err := s.db.CreateTicketTier(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.TicketTierResp{Message: "ticket tier created", Tier: tier}, nil
}

func (s *EventService) UpdateTicketTier(ctx context.Context, req *pb.UpdateTicketTierReq) (*pb.TicketTierResp, error) {
	if err := s.requirePayments(req.Tier.GetPrice()); err != nil {
		return nil, err
	}
	tier, err := s.db.UpdateTicketTier(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.TicketTierResp{Message: "ticket tier updated", Tier: tier}, nil
}

func (s *EventService) DeleteTicketTier(ctx context.Context, req *pb.TierActionReq) (*pb.BasicResp, error) {
	if err := s.db.DeleteTicketTier(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "ticket tier deleted", Success: true}, nil
}

func (s *EventService) CreateCoupon(ctx context.Context, req *pb.CreateCouponReq) (*pb.CouponResp, error) {
	coupon, err := s.db.CreateCoupon(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.CouponResp{Message: "coupon created", Coupon: coupon}, nil
}

func (s *EventService) ListCoupons(ctx context.Context, req *pb.ListCouponsReq) (*pb.ListCouponsResp, error) {
	coupons, err := s.db.ListCoupons(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.ListCouponsResp{Coupons: coupons}, nil
}

func (s *EventService) DeleteCoupon(ctx context.Context, req *pb.CouponActionReq) (*pb.BasicResp, error) {
	if err := s.db.DeleteCoupon(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "coupon deleted", Success: true}, nil
}

func (s *EventService) ValidateCoupon(ctx context.Context, req *pb.ValidateCouponReq) (*pb.CouponResp, error) {
	coupon, amount, err := s.db.ValidateCoupon(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.CouponResp{Message: "coupon is valid", Coupon: coupon, DiscountedAmount: amount}, nil
}

// -----------------------------------------------------------------------------
// RSVP & payments
// -----------------------------------------------------------------------------

// RSVPEvent reserves a seat. Free and waitlisted responses settle immediately;
// paid tiers return a Razorpay order for the client to complete.
func (s *EventService) RSVPEvent(ctx context.Context, req *pb.RSVPReq) (*pb.RSVPResp, error) {
	result, err := s.db.CreateRSVP(ctx, req)
	if err != nil {
		return nil, err
	}

	resp := &pb.RSVPResp{Status: result.Status, Currency: result.Currency}

	switch result.Status {
	case database.RSVPConfirmed:
		resp.Message = "your spot is confirmed"
		s.notify(eventNotification{
			NewUsername:  result.Username,
			Username:     result.OrganizerUsername,
			Action:       constants.EVENT_RSVP_CONFIRMED,
			Notification: fmt.Sprintf("You are going to %s", result.EventTitle),
			EventSlug:    result.EventSlug,
			EventTitle:   result.EventTitle,
		})

	case database.RSVPWaitlisted:
		resp.Message = "the event is full, you are on the waitlist"
		s.notify(eventNotification{
			NewUsername:  result.Username,
			Username:     result.OrganizerUsername,
			Action:       constants.EVENT_RSVP_WAITLISTED,
			Notification: fmt.Sprintf("You are on the waitlist for %s", result.EventTitle),
			EventSlug:    result.EventSlug,
			EventTitle:   result.EventTitle,
		})

	case database.RSVPPendingHostReview:
		resp.Message = "waiting for host approval"

	case database.RSVPPendingPayment:
		if err := s.startCheckout(ctx, result.AttendeeID, result.AmountDue, resp); err != nil {
			return nil, err
		}
	}

	if msg := database.SeriesRSVPMessage(result.DatesSaved, result.WaitlistedDates); msg != "" {
		resp.Message = msg
	}

	return resp, nil
}

func (s *EventService) startCheckout(ctx context.Context, attendeeID int64, amountDue float64, resp *pb.RSVPResp) error {
	if err := s.requirePayments(amountDue); err != nil {
		_ = s.db.ReleaseReservation(ctx, attendeeID)
		return err
	}
	duePaise := money.ToPaise(amountDue)
	orderID, err := s.pay.createOrder(ctx, duePaise, money.CurrencyINR,
		fmt.Sprintf("evt-rsvp-%d", attendeeID))
	if err != nil {
		_ = s.db.ReleaseReservation(ctx, attendeeID)
		s.log.Errorw("failed to create payment order", "attendee", attendeeID, "err", err)
		return status.Error(codes.Unavailable, "could not start the payment, please try again")
	}
	if err := s.db.AttachPaymentOrder(ctx, attendeeID, orderID, amountDue); err != nil {
		return err
	}
	resp.Message = "complete the payment to confirm your spot"
	resp.PaymentOrderId = orderID
	resp.AmountDue = amountDue
	resp.RazorpayKeyId = s.cfg.Keys.RazorpayKeyID
	return nil
}

// ReviewRSVP lets the organizer or a co-host approve or reject a guest.
// Unpaid meetups confirm immediately; paid ones return a Razorpay order.
func (s *EventService) ReviewRSVP(ctx context.Context, req *pb.ReviewRSVPReq) (*pb.RSVPResp, error) {
	result, err := s.db.ReviewRSVP(ctx, req)
	if err != nil {
		return nil, err
	}

	resp := &pb.RSVPResp{Status: result.Status, Currency: result.Currency}
	switch result.Status {
	case database.RSVPCancelled:
		resp.Message = "application declined"
	case database.RSVPConfirmed:
		resp.Message = "guest approved"
	case database.RSVPPendingPayment:
		if err := s.startCheckout(ctx, result.AttendeeID, result.AmountDue, resp); err != nil {
			return nil, err
		}
		resp.Message = "guest approved — they need to complete payment"
	}
	return resp, nil
}

// CancelRSVP releases the seat, refunds any captured payment and promotes the
// next person waiting.
func (s *EventService) CancelRSVP(ctx context.Context, req *pb.CancelRSVPReq) (*pb.BasicResp, error) {
	result, err := s.db.CancelRSVP(ctx, req)
	if err != nil {
		return nil, err
	}

	if result.PromotedUsername != "" {
		message := fmt.Sprintf("A spot opened up — you are in for %s", result.EventTitle)
		if result.PromotedStatus == database.RSVPPendingPayment {
			message = fmt.Sprintf("A spot opened up for %s — complete payment to confirm", result.EventTitle)
		}
		s.notify(eventNotification{
			NewUsername:  result.PromotedUsername,
			Username:     result.Username,
			Action:       constants.EVENT_WAITLIST_PROMOTED,
			Notification: message,
			EventSlug:    req.EventSlug,
			EventTitle:   result.EventTitle,
		})
	}

	if result.PaymentID != "" {
		go s.refundAll(context.WithoutCancel(ctx), req.EventSlug, result.EventTitle)
	}

	return &pb.BasicResp{Message: "your rsvp has been cancelled", Success: true}, nil
}

// ProcessPaymentWebhook settles or releases a reservation from Razorpay's
// callback. The signature is verified against the untouched request body.
func (s *EventService) ProcessPaymentWebhook(ctx context.Context, req *pb.PaymentWebhookReq) (*pb.BasicResp, error) {
	if !s.pay.verifyWebhook(req.RawBody, req.Signature) {
		return nil, status.Error(codes.PermissionDenied, "invalid webhook signature")
	}

	hook, err := parseWebhook(req.RawBody)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	entity := hook.Payload.Payment.Entity
	if entity.OrderID == "" {
		return &pb.BasicResp{Message: "ignored: no order reference", Success: true}, nil
	}

	switch hook.Event {
	case "payment.captured":
		result, err := s.db.ConfirmPayment(ctx, entity.OrderID, entity.ID)
		if err != nil {
			return nil, err
		}
		if result == nil {
			// Razorpay retries; an already-settled order is not an error.
			return &pb.BasicResp{Message: "already processed", Success: true}, nil
		}
		s.notify(eventNotification{
			NewUsername:  result.Username,
			Action:       constants.EVENT_RSVP_CONFIRMED,
			Notification: fmt.Sprintf("Payment received — you are going to %s", result.EventTitle),
			EventSlug:    result.EventSlug,
			EventTitle:   result.EventTitle,
		})

	case "payment.failed":
		if err := s.db.FailPayment(ctx, entity.OrderID); err != nil {
			return nil, err
		}

	default:
		return &pb.BasicResp{Message: "ignored: unhandled event", Success: true}, nil
	}

	return &pb.BasicResp{Message: "processed", Success: true}, nil
}

// refundAll settles every outstanding refund for an event. It is safe to
// re-run: each refunded attendee drops out of PendingRefunds.
func (s *EventService) refundAll(ctx context.Context, slug, title string) {
	if !s.pay.enabled() {
		return
	}
	refunds, err := s.db.PendingRefunds(ctx, slug)
	if err != nil {
		s.log.Errorw("failed to load pending refunds", "slug", slug, "err", err)
		return
	}

	for _, refund := range refunds {
		refundID, err := s.pay.refund(ctx, refund.PaymentID, refund.AmountPaise)
		if err != nil {
			s.log.Errorw("refund failed", "slug", slug, "payment", refund.PaymentID, "err", err)
			continue
		}
		if err := s.db.MarkRefunded(ctx, refund.PaymentID, refundID); err != nil {
			s.log.Errorw("failed to record refund", "payment", refund.PaymentID, "err", err)
		}
		s.notify(eventNotification{
			NewUsername:  refund.Username,
			Action:       constants.EVENT_PAYMENT_REFUND,
			Notification: fmt.Sprintf("Your ticket for %s has been refunded", title),
			EventSlug:    slug,
			EventTitle:   title,
		})
	}
}

func (s *EventService) GetAttendees(ctx context.Context, req *pb.ListAttendeesReq) (*pb.ListAttendeesResp, error) {
	attendees, total, err := s.db.ListAttendees(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.ListAttendeesResp{Attendees: attendees, Total: total}, nil
}

// -----------------------------------------------------------------------------
// Social
// -----------------------------------------------------------------------------

func (s *EventService) AddEventComment(ctx context.Context, req *pb.AddCommentReq) (*pb.CommentResp, error) {
	result, err := s.db.AddComment(ctx, req)
	if err != nil {
		return nil, err
	}

	s.notify(eventNotification{
		NewUsername:  result.OrganizerUsername,
		Username:     result.Comment.UserName,
		Action:       constants.EVENT_COMMENT_NEW,
		Notification: fmt.Sprintf("%s commented on %s", result.Comment.UserName, result.EventTitle),
		EventSlug:    req.EventSlug,
		EventTitle:   result.EventTitle,
	})

	return &pb.CommentResp{Message: "comment added", Comment: result.Comment}, nil
}

func (s *EventService) ListEventComments(ctx context.Context, req *pb.ListCommentsReq) (*pb.ListCommentsResp, error) {
	comments, total, err := s.db.ListComments(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.ListCommentsResp{Comments: comments, Total: total}, nil
}

func (s *EventService) DeleteEventComment(ctx context.Context, req *pb.DeleteCommentReq) (*pb.BasicResp, error) {
	if err := s.db.DeleteComment(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "comment deleted", Success: true}, nil
}

func (s *EventService) ReactToEvent(ctx context.Context, req *pb.ReactReq) (*pb.BasicResp, error) {
	if err := s.db.AddReaction(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "reaction added", Success: true}, nil
}

func (s *EventService) RemoveReaction(ctx context.Context, req *pb.ReactReq) (*pb.BasicResp, error) {
	if err := s.db.RemoveReaction(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "reaction removed", Success: true}, nil
}

func (s *EventService) ReportEvent(ctx context.Context, req *pb.ReportEventReq) (*pb.BasicResp, error) {
	if err := s.db.ReportEvent(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "thanks, our team will review this event", Success: true}, nil
}

// -----------------------------------------------------------------------------
// Co-hosts & utility
// -----------------------------------------------------------------------------

func (s *EventService) AddCoHost(ctx context.Context, req *pb.CoHostReq) (*pb.BasicResp, error) {
	if err := s.db.AddCoHost(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "co-host added", Success: true}, nil
}

func (s *EventService) RemoveCoHost(ctx context.Context, req *pb.CoHostReq) (*pb.BasicResp, error) {
	if err := s.db.RemoveCoHost(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "co-host removed", Success: true}, nil
}

func (s *EventService) GetCalendarFile(ctx context.Context, req *pb.CalendarReq) (*pb.CalendarResp, error) {
	event, viewerStatus, err := s.db.GetEvent(ctx, req.EventSlug, req.AccountId)
	if err != nil {
		return nil, err
	}
	// buildICS folds the join link into the description, so the same redaction
	// the detail page gets has to happen before the file is rendered.
	if err := s.redactForViewer(ctx, event, req.AccountId, viewerStatus); err != nil {
		return nil, err
	}
	return &pb.CalendarResp{IcsData: buildICS(event)}, nil
}

// Authorize backs the gateway's fast-reject layer. It reports what the caller
// may do without performing it; the mutating RPCs re-check independently.
func (s *EventService) Authorize(ctx context.Context, req *pb.AuthorizeReq) (*pb.AuthorizeResp, error) {
	return s.db.Authorize(ctx, req)
}

func (s *EventService) CloneEvent(ctx context.Context, req *pb.CloneEventReq) (*pb.EventResp, error) {
	event, err := s.db.CloneEvent(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.EventResp{Message: "event cloned as draft", Event: event}, nil
}

func (s *EventService) CreateSeries(ctx context.Context, req *pb.CreateSeriesReq) (*pb.EventResp, error) {
	if hasPaidTier(req.TicketTiers) {
		if err := s.rejectIfPaymentsDisabled(); err != nil {
			return nil, err
		}
	}
	occs, rule, err := expandRecurrence(req.Recurrence, req.GetStartTime().AsTime())
	if err != nil {
		return nil, err
	}
	event, err := s.db.MaterializeSeries(ctx, req, occs, rule)
	if err != nil {
		return nil, err
	}
	return &pb.EventResp{Message: "series created", Event: event}, nil
}

func (s *EventService) CancelSeriesOccurrence(ctx context.Context, req *pb.EventActionReq) (*pb.BasicResp, error) {
	if err := s.db.CancelSeriesOccurrence(ctx, req.Slug, req.AccountId); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "occurrence cancelled", Success: true}, nil
}

func (s *EventService) AdminListEventPayments(ctx context.Context, req *pb.AdminListEventPaymentsReq) (*pb.AdminListEventPaymentsResp, error) {
	return s.db.AdminListEventPayments(ctx, req)
}

func (s *EventService) AdminGetEventPayments(ctx context.Context, req *pb.AdminGetEventPaymentsReq) (*pb.AdminGetEventPaymentsResp, error) {
	return s.db.AdminGetEventPayments(ctx, req)
}

func (s *EventService) AdminCreateSettlement(ctx context.Context, req *pb.AdminCreateSettlementReq) (*pb.AdminSettlementResp, error) {
	return s.db.AdminCreateSettlement(ctx, req)
}

func (s *EventService) AdminMarkSettlementPaid(ctx context.Context, req *pb.AdminMarkSettlementPaidReq) (*pb.AdminSettlementResp, error) {
	return s.db.AdminMarkSettlementPaid(ctx, req)
}

func (s *EventService) AdminFlagNsfw(ctx context.Context, req *pb.AdminFlagNsfwReq) (*pb.BasicResp, error) {
	if err := s.db.AdminFlagNsfw(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "flagged", Success: true}, nil
}

func (s *EventService) AdminHideEventComment(ctx context.Context, req *pb.AdminHideEventCommentReq) (*pb.BasicResp, error) {
	if err := s.db.AdminHideEventComment(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "comment hidden", Success: true}, nil
}

func (s *EventService) AdminHideEventQuestion(ctx context.Context, req *pb.AdminHideEventQuestionReq) (*pb.BasicResp, error) {
	if err := s.db.AdminHideEventQuestion(ctx, req); err != nil {
		return nil, err
	}
	return &pb.BasicResp{Message: "question hidden", Success: true}, nil
}

func (s *EventService) AdminListEvents(ctx context.Context, req *pb.AdminListEventsReq) (*pb.AdminListEventsResp, error) {
	return s.db.AdminListEvents(ctx, req)
}

func (s *EventService) AdminCancelEvent(ctx context.Context, req *pb.AdminEventActionReq) (*pb.EventResp, error) {
	event, err := s.db.AdminCancelEvent(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.EventResp{Message: "event cancelled", Event: event}, nil
}

func (s *EventService) AdminUnpublishEvent(ctx context.Context, req *pb.AdminEventActionReq) (*pb.EventResp, error) {
	event, err := s.db.AdminUnpublishEvent(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.EventResp{Message: "event unpublished", Event: event}, nil
}

func (s *EventService) AdminEventStats(ctx context.Context, _ *pb.AdminEmpty) (*pb.AdminEventStatsResp, error) {
	return s.db.AdminEventStats(ctx)
}

func (s *EventService) AdminPaymentStats(ctx context.Context, _ *pb.AdminEmpty) (*pb.AdminPaymentStatsResp, error) {
	return s.db.AdminPaymentStats(ctx)
}
