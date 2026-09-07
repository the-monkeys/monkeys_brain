package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
)

func (asc *AdminServiceClient) ListAdminGroups(ctx *gin.Context) {
	limit, offset := queryLimitOffset(ctx)
	res, err := asc.Groups.AdminListGroups(ctx.Request.Context(), &grouppb.AdminListGroupsReq{
		Limit: limit, Offset: offset, Query: strings.TrimSpace(ctx.Query("q")), Status: strings.TrimSpace(ctx.Query("status")),
	})
	if asc.failRPC(ctx, err, "list groups") {
		return
	}
	groups := make([]gin.H, 0, len(res.Groups))
	for _, g := range res.Groups {
		groups = append(groups, gin.H{
			"slug": g.Slug, "name": g.Name, "status": g.Status, "visibility": g.Visibility,
			"organizer_username": g.OrganizerUsername, "member_count": g.MemberCount,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{"groups": groups, "total": res.Total})
}

func (asc *AdminServiceClient) SuspendGroup(ctx *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = ctx.ShouldBindJSON(&body)
	_, err := asc.Groups.AdminSuspendGroup(ctx.Request.Context(), &grouppb.AdminSuspendGroupReq{
		Slug: ctx.Param("slug"), Reason: body.Reason, Actor: asc.groupActor(ctx),
	})
	if asc.failRPC(ctx, err, "suspend group") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "suspended", "slug": ctx.Param("slug")})
}

func (asc *AdminServiceClient) DeleteAdminGroup(ctx *gin.Context) {
	res, err := asc.Groups.AdminDeleteGroup(ctx.Request.Context(), &grouppb.AdminSuspendGroupReq{
		Slug: ctx.Param("slug"), Actor: asc.groupActor(ctx),
	})
	if asc.failRPC(ctx, err, "delete group") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": res.GetMessage(), "slug": ctx.Param("slug")})
}
