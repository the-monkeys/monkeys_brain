package user_service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// BusinessCardRequest is the REST body for create/update. card_state is kept as
// a raw JSON document and forwarded verbatim to the user service (which owns
// validation and storage), so the frontend contract can evolve without gateway
// changes.
type BusinessCardRequest struct {
	Name                string          `json:"name"`
	TemplateId          string          `json:"template_id"`
	ThemeId             string          `json:"theme_id"`
	CardState           json.RawMessage `json:"card_state"`
	IsDefault           bool            `json:"is_default"`
	AvatarAssetChecksum string          `json:"avatar_asset_checksum"`
	LogoAssetChecksum   string          `json:"logo_asset_checksum"`
}

// abortWithCardError maps gRPC status codes coming from the user service onto
// HTTP responses. Centralised so every card handler behaves consistently.
func abortWithCardError(ctx *gin.Context, err error) {
	switch status.Code(err) {
	case codes.NotFound:
		ctx.AbortWithStatusJSON(http.StatusNotFound, ReturnMessage{Message: "business card not found"})
	case codes.InvalidArgument:
		ctx.AbortWithStatusJSON(http.StatusBadRequest, ReturnMessage{Message: status.Convert(err).Message()})
	case codes.Unauthenticated:
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
	default:
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage{Message: "something went wrong"})
	}
}

func (asc *UserServiceClient) CreateBusinessCard(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	var req BusinessCardRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		asc.log.Errorw("create business card bind json failed", "err", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, ReturnMessage{Message: "invalid request body"})
		return
	}

	res, err := asc.Client.CreateBusinessCard(context.Background(), &pb.CreateBusinessCardReq{
		AccountId:           accountID,
		Name:                req.Name,
		TemplateId:          req.TemplateId,
		ThemeId:             req.ThemeId,
		CardState:           string(req.CardState),
		IsDefault:           req.IsDefault,
		AvatarAssetChecksum: req.AvatarAssetChecksum,
		LogoAssetChecksum:   req.LogoAssetChecksum,
		Ip:                  ctx.Request.Header.Get("IP"),
		Client:              ctx.Request.Header.Get("Client"),
	})
	if err != nil {
		asc.log.Errorw("create business card failed", "err", err)
		abortWithCardError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, res)
}

func (asc *UserServiceClient) ListBusinessCards(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	limit, _ := strconv.Atoi(ctx.Query("limit"))
	offset, _ := strconv.Atoi(ctx.Query("offset"))

	res, err := asc.Client.ListBusinessCards(context.Background(), &pb.ListBusinessCardsReq{
		AccountId: accountID,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		asc.log.Errorw("list business cards failed", "err", err)
		abortWithCardError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) GetBusinessCard(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	res, err := asc.Client.GetBusinessCard(context.Background(), &pb.GetBusinessCardReq{
		AccountId: accountID,
		Id:        ctx.Param("card_id"),
	})
	if err != nil {
		asc.log.Errorw("get business card failed", "err", err)
		abortWithCardError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) UpdateBusinessCard(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	var req BusinessCardRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		asc.log.Errorw("update business card bind json failed", "err", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, ReturnMessage{Message: "invalid request body"})
		return
	}

	res, err := asc.Client.UpdateBusinessCard(context.Background(), &pb.UpdateBusinessCardReq{
		AccountId:           accountID,
		Id:                  ctx.Param("card_id"),
		Name:                req.Name,
		TemplateId:          req.TemplateId,
		ThemeId:             req.ThemeId,
		CardState:           string(req.CardState),
		IsDefault:           req.IsDefault,
		AvatarAssetChecksum: req.AvatarAssetChecksum,
		LogoAssetChecksum:   req.LogoAssetChecksum,
		Ip:                  ctx.Request.Header.Get("IP"),
		Client:              ctx.Request.Header.Get("Client"),
	})
	if err != nil {
		asc.log.Errorw("update business card failed", "err", err)
		abortWithCardError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) DeleteBusinessCard(ctx *gin.Context) {
	accountID := ctx.GetString("accountId")
	if accountID == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	res, err := asc.Client.DeleteBusinessCard(context.Background(), &pb.DeleteBusinessCardReq{
		AccountId: accountID,
		Id:        ctx.Param("card_id"),
		Ip:        ctx.Request.Header.Get("IP"),
		Client:    ctx.Request.Header.Get("Client"),
	})
	if err != nil {
		asc.log.Errorw("delete business card failed", "err", err)
		abortWithCardError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, res)
}
