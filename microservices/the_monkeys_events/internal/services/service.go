package services

import (
	"context"
	"fmt"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/database"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// notificationRoutingKey is the index of the notification consumer's key in
// the shared RabbitMQ routing key list.
const notificationRoutingKey = 4

type EventService struct {
	pb.UnimplementedEventServiceServer
	db    database.EventDB
	log   *zap.SugaredLogger
	cfg   *config.Config
	qConn *rabbitmq.ConnManager
	pay   *razorpay
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
	return &pb.BasicResp{Message: "event deleted", Success: true}, nil
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

// redactForViewer enforces the two things a raw event row does not know about
// the person reading it: a draft belongs to its hosts, and the join link
// belongs to people holding a ticket. Handing the link to every visitor on the
// detail page would make the ticket optional.
//
// The gateway rejects most of this earlier, but the check lives here too so
// the rule holds for any caller that reaches the service directly.
func (s *EventService) redactForViewer(ctx context.Context, event *pb.Event, accountID, viewerStatus string) error {
	grant, err := s.db.Authorize(ctx, &pb.AuthorizeReq{AccountId: accountID, EventSlug: event.Slug})
	if err != nil {
		return err
	}
	isHost := grant.Role == "organizer" || grant.Role == "co_host"

	if event.Status == "draft" && !isHost {
		return status.Error(codes.NotFound, "event not found")
	}
	if !isHost && viewerStatus != "confirmed" {
		event.MeetingLink = ""
	}
	return nil
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
	if req.Tier.GetPrice() > 0 && !s.pay.enabled() {
		return nil, status.Error(codes.FailedPrecondition, "payments are not configured on this deployment")
	}
	tier, err := s.db.CreateTicketTier(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pb.TicketTierResp{Message: "ticket tier created", Tier: tier}, nil
}

func (s *EventService) UpdateTicketTier(ctx context.Context, req *pb.UpdateTicketTierReq) (*pb.TicketTierResp, error) {
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

	case database.RSVPPendingPayment:
		if !s.pay.enabled() {
			_ = s.db.ReleaseReservation(ctx, result.AttendeeID)
			return nil, status.Error(codes.FailedPrecondition, "payments are not configured on this deployment")
		}
		orderID, err := s.pay.createOrder(ctx, result.AmountDue, result.Currency,
			fmt.Sprintf("evt-rsvp-%d", result.AttendeeID))
		if err != nil {
			// Free the held seat so a failed checkout does not block others.
			_ = s.db.ReleaseReservation(ctx, result.AttendeeID)
			s.log.Errorw("failed to create payment order", "attendee", result.AttendeeID, "err", err)
			return nil, status.Error(codes.Unavailable, "could not start the payment, please try again")
		}
		if err := s.db.AttachPaymentOrder(ctx, result.AttendeeID, orderID, result.AmountDue); err != nil {
			return nil, err
		}
		resp.Message = "complete the payment to confirm your spot"
		resp.PaymentOrderId = orderID
		resp.AmountDue = result.AmountDue
		resp.RazorpayKeyId = s.cfg.Keys.RazorpayKeyID
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
		refundID, err := s.pay.refund(ctx, refund.PaymentID, refund.Amount)
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
