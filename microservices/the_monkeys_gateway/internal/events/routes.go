package events

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/events/authx"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/storage_v2"
	"go.uber.org/zap"
)

// RegisterEventRouter wires the events REST surface.
//
// Reads are public so events can be shared and indexed; everything that
// mutates state, or exposes attendee data, sits behind authentication. The
// Razorpay webhook is public by necessity and authenticated by its signature.
//
// Three things are deliberate about the shape below.
//
// The public and authenticated groups are siblings hanging off the router, not
// nested. Nesting made authenticated routes inherit AuthOptional and validate
// their token twice, once per middleware.
//
// AuthOptional is attached per route, only where the response is shaped around
// the viewer: the detail page reports your own RSVP, a profile shows its owner
// their drafts, and the guard cannot gate a draft without knowing who is
// asking. Routes that ignore the viewer skip the validation entirely.
//
// Every route scoped to one event carries a guard. The events service still
// re-checks each mutation against event_permissions, so the guard is there to
// reject early and to keep drafts and meeting links out of responses.
func RegisterEventRouter(router *gin.Engine, cfg *config.Config, authClient *auth.ServiceClient, storageSvc *storage_v2.Service, lg *zap.SugaredLogger) *EventServiceClient {
	mware := auth.InitAuthMiddleware(authClient, lg)

	esc := &EventServiceClient{
		Client:  NewEventServiceClient(cfg, lg),
		log:     lg,
		baseURL: strings.TrimRight(cfg.SEO.BaseURL, "/"),
	}
	guard := authx.NewGuard(esc.Client, lg)

	// --------------------  -----------------------------------------------
	// Public reads
	// -------------------------------------------------------------------
	pub := router.Group("/api/v1/events", rateLimit(rateRead))

	pub.GET("", esc.ListEvents)
	pub.GET("/user/:username", mware.AuthOptional, esc.GetUserEvents)
	pub.GET("/group/:slug", mware.AuthOptional, esc.GetGroupEvents)
	pub.GET("/:slug", mware.AuthOptional, guard.RequireVisible(), esc.GetEvent)
	pub.GET("/:slug/comments", mware.AuthOptional, guard.RequireVisible(), esc.ListComments)
	pub.GET("/:slug/share", mware.AuthOptional, guard.RequireVisible(), esc.ShareMeta)

	// Calendar generation renders a file per call, so it gets the tighter tier.
	router.GET("/api/v1/events/:slug/calendar",
		rateLimit(rateCostly), mware.AuthOptional, guard.RequireVisible(), esc.DownloadCalendar)

	// Razorpay callback: no session to speak of, verified by HMAC inside the
	// events service. Limited per IP purely to cap forced signature checks.
	router.POST("/api/v1/events/payment/webhook", rateLimit(rateWebhook), esc.PaymentWebhook)

	// -------------------------------------------------------------------
	// Authenticated
	// -------------------------------------------------------------------
	authed := router.Group("/api/v1/events", mware.AuthRequired)

	write := authed.Group("", rateLimit(rateWrite))
	read := authed.Group("", rateLimit(rateRead))
	costly := authed.Group("", rateLimit(rateCostly))

	// Event lifecycle. Deleting an event is destructive enough to stay with
	// the organizer rather than delegate to co-hosts.
	write.POST("", esc.CreateEvent)
	write.PUT("/:slug", guard.Require(authx.PermEditEvent), esc.UpdateEvent)
	write.DELETE("/:slug", guard.RequireOrganizer(), esc.DeleteEvent)
	write.POST("/:slug/publish", guard.Require(authx.PermEditEvent), esc.PublishEvent)
	write.POST("/:slug/cancel", guard.Require(authx.PermEditEvent), esc.CancelEvent)

	read.GET("/attending", esc.GetAttendingEvents)

	// Ticket tiers.
	write.POST("/:slug/tiers", guard.Require(authx.PermManageTickets), esc.CreateTier)
	write.PUT("/:slug/tiers/:id", guard.Require(authx.PermManageTickets), esc.UpdateTier)
	write.DELETE("/:slug/tiers/:id", guard.Require(authx.PermManageTickets), esc.DeleteTier)

	// Coupons are host-only to read or change. Validation is the exception:
	// it is how an attendee checks a code at checkout, so it only needs a
	// visible event.
	write.POST("/:slug/coupons", guard.Require(authx.PermManageCoupons), esc.CreateCoupon)
	read.GET("/:slug/coupons", guard.Require(authx.PermManageCoupons), esc.ListCoupons)
	write.DELETE("/:slug/coupons/:id", guard.Require(authx.PermManageCoupons), esc.DeleteCoupon)
	write.POST("/:slug/coupons/validate", guard.RequireVisible(), esc.ValidateCoupon)

	// RSVP acts on the caller's own row, so a visible event is the only gate.
	write.POST("/:slug/rsvp", guard.RequireCanRSVP(), esc.RSVP)
	write.DELETE("/:slug/rsvp", guard.RequireCanRSVP(), esc.CancelRSVP)

	// Bookmarks act on the caller's own saved list; a visible event is enough.
	write.POST("/:slug/save", guard.RequireVisible(), esc.SaveEvent)
	write.DELETE("/:slug/save", guard.RequireVisible(), esc.UnsaveEvent)

	// Attendance is host-only: marking an attendee checked in or no-show
	// touches another person's row, so it needs manage_checkins.
	write.PUT("/:slug/attendees/:id/attendance", guard.RequireCanCheckIn(), esc.UpdateAttendance)

	// Attendee data carries contact details; the export walks every page.
	read.GET("/:slug/attendees", guard.RequireCanViewAttendeeContact(), esc.ListAttendees)
	costly.GET("/:slug/attendees/export", guard.RequireCanViewAttendeeContact(), esc.ExportAttendees)

	// Social. Deleting a comment is the author's right, or a host's as
	// moderator; everything else acts on the caller's own row.
	write.POST("/:slug/comments", guard.RequireVisible(), esc.AddComment)
	write.DELETE("/:slug/comments/:id", guard.RequireCommentOwner(), esc.DeleteComment)
	write.POST("/:slug/react", guard.RequireVisible(), esc.AddReaction)
	write.DELETE("/:slug/react", guard.RequireVisible(), esc.RemoveReaction)
	write.POST("/:slug/report", guard.RequireVisible(), esc.ReportEvent)

	// Co-hosts.
	write.POST("/:slug/cohosts", guard.Require(authx.PermManageHosts), esc.AddCoHost)
	write.DELETE("/:slug/cohosts/:username", guard.Require(authx.PermManageHosts), esc.RemoveCoHost)

	// Event gallery. Hosts and co-hosts (edit_event) add or remove up to four
	// photos, stored under events/{slug}/photos by the storage service. Reads
	// are public and live on the /api/v2/storage surface. The cap and the
	// object lifecycle are owned by the storage handlers.
	if storageSvc != nil {
		write.POST("/:slug/images/cover", guard.Require(authx.PermEditEvent), storageSvc.UploadEventCoverImage)
		write.DELETE("/:slug/images/cover", guard.Require(authx.PermEditEvent), storageSvc.DeleteEventCoverImage)
		write.POST("/:slug/photos", guard.Require(authx.PermEditEvent), storageSvc.UploadEventPhoto)
		write.DELETE("/:slug/photos/:photo", guard.Require(authx.PermEditEvent), storageSvc.DeleteEventPhoto)
	}

	return esc
}
