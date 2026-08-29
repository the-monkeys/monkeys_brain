package events

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

// accountID returns the caller's account id, empty for anonymous requests.
func accountID(ctx *gin.Context) string {
	id, _ := ctx.Get("accountId")
	if s, ok := id.(string); ok {
		return s
	}
	return ""
}

// pathID parses a numeric path parameter.
func pathID(ctx *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(ctx.Param(name), 10, 64)
	if err != nil || id <= 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid " + name})
		return 0, false
	}
	return id, true
}

// bind parses and validates a JSON body, writing a 400 on failure.
func bind[T any](ctx *gin.Context) (*T, bool) {
	var body T
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	return &body, true
}

// listQuery reads the discovery filters shared by the listing endpoints.
func listQuery(ctx *gin.Context) *pb.ListEventsReq {
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(ctx.DefaultQuery("offset", "0"))

	req := &pb.ListEventsReq{
		Limit:      int32(limit),
		Offset:     int32(offset),
		Status:     ctx.Query("status"),
		EventType:  ctx.Query("type"),
		Query:      ctx.Query("q"),
		Location:   ctx.Query("location"),
		AccountId:  accountID(ctx),
		Username:   ctx.Param("username"),
		ClientInfo: clientInfo(ctx),
		DateFilter: ctx.Query("date"),
		SortBy:     ctx.Query("sort"),
	}
	if tags := ctx.Query("tags"); tags != "" {
		req.Tags = strings.Split(tags, ",")
	}
	return req
}

// -----------------------------------------------------------------------------
// Event CRUD
// -----------------------------------------------------------------------------

