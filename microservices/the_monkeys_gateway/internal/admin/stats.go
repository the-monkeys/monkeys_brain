package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	eventpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	userpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/constants"
	"golang.org/x/sync/errgroup"
)

func (asc *AdminServiceClient) GetStats(ctx *gin.Context) {
	g, gctx := errgroup.WithContext(ctx.Request.Context())
	var (
		users    *userpb.AdminUserStatsResp
		blogs    *userpb.AdminBlogStatsResp
		events   *eventpb.AdminEventStatsResp
		groups   *grouppb.AdminGroupStatsResp
		payments *eventpb.AdminPaymentStatsResp
		orphan   = int32(-1)
	)
	g.Go(func() error {
		var err error
		users, err = asc.Client.AdminUserStats(gctx, &userpb.AdminEmpty{})
		return err
	})
	g.Go(func() error {
		var err error
		blogs, err = asc.Client.AdminBlogStats(gctx, &userpb.AdminEmpty{})
		return err
	})
	g.Go(func() error {
		var err error
		events, err = asc.Events.AdminEventStats(gctx, &eventpb.AdminEmpty{})
		return err
	})
	g.Go(func() error {
		var err error
		groups, err = asc.Groups.AdminGroupStats(gctx, &grouppb.AdminEmpty{})
		return err
	})
	isAdmin := ctx.GetString("user_role") == constants.RoleAdmin
	if isAdmin {
		g.Go(func() error {
			var err error
			payments, err = asc.Events.AdminPaymentStats(gctx, &eventpb.AdminEmpty{})
			return err
		})
	}
	g.Go(func() error {
		es, err := asc.Blogs.AdminListESBlogIds(gctx, &blogpb.AdminListESBlogIdsReq{Limit: 100, Offset: 0})
		if err != nil {
			return nil
		}
		if es.GetTotal() > 100 {
			orphan = -1
			return nil
		}
		missing, err := asc.Client.AdminMissingBlogIds(gctx, &userpb.AdminMissingBlogIdsReq{BlogIds: es.BlogIds})
		if err != nil {
			return nil
		}
		orphan = int32(len(missing.GetBlogIds()))
		return nil
	})
	if asc.failRPC(ctx, g.Wait(), "admin stats") {
		return
	}
	out := gin.H{
		"users": gin.H{"total": users.GetTotal(), "new_7d": users.GetNew_7D()},
		"blogs": gin.H{
			"draft": blogs.GetDraft(), "published": blogs.GetPublished(),
			"scheduled": blogs.GetScheduled(), "archived": blogs.GetArchived(),
			"orphan_es": orphan,
		},
		"events": gin.H{
			"draft": events.GetDraft(), "published": events.GetPublished(),
			"live": events.GetLive(), "completed": events.GetCompleted(), "cancelled": events.GetCancelled(),
		},
		"groups": gin.H{
			"draft": groups.GetDraft(), "published": groups.GetPublished(),
			"archived": groups.GetArchived(), "suspended": groups.GetSuspended(),
		},
	}
	if isAdmin && payments != nil {
		out["payments_inr"] = gin.H{
			"captured_gross_paise":     payments.CapturedGrossPaise,
			"platform_fee_paise":       payments.PlatformFeePaise,
			"gst_paise":                payments.GstPaise,
			"host_payable_open_paise":  payments.HostPayableOpenPaise,
			"refunded_gross_paise":     payments.RefundedGrossPaise,
		}
	}
	ctx.JSON(http.StatusOK, out)
}
