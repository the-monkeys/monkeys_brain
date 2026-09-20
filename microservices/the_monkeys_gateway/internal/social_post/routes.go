package social_post

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_social_post/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type createMockAccountRequest struct {
	Platform    string `json:"platform" binding:"required"`
	Handle      string `json:"handle" binding:"required"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

type createPostRequest struct {
	BaseText string `json:"base_text" binding:"max=63206"`
}

type updatePostRequest struct {
	BaseText        string `json:"base_text" binding:"max=63206"`
	ExpectedVersion int64  `json:"expected_version"`
}

type renditionRequest struct {
	SocialAccountID          string `json:"social_account_id" binding:"required"`
	TextOverride             string `json:"text_override"`
	ScheduledAtOverride      string `json:"scheduled_at_override"`
	ScheduleTimezoneOverride string `json:"schedule_timezone_override"`
	ExpectedVersion          int64  `json:"expected_version"`
}

type mediaRequest struct {
	SocialAccountID string   `json:"social_account_id" binding:"required"`
	AssetIDs        []string `json:"asset_ids"`
	ExpectedVersion int64    `json:"expected_version"`
}

type scheduleRequest struct {
	ScheduledAt      string `json:"scheduled_at" binding:"required"`
	ScheduleTimezone string `json:"schedule_timezone" binding:"required"`
	ExpectedVersion  int64  `json:"expected_version"`
}

type actionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type importAssetRequest struct {
	SourceAssetRef string `json:"source_asset_ref" binding:"required"`
	SourceKind     string `json:"source_kind" binding:"required"`
}

type reorderQueueRequest struct {
	PostIDsInOrder []string `json:"post_ids_in_order" binding:"required"`
}

var platformToString = map[pb.Platform]string{
	pb.Platform_PLATFORM_X:         "x",
	pb.Platform_PLATFORM_LINKEDIN:  "linkedin",
	pb.Platform_PLATFORM_INSTAGRAM: "instagram",
	pb.Platform_PLATFORM_FACEBOOK:  "facebook",
	pb.Platform_PLATFORM_YOUTUBE:   "youtube",
	pb.Platform_PLATFORM_TIKTOK:    "tiktok",
}

func platformToName(p pb.Platform) string {
	if s, ok := platformToString[p]; ok {
		return s
	}
	return ""
}

type MediaAssetDTO struct {
	ID          string `json:"id"`
	ObjectKey   string `json:"object_key"`
	Checksum    string `json:"checksum,omitempty"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
	MediaKind   string `json:"media_kind"`
	SourceKind  string `json:"source_kind,omitempty"`
	Width       int32  `json:"width,omitempty"`
	Height      int32  `json:"height,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
}

type ValidationMetadataDTO struct {
	Platform           string   `json:"platform"`
	MaxTextCharacters  int32    `json:"max_text_characters"`
	AllowedMediaKinds  []string `json:"allowed_media_kinds"`
	MaxMediaCount      int32    `json:"max_media_count"`
	MaxMediaBytes      int64    `json:"max_media_bytes"`
	MaxVideoDurationMs int64    `json:"max_video_duration_ms"`
	MediaRequired      bool     `json:"media_required"`
}

type SocialAccountDTO struct {
	ID          string                 `json:"id"`
	Platform    string                 `json:"platform"`
	DisplayName string                 `json:"display_name"`
	Handle      string                 `json:"handle"`
	Status      string                 `json:"status"`
	IsMock      bool                   `json:"is_mock"`
	AvatarURL   string                 `json:"avatar_url,omitempty"`
	Validation  *ValidationMetadataDTO `json:"validation,omitempty"`
}

type RenditionDTO struct {
	ID                       string          `json:"id"`
	SocialAccountID          string          `json:"social_account_id"`
	Platform                 string          `json:"platform"`
	TextOverride             string          `json:"text_override,omitempty"`
	ScheduledAt              string          `json:"scheduled_at,omitempty"`
	ScheduleTimezone         string          `json:"schedule_timezone,omitempty"`
	ScheduledAtOverride      string          `json:"scheduled_at_override,omitempty"`
	ScheduleTimezoneOverride string          `json:"schedule_timezone_override,omitempty"`
	State                    string          `json:"state"`
	Status                   string          `json:"status,omitempty"`
	Version                  int64           `json:"version"`
	Media                    []MediaAssetDTO `json:"media"`
	MediaAssetIDs            []string        `json:"media_asset_ids,omitempty"`
	ProviderPostRef          string          `json:"provider_post_ref,omitempty"`
	LastErrorCode            string          `json:"last_error_code,omitempty"`
	LastErrorMessage         string          `json:"last_error_message,omitempty"`
}

type SocialPostDTO struct {
	ID               string         `json:"id"`
	BaseText         string         `json:"base_text"`
	Text             string         `json:"text,omitempty"`
	State            string         `json:"state"`
	Status           string         `json:"status"`
	Version          int64          `json:"version"`
	ScheduledAt      string         `json:"scheduled_at,omitempty"`
	ScheduleTimezone string         `json:"schedule_timezone,omitempty"`
	QueuePosition    *int64         `json:"queue_position,omitempty"`
	Renditions       []RenditionDTO `json:"renditions"`
	CreatedAt        string         `json:"created_at,omitempty"`
	UpdatedAt        string         `json:"updated_at,omitempty"`
}

type FieldViolationDTO struct {
	Field   string `json:"field"`
	RuleID  string `json:"rule_id"`
	Message string `json:"message"`
	Actual  string `json:"actual,omitempty"`
	Allowed string `json:"allowed,omitempty"`
}

func toMediaAssetDTO(a *pb.MediaAsset) *MediaAssetDTO {
	if a == nil {
		return nil
	}
	return &MediaAssetDTO{
		ID:          a.GetId(),
		ObjectKey:   a.GetObjectKey(),
		Checksum:    a.GetChecksum(),
		ContentType: a.GetContentType(),
		ByteSize:    a.GetByteSize(),
		MediaKind:   a.GetMediaKind(),
		SourceKind:  a.GetSourceKind(),
		Width:       a.GetWidth(),
		Height:      a.GetHeight(),
		DurationMs:  a.GetDurationMs(),
	}
}

func toValidationMetadataDTO(v *pb.ValidationMetadata) *ValidationMetadataDTO {
	if v == nil {
		return nil
	}
	allowedMedia := v.GetAllowedMediaKinds()
	if allowedMedia == nil {
		allowedMedia = []string{}
	}
	return &ValidationMetadataDTO{
		Platform:           platformToName(v.GetPlatform()),
		MaxTextCharacters:  v.GetMaxTextCharacters(),
		AllowedMediaKinds:  allowedMedia,
		MaxMediaCount:      v.GetMaxMediaCount(),
		MaxMediaBytes:      v.GetMaxMediaBytes(),
		MaxVideoDurationMs: v.GetMaxVideoDurationMs(),
		MediaRequired:      v.GetMediaRequired(),
	}
}

func toSocialAccountDTO(a *pb.SocialAccount) *SocialAccountDTO {
	if a == nil {
		return nil
	}
	return &SocialAccountDTO{
		ID:          a.GetId(),
		Platform:    platformToName(a.GetPlatform()),
		DisplayName: a.GetDisplayName(),
		Handle:      a.GetHandle(),
		Status:      a.GetStatus(),
		IsMock:      a.GetIsMock(),
		AvatarURL:   a.GetAvatarUrl(),
		Validation:  toValidationMetadataDTO(a.GetValidation()),
	}
}

func toRenditionDTO(r *pb.Rendition) *RenditionDTO {
	if r == nil {
		return nil
	}
	media := make([]MediaAssetDTO, 0, len(r.GetMedia()))
	mediaIDs := make([]string, 0, len(r.GetMedia()))
	for _, m := range r.GetMedia() {
		if dto := toMediaAssetDTO(m); dto != nil {
			media = append(media, *dto)
			mediaIDs = append(mediaIDs, dto.ID)
		}
	}
	return &RenditionDTO{
		ID:                       r.GetId(),
		SocialAccountID:          r.GetSocialAccountId(),
		Platform:                 platformToName(r.GetPlatform()),
		TextOverride:             r.GetTextOverride(),
		ScheduledAt:              r.GetScheduledAt(),
		ScheduleTimezone:         r.GetScheduleTimezone(),
		ScheduledAtOverride:      r.GetScheduledAtOverride(),
		ScheduleTimezoneOverride: r.GetScheduleTimezoneOverride(),
		State:                    r.GetState(),
		Status:                   r.GetState(),
		Version:                  r.GetVersion(),
		Media:                    media,
		MediaAssetIDs:            mediaIDs,
		ProviderPostRef:          r.GetProviderPostRef(),
		LastErrorCode:            r.GetLastErrorCode(),
		LastErrorMessage:         r.GetLastErrorMessage(),
	}
}

func toPostDTO(p *pb.SocialPost) *SocialPostDTO {
	if p == nil {
		return nil
	}
	renditions := make([]RenditionDTO, 0, len(p.GetRenditions()))
	for _, r := range p.GetRenditions() {
		if dto := toRenditionDTO(r); dto != nil {
			renditions = append(renditions, *dto)
		}
	}
	var queuePos *int64
	if p.GetQueuePosition() > 0 {
		qp := p.GetQueuePosition()
		queuePos = &qp
	}
	return &SocialPostDTO{
		ID:               p.GetId(),
		BaseText:         p.GetBaseText(),
		Text:             p.GetBaseText(),
		State:            p.GetState(),
		Status:           p.GetState(),
		Version:          p.GetVersion(),
		ScheduledAt:      p.GetScheduledAt(),
		ScheduleTimezone: p.GetScheduleTimezone(),
		QueuePosition:    queuePos,
		Renditions:       renditions,
		CreatedAt:        p.GetCreatedAt(),
		UpdatedAt:        p.GetUpdatedAt(),
	}
}

func toViolationsDTO(violations []*pb.FieldViolation) []FieldViolationDTO {
	if violations == nil {
		return []FieldViolationDTO{}
	}
	out := make([]FieldViolationDTO, 0, len(violations))
	for _, v := range violations {
		if v == nil {
			continue
		}
		out = append(out, FieldViolationDTO{
			Field:   v.GetField(),
			RuleID:  v.GetRuleId(),
			Message: v.GetMessage(),
			Actual:  v.GetActual(),
			Allowed: v.GetAllowed(),
		})
	}
	return out
}

func toPostResponseDTO(resp *pb.PostResponse) gin.H {
	if resp == nil {
		return gin.H{
			"post":       nil,
			"violations": []FieldViolationDTO{},
		}
	}
	return gin.H{
		"post":       toPostDTO(resp.GetPost()),
		"violations": toViolationsDTO(resp.GetViolations()),
	}
}

func toListPostsResponseDTO(resp *pb.ListPostsResponse) gin.H {
	if resp == nil {
		return gin.H{
			"items":           []SocialPostDTO{},
			"posts":           []SocialPostDTO{},
			"next_page_token": "",
		}
	}
	posts := make([]SocialPostDTO, 0, len(resp.GetPosts()))
	for _, p := range resp.GetPosts() {
		if dto := toPostDTO(p); dto != nil {
			posts = append(posts, *dto)
		}
	}
	return gin.H{
		"items":           posts,
		"posts":           posts,
		"next_page_token": resp.GetNextPageToken(),
	}
}

func toListAccountsResponseDTO(resp *pb.ListAccountsResponse) gin.H {
	if resp == nil {
		return gin.H{
			"accounts": []SocialAccountDTO{},
		}
	}
	accounts := make([]SocialAccountDTO, 0, len(resp.GetAccounts()))
	for _, a := range resp.GetAccounts() {
		if dto := toSocialAccountDTO(a); dto != nil {
			accounts = append(accounts, *dto)
		}
	}
	return gin.H{
		"accounts": accounts,
	}
}

func toListMediaResponseDTO(resp *pb.ListMediaAssetsResponse) gin.H {
	if resp == nil {
		return gin.H{
			"assets":          []MediaAssetDTO{},
			"items":           []MediaAssetDTO{},
			"next_page_token": "",
		}
	}
	assets := make([]MediaAssetDTO, 0, len(resp.GetAssets()))
	for _, a := range resp.GetAssets() {
		if dto := toMediaAssetDTO(a); dto != nil {
			assets = append(assets, *dto)
		}
	}
	return gin.H{
		"assets":          assets,
		"items":           assets,
		"next_page_token": resp.GetNextPageToken(),
	}
}

func toValidationMetadataResponseDTO(resp *pb.ListValidationMetadataResponse) gin.H {
	if resp == nil {
		return gin.H{
			"platforms": []ValidationMetadataDTO{},
		}
	}
	platforms := make([]ValidationMetadataDTO, 0, len(resp.GetPlatforms()))
	for _, vm := range resp.GetPlatforms() {
		if dto := toValidationMetadataDTO(vm); dto != nil {
			platforms = append(platforms, *dto)
		}
	}
	return gin.H{
		"platforms": platforms,
	}
}

type AssetOwnershipVerifier interface {
	VerifySocialAssetReference(*gin.Context, string) bool
}

func RegisterRoutes(router *gin.Engine, cfg *config.Config, authClient *auth.ServiceClient, log *zap.SugaredLogger, assetVerifier AssetOwnershipVerifier) {
	conn, err := grpc.NewClient(
		fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysSocialPost, cfg.Microservices.SocialPostPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		panic(fmt.Errorf("dial social post service: %w", err))
	}
	client := pb.NewSocialPostServiceClient(conn)
	mw := auth.InitAuthMiddleware(authClient, log)
	routes := router.Group("/api/v1/social-posts")
	routes.Use(mw.AuthRequired)

	call := func(c *gin.Context, fn func(context.Context, *pb.RequestContext) (interface{}, error)) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		resp, err := fn(ctx, requestContext(c))
		if err != nil {
			writeGRPCError(c, err)
			return
		}
		switch v := resp.(type) {
		case *pb.PostResponse:
			c.JSON(http.StatusOK, toPostResponseDTO(v))
		case *pb.ListPostsResponse:
			c.JSON(http.StatusOK, toListPostsResponseDTO(v))
		case *pb.ListAccountsResponse:
			c.JSON(http.StatusOK, toListAccountsResponseDTO(v))
		case *pb.ListValidationMetadataResponse:
			c.JSON(http.StatusOK, toValidationMetadataResponseDTO(v))
		case *pb.ListMediaAssetsResponse:
			c.JSON(http.StatusOK, toListMediaResponseDTO(v))
		case *pb.ImportStudioAssetResponse:
			var assetDTO *MediaAssetDTO
			if v.GetAsset() != nil {
				assetDTO = toMediaAssetDTO(v.GetAsset())
			}
			c.JSON(http.StatusOK, gin.H{"asset": assetDTO})
		case *pb.SocialAccount:
			c.JSON(http.StatusOK, gin.H{"account": toSocialAccountDTO(v)})
		case *pb.DisconnectAccountResponse:
			c.JSON(http.StatusOK, gin.H{
				"success":               v.GetSuccess(),
				"cancelled_jobs_count":  v.GetCancelledJobsCount(),
				"drafts_reverted_count": v.GetDraftsRevertedCount(),
			})
		case *pb.ReorderQueueResponse:
			posts := make([]SocialPostDTO, 0, len(v.GetPosts()))
			for _, p := range v.GetPosts() {
				if dto := toPostDTO(p); dto != nil {
					posts = append(posts, *dto)
				}
			}
			c.JSON(http.StatusOK, gin.H{"items": posts, "posts": posts})
		default:
			c.JSON(http.StatusOK, resp)
		}
	}

	routes.POST("", func(c *gin.Context) {
		var body createPostRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid post payload"})
			return
		}
		key, ok := idempotencyKey(c)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		resp, err := client.CreatePost(ctx, &pb.CreatePostRequest{
			Context:  requestContextWithKey(c, key),
			BaseText: body.BaseText,
		})
		if err != nil {
			writeGRPCError(c, err)
			return
		}
		c.JSON(http.StatusCreated, toPostResponseDTO(resp))
	})

	routes.GET("", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			pageSize := parsePageSize(c.Query("page_size"))
			return client.ListPosts(ctx, &pb.ListPostsRequest{
				Context: reqCtx, States: splitQuery(c.Query("states")),
				From: c.Query("from"), To: c.Query("to"), PageSize: int32(pageSize),
			})
		})
	})
	routes.GET("/calendar", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.ListCalendar(ctx, &pb.ListPostsRequest{
				Context: reqCtx, From: c.Query("from"), To: c.Query("to"),
				PageSize: int32(parsePageSize(c.Query("page_size"))),
			})
		})
	})
	routes.GET("/queue", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.ListQueue(ctx, &pb.ListPostsRequest{
				Context: reqCtx, PageSize: int32(parsePageSize(c.Query("page_size"))),
			})
		})
	})
	routes.PUT("/queue/order", func(c *gin.Context) {
		var body reorderQueueRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid queue order"})
			return
		}
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.ReorderQueue(ctx, &pb.ReorderQueueRequest{
				Context: reqCtx, PostIdsInOrder: body.PostIDsInOrder,
			})
		})
	})
	routes.GET("/accounts", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.ListAccounts(ctx, &pb.ListAccountsRequest{Context: reqCtx})
		})
	})
	routes.POST("/accounts/mock", func(c *gin.Context) {
		var body struct {
			Platform    string `json:"platform" binding:"required"`
			Handle      string `json:"handle" binding:"required"`
			DisplayName string `json:"display_name"`
			AvatarURL   string `json:"avatar_url"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mock account payload: platform and handle are required"})
			return
		}
		if body.DisplayName == "" {
			body.DisplayName = body.Handle
		}
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.LinkAccount(ctx, &pb.LinkAccountRequest{
				Context:            reqCtx,
				Platform:           body.Platform,
				Handle:             body.Handle,
				DisplayName:        body.DisplayName,
				ExternalAccountRef: fmt.Sprintf("mock:%s:%s:%d", body.Platform, body.Handle, time.Now().UnixNano()),
				AvatarUrl:          body.AvatarURL,
				IsMock:             true,
			})
		})
	})
	routes.DELETE("/accounts/:accountID", func(c *gin.Context) {
		accountID := c.Param("accountID")
		if accountID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "accountID is required"})
			return
		}
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.DisconnectAccount(ctx, &pb.DisconnectAccountRequest{
				Context:   reqCtx,
				AccountId: accountID,
			})
		})
	})
	routes.GET("/oauth/:platform/authorize", func(c *gin.Context) {
		platform := c.Param("platform")
		redirectURL := fmt.Sprintf("/studio/accounts?mock_prompt=true&platform=%s", platform)
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
	})
	routes.GET("/oauth/:platform/callback", func(c *gin.Context) {
		platform := c.Param("platform")
		c.Redirect(http.StatusTemporaryRedirect, fmt.Sprintf("/studio/accounts?connected=true&platform=%s", platform))
	})
	routes.GET("/validation-metadata", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.ListValidationMetadata(ctx, &pb.ListValidationMetadataRequest{Context: reqCtx})
		})
	})
	routes.GET("/media", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.ListMediaAssets(ctx, &pb.ListMediaAssetsRequest{
				Context: reqCtx, PageSize: int32(parsePageSize(c.Query("page_size"))),
			})
		})
	})
	routes.POST("/media/import", func(c *gin.Context) {
		var body importAssetRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid media import payload"})
			return
		}
		key, ok := idempotencyKey(c)
		if !ok {
			return
		}
		if (body.SourceKind == "snapshot" || body.SourceKind == "card") &&
			!verifyImportSource(c, assetVerifier, body.SourceAssetRef) {
			return
		}
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			reqCtx.IdempotencyKey = key
			return client.ImportStudioAsset(ctx, &pb.ImportStudioAssetRequest{
				Context: reqCtx, SourceAssetRef: body.SourceAssetRef, SourceKind: body.SourceKind,
			})
		})
	})
	routes.DELETE("/media/:assetID", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.DeleteMediaAsset(ctx, &pb.DeleteMediaAssetRequest{
				Context: reqCtx, AssetId: c.Param("assetID"),
			})
		})
	})

	routes.GET("/:postID", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.GetPost(ctx, &pb.GetPostRequest{Context: reqCtx, PostId: c.Param("postID")})
		})
	})
	routes.PATCH("/:postID", func(c *gin.Context) {
		var body updatePostRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid post payload"})
			return
		}

		key, ok := idempotencyKey(c)
		if !ok {
			return
		}
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			reqCtx.IdempotencyKey = key
			return client.UpdatePost(ctx, &pb.UpdatePostRequest{
				Context: reqCtx, PostId: c.Param("postID"), BaseText: body.BaseText,
				ExpectedVersion: body.ExpectedVersion,
			})
		})
	})
	routes.DELETE("/:postID", func(c *gin.Context) {
		var body actionRequest
		_ = c.ShouldBindJSON(&body)
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.DeleteDraft(ctx, &pb.DeleteDraftRequest{
				Context: reqCtx, PostId: c.Param("postID"), ExpectedVersion: body.ExpectedVersion,
			})
		})
	})
	routes.PUT("/:postID/renditions", func(c *gin.Context) {
		var body renditionRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rendition payload"})
			return
		}
		key, ok := idempotencyKey(c)
		if !ok {
			return
		}
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			reqCtx.IdempotencyKey = key
			return client.UpsertRendition(ctx, &pb.UpsertRenditionRequest{
				Context: reqCtx, PostId: c.Param("postID"), SocialAccountId: body.SocialAccountID,
				TextOverride: body.TextOverride, ScheduledAtOverride: body.ScheduledAtOverride,
				ScheduleTimezoneOverride: body.ScheduleTimezoneOverride, ExpectedVersion: body.ExpectedVersion,
			})
		})
	})
	routes.PUT("/:postID/renditions/media", func(c *gin.Context) {
		var body mediaRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rendition media payload"})
			return
		}
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.SetRenditionMedia(ctx, &pb.SetRenditionMediaRequest{
				Context: reqCtx, PostId: c.Param("postID"), SocialAccountId: body.SocialAccountID,
				AssetIds: body.AssetIDs, ExpectedVersion: body.ExpectedVersion,
			})
		})
	})
	routes.POST("/:postID/schedule", func(c *gin.Context) {
		scheduleRoute(c, client, false)
	})
	routes.PUT("/:postID/schedule", func(c *gin.Context) {
		scheduleRoute(c, client, true)
	})
	routes.DELETE("/:postID/schedule", func(c *gin.Context) {
		var body actionRequest
		_ = c.ShouldBindJSON(&body)
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.CancelSchedule(ctx, &pb.PostActionRequest{
				Context: reqCtx, PostId: c.Param("postID"), ExpectedVersion: body.ExpectedVersion,
			})
		})
	})
	routes.POST("/:postID/publish-now", func(c *gin.Context) {
		var body actionRequest
		_ = c.ShouldBindJSON(&body)
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.PublishNow(ctx, &pb.PostActionRequest{
				Context: reqCtx, PostId: c.Param("postID"), ExpectedVersion: body.ExpectedVersion,
			})
		})
	})
	routes.GET("/:postID/history", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.ListPostHistory(ctx, &pb.HistoryRequest{
				Context: reqCtx, PostId: c.Param("postID"),
				PageSize: int32(parsePageSize(c.Query("page_size"))),
			})
		})
	})
	routes.GET("/jobs/:jobID", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.GetJobStatus(ctx, &pb.JobStatusRequest{Context: reqCtx, JobId: c.Param("jobID")})
		})
	})
	routes.POST("/jobs/:jobID/replay", func(c *gin.Context) {
		call(c, func(ctx context.Context, reqCtx *pb.RequestContext) (interface{}, error) {
			return client.ReplayJob(ctx, &pb.ReplayJobRequest{Context: reqCtx, JobId: c.Param("jobID")})
		})
	})
}

