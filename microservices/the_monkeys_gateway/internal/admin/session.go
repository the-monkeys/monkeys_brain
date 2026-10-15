package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
)

func (asc *AdminServiceClient) StaffSession(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Vary", "Cookie, Authorization")
	ctx.JSON(http.StatusOK, gin.H{
		"username":    ctx.GetString("userName"),
		"account_id":  ctx.GetString("accountId"),
		"role":        ctx.GetString("user_role"),
		"permissions": staffaccess.Permissions(ctx.GetString("user_role")),
	})
}