func (esc *EventServiceClient) CreateEvent(ctx *gin.Context) {
	body, ok := bind[EventBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.CreateEvent(ctx, &pb.CreateEventReq{
		AccountId:       accountID(ctx),
		Title:           body.Title,
		Description:     body.Description,
		StartTime:       toTimestamp(&body.StartTime),
		EndTime:         toTimestamp(&body.EndTime),
		Timezone:        body.Timezone,
		EventType:       body.EventType,
		Location:        body.Location,
		MeetingLink:     body.MeetingLink,
		Capacity:        body.Capacity,
		CoverImage:      body.CoverImage,
		Tags:            body.Tags,
		CoHostUsernames: body.CoHostUsernames,
		TicketTiers:     tiersToProto(body.TicketTiers),
		GroupSlug:       body.GroupSlug,
		Visibility:      body.Visibility,
		ClientInfo:      clientInfo(ctx),
	})
	if esc.fail(ctx, err, "create event") {
		return
	}
	ctx.JSON(http.StatusCreated, res)
}

func (esc *EventServiceClient) UpdateEvent(ctx *gin.Context) {
	body, ok := bind[EventBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.UpdateEvent(ctx, &pb.UpdateEventReq{
		Slug:        ctx.Param("slug"),
		AccountId:   accountID(ctx),
		Title:       body.Title,
		Description: body.Description,
		StartTime:   toTimestamp(&body.StartTime),
		EndTime:     toTimestamp(&body.EndTime),
		Timezone:    body.Timezone,
		EventType:   body.EventType,
		Location:    body.Location,
		MeetingLink: body.MeetingLink,
		Capacity:    body.Capacity,
		CoverImage:  body.CoverImage,
		Tags:        body.Tags,
		Visibility:  body.Visibility,
		ClientInfo:  clientInfo(ctx),
	})

	if esc.fail(ctx, err, "update event") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// action is the shared shape of the slug-scoped verbs.
func (esc *EventServiceClient) action(ctx *gin.Context) *pb.EventActionReq {
	return &pb.EventActionReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	}
}

func (esc *EventServiceClient) DeleteEvent(ctx *gin.Context) {
	res, err := esc.Client.DeleteEvent(ctx, esc.action(ctx))
	if esc.fail(ctx, err, "delete event") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) PublishEvent(ctx *gin.Context) {
	res, err := esc.Client.PublishEvent(ctx, esc.action(ctx))
	if esc.fail(ctx, err, "publish event") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) CancelEvent(ctx *gin.Context) {
	res, err := esc.Client.CancelEvent(ctx, esc.action(ctx))
	if esc.fail(ctx, err, "cancel event") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) GetEvent(ctx *gin.Context) {
	res, err := esc.Client.GetEvent(ctx, &pb.GetEventReq{
		Slug:       ctx.Param("slug"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	})
	if esc.fail(ctx, err, "get event") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) ListEvents(ctx *gin.Context) {
	req := listQuery(ctx)
	esc.log.Debugw("[DEBUG] ListEvents filters",
		"type", req.EventType,
		"date", req.DateFilter,
		"sort", req.SortBy,
		"q", req.Query,
		"location", req.Location,
		"tags", req.Tags,
	)
	res, err := esc.Client.ListEvents(ctx, req)
	if esc.fail(ctx, err, "list events") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) GetUserEvents(ctx *gin.Context) {
	res, err := esc.Client.GetUserEvents(ctx, listQuery(ctx))
	if esc.fail(ctx, err, "list user events") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) GetAttendingEvents(ctx *gin.Context) {
	res, err := esc.Client.GetUserAttendingEvents(ctx, listQuery(ctx))
	if esc.fail(ctx, err, "list attending events") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) GetGroupEvents(ctx *gin.Context) {
	req := listQuery(ctx)
	req.GroupSlug = ctx.Param("slug")
	res, err := esc.Client.GetGroupEvents(ctx, req)
	if esc.fail(ctx, err, "list group events") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// -----------------------------------------------------------------------------
// Ticket tiers & coupons
// -----------------------------------------------------------------------------

func (esc *EventServiceClient) CreateTier(ctx *gin.Context) {
	body, ok := bind[TierBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.CreateTicketTier(ctx, &pb.CreateTicketTierReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		Tier:       body.toProto(),
		ClientInfo: clientInfo(ctx),
	})
	if esc.fail(ctx, err, "create ticket tier") {
		return
	}
	ctx.JSON(http.StatusCreated, res)
}

func (esc *EventServiceClient) UpdateTier(ctx *gin.Context) {
	tierID, ok := pathID(ctx, "id")
	if !ok {
		return
	}
	body, ok := bind[TierBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.UpdateTicketTier(ctx, &pb.UpdateTicketTierReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		TierId:     tierID,
		Tier:       body.toProto(),
		ClientInfo: clientInfo(ctx),
	})
	if esc.fail(ctx, err, "update ticket tier") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) DeleteTier(ctx *gin.Context) {
	tierID, ok := pathID(ctx, "id")
	if !ok {
		return
	}

	res, err := esc.Client.DeleteTicketTier(ctx, &pb.TierActionReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		TierId:     tierID,
		ClientInfo: clientInfo(ctx),
	})
	if esc.fail(ctx, err, "delete ticket tier") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) CreateCoupon(ctx *gin.Context) {
	body, ok := bind[CouponBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.CreateCoupon(ctx, &pb.CreateCouponReq{
		EventSlug:       ctx.Param("slug"),
		AccountId:       accountID(ctx),
		Code:            body.Code,
		DiscountPercent: body.DiscountPercent,
		MaxUses:         body.MaxUses,
		ValidFrom:       toTimestamp(body.ValidFrom),
		ValidTo:         toTimestamp(body.ValidTo),
		ClientInfo:      clientInfo(ctx),
	})
	if esc.fail(ctx, err, "create coupon") {
		return
	}
	ctx.JSON(http.StatusCreated, res)
}

func (esc *EventServiceClient) ListCoupons(ctx *gin.Context) {
	res, err := esc.Client.ListCoupons(ctx, &pb.ListCouponsReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	})
	if esc.fail(ctx, err, "list coupons") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) DeleteCoupon(ctx *gin.Context) {
	couponID, ok := pathID(ctx, "id")
	if !ok {
		return
	}

	res, err := esc.Client.DeleteCoupon(ctx, &pb.CouponActionReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		CouponId:   couponID,
		ClientInfo: clientInfo(ctx),
	})
	if esc.fail(ctx, err, "delete coupon") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) ValidateCoupon(ctx *gin.Context) {
	body, ok := bind[ValidateCouponBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.ValidateCoupon(ctx, &pb.ValidateCouponReq{
		EventSlug:    ctx.Param("slug"),
		Code:         body.Code,
		TicketTierId: body.TicketTierID,
		ClientInfo:   clientInfo(ctx),
	})
	if esc.fail(ctx, err, "validate coupon") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// -----------------------------------------------------------------------------
// RSVP, payments & attendees
// -----------------------------------------------------------------------------

func (esc *EventServiceClient) RSVP(ctx *gin.Context) {
	body, ok := bind[RSVPBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.RSVPEvent(ctx, &pb.RSVPReq{
		EventSlug:    ctx.Param("slug"),
		AccountId:    accountID(ctx),
		TicketTierId: body.TicketTierID,
		CouponCode:   body.CouponCode,
		ClientInfo:   clientInfo(ctx),
	})
	if esc.fail(ctx, err, "rsvp") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) CancelRSVP(ctx *gin.Context) {
	res, err := esc.Client.CancelRSVP(ctx, &pb.CancelRSVPReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		ClientInfo: clientInfo(ctx),
	})
	if esc.fail(ctx, err, "cancel rsvp") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// PaymentWebhook forwards Razorpay's callback verbatim; the events service
// verifies the HMAC signature over the exact bytes received here.
func (esc *EventServiceClient) PaymentWebhook(ctx *gin.Context) {
	body, err := ctx.GetRawData()
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unreadable payload"})
		return
	}

	res, err := esc.Client.ProcessPaymentWebhook(ctx, &pb.PaymentWebhookReq{
		Signature: ctx.GetHeader("X-Razorpay-Signature"),
		RawBody:   body,
	})
	if esc.fail(ctx, err, "payment webhook") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) attendeeQuery(ctx *gin.Context, limit int32) *pb.ListAttendeesReq {
	offset, _ := strconv.Atoi(ctx.DefaultQuery("offset", "0"))
	if q := ctx.Query("limit"); q != "" {
		if parsed, err := strconv.Atoi(q); err == nil {
			limit = int32(parsed)
		}
	}
	return &pb.ListAttendeesReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		Limit:      limit,
		Offset:     int32(offset),
		Status:     ctx.Query("status"),
		ClientInfo: clientInfo(ctx),
	}
}

func (esc *EventServiceClient) ListAttendees(ctx *gin.Context) {
	res, err := esc.Client.GetAttendees(ctx, esc.attendeeQuery(ctx, 50))
	if esc.fail(ctx, err, "list attendees") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// exportPageSize is the per-request page the export walks through; the events
// service caps a single attendee query at 500 rows.
const exportPageSize = 500

// ExportAttendees streams the full roster as CSV, paging through the service
// so large events are not silently truncated.
func (esc *EventServiceClient) ExportAttendees(ctx *gin.Context) {
	req := esc.attendeeQuery(ctx, exportPageSize)
	req.Limit = exportPageSize
	req.Offset = 0

	slug := ctx.Param("slug")
	ctx.Header("Content-Type", "text/csv; charset=utf-8")
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-attendees.csv"`, slug))

	w := csv.NewWriter(ctx.Writer)
	defer w.Flush()

	header := []string{"username", "email", "ticket_tier", "status", "coupon_used", "checked_in", "registered_at"}
	if err := w.Write(header); err != nil {
		esc.log.Errorw("failed to write csv header", "slug", slug, "err", err)
		return
	}

	for {
		res, err := esc.Client.GetAttendees(ctx, req)
		if esc.fail(ctx, err, "export attendees") {
			return
		}

		for _, a := range res.Attendees {
			record := []string{
				a.UserName, a.UserEmail, a.TicketTierName, a.Status, a.CouponUsed,
				strconv.FormatBool(a.CheckedIn), a.CreatedAt.AsTime().Format("2006-01-02 15:04:05"),
			}
			if err := w.Write(record); err != nil {
				esc.log.Errorw("failed to write csv row", "slug", slug, "err", err)
				return
			}
		}

		req.Offset += int32(len(res.Attendees))
		if len(res.Attendees) < exportPageSize || req.Offset >= res.Total {
			return
		}
		w.Flush()
	}
}

// -----------------------------------------------------------------------------
// Social
// -----------------------------------------------------------------------------

func (esc *EventServiceClient) AddComment(ctx *gin.Context) {
	body, ok := bind[CommentBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.AddEventComment(ctx, &pb.AddCommentReq{
		EventSlug:   ctx.Param("slug"),
		AccountId:   accountID(ctx),
		CommentText: body.CommentText,
		ClientInfo:  clientInfo(ctx),
	})
	if esc.fail(ctx, err, "add comment") {
		return
	}
	ctx.JSON(http.StatusCreated, res)
}

func (esc *EventServiceClient) ListComments(ctx *gin.Context) {
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(ctx.DefaultQuery("offset", "0"))

	res, err := esc.Client.ListEventComments(ctx, &pb.ListCommentsReq{
		EventSlug: ctx.Param("slug"),
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if esc.fail(ctx, err, "list comments") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) DeleteComment(ctx *gin.Context) {
	commentID, ok := pathID(ctx, "id")
	if !ok {
		return
	}

	res, err := esc.Client.DeleteEventComment(ctx, &pb.DeleteCommentReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		CommentId:  commentID,
		ClientInfo: clientInfo(ctx),
	})
	if esc.fail(ctx, err, "delete comment") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) reaction(ctx *gin.Context) (*pb.ReactReq, bool) {
	body, ok := bind[ReactionBody](ctx)
	if !ok {
		return nil, false
	}
	return &pb.ReactReq{
		EventSlug:    ctx.Param("slug"),
		AccountId:    accountID(ctx),
		ReactionType: body.ReactionType,
		ClientInfo:   clientInfo(ctx),
	}, true
}

func (esc *EventServiceClient) AddReaction(ctx *gin.Context) {
	req, ok := esc.reaction(ctx)
	if !ok {
		return
	}
	res, err := esc.Client.ReactToEvent(ctx, req)
	if esc.fail(ctx, err, "add reaction") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) RemoveReaction(ctx *gin.Context) {
	req, ok := esc.reaction(ctx)
	if !ok {
		return
	}
	res, err := esc.Client.RemoveReaction(ctx, req)
	if esc.fail(ctx, err, "remove reaction") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) ReportEvent(ctx *gin.Context) {
	body, ok := bind[ReportBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.ReportEvent(ctx, &pb.ReportEventReq{
		EventSlug:  ctx.Param("slug"),
		AccountId:  accountID(ctx),
		Reason:     body.Reason,
		ClientInfo: clientInfo(ctx),
	})
	if esc.fail(ctx, err, "report event") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// -----------------------------------------------------------------------------
// Co-hosts, calendar & sharing
// -----------------------------------------------------------------------------

func (esc *EventServiceClient) AddCoHost(ctx *gin.Context) {
	body, ok := bind[CoHostBody](ctx)
	if !ok {
		return
	}

	res, err := esc.Client.AddCoHost(ctx, &pb.CoHostReq{
		EventSlug:      ctx.Param("slug"),
		AccountId:      accountID(ctx),
		CohostUsername: body.Username,
		ClientInfo:     clientInfo(ctx),
	})
	if esc.fail(ctx, err, "add co-host") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func (esc *EventServiceClient) RemoveCoHost(ctx *gin.Context) {
	res, err := esc.Client.RemoveCoHost(ctx, &pb.CoHostReq{
		EventSlug:      ctx.Param("slug"),
		AccountId:      accountID(ctx),
		CohostUsername: ctx.Param("username"),
		ClientInfo:     clientInfo(ctx),
	})
	if esc.fail(ctx, err, "remove co-host") {
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// DownloadCalendar returns the event as an .ics file.
func (esc *EventServiceClient) DownloadCalendar(ctx *gin.Context) {
	slug := ctx.Param("slug")

	res, err := esc.Client.GetCalendarFile(ctx, &pb.CalendarReq{EventSlug: slug})
	if esc.fail(ctx, err, "download calendar") {
		return
	}

	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.ics"`, slug))
	ctx.Data(http.StatusOK, "text/calendar; charset=utf-8", res.IcsData)
}

// ShareMeta returns Open Graph fields plus ready-made share links, so the
// frontend does not have to assemble them for every network.
func (esc *EventServiceClient) ShareMeta(ctx *gin.Context) {
	slug := ctx.Param("slug")

	res, err := esc.Client.GetEvent(ctx, &pb.GetEventReq{Slug: slug, ClientInfo: clientInfo(ctx)})
	if esc.fail(ctx, err, "share meta") {
		return
	}

	event := res.Event
	base := esc.baseURL
	if base == "" {
		base = "https://monkeys.com.co"
	}
	shareURL := fmt.Sprintf("%s/events/%s", base, slug)
	summary := fmt.Sprintf("%s — %s",
		event.Title, event.StartTime.AsTime().Format("Mon, 02 Jan 2006 15:04 MST"))
	enc := url.QueryEscape

	ctx.JSON(http.StatusOK, gin.H{
		"og_title":       event.Title,
		"og_description": truncate(event.Description, 200),
		"og_image":       event.CoverImage,
		"og_url":         shareURL,
		"og_type":        "event",
		"share_links": gin.H{
			"twitter":  "https://twitter.com/intent/tweet?text=" + enc(summary) + "&url=" + enc(shareURL),
			"linkedin": "https://www.linkedin.com/sharing/share-offsite/?url=" + enc(shareURL),
			"whatsapp": "https://wa.me/?text=" + enc(summary+" "+shareURL),
			"email":    "mailto:?subject=" + enc(event.Title) + "&body=" + enc(summary+"\n"+shareURL),
		},
	})
}

// truncate shortens a description for social previews, counting runes so
// multi-byte characters are never cut in half.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimSpace(string(runes[:max])) + "…"
}