func scheduleRoute(c *gin.Context, client pb.SocialPostServiceClient, reschedule bool) {
	var body scheduleRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid schedule payload"})
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	req := &pb.SchedulePostRequest{
		Context: requestContextWithKey(c, key), PostId: c.Param("postID"),
		ScheduledAt: body.ScheduledAt, ScheduleTimezone: body.ScheduleTimezone,
		ExpectedVersion: body.ExpectedVersion,
	}
	var resp *pb.PostResponse
	var err error
	if reschedule {
		resp, err = client.ReschedulePost(ctx, req)
	} else {
		resp, err = client.SchedulePost(ctx, req)
	}
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, toPostResponseDTO(resp))
}

func requestContext(c *gin.Context) *pb.RequestContext {
	return &pb.RequestContext{
		AccountId:     c.GetString("accountId"),
		CorrelationId: c.GetHeader("X-Correlation-ID"),
		SourceIp:      c.ClientIP(),
		UserAgent:     c.GetHeader("User-Agent"),
	}
}

func requestContextWithKey(c *gin.Context, key string) *pb.RequestContext {
	req := requestContext(c)
	req.IdempotencyKey = key
	return req
}

func idempotencyKey(c *gin.Context) (string, bool) {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" || len(key) > 256 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid Idempotency-Key is required"})
		return "", false
	}
	return key, true
}

func parsePageSize(value string) int {
	if value == "" {
		return 20
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return 20
	}
	if n > 100 {
		return 100
	}
	return n
}

func splitQuery(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func writeGRPCError(c *gin.Context, err error) {
	s := status.Convert(err)
	code := http.StatusInternalServerError
	switch s.Code() {
	case 3:
		code = http.StatusBadRequest
	case 5:
		code = http.StatusNotFound
	case 6:
		code = http.StatusConflict
	case 7, 16:
		code = http.StatusUnauthorized
	case 9:
		code = http.StatusPreconditionFailed
	case 10:
		code = http.StatusConflict
	}
	c.JSON(code, gin.H{"error": s.Message(), "code": s.Code().String()})
}

func verifyImportSource(c *gin.Context, verifier AssetOwnershipVerifier, reference string) bool {
	if verifier != nil && verifier.VerifySocialAssetReference(c, reference) {
		return true
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "source asset not found or not visible to this account"})
	return false
}
