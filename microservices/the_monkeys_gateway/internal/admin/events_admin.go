package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

func (asc *AdminServiceClient) ListAdminEvents(ctx *gin.Context) {
	limit, offset := queryLimitOffset(ctx)
	res, err := asc.Events.AdminListEvents(ctx.Request.Context(), &pb.AdminListEventsReq{
		Limit: limit, Offset: offset, Query: strings.TrimSpace(ctx.Query("q")),
		Status: strings.TrimSpace(ctx.Query("status")), GroupSlug: strings.TrimSpace(ctx.Query("group_slug")),
	})
	if asc.failRPC(ctx, err, "list events") {
		return
	}
	events := make([]gin.H, 0, len(res.Events))
	for _, e := range res.Events {
		start := ""
		if e.StartTime != nil {
			start = e.StartTime.AsTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		events = append(events, gin.H{
			"slug": e.Slug, "title": e.Title, "status": e.Status,
			"organizer_username": e.OrganizerUsername, "group_slug": e.GroupSlug, "start_time": start,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{"events": events, "total": res.Total})
}

func (asc *AdminServiceClient) CancelAdminEvent(ctx *gin.Context) {
	res, err := asc.Events.AdminCancelEvent(ctx.Request.Context(), &pb.AdminEventActionReq{
		Slug: ctx.Param("slug"), Actor: asc.staffActor(ctx),
	})
	if asc.failRPC(ctx, err, "cancel event") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": res.GetMessage(), "slug": ctx.Param("slug")})
}

func (asc *AdminServiceClient) UnpublishAdminEvent(ctx *gin.Context) {
	res, err := asc.Events.AdminUnpublishEvent(ctx.Request.Context(), &pb.AdminEventActionReq{
		Slug: ctx.Param("slug"), Actor: asc.staffActor(ctx),
	})
	if asc.failRPC(ctx, err, "unpublish event") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": res.GetMessage(), "slug": ctx.Param("slug")})
}

func (asc *AdminServiceClient) DeleteAdminEvent(ctx *gin.Context) {
	res, err := asc.Events.AdminDeleteEvent(ctx.Request.Context(), &pb.AdminEventActionReq{
		Slug: ctx.Param("slug"), Actor: asc.staffActor(ctx),
	})
	if asc.failRPC(ctx, err, "delete event") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": res.GetMessage(), "slug": ctx.Param("slug")})
}
