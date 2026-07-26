package user_service

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UserAddressRequest is the REST body for create/update of a PRIVATE address.
// These values are never exposed on the public profile.
type UserAddressRequest struct {
	Label      string `json:"label"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
	IsDefault  bool   `json:"is_default"`
}

// abortWithAddressError maps gRPC status codes coming from the user service onto
// HTTP responses. Centralised so every address handler behaves consistently.
func abortWithAddressError(ctx *gin.Context, err error) {
	switch status.Code(err) {
	case codes.NotFound:
		ctx.AbortWithStatusJSON(http.StatusNotFound, ReturnMessage{Message: "address not found"})
	case codes.InvalidArgument:
		ctx.AbortWithStatusJSON(http.StatusBadRequest, ReturnMessage{Message: status.Convert(err).Message()})
	case codes.Unauthenticated:
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
	default:
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage{Message: "something went wrong"})
	}
}

func (asc *UserServiceClient) CreateUserAddress(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	var req UserAddressRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		asc.log.Errorw("create user address bind json failed", "err", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, ReturnMessage{Message: "invalid request body"})
		return
	}

	res, err := asc.Client.CreateUserAddress(context.Background(), &pb.CreateUserAddressReq{
		AccountId:  accountID,
		Label:      req.Label,
		Line1:      req.Line1,
		Line2:      req.Line2,
		City:       req.City,
		State:      req.State,
		PostalCode: req.PostalCode,
		Country:    req.Country,
		IsDefault:  req.IsDefault,
		Ip:         ctx.Request.Header.Get("IP"),
		Client:     ctx.Request.Header.Get("Client"),
	})
	if err != nil {
		asc.log.Errorw("create user address failed", "err", err)
		abortWithAddressError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, res)
}

func (asc *UserServiceClient) ListUserAddresses(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	res, err := asc.Client.ListUserAddresses(context.Background(), &pb.ListUserAddressesReq{
		AccountId: accountID,
	})
	if err != nil {
		asc.log.Errorw("list user addresses failed", "err", err)
		abortWithAddressError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) GetUserAddress(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	res, err := asc.Client.GetUserAddress(context.Background(), &pb.GetUserAddressReq{
		AccountId: accountID,
		Id:        ctx.Param("address_id"),
	})
	if err != nil {
		asc.log.Errorw("get user address failed", "err", err)
		abortWithAddressError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) UpdateUserAddress(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	var req UserAddressRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		asc.log.Errorw("update user address bind json failed", "err", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, ReturnMessage{Message: "invalid request body"})
		return
	}

	res, err := asc.Client.UpdateUserAddress(context.Background(), &pb.UpdateUserAddressReq{
		AccountId:  accountID,
		Id:         ctx.Param("address_id"),
		Label:      req.Label,
		Line1:      req.Line1,
		Line2:      req.Line2,
		City:       req.City,
		State:      req.State,
		PostalCode: req.PostalCode,
		Country:    req.Country,
		IsDefault:  req.IsDefault,
		Ip:         ctx.Request.Header.Get("IP"),
		Client:     ctx.Request.Header.Get("Client"),
	})
	if err != nil {
		asc.log.Errorw("update user address failed", "err", err)
		abortWithAddressError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) DeleteUserAddress(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	res, err := asc.Client.DeleteUserAddress(context.Background(), &pb.DeleteUserAddressReq{
		AccountId: accountID,
		Id:        ctx.Param("address_id"),
		Ip:        ctx.Request.Header.Get("IP"),
		Client:    ctx.Request.Header.Get("Client"),
	})
	if err != nil {
		asc.log.Errorw("delete user address failed", "err", err)
		abortWithAddressError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, res)
}
