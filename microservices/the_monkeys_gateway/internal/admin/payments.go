package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func inrJSON(m *pb.MoneyINR) gin.H {
	if m == nil {
		return gin.H{"paise": int64(0), "inr": "0.00"}
	}
	return gin.H{"paise": m.Paise, "inr": m.Inr}
}

func (asc *AdminServiceClient) staffActor(ctx *gin.Context) *pb.AdminActor {
	return &pb.AdminActor{
		Username:  ctx.GetString("userName"),
		AccountId: ctx.GetString("accountId"),
		Role:      ctx.GetString("user_role"),
		Ip:        ctx.ClientIP(),
	}
}

func (asc *AdminServiceClient) failRPC(ctx *gin.Context, err error, action string) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		asc.logger.Errorf("%s: %v", action, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed"})
		return true
	}
	code := http.StatusInternalServerError
	switch st.Code() {
	case codes.InvalidArgument:
		code = http.StatusBadRequest
	case codes.NotFound:
		code = http.StatusNotFound
	case codes.FailedPrecondition:
		code = http.StatusConflict
	case codes.PermissionDenied:
		code = http.StatusForbidden
	}
	if code >= 500 {
		asc.logger.Errorf("%s: %v", action, err)
	}
	ctx.AbortWithStatusJSON(code, gin.H{"error": st.Message()})
	return true
}

func summaryJSON(s *pb.AdminEventPaymentSummary) gin.H {
	if s == nil {
		return gin.H{}
	}
	return gin.H{
		"event_id":               s.EventId,
		"slug":                   s.Slug,
		"title":                  s.Title,
		"organizer_username":     s.OrganizerUsername,
		"start_time":             s.StartTime,
		"gross_captured":         inrJSON(s.GrossCaptured),
		"platform_fee":           inrJSON(s.PlatformFee),
		"gst":                    inrJSON(s.Gst),
		"host_payable_captured":  inrJSON(s.HostPayableCaptured),
		"refunded_gross":         inrJSON(s.RefundedGross),
		"settled":                inrJSON(s.Settled),
		"open_payable":           inrJSON(s.OpenPayable),
	}
}

func queryLimitOffset(ctx *gin.Context) (int32, int32) {
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(ctx.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return int32(limit), int32(offset)
}

func (asc *AdminServiceClient) ListEventPayments(ctx *gin.Context) {
	limit, offset := queryLimitOffset(ctx)
	res, err := asc.Events.AdminListEventPayments(ctx.Request.Context(), &pb.AdminListEventPaymentsReq{
		Limit:  limit,
		Offset: offset,
		Query:  strings.TrimSpace(ctx.Query("q")),
		Status: strings.TrimSpace(ctx.Query("status")),
	})
	if asc.failRPC(ctx, err, "list event payments") {
		return
	}
	events := make([]gin.H, 0, len(res.Events))
	for _, e := range res.Events {
		events = append(events, summaryJSON(e))
	}
	ctx.JSON(http.StatusOK, gin.H{"events": events, "total": res.Total})
}

func (asc *AdminServiceClient) GetEventPayments(ctx *gin.Context) {
	res, err := asc.Events.AdminGetEventPayments(ctx.Request.Context(), &pb.AdminGetEventPaymentsReq{
		Slug: ctx.Param("slug"),
	})
	if asc.failRPC(ctx, err, "get event payments") {
		return
	}
	payments := make([]gin.H, 0, len(res.Payments))
	for _, p := range res.Payments {
		payments = append(payments, gin.H{
			"attendee_username":  p.AttendeeUsername,
			"razorpay_order_id":  p.RazorpayOrderId,
			"razorpay_payment_id": p.RazorpayPaymentId,
			"status":             p.Status,
			"gross":              inrJSON(p.Gross),
			"platform_fee":       inrJSON(p.PlatformFee),
			"gst":                inrJSON(p.Gst),
			"host_payable":       inrJSON(p.HostPayable),
		})
	}
	settlements := make([]gin.H, 0, len(res.Settlements))
	for _, s := range res.Settlements {
		settlements = append(settlements, gin.H{
			"id":      s.Id,
			"payable": inrJSON(s.Payable),
			"status":  s.Status,
			"note":    s.Note,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{
		"event":       summaryJSON(res.Event),
		"payments":    payments,
		"settlements": settlements,
	})
}

func (asc *AdminServiceClient) CreateSettlement(ctx *gin.Context) {
	var body struct {
		Note string `json:"note"`
	}
	_ = ctx.ShouldBindJSON(&body)
	res, err := asc.Events.AdminCreateSettlement(ctx.Request.Context(), &pb.AdminCreateSettlementReq{
		Slug:  ctx.Param("slug"),
		Note:  body.Note,
		Actor: asc.staffActor(ctx),
	})
	if asc.failRPC(ctx, err, "create settlement") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"id":      res.Settlement.GetId(),
		"payable": inrJSON(res.Settlement.GetPayable()),
		"status":  res.Settlement.GetStatus(),
		"note":    res.Settlement.GetNote(),
	})
}

func (asc *AdminServiceClient) MarkSettlementPaid(ctx *gin.Context) {
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid settlement id"})
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = ctx.ShouldBindJSON(&body)
	res, err := asc.Events.AdminMarkSettlementPaid(ctx.Request.Context(), &pb.AdminMarkSettlementPaidReq{
		SettlementId: id,
		Note:         body.Note,
		Actor:        asc.staffActor(ctx),
	})
	if asc.failRPC(ctx, err, "mark settlement paid") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"id":      res.Settlement.GetId(),
		"payable": inrJSON(res.Settlement.GetPayable()),
		"status":  res.Settlement.GetStatus(),
		"note":    res.Settlement.GetNote(),
	})
}

func (asc *AdminServiceClient) FlagNsfw(entityType string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id := ctx.Param("slug")
		if id == "" {
			id = ctx.Param("blog_id")
		}
		if id == "" {
			id = ctx.Param("id")
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if err := ctx.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Reason) == "" {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "reason is required"})
			return
		}
		_, err := asc.Events.AdminFlagNsfw(ctx.Request.Context(), &pb.AdminFlagNsfwReq{
			EntityType: entityType,
			EntityId:   id,
			Reason:     body.Reason,
			Actor:      asc.staffActor(ctx),
		})
		if asc.failRPC(ctx, err, "flag nsfw") {
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"message": "flagged", "entity_type": entityType, "entity_id": id})
	}
}

func (asc *AdminServiceClient) HideEventComment(ctx *gin.Context) {
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid comment id"})
		return
	}
	_, err = asc.Events.AdminHideEventComment(ctx.Request.Context(), &pb.AdminHideEventCommentReq{
		EventSlug: ctx.Param("slug"),
		CommentId: id,
		Actor:     asc.staffActor(ctx),
	})
	if asc.failRPC(ctx, err, "hide comment") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "comment hidden"})
}

func (asc *AdminServiceClient) HideEventQuestion(ctx *gin.Context) {
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid question id"})
		return
	}
	_, err = asc.Events.AdminHideEventQuestion(ctx.Request.Context(), &pb.AdminHideEventQuestionReq{
		EventSlug:  ctx.Param("slug"),
		QuestionId: id,
		Actor:      asc.staffActor(ctx),
	})
	if asc.failRPC(ctx, err, "hide question") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "question hidden"})
}
