package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	userpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/constants"
)

func (asc *AdminServiceClient) ListUsers(ctx *gin.Context) {
	limit, offset := queryLimitOffset(ctx)
	res, err := asc.Client.AdminListUsers(ctx.Request.Context(), &userpb.AdminListUsersReq{
		Limit: limit, Offset: offset, Query: strings.TrimSpace(ctx.Query("q")),
	})
	if asc.failRPC(ctx, err, "list users") {
		return
	}
	users := make([]gin.H, 0, len(res.Users))
	for _, u := range res.Users {
		created := ""
		if u.CreatedAt != nil {
			created = u.CreatedAt.AsTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		users = append(users, gin.H{
			"account_id": u.AccountId, "username": u.Username, "email": u.Email,
			"status": u.Status, "role": u.Role, "verified": u.Verified,
			"created_at": created, "flags": u.Flags,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{"users": users, "total": res.Total})
}

func (asc *AdminServiceClient) SetUserRole(ctx *gin.Context) {
	var body struct {
		Role string `json:"role"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	_, err := asc.Client.AdminSetUserRole(ctx.Request.Context(), &userpb.AdminSetUserRoleReq{
		Username: ctx.Param("id"), Role: body.Role, Actor: asc.userActor(ctx),
	})
	if asc.failRPC(ctx, err, "set role") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "role updated"})
}

func (asc *AdminServiceClient) FlagUser(ctx *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
		Type   string `json:"type"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Reason) == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "reason and type are required"})
		return
	}
	_, err := asc.Client.AdminFlagUser(ctx.Request.Context(), &userpb.AdminFlagUserReq{
		Username: ctx.Param("id"), FlagType: body.Type, Reason: body.Reason, Actor: asc.userActor(ctx),
	})
	if asc.failRPC(ctx, err, "flag user") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "flagged", "user_id": ctx.Param("id"), "flag_type": body.Type})
}

func (asc *AdminServiceClient) UnflagUserJWT(ctx *gin.Context) {
	var body struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}
	_ = ctx.ShouldBindJSON(&body)
	_, err := asc.Client.AdminUnflagUser(ctx.Request.Context(), &userpb.AdminFlagUserReq{
		Username: ctx.Param("id"), FlagType: body.Type, Reason: body.Reason, Actor: asc.userActor(ctx),
	})
	if asc.failRPC(ctx, err, "unflag user") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "unflagged", "user_id": ctx.Param("id")})
}

func (asc *AdminServiceClient) SuspendUser(ctx *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = ctx.ShouldBindJSON(&body)
	_, err := asc.Client.AdminSuspendUser(ctx.Request.Context(), &userpb.AdminSuspendUserReq{
		Username: ctx.Param("id"), Reason: body.Reason, Actor: asc.userActor(ctx),
	})
	if asc.failRPC(ctx, err, "suspend user") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "suspended", "user_id": ctx.Param("id")})
}

func (asc *AdminServiceClient) lookupUserRole(ctx *gin.Context, username string) (string, bool, error) {
	res, err := asc.Client.AdminListUsers(ctx.Request.Context(), &userpb.AdminListUsersReq{
		Query: username, Limit: 20,
	})
	if err != nil {
		return "", false, err
	}
	for _, u := range res.Users {
		if strings.EqualFold(u.GetUsername(), username) {
			return u.GetRole(), true, nil
		}
	}
	return "", false, nil
}

func (asc *AdminServiceClient) DeleteUserJWT(ctx *gin.Context) {
	target := strings.TrimSpace(ctx.Param("id"))
	actor := strings.TrimSpace(ctx.GetString("userName"))
	if target == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}
	if strings.EqualFold(target, actor) {
		ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cannot delete your own account here"})
		return
	}
	if ctx.GetString("user_role") == constants.RoleCommunity {
		role, found, err := asc.lookupUserRole(ctx, target)
		if asc.failRPC(ctx, err, "lookup user") {
			return
		}
		if found {
			switch role {
			case constants.RoleAdmin, constants.RoleSupport, constants.RoleCommunity, constants.RoleOwner:
				ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Community cannot delete staff accounts"})
				return
			}
		}
	}
	_, err := asc.Client.AdminDeleteUser(ctx.Request.Context(), &userpb.AdminDeleteUserReq{
		Username: target, Actor: asc.userActor(ctx),
	})
	if asc.failRPC(ctx, err, "delete user") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "User successfully deleted", "user_id": target})
}
