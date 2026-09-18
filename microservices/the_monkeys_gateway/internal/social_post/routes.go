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
		c.JSON(http.StatusOK, resp)
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
		c.JSON(http.StatusCreated, resp)
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
	c.JSON(http.StatusOK, resp)
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
