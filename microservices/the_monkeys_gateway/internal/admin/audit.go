package admin

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	userpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
)

func (asc *AdminServiceClient) userActor(ctx *gin.Context) *userpb.AdminActor {
	return &userpb.AdminActor{
		Username:  ctx.GetString("userName"),
		AccountId: ctx.GetString("accountId"),
		Role:      ctx.GetString("user_role"),
		Ip:        ctx.ClientIP(),
	}
}

func (asc *AdminServiceClient) groupActor(ctx *gin.Context) *grouppb.AdminActor {
	return &grouppb.AdminActor{
		Username:  ctx.GetString("userName"),
		AccountId: ctx.GetString("accountId"),
		Role:      ctx.GetString("user_role"),
		Ip:        ctx.ClientIP(),
	}
}

func (asc *AdminServiceClient) audit(c *gin.Context, action, entityType, entityID string, payload any) {
	raw := ""
	if payload != nil {
		b, err := json.Marshal(payload)
		if err == nil {
			raw = string(b)
		}
	}
	_, err := asc.Client.AdminWriteAudit(c.Request.Context(), &userpb.AdminWriteAuditReq{
		Action: action, EntityType: entityType, EntityId: entityID, Payload: raw, Actor: asc.userActor(c),
	})
	if err != nil && asc.logger != nil {
		asc.logger.Warnw("admin audit write failed", "action", action, "err", err)
	}
}

func blogReq(ctx *gin.Context, blogID string) *blogpb.BlogReq {
	return &blogpb.BlogReq{
		BlogId:    blogID,
		AccountId: ctx.GetString("accountId"),
		Username:  ctx.GetString("userName"),
		Ip:        ctx.ClientIP(),
		Client:    "admin",
	}
}
