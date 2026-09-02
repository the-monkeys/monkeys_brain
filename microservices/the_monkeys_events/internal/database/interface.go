package database

import (
	"context"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

// EventDB is the data access contract consumed by the events gRPC service.
// Requests arrive as protobuf messages carrying account_id / username; the
// implementation resolves those to numeric ids and enforces host permissions.
type EventDB interface {
	// Events
	CloneEvent(ctx context.Context, req *pb.CloneEventReq) (*pb.Event, error)
	CreateEvent(ctx context.Context, req *pb.CreateEventReq) (*pb.Event, error)
	UpdateEvent(ctx context.Context, req *pb.UpdateEventReq) (*pb.Event, error)
	DeleteEvent(ctx context.Context, req *pb.EventActionReq) error
	SetEventStatus(ctx context.Context, req *pb.EventActionReq, newStatus string) (*pb.Event, error)
	GetEvent(ctx context.Context, slug, viewerAccountID string) (*pb.Event, string, error)
	ListEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error)
	GetUserEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error)
	GetUserAttendingEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error)
	GetGroupEvents(ctx context.Context, req *pb.ListEventsReq) ([]*pb.Event, int32, error)

	// Ticket tiers
	CreateTicketTier(ctx context.Context, req *pb.CreateTicketTierReq) (*pb.TicketTier, error)
	UpdateTicketTier(ctx context.Context, req *pb.UpdateTicketTierReq) (*pb.TicketTier, error)
	DeleteTicketTier(ctx context.Context, req *pb.TierActionReq) error

	// Coupons
	CreateCoupon(ctx context.Context, req *pb.CreateCouponReq) (*pb.Coupon, error)
	ListCoupons(ctx context.Context, req *pb.ListCouponsReq) ([]*pb.Coupon, error)
	DeleteCoupon(ctx context.Context, req *pb.CouponActionReq) error
	ValidateCoupon(ctx context.Context, req *pb.ValidateCouponReq) (*pb.Coupon, float64, error)

	// RSVP, payments & attendees
	CreateRSVP(ctx context.Context, req *pb.RSVPReq) (*RSVPResult, error)
	AttachPaymentOrder(ctx context.Context, attendeeID int64, orderID string, amount float64) error
	ReleaseReservation(ctx context.Context, attendeeID int64) error
	ConfirmPayment(ctx context.Context, orderID, paymentID string) (*PaymentResult, error)
	FailPayment(ctx context.Context, orderID string) error
	CancelRSVP(ctx context.Context, req *pb.CancelRSVPReq) (*CancelResult, error)
	PendingRefunds(ctx context.Context, slug string) ([]Refund, error)
	MarkRefunded(ctx context.Context, paymentID, refundID string) error
	ListAttendees(ctx context.Context, req *pb.ListAttendeesReq) ([]*pb.Attendee, int32, error)

	// Social
	AddComment(ctx context.Context, req *pb.AddCommentReq) (*CommentResult, error)
	ListComments(ctx context.Context, req *pb.ListCommentsReq) ([]*pb.EventComment, int32, error)
	DeleteComment(ctx context.Context, req *pb.DeleteCommentReq) error
	AddReaction(ctx context.Context, req *pb.ReactReq) error
	RemoveReaction(ctx context.Context, req *pb.ReactReq) error
	ReportEvent(ctx context.Context, req *pb.ReportEventReq) error

	// Co-hosts
	AddCoHost(ctx context.Context, req *pb.CoHostReq) error
	RemoveCoHost(ctx context.Context, req *pb.CoHostReq) error

	// Notification fan-out
	AttendeeUsernames(ctx context.Context, slug string) ([]string, error)
	FollowerUsernames(ctx context.Context, slug string) ([]string, error)

	// Authorization
	Authorize(ctx context.Context, req *pb.AuthorizeReq) (*pb.AuthorizeResp, error)

	// Venues (Meetup-parity)
	CreateVenue(ctx context.Context, v *pb.Venue, createdByAccountID string) (*pb.Venue, error)
	SearchVenues(ctx context.Context, query, city, country string, limit int32) ([]*pb.Venue, error)
	AttachVenueToEvent(ctx context.Context, slug, accountID string, venueID int64) error

	// Registration questions (Meetup-parity)
	CreateEventQuestions(ctx context.Context, slug, accountID string, questions []*pb.EventQuestion) ([]*pb.EventQuestion, error)
	ReplaceEventQuestions(ctx context.Context, slug, accountID string, questions []*pb.EventQuestion) ([]*pb.EventQuestion, error)

	// Attendance & saves (Meetup-parity)
	UpdateAttendance(ctx context.Context, req *pb.UpdateAttendanceReq) error
	SaveEvent(ctx context.Context, req *pb.SaveEventReq) error
	UnsaveEvent(ctx context.Context, req *pb.SaveEventReq) error

	// Recurring events (Meetup-parity)
	CreateSeries(ctx context.Context, in SeriesInput) (int64, error)
	GenerateSeriesOccurrences(ctx context.Context, seriesID int64, occurrences []time.Time, tmpl OccurrenceTemplate) ([]string, error)
	CancelSeriesOccurrence(ctx context.Context, slug, accountID string) error
	UpdateSeriesFutureOccurrences(ctx context.Context, seriesID int64, actorAccountID string, cutoff time.Time, tmpl OccurrenceTemplate) (int64, error)
	MaterializeSeries(ctx context.Context, req *pb.CreateSeriesReq, occs []time.Time, rule string) (*pb.Event, error)

	// Background upkeep
	ClaimDueReminders(ctx context.Context, offset string, earliest, latest time.Duration) ([]Reminder, error)
	ArchivePastEvents(ctx context.Context) (int64, error)

	Close() error
}
