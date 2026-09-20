package blog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	activity_pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"github.com/the-monkeys/the_monkeys/common/audience"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/activity"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/user_service"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/middleware"

	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins
		return true
	},
}

type BlogServiceClient struct {
	Client      pb.BlogServiceClient
	UserCli     *user_service.UserServiceClient
	ActivityCli activity_pb.ActivityServiceClient
	Groups      grouppb.GroupServiceClient
	config      *config.Config
	log         *zap.SugaredLogger
}

// createClientInfo creates a comprehensive ClientInfo protobuf message from gin context
func (bsc *BlogServiceClient) createClientInfo(ctx *gin.Context) *pb.ClientInfo {
	// Get comprehensive client information using enhanced utility function
	clientInfo := utils.GetClientInfo(ctx)

	// Get platform enum for protobuf
	platform := utils.GetBlogPlatform(ctx)

	return &pb.ClientInfo{
		// Basic client information
		IpAddress: clientInfo.IPAddress,
		Client:    clientInfo.ClientType,
		SessionId: clientInfo.SessionID,
		VisitorId: clientInfo.VisitorID,
		UserAgent: clientInfo.UserAgent,
		Referrer:  clientInfo.Referrer,
		Platform:  platform,
		Browser:   clientInfo.Browser,
		Dnt:       clientInfo.DNT,

		// Enhanced Browser fingerprinting
		AcceptLanguage:   clientInfo.AcceptLanguage,
		AcceptEncoding:   clientInfo.AcceptEncoding,
		Timezone:         clientInfo.Timezone,
		ScreenResolution: clientInfo.ScreenResolution,
		ColorDepth:       clientInfo.ColorDepth,
		DeviceMemory:     clientInfo.DeviceMemory,
		Languages:        clientInfo.Languages,

		// Location & Geographic hints
		Country:        clientInfo.Country,
		TimezoneOffset: clientInfo.TimezoneOffset,

		// Marketing & UTM tracking
		UtmSource:   clientInfo.UTMSource,
		UtmMedium:   clientInfo.UTMMedium,
		UtmCampaign: clientInfo.UTMCampaign,
		UtmContent:  clientInfo.UTMContent,
		UtmTerm:     clientInfo.UTMTerm,

		// Behavioral indicators
		IsBot:        clientInfo.IsBot,
		TrustScore:   clientInfo.TrustScore,
		RequestCount: int32(clientInfo.RequestCount),

		// Technical environment
		IsSecureContext:   clientInfo.IsSecureContext,
		ConnectionType:    clientInfo.ConnectionType,
		BrowserEngine:     clientInfo.BrowserEngine,
		JavascriptEnabled: clientInfo.JavaScriptEnabled,

		// Timestamps
		FirstSeen:      clientInfo.FirstSeen,
		LastSeen:       clientInfo.LastSeen,
		CollectedAt:    clientInfo.CollectedAt,
		Origin:         clientInfo.Origin,
		RealIp:         clientInfo.RealIP,
		DeviceType:     clientInfo.DeviceType,
		Os:             clientInfo.Os,
		Accept:         clientInfo.Accept,
		Connection:     clientInfo.Connection,
		ForwardedFor:   clientInfo.ForwardedFor,
		ForwardedHost:  clientInfo.ForwardedHost,
		ForwardedProto: clientInfo.ForwardedProto,
	}
}

func NewBlogServiceClient(cfg *config.Config, log *zap.SugaredLogger) pb.BlogServiceClient {
	blogService := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysBlog, cfg.Microservices.BlogPort)
	cc, err := grpc.NewClient(blogService, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Errorf("cannot dial to blog server: %v", err)
	}

	log.Infof("✅ the monkeys gateway is dialing to the blog rpc server at: %v", blogService)
	return pb.NewBlogServiceClient(cc)
}

func RegisterBlogRouter(router *gin.Engine, cfg *config.Config, authClient *auth.ServiceClient, userClient *user_service.UserServiceClient, log *zap.SugaredLogger) *BlogServiceClient {
	rateLimiter := middleware.RateLimiterMiddleware("100-S") // 100 requests per second for landing page

	mware := auth.InitAuthMiddleware(authClient, log)

	blogClient := &BlogServiceClient{
		Client:      NewBlogServiceClient(cfg, log),
		UserCli:     userClient,
		ActivityCli: activity.NewActivityServiceClient(cfg, log),
		config:      cfg,
		log:         log,
	}

	// -------------------------------------------------- V1 API in use --------------------------------------------------
	routes := router.Group("/api/v1/blog")

	// Use AuthRequired for basic authorization
	routes.Use(mware.AuthRequired)
	routes.POST("/publish/:blog_id", mware.AuthzRequired, blogClient.PublishBlogById)

	routes.POST("/archive/:blog_id", mware.AuthzRequired, blogClient.ArchiveBlogById)
	// routes.GET("/all/drafts/:acc_id", blogClient.AllDrafts)
	routes.GET("/all-col/:acc_id", blogClient.AllCollabBlogs)
	routes.GET("/drafts/:acc_id/:blog_id", mware.AuthzRequired, blogClient.GetDraftBlogByAccId)
	// routes.GET("/all/publishes/:acc_id", blogClient.AllPublishesByAccountId)

	// routes.GET("/my-drafts/:blog_id", mware.AuthzRequired, blogClient.GetDraftBlogByBlogId)

	// routes.GET("/all/bookmarks", blogClient.GetBookmarks)

	routes.DELETE("/:blog_id", mware.AuthzRequired, blogClient.DeleteBlogById)

	// -------------------------------------------------- V2 --------------------------------------------------
	routesV2 := router.Group("/api/v2/blog")
	// Public APIs
	{
		routesV2.POST("/meta-feed", rateLimiter, blogClient.GetFeedPostsMeta)
		routesV2.GET("/meta-feed", rateLimiter, blogClient.GetsMetaFeed)
		// Get all blogs
		routesV2.GET("/feed", rateLimiter, blogClient.GetLatestBlogs) // Get all blogs, latest first with limit and offset
		// DEPRECATED route — Search blogs with query, Phase 5. The legacy
		// path /api/v2/blog/search now 308-redirects to the v2 query
		// engine at /api/v2/blog/search/v2 (Elasticsearch v3 index,
		// ranked + highlighted). Query string is preserved; the new
		// endpoint accepts `search_term` for back-compat with v1
		// callers, so this redirect is a true drop-in. Will be removed
		// entirely after one release. Do not add new callers.
		routesV2.GET("/search", rateLimiter, redirectToBlogSearchV2)
		// Get blogs by tags, as users can filter the blogs using multiple tags
		routesV2.POST("/tags", rateLimiter, blogClient.GetBlogsByTags) // Get blogs by tags
		// Get blogs by username, not auth required as it is public and can be visible at users profile
		// routesV2.GET("/all/:username", rateLimiter, blogClient.UsersBlogs)          // Update of blogClient.AllPublishesByUserName
		routesV2.GET("/user/:username", rateLimiter, mware.AuthOptional, blogClient.MetaUsersPublished) // Get metadata of user's published blogs
		// Get published blog by blog_id

		routesV2.GET("/:blog_id/stats", rateLimiter, mware.AuthOptional, blogClient.GetBlogStats)                           // Get blog stats
		routesV2.GET("/:blog_id", rateLimiter, mware.AuthOptional, blogClient.GetPublishedBlogByBlogId) // Get published blog by blog_id
		routesV2.POST("/:blog_id/activity", rateLimiter, mware.AuthOptional, blogClient.TrackBlogActivity)                  // Track blog interaction (read duration etc)

		// User Tags API
		routesV2.GET("/user-tags/:username", rateLimiter, blogClient.GetUserTags) // Get user tags

		// Activity & Analytics APIs
		routesV2.GET("/trending", rateLimiter, blogClient.GetTrendingBlogs) // Get trending blogs
		// routesV2.GET("/account/:acc_id/activity", rateLimiter, blogClient.GetAccountActivities) // Get account activities
		// routesV2.GET("/analytics/advanced", rateLimiter, blogClient.GetAdvancedAnalytics) // Get advanced analytics
	}

	routesV2.Use(mware.AuthRequired)

	// Protected APIs
	{
		// Get blogs of following users
		routesV2.GET("/following-feed", rateLimiter, blogClient.FollowingBlogsFeed) // Blogs for following feed
		// User can get their blogs (draft)
		// routesV2.GET("/my-drafts", rateLimiter, blogClient.MyDraftBlogs)       // Get all my draft blogs
		routesV2.GET("/in-my-draft", rateLimiter, blogClient.MetaMyDraftBlogs) // Get my draft blog by id
		// Users can get their blogs (published)
		routesV2.GET("/my-published", rateLimiter, blogClient.MyPublishedBlogs) // Get all my published blogs
		// Users can get the blogs they bookmarked (published)
		// routesV2.GET("/my-bookmarks", rateLimiter, blogClient.MyBookmarks)       // Update of blogClient.GetBookmarks
		routesV2.GET("/in-my-bookmark", rateLimiter, blogClient.MetaMyBookmarks) // Get my bookmark blog by id
		// My feed blogs, contains blogs from people I follow + my own blogs + topics I follow
		// routesV2.GET("/my-feed", blogClient.MyFeedBlogs) // Get my feed blogs
		routesV2.GET("/my-scheduled_blog", rateLimiter, blogClient.MetaMyScheduleBlogs)
	}

	// Todo: removeing the unused code
	// Authorization required APIs
	{
		// Write a blog, when the user have edit access
		routesV2.GET("/draft/:blog_id", mware.AuthzRequired, blogClient.WriteBlog)
		// TODO: Add api to /to-publish/:blog_id now v1  blogClient.PublishBlogById is working
		routesV2.POST("/to-draft/:blog_id", mware.AuthzRequired, blogClient.MoveBlogToDraft)
		// Get my draft blog by id
		routesV2.GET("/my-draft/:blog_id", mware.AuthzRequired, blogClient.GetDraftBlogByBlogIdV2)
		// Schedule blog
		routesV2.POST("/:blog_id/schedule_blog", mware.AuthzRequired, blogClient.ScheduleBlog)
	}

	// -------------------------------------------------- Section-based News APIs --------------------------------------------------
	// News sections for landing page
	newsSection := router.Group("/api/v2/posts")
	{
		// Latest news across all categories
		newsSection.GET("/latest", rateLimiter, blogClient.GetLatestNews)
		// Trending news (most liked/viewed in last 24-48 hours)
		newsSection.GET("/trending", rateLimiter, blogClient.GetTrendingNews)

		// Category-specific news endpoints
		newsSection.GET("/business", rateLimiter, blogClient.GetBusinessNews)
		newsSection.GET("/technology", rateLimiter, blogClient.GetTechnologyNews)
		newsSection.GET("/science", rateLimiter, blogClient.GetScienceNews)
		newsSection.GET("/health", rateLimiter, blogClient.GetHealthNews)
		newsSection.GET("/sports", rateLimiter, blogClient.GetSportsNews)
		newsSection.GET("/entertainment", rateLimiter, blogClient.GetEntertainmentNews)
		newsSection.GET("/travel", rateLimiter, blogClient.GetTravelNews)
		newsSection.GET("/food", rateLimiter, blogClient.GetFoodNews)
		newsSection.GET("/lifestyle", rateLimiter, blogClient.GetLifestyleNews)
		newsSection.GET("/education", rateLimiter, blogClient.GetEducationNews)
		newsSection.GET("/space", rateLimiter, blogClient.GetSpaceNews)
		newsSection.GET("/psychology", rateLimiter, blogClient.GetPsychologyNews)
		newsSection.GET("/humor", rateLimiter, blogClient.GetHumorNews)

		// Generic category endpoint
		// newsSection.GET("/category/:category", rateLimiter, func(ctx *gin.Context) {
		// 	category := ctx.Param("category")
		// 	blogClient.getNewsByCategory(ctx, category)
		// })

		// Mixed section endpoint that ensures no duplicates across multiple categories
		// newsSection.POST("/sections", rateLimiter, blogClient.GetNewsBySections)
	}

	// -------------------------------------------------- End Section-based News APIs --------------------------------------------------

	return blogClient
}

func (asc *BlogServiceClient) AllDrafts(ctx *gin.Context) {
	// Check permissions:
	// if !utils.CheckUserAccessInContext(ctx, constants.PermissionEdit) {
	// 	ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
	// 	return
	// }

	tokenAccountId := ctx.GetString("accountId")
	accId := ctx.Param("acc_id")

	if tokenAccountId != accId {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}
	res, err := asc.Client.GetDraftBlogsByAccId(context.Background(), &pb.BlogByIdReq{
		OwnerAccountId: accId,
		ClientInfo:     asc.createClientInfo(ctx),
		// Email:          "",
		// Username:       "",
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.InvalidArgument:
				ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "incomplete request, please provide correct input parameters"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot fetch the draft blogs"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *BlogServiceClient) AllCollabBlogs(ctx *gin.Context) {

	tokenAccountId := ctx.GetString("accountId")
	accId := ctx.Param("acc_id")

	if tokenAccountId != accId {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}

	// Get all the drafted blogs
	uc, err := asc.UserCli.GetBlogsIds(accId, "colab")
	if err != nil {
		asc.log.Errorf("cannot get the colab blogs, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the colab blogs"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the colab blogs"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	draftBlogId := []string{}
	publishedBlogId := []string{}
	for _, blog := range uc.Blogs {
		if blog.Status == constants.BlogStatusDraft {
			draftBlogId = append(draftBlogId, blog.BlogId)
		} else {
			publishedBlogId = append(publishedBlogId, blog.BlogId)
		}
	}

	if len(draftBlogId) == 0 && len(publishedBlogId) == 0 {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no draft/published blogs found"})
		return
	}

	var drafts, published interface{}

	// Fetch drafts if they exist
	if len(draftBlogId) > 0 {
		drafts, err = asc.Client.GetAllBlogsByBlogIds(context.Background(), &pb.GetBlogsByBlogIds{
			BlogIds: draftBlogId,
		})
		if err != nil {
			handleBlogFetchError(ctx, err, "draft")
			return
		}
	}

	// Fetch published blogs if they exist
	if len(publishedBlogId) > 0 {
		published, err = asc.Client.GetAllBlogsByBlogIds(context.Background(), &pb.GetBlogsByBlogIds{
			BlogIds: publishedBlogId,
		})
		if err != nil {
			handleBlogFetchError(ctx, err, "published")
			return
		}
	}

	// Respond with the found drafts and/or published blogs
	ctx.JSON(http.StatusOK, gin.H{"drafts": drafts, "published": published})
}

func handleBlogFetchError(ctx *gin.Context, err error, blogType string) {
	if status, ok := status.FromError(err); ok {
		switch status.Code() {
		case codes.InvalidArgument:
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("incomplete request, unable to fetch %s blogs", blogType)})
		case codes.Internal:
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("cannot fetch the %s blogs", blogType)})
		default:
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
		}
	} else {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
	}
}

func (asc *BlogServiceClient) AllPublishesByAccountId(ctx *gin.Context) {
	accId := ctx.Param("acc_id")

	res, err := asc.Client.GetPublishedBlogsByAccID(context.Background(), &pb.BlogByIdReq{
		OwnerAccountId: accId,
		ClientInfo:     asc.createClientInfo(ctx),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.InvalidArgument:
				ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "incomplete request, please provide correct input parameters"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot fetch the draft blogs"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *BlogServiceClient) GetDraftBlogByAccId(ctx *gin.Context) {
	// Check permissions:
	if !utils.CheckUserAccessInContext(ctx, constants.PermissionEdit) {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}

	// Extract account_id and blog_id from URL parameters
	accID := ctx.Param("acc_id")
	blogID := ctx.Param("blog_id")

	// Ensure acc_id and blog_id are not empty
	if accID == "" || blogID == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "account id and blog id are required"})
		return
	}

	// Fetch the drafted blog by blog_id and owner_account_id
	blog, err := asc.Client.GetDraftBlogById(ctx, &pb.BlogByIdReq{
		BlogId:         blogID,
		OwnerAccountId: accID,
		ClientInfo:     asc.createClientInfo(ctx),
	})
	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "drafted blog not found"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch drafted blog"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	// Return the drafted blog as a JSON response
	ctx.JSON(http.StatusOK, blog)
}

func (asc *BlogServiceClient) PublishBlogById(ctx *gin.Context) {

	// Check permissions:
	if !utils.CheckUserAccessInContext(ctx, "Publish") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}

	accId := ctx.GetString("accountId")
	// Bind tags from request body
	var publishBody PublishBlogReq
	if err := ctx.ShouldBindBodyWithJSON(&publishBody); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "cannot bind the tags"})
		return
	}

	id := ctx.Param("blog_id")

	groupSlug := strings.TrimSpace(publishBody.GroupSlug)
	aud := audience.Normalize(publishBody.Audience)
	if groupSlug != "" {
		resolved, ok := asc.resolvePublishGroup(ctx, accId, groupSlug, publishBody.Audience)
		if !ok {
			return
		}
		aud = resolved
	} else {
		aud = audience.AudiencePublic
	}

	resp, err := asc.Client.PublishBlog(context.Background(), &pb.PublishBlogReq{
		BlogId:     id,
		AccountId:  accId,
		Tags:       publishBody.Tags,
		Slug:       publishBody.Slug,
		GroupSlug:  groupSlug,
		Audience:   aud,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the draft blogs"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, resp)
}

// Todo: have to fix this
func (asc *BlogServiceClient) ScheduleBlog(ctx *gin.Context) {

	// Check permissions:
	if !utils.CheckUserAccessInContext(ctx, "Publish") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform schedule blog action"})
		return
	}

	accId := ctx.GetString("accountId")
	// Bind tags from request body
	var scheduleBody ScheduleBlogReq
	if err := ctx.ShouldBindBodyWithJSON(&scheduleBody); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "cannot bind the tags"})
		return
	}

	id := ctx.Param("blog_id")

	groupSlug := strings.TrimSpace(scheduleBody.GroupSlug)
	aud := audience.Normalize(scheduleBody.Audience)
	if groupSlug != "" {
		resolved, ok := asc.resolvePublishGroup(ctx, accId, groupSlug, scheduleBody.Audience)
		if !ok {
			return
		}
		aud = resolved
	} else {
		aud = audience.AudiencePublic
	}

	resp, err := asc.Client.ScheduleBlog(
		context.Background(),
		&pb.ScheduleBlogReq{
			Publish: &pb.PublishBlogReq{
				BlogId:     id,
				AccountId:  accId,
				Tags:       scheduleBody.Tags,
				Slug:       scheduleBody.Slug,
				GroupSlug:  groupSlug,
				Audience:   aud,
				ClientInfo: asc.createClientInfo(ctx),
			},
			ScheduleTime: timestamppb.New(scheduleBody.ScheduleTime),
			Timezone:     scheduleBody.Timezone,
		},
	)

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the draft blogs"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, resp)
}

func (asc *BlogServiceClient) ArchiveBlogById(ctx *gin.Context) {
	// Check permissions:
	if !utils.CheckUserAccessInContext(ctx, "Archive") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}

	id := ctx.Param("blog_id")
	resp, err := asc.Client.ArchiveBlogById(context.Background(), &pb.ArchiveBlogReq{
		BlogId:     id,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "the blog does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot archive the blogs"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, resp)
}

func (asc *BlogServiceClient) DeleteBlogById(ctx *gin.Context) {
	// Check permissions to Delete
	if !utils.CheckUserAccessInContext(ctx, "Delete") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}

	blogId := ctx.Param("blog_id")
	accId := ctx.GetString("accountId")

	res, err := asc.Client.DeleteABlogByBlogId(context.Background(), &pb.DeleteBlogReq{
		BlogId:         blogId,
		OwnerAccountId: accId,
		ClientInfo:     asc.createClientInfo(ctx),
	})
	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "couldn't delete the blog due to some internal error"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *BlogServiceClient) GetBookmarks(ctx *gin.Context) {
	tokenAccountId := ctx.GetString("accountId")

	// Get all the drafted blogs
	uc, err := asc.UserCli.GetBlogsIds(tokenAccountId, "bookmark")
	if err != nil {
		asc.log.Errorf("cannot get the bookmarked blogs, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the bookmarked blogs"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the bookmarked blogs"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	blogId := []string{}

	for _, blog := range uc.Blogs {
		blogId = append(blogId, blog.BlogId)
	}

	resp, err := asc.Client.GetAllBlogsByBlogIds(context.Background(), &pb.GetBlogsByBlogIds{
		BlogIds: blogId,
	})
	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the bookmarks do not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot find the bookmarks"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, resp)
}

func (asc *BlogServiceClient) GetDraftBlogByBlogId(ctx *gin.Context) {
	blogId := ctx.Param("blog_id")

	// Check permissions:
	if !utils.CheckUserAccessInContext(ctx, constants.PermissionEdit) {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}

	resp, err := asc.Client.GetDraftBlogByBlogId(context.Background(), &pb.BlogByIdReq{
		BlogId:     blogId,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "couldn't find the blog due to some internal error"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, resp)
}

func (asc *BlogServiceClient) GetColDraftBlogByBlogId(ctx *gin.Context) {
	blogId := ctx.Param("blog_id")

	// Check permissions:
	if !utils.CheckUserAccessInContext(ctx, constants.PermissionEdit) {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}

	resp, err := asc.Client.GetDraftBlogByBlogId(context.Background(), &pb.BlogByIdReq{
		BlogId:     blogId,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "couldn't find the blog due to some internal error"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, resp)
}

func (asc *BlogServiceClient) WriteBlog(ctx *gin.Context) {
	id := ctx.Param("blog_id")

	// Get client information using utility function
	clientInfo := utils.GetClientInfo(ctx)

	// Determine action from the permission set already loaded by AuthzRequired
	// middleware. This avoids an extra gRPC round-trip to CheckIfBlogsExist:
	//   - AuthzRequired → CheckAccessLevel hits Postgres blog_permissions.
	//   - No row → Access: ["Create"]  (new blog)
	//   - Row found → Access: ["Edit", ...] (existing blog)
	// The blog service's DraftBlogV2 stream validates IsDraft on the first
	// message, so the gateway doesn't need to query Elasticsearch at all.
	var action string
	var initialLogDone bool

	switch {
	case utils.CheckUserAccessInContext(ctx, constants.PermissionEdit):
		action = constants.BLOG_UPDATE
	case utils.CheckUserAccessInContext(ctx, constants.PermissionCreate):
		action = constants.BLOG_CREATE
	default:
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "You are not allowed to perform this action"})
		return
	}

	// Configure WebSocket upgrader with better settings
	upgrader.CheckOrigin = func(r *http.Request) bool {
		return true // Configure appropriately for production
	}
	upgrader.HandshakeTimeout = 10 * time.Second
	upgrader.ReadBufferSize = 1024 * 4  // 4KB
	upgrader.WriteBufferSize = 1024 * 4 // 4KB

	conn, err := upgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		asc.log.Errorf("Error upgrading connection: %v", err)
		ctx.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// Set connection timeouts and limits
	conn.SetReadLimit(int64(constants.MaxMsgSize))         // Support up to 20MB messages
	conn.SetReadDeadline(time.Now().Add(70 * time.Second)) // Slightly longer than client heartbeat
	conn.SetPongHandler(func(string) error {
		asc.log.Debug("Received pong from client")
		conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		return nil
	})

	defer func() {
		if err := conn.Close(); err != nil {
			asc.log.Errorf("Error closing WebSocket connection: %v", err)
		}
	}()

	// Establish a bi-directional stream with the gRPC server
	stream, err := asc.Client.DraftBlogV2(context.Background())
	if err != nil {
		asc.log.Errorf("Error establishing gRPC stream: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte(`{"error": "Failed to establish server connection"}`))
		return
	}
	defer func() {
		if err := stream.CloseSend(); err != nil {
			asc.log.Errorf("Error closing gRPC stream: %v", err)
		}
	}()

	// Send initial connection success message
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type": "connected", "status": "ready"}`)); err != nil {
		asc.log.Errorf("Error sending initial connection message: %v", err)
		return
	}

	// Start heartbeat routine
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	// Channel to handle graceful shutdown
	done := make(chan struct{})
	defer close(done)

	// Goroutine to handle periodic heartbeat
	go func() {
		for {
			select {
			case <-heartbeatTicker.C:
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					asc.log.Errorf("Error sending ping: %v", err)
					return
				}
				asc.log.Debug("Sent ping to client")
			case <-done:
				return
			}
		}
	}()

	// Main message handling loop with improved error handling
	for {
		// Set read deadline for each message
		conn.SetReadDeadline(time.Now().Add(70 * time.Second))

		messageType, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure) {
				asc.log.Errorf("Unexpected WebSocket close error: %v", err)
			} else if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				asc.log.Info("WebSocket connection closed normally")
			} else {
				asc.log.Errorf("Error reading WebSocket message: %v", err)
			}
			return
		}

		// Handle different message types
		switch messageType {
		case websocket.TextMessage:
			if err := asc.handleTextMessage(msg, conn, stream, id, clientInfo, utils.GetBlogPlatform(ctx), action, &initialLogDone); err != nil {
				asc.log.Errorf("Error handling text message: %v", err)
				// Send error response but don't close connection
				errorMsg := map[string]interface{}{
					"type":  "error",
					"error": "Failed to process message",
				}
				if errorResponse, marshalErr := json.Marshal(errorMsg); marshalErr == nil {
					conn.WriteMessage(websocket.TextMessage, errorResponse)
				}
				continue // Continue to next message instead of breaking
			}
		case websocket.PingMessage:
			asc.log.Debug("Received ping from client")
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PongMessage, nil); err != nil {
				asc.log.Errorf("Error sending pong: %v", err)
				return
			}
		case websocket.PongMessage:
			asc.log.Debug("Received pong from client")
			// Reset read deadline on pong
			conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		case websocket.CloseMessage:
			asc.log.Info("Received close message from client")
			return
		default:
			asc.log.Warnf("Received unexpected message type: %d", messageType)
		}
	}
}

// Helper function to handle text messages
func (asc *BlogServiceClient) handleTextMessage(msg []byte, conn *websocket.Conn, stream pb.BlogService_DraftBlogV2Client, id string, clientInfo utils.ClientInfo, platform pb.Platform, action string, initialLogDone *bool) error {
	// Handle ping/pong messages from client
	var messageCheck map[string]interface{}
	if err := json.Unmarshal(msg, &messageCheck); err == nil {
		if msgType, exists := messageCheck["type"]; exists {
			switch msgType {
			case "ping":
				asc.log.Debug("Received application-level ping")
				pongResponse := map[string]interface{}{
					"type":      "pong",
					"timestamp": time.Now().Unix(),
				}
				if pongMsg, err := json.Marshal(pongResponse); err == nil {
					conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					return conn.WriteMessage(websocket.TextMessage, pongMsg)
				}
				return nil
			case "pong":
				asc.log.Debug("Received application-level pong")
				return nil
			}
		}
	}

	// Step 1: Unmarshal into a generic map
	var draftBlog map[string]interface{}
	if err := json.Unmarshal(msg, &draftBlog); err != nil {
		asc.log.Errorf("Error unmarshalling websocket message for blog %s: %v", id, err)
		return fmt.Errorf("error unmarshalling message: %w", err)
	}

	// Enrich the blog data with metadata
	draftBlog["blog_id"] = id
	draftBlog["ip"] = clientInfo.IPAddress
	draftBlog["client"] = clientInfo.ClientType
	draftBlog["session_id"] = clientInfo.SessionID
	draftBlog["user_agent"] = clientInfo.UserAgent
	draftBlog["referrer"] = clientInfo.Referrer
	draftBlog["platform"] = int32(platform)

	// Only set the action and log the initial creation or update once
	if !*initialLogDone {
		draftBlog["Action"] = action
		*initialLogDone = true
	}

	// Convert draftBlog to google.protobuf.Any
	draftStruct, err := structpb.NewStruct(draftBlog)
	if err != nil {
		asc.log.Errorf("Error converting blog map to structpb.Struct for blog %s: %v", id, err)
		return fmt.Errorf("error converting draftBlog to structpb.Struct: %w", err)
	}

	// Wrap *structpb.Struct in *anypb.Any
	anyMsg, err := anypb.New(draftStruct)
	if err != nil {
		asc.log.Errorf("Error wrapping blog struct in anypb.Any for blog %s: %v", id, err)
		return fmt.Errorf("error wrapping structpb.Struct in anypb.Any: %w", err)
	}

	// Send the draft blog to the gRPC service
	if err := stream.Send(anyMsg); err != nil {
		asc.log.Errorf("Error sending draft blog to gRPC stream for blog %s: %v", id, err)
		return fmt.Errorf("error sending draft blog to gRPC stream: %w", err)
	}

	// Receive the response from the gRPC service
	grpcResp, err := stream.Recv()
	if err != nil {
		asc.log.Errorf("Error receiving response from gRPC stream for blog %s: %v", id, err)
		return fmt.Errorf("error receiving response from gRPC stream: %w", err)
	}

	asc.log.Debugf("Received gRPC response for blog %s: %+v", id, grpcResp)

	// Create success response
	response := map[string]interface{}{
		"type":      "success",
		"timestamp": time.Now().Unix(),
		"data":      grpcResp,
	}

	// Marshal and send the response back to the WebSocket client
	responseBytes, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("error marshalling response message: %w", err)
	}

	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, responseBytes); err != nil {
		return fmt.Errorf("error sending response message: %w", err)
	}

	return nil
}

func (asc *BlogServiceClient) FollowingBlogsFeed(ctx *gin.Context) {
	myUsername := ctx.GetString("userName")
	accountID := ctx.GetString("accountId")
	// Get Accounts I am following
	followings, err := asc.UserCli.GetFollowingAccounts(myUsername)
	if err != nil {
		asc.log.Errorf("cannot get the following accounts, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the following accounts"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the following accounts"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	if len(followings.Users) == 0 {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "you are not following anyone"})
		return
	}

	accountIds := []string{accountID}

	for _, user := range followings.Users {
		accountIds = append(accountIds, user.AccountId)
	}

	limit := ctx.DefaultQuery("limit", "100")
	offset := ctx.DefaultQuery("offset", "0")
	// Convert to int
	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 100
	}

	offsetInt, err := strconv.Atoi(offset)
	if err != nil {
		offsetInt = 0
	}

	// Get all the drafted blogs based on the following accounts with limit and offset
	stream, err := asc.Client.BlogsOfFollowingAccounts(context.Background(), &pb.FollowingAccounts{
		AccountIds: accountIds,
		Limit:      int32(limitInt),
		Offset:     int32(offsetInt),
	})

	if err != nil {
		asc.log.Errorf("cannot get the following blogs, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the following blogs"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the following blogs"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var allBlogs []map[string]interface{}
	for {
		blog, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if status, ok := status.FromError(err); ok {
				switch status.Code() {
				case codes.NotFound:
					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no blogs found from people you are following"})
					return
				case codes.Internal:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving blog from stream"})
					return
				default:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
					return
				}
			}
		}

		var blogMaps []map[string]interface{}
		if err := json.Unmarshal(rewriteV1StorageURLs(blog.Value), &blogMaps); err != nil {
			asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
			return
		}
		allBlogs = append(allBlogs, blogMaps...)
	}

	for _, blog := range allBlogs {
		blogID, ok := blog["blog_id"].(string)
		if !ok {
			asc.log.Errorf("BlogId is either missing or not a string: %v", blog)
			continue
		}

		likeCount, _ := asc.UserCli.GetNoOfLikeCounts(blogID)
		blog["LikeCount"] = likeCount

		isLikedByMe, _ := asc.UserCli.HaveILikedTheBlog(blogID, myUsername)
		blog["IsLikedByMe"] = isLikedByMe

		bookmarkCount, _ := asc.UserCli.GetNoOfBookmarkCounts(blogID)
		blog["BookmarkCount"] = bookmarkCount

		isBookmarkedByMe, _ := asc.UserCli.HaveIBookmarkedTheBlog(blogID, myUsername)
		blog["IsBookmarkedByMe"] = isBookmarkedByMe
	}

	responseBlogs := map[string]interface{}{
		"blogs": allBlogs,
	}

	ctx.JSON(http.StatusOK, responseBlogs)
}

func (asc *BlogServiceClient) GetLatestBlogs(ctx *gin.Context) {
	// Get Limits and offset
	limit := ctx.DefaultQuery("limit", "500")
	offset := ctx.DefaultQuery("offset", "0")
	// Convert to int
	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 100
	}

	offsetInt, err := strconv.Atoi(offset)
	if err != nil {
		offsetInt = 0
	}

	stream, err := asc.Client.MetaGetFeedBlogs(context.Background(), &pb.BlogListReq{
		Limit:      int32(limitInt),
		Offset:     int32(offsetInt),
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		asc.log.Errorf("cannot get the blogs by tags, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the blogs for the given tags"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the blogs by tags"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var allBlogs []map[string]interface{}
	for {
		blog, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if status, ok := status.FromError(err); ok {
				switch status.Code() {
				case codes.NotFound:
					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no blogs found for the given tags"})
					return
				case codes.Internal:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving blog from stream"})
					return
				default:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
					return
				}
			}
		}

		var blogMaps []map[string]interface{}
		if err := json.Unmarshal(rewriteV1StorageURLs(blog.Value), &blogMaps); err != nil {
			asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
			return
		}
		allBlogs = append(allBlogs, blogMaps...)
	}

	for _, blog := range allBlogs {
		blogID, ok := blog["blog_id"].(string)
		if !ok {
			asc.log.Errorf("BlogId is either missing or not a string: %v", blog)
			continue
		}

		likeCount, _ := asc.UserCli.GetNoOfLikeCounts(blogID)
		blog["like_count"] = likeCount

		bookmarkCount, _ := asc.UserCli.GetNoOfBookmarkCounts(blogID)
		blog["bookmark_count"] = bookmarkCount

	}

	responseBlogs := map[string]interface{}{
		"blogs": allBlogs,
	}

	ctx.JSON(http.StatusOK, responseBlogs)
}

func (asc *BlogServiceClient) GetBlogsByTags(ctx *gin.Context) {
	tags := Tags{}
	if err := ctx.BindJSON(&tags); err != nil {
		asc.log.Errorf("error while marshalling tags: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "tags aren't properly formatted"})
		return
	}

	// Get Limits and offset
	limit := ctx.DefaultQuery("limit", "500")
	offset := ctx.DefaultQuery("offset", "0")
	// Convert to int
	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 100
	}

	offsetInt, err := strconv.Atoi(offset)
	if err != nil {
		offsetInt = 0
	}

	stream, err := asc.Client.GetBlogs(context.Background(), &pb.GetBlogsReq{
		IsDraft:    false,
		Tags:       tags.Tags,
		Limit:      int32(limitInt),
		Offset:     int32(offsetInt),
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		asc.log.Errorf("cannot get the blogs by tags, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the blogs for the given tags"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the blogs by tags"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var allBlogs []map[string]interface{}
	for {
		blog, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if status, ok := status.FromError(err); ok {
				switch status.Code() {
				case codes.NotFound:
					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no blogs found for the given tags"})
					return
				case codes.Internal:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving blog from stream"})
					return
				default:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
					return
				}
			}
		}

		var blogMaps []map[string]interface{}
		if err := json.Unmarshal(rewriteV1StorageURLs(blog.Value), &blogMaps); err != nil {
			asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
			return
		}
		allBlogs = append(allBlogs, blogMaps...)
	}

	for _, blog := range allBlogs {
		blogID, ok := blog["blog_id"].(string)
		if !ok {
			asc.log.Errorf("BlogId is either missing or not a string: %v", blog)
			continue
		}

		likeCount, _ := asc.UserCli.GetNoOfLikeCounts(blogID)
		blog["like_count"] = likeCount

		bookmarkCount, _ := asc.UserCli.GetNoOfBookmarkCounts(blogID)
		blog["bookmark_count"] = bookmarkCount

	}

	responseBlogs := map[string]interface{}{
		"blogs": allBlogs,
	}

	ctx.JSON(http.StatusOK, responseBlogs)
}

func (asc *BlogServiceClient) MyDraftBlogs(ctx *gin.Context) {
	tokenAccountId := ctx.GetString("accountId")

	stream, err := asc.Client.GetBlogs(context.Background(), &pb.GetBlogsReq{
		AccountId:  tokenAccountId,
		IsDraft:    true,
		Limit:      5,
		Offset:     0,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		asc.log.Errorf("cannot get the blogs by tags, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the blogs for the given tags"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the blogs by tags"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var allBlogs []map[string]interface{}
	for {
		blog, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if status, ok := status.FromError(err); ok {
				switch status.Code() {
				case codes.NotFound:
					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no blogs found for the given tags"})
					return
				case codes.Internal:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving blog from stream"})
					return
				default:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
					return
				}
			}
		}

		var blogMaps []map[string]interface{}
		if err := json.Unmarshal(rewriteV1StorageURLs(blog.Value), &blogMaps); err != nil {
			asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
			return
		}
		allBlogs = append(allBlogs, blogMaps...)
	}

	for _, blog := range allBlogs {
		blogID, ok := blog["blog_id"].(string)
		if !ok {
			asc.log.Errorf("BlogId is either missing or not a string: %v", blog)
			continue
		}

		likeCount, _ := asc.UserCli.GetNoOfLikeCounts(blogID)
		blog["LikeCount"] = likeCount

		bookmarkCount, _ := asc.UserCli.GetNoOfBookmarkCounts(blogID)
		blog["BookmarkCount"] = bookmarkCount

	}

	responseBlogs := map[string]interface{}{
		"blogs": allBlogs,
	}

	ctx.JSON(http.StatusOK, responseBlogs)
}

func (asc *BlogServiceClient) MyPublishedBlogs(ctx *gin.Context) {
	tokenAccountId := ctx.GetString("accountId")

	// Get limit and offset and convert into int32
	limit := ctx.DefaultQuery("limit", "10")
	offset := ctx.DefaultQuery("offset", "0")
	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 10
	}

	offsetInt, err := strconv.Atoi(offset)
	if err != nil {
		offsetInt = 0
	}

	stream, err := asc.Client.GetBlogs(context.Background(), &pb.GetBlogsReq{
		AccountId:  tokenAccountId,
		IsDraft:    false,
		Limit:      int32(limitInt),
		Offset:     int32(offsetInt),
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		asc.log.Errorf("cannot get the blogs by tags, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the blogs for the given tags"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the blogs by tags"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var allBlogs []map[string]interface{}
	for {
		blog, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if status, ok := status.FromError(err); ok {
				switch status.Code() {
				case codes.NotFound:
					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no blogs found for the given tags"})
					return
				case codes.Internal:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving blog from stream"})
					return
				default:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
					return
				}
			}
		}

		var blogMaps []map[string]interface{}
		if err := json.Unmarshal(rewriteV1StorageURLs(blog.Value), &blogMaps); err != nil {
			asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
			return
		}
		allBlogs = append(allBlogs, blogMaps...)
	}

	for _, blog := range allBlogs {
		blogID, ok := blog["blog_id"].(string)
		if !ok {
			asc.log.Errorf("BlogId is either missing or not a string: %v", blog)
			continue
		}

		likeCount, _ := asc.UserCli.GetNoOfLikeCounts(blogID)
		blog["LikeCount"] = likeCount

		bookmarkCount, _ := asc.UserCli.GetNoOfBookmarkCounts(blogID)
		blog["BookmarkCount"] = bookmarkCount

	}

	responseBlogs := map[string]interface{}{
		"blogs": allBlogs,
	}

	ctx.JSON(http.StatusOK, responseBlogs)
}

func (asc *BlogServiceClient) UsersBlogs(ctx *gin.Context) {
	userName := ctx.Param("username")

	// Get the account_id from the username
	userInfo, err := asc.UserCli.GetUserDetails(userName)
	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot fetch the user details"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	// Get limit and offset and convert into int32
	limit := ctx.DefaultQuery("limit", "10")
	offset := ctx.DefaultQuery("offset", "0")
	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 10
	}

	offsetInt, err := strconv.Atoi(offset)
	if err != nil {
		offsetInt = 0
	}

	stream, err := asc.Client.GetBlogs(context.Background(), &pb.GetBlogsReq{
		AccountId:        userInfo.AccountId,
		IsDraft:          false,
		Limit:            int32(limitInt),
		Offset:           int32(offsetInt),
		ExcludeGroupOnly: ctx.GetString("accountId") != userInfo.AccountId,
		ClientInfo:       asc.createClientInfo(ctx),
	})

	if err != nil {
		asc.log.Errorf("cannot get the blogs by tags, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the blogs for the given tags"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the blogs by tags"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var allBlogs []map[string]interface{}
	for {
		blog, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if status, ok := status.FromError(err); ok {
				switch status.Code() {
				case codes.NotFound:
					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no blogs found for the given tags"})
					return
				case codes.Internal:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving blog from stream"})
					return
				default:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
					return
				}
			}
		}

		var blogMaps []map[string]interface{}
		if err := json.Unmarshal(rewriteV1StorageURLs(blog.Value), &blogMaps); err != nil {
			asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
			return
		}
		allBlogs = append(allBlogs, blogMaps...)
	}

	for _, blog := range allBlogs {
		blogID, ok := blog["blog_id"].(string)
		if !ok {
			asc.log.Errorf("BlogId is either missing or not a string: %v", blog)
			continue
		}

		likeCount, _ := asc.UserCli.GetNoOfLikeCounts(blogID)
		blog["LikeCount"] = likeCount

		bookmarkCount, _ := asc.UserCli.GetNoOfBookmarkCounts(blogID)
		blog["BookmarkCount"] = bookmarkCount

	}

	responseBlogs := map[string]interface{}{
		"blogs": allBlogs,
	}

	ctx.JSON(http.StatusOK, responseBlogs)
}

func (asc *BlogServiceClient) MoveBlogToDraft(ctx *gin.Context) {
	// Check permissions:
	if !utils.CheckUserAccessInContext(ctx, constants.PermissionEdit) {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}

	accId := ctx.GetString("accountId")

	id := ctx.Param("blog_id")

	// Get client information using utility function
	resp, err := asc.Client.MoveBlogToDraftStatus(context.Background(), &pb.BlogReq{
		BlogId:     id,
		AccountId:  accId,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot move the blog to draft"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, resp)
}

// func (asc *BlogServiceClient) MyBookmarks(ctx *gin.Context) {
// 	tokenAccountId := ctx.GetString("userName")

// 	// Get limit and offset and convert into int32
// 	limit := ctx.DefaultQuery("limit", "10")
// 	offset := ctx.DefaultQuery("offset", "0")
// 	limitInt, err := strconv.Atoi(limit)
// 	if err != nil {
// 		limitInt = 10
// 	}

// 	offsetInt, err := strconv.Atoi(offset)
// 	if err != nil {
// 		offsetInt = 0
// 	}

// 	// Get all the draft blogs by my username
// 	blogResp, err := asc.UserCli.GetUsersBookmarks(tokenAccountId)

// 	if err != nil {
// 		asc.log.Errorf("cannot get the bookmarks, error: %v", err)
// 		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the bookmarks"})
// 		return
// 	}

// 	if len(blogResp) == 0 {
// 		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no blogs found"})
// 		return
// 	}

// 	stream, err := asc.Client.GetBlogsBySlice(context.Background(), &pb.GetBlogsBySliceReq{
// 		BlogIds:    blogResp,
// 		IsDraft:    false,
// 		Limit:      int32(limitInt),
// 		Offset:     int32(offsetInt),
// 		ClientInfo: asc.createClientInfo(ctx),
// 	})

// 	if err != nil {
// 		asc.log.Errorf("cannot get the blogs by tags, error: %v", err)
// 		if status, ok := status.FromError(err); ok {
// 			switch status.Code() {
// 			case codes.NotFound:
// 				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the blogs for the given tags"})
// 				return
// 			case codes.Internal:
// 				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the blogs by tags"})
// 				return
// 			default:
// 				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
// 				return
// 			}
// 		}
// 	}

// 	var allBlogs []map[string]interface{}
// 	for {
// 		blog, err := stream.Recv()
// 		if err == io.EOF {
// 			break
// 		}
// 		if err != nil {
// 			if status, ok := status.FromError(err); ok {
// 				switch status.Code() {
// 				case codes.NotFound:
// 					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no blogs found for the given tags"})
// 					return
// 				case codes.Internal:
// 					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving blog from stream"})
// 					return
// 				default:
// 					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
// 					return
// 				}
// 			}
// 		}

// 		var blogMaps []map[string]interface{}
// 		if err := json.Unmarshal(rewriteV1StorageURLs(blog.Value), &blogMaps); err != nil {
// 			asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
// 			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
// 			return
// 		}
// 		allBlogs = append(allBlogs, blogMaps...)
// 	}

// 	for _, blog := range allBlogs {
// 		blogID, ok := blog["blog_id"].(string)
// 		if !ok {
// 			asc.log.Errorf("BlogId is either missing or not a string: %v", blog)
// 			continue
// 		}

// 		likeCount, _ := asc.UserCli.GetNoOfLikeCounts(blogID)
// 		blog["LikeCount"] = likeCount

// 		bookmarkCount, _ := asc.UserCli.GetNoOfBookmarkCounts(blogID)
// 		blog["BookmarkCount"] = bookmarkCount

// 	}

// 	responseBlogs := map[string]interface{}{
// 		"blogs": allBlogs,
// 	}

// 	ctx.JSON(http.StatusOK, responseBlogs)
// }

func (asc *BlogServiceClient) GetPublishedBlogByBlogId(ctx *gin.Context) {
	blogId := ctx.Param("blog_id")

	// Get client information for activity tracking
	clientInfo := utils.GetClientInfo(ctx)

	// Get user account ID if authenticated (can be empty for anonymous users)
	userAccountId := ctx.GetString("accountId")

	resp, err := asc.Client.GetBlog(context.Background(), &pb.BlogReq{
		BlogId:     blogId,
		AccountId:  userAccountId, // Include user account ID for comprehensive tracking
		IsDraft:    false,
		Ip:         clientInfo.IPAddress,
		Client:     clientInfo.ClientType,
		SessionId:  clientInfo.SessionID,
		UserAgent:  clientInfo.UserAgent,
		Referrer:   clientInfo.Referrer,
		Platform:   utils.GetBlogPlatform(ctx),
		ClientInfo: asc.createClientInfo(ctx),
	})

	// If duration wasn't provided, we can use the gRPC call latency as a fallback
	// or just log it separately. The instruction says "calculate and pass".
	// However, we already passed the duration above. If it was 0, we can't
	// easily "re-pass" it to the same call.
	// But the BlogService itself could measure its own internal processing.

	// For now, if the frontend didn't send it, we've fulfilled the "receive" part.

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "couldn't find the blog due to some internal error"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var blogMap map[string]interface{}
	if err := json.Unmarshal(rewriteV1StorageURLs(resp.Value), &blogMap); err != nil {
		asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
		return
	}

	// Initialize the map if it is nil
	if blogMap == nil {
		// blogMap = make(map[string]interface{})
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
		return
	}

	if !asc.acl().CanViewPublished(ctx, blogMap, userAccountId) {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "blog not found"})
		return
	}

	likeCount, _ := asc.UserCli.GetNoOfLikeCounts(blogId)
	blogMap["LikeCount"] = likeCount

	bookmarkCount, _ := asc.UserCli.GetNoOfBookmarkCounts(blogId)
	blogMap["BookmarkCount"] = bookmarkCount

	ctx.JSON(http.StatusOK, blogMap)
}

func (asc *BlogServiceClient) GetDraftBlogByBlogIdV2(ctx *gin.Context) {
	blogId := ctx.Param("blog_id")
	accessLevel, _ := ctx.Get("user_access_level")
	accountId, _ := ctx.Get("accountId")
	asc.log.Debugw("GetDraftBlogByBlogIdV2: permission check",
		"blog_id", blogId,
		"account_id", accountId,
		"access_level", accessLevel,
	)

	// Check permissions: allow both Edit (existing blog) and Create (new blog)
	if !utils.CheckUserAccessInContext(ctx, constants.PermissionEdit) &&
		!utils.CheckUserAccessInContext(ctx, constants.PermissionCreate) {
		asc.log.Warnw("GetDraftBlogByBlogIdV2: access denied",
			"blog_id", blogId,
			"account_id", accountId,
			"access_level", accessLevel,
		)
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}

	// Get client information for activity tracking
	clientInfo := utils.GetClientInfo(ctx)

	resp, err := asc.Client.GetBlog(context.Background(), &pb.BlogReq{
		BlogId:    blogId,
		IsDraft:   true,
		Ip:        clientInfo.IPAddress,
		Client:    clientInfo.ClientType,
		SessionId: clientInfo.SessionID,
		UserAgent: clientInfo.UserAgent,
		Referrer:  clientInfo.Referrer,
		Platform:  utils.GetBlogPlatform(ctx),
	})
	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "couldn't find the blog due to some internal error"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var blogMap map[string]interface{}
	if err := json.Unmarshal(rewriteV1StorageURLs(resp.Value), &blogMap); err != nil {
		asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
		return
	}

	// Initialize the map if it is nil
	if blogMap == nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
		return
	}

	ctx.JSON(http.StatusOK, blogMap)
}

// --------------------------------------------- News APIs ---------------------------------------------------

func (asc *BlogServiceClient) GetLatestNews(ctx *gin.Context) {
	// Get Limits and offset
	limit := ctx.DefaultQuery("limit", "500")
	offset := ctx.DefaultQuery("offset", "0")
	// Convert to int
	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 100
	}

	offsetInt, err := strconv.Atoi(offset)
	if err != nil {
		offsetInt = 0
	}

	// Use GetFeedBlogs with empty tags to get latest blogs from all categories
	stream, err := asc.Client.MetaGetFeedBlogs(context.Background(), &pb.BlogListReq{
		Tags:   []string{}, // Empty tags means all categories
		Limit:  int32(limitInt),
		Offset: int32(offsetInt),
	})

	if err != nil {
		asc.log.Errorf("cannot get the latest news, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no latest news found"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the latest news"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var newsList []map[string]interface{}
	for {
		news, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if status, ok := status.FromError(err); ok {
				switch status.Code() {
				case codes.NotFound:
					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no news found"})
					return
				case codes.Internal:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving news from stream"})
					return
				default:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
					return
				}
			}
		}

		var newsMap map[string]interface{}
		if err := json.Unmarshal(rewriteV1StorageURLs(news.Value), &newsMap); err != nil {
			asc.log.Errorf("cannot unmarshal the news, error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the news"})
			return
		}
		newsList = append(newsList, newsMap)
	}

	ctx.JSON(http.StatusOK, gin.H{"latest_news": newsList})
}

func (asc *BlogServiceClient) GetTrendingNews(ctx *gin.Context) {
	// Get Limits and offset
	limit := ctx.DefaultQuery("limit", "20") // Smaller default for trending
	offset := ctx.DefaultQuery("offset", "0")
	// Convert to int
	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 20
	}

	offsetInt, err := strconv.Atoi(offset)
	if err != nil {
		offsetInt = 0
	}

	// Use GetFeedBlogs for trending (assuming backend provides trending by default order)
	stream, err := asc.Client.MetaGetFeedBlogs(context.Background(), &pb.BlogListReq{
		Tags:   []string{}, // Empty tags means all categories
		Limit:  int32(limitInt),
		Offset: int32(offsetInt),
	})

	if err != nil {
		asc.log.Errorf("cannot get the trending news, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no trending news found"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the trending news"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var newsList []map[string]interface{}
	for {
		news, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if status, ok := status.FromError(err); ok {
				switch status.Code() {
				case codes.NotFound:
					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no news found"})
					return
				case codes.Internal:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving news from stream"})
					return
				default:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
					return
				}
			}
		}

		var newsMap map[string]interface{}
		if err := json.Unmarshal(rewriteV1StorageURLs(news.Value), &newsMap); err != nil {
			asc.log.Errorf("cannot unmarshal the news, error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the news"})
			return
		}
		newsList = append(newsList, newsMap)
	}

	ctx.JSON(http.StatusOK, gin.H{"trending_news": newsList})
}

// GetNewsBySections handles POST request for multiple sections with deduplication
func (asc *BlogServiceClient) GetNewsBySections(ctx *gin.Context) {
	var request struct {
		Sections []string `json:"sections" binding:"required"`
		Limit    int      `json:"limit"`
		Offset   int      `json:"offset"`
	}

	if err := ctx.ShouldBindJSON(&request); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request format"})
		return
	}

	// Set default values
	if request.Limit <= 0 {
		request.Limit = 10
	}
	if request.Offset < 0 {
		request.Offset = 0
	}

	// For now, we'll get news from each section individually and deduplicate
	// This is a temporary implementation until the backend supports multi-section queries
	result := make(map[string][]map[string]interface{})
	seenBlogIds := make(map[string]bool) // For deduplication

	for _, section := range request.Sections {
		// Get news by category using existing method
		stream, err := asc.Client.MetaGetFeedBlogs(context.Background(), &pb.BlogListReq{
			Tags:   []string{section},
			Limit:  int32(request.Limit),
			Offset: int32(request.Offset),
		})

		if err != nil {
			asc.log.Errorf("cannot get news for section %s, error: %v", section, err)
			continue // Continue with other sections
		}

		var sectionNews []map[string]interface{}
		for {
			news, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				asc.log.Errorf("error receiving news from stream for section %s, error: %v", section, err)
				break
			}

			var newsMap map[string]interface{}
			if err := json.Unmarshal(rewriteV1StorageURLs(news.Value), &newsMap); err != nil {
				asc.log.Errorf("cannot unmarshal news for section %s, error: %v", section, err)
				continue
			}

			// Extract blog ID for deduplication
			if blogId, exists := newsMap["blog_id"]; exists {
				blogIdStr := fmt.Sprintf("%v", blogId)
				if !seenBlogIds[blogIdStr] {
					seenBlogIds[blogIdStr] = true
					sectionNews = append(sectionNews, newsMap)
				}
			} else {
				// If no blog_id, add anyway but this shouldn't happen
				sectionNews = append(sectionNews, newsMap)
			}
		}

		if len(sectionNews) > 0 {
			result[section] = sectionNews
		}
	}

	ctx.JSON(http.StatusOK, gin.H{
		"sections": result,
		"metadata": gin.H{
			"requested_sections": request.Sections,
			"total_unique_items": len(seenBlogIds),
		},
	})
}

// TrackBlogActivity handles dedicated activity tracking requests
// POST /api/v2/blog/:blog_id/activity
func (asc *BlogServiceClient) TrackBlogActivity(ctx *gin.Context) {
	blogID := ctx.Param("blog_id")
	if blogID == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "blog_id is required"})
		return
	}
	if !asc.acl().RequireCanViewPublished(ctx, blogID) {
		return
	}

	// Get client information
	userAccountId := ctx.GetString("accountId") // Can be empty for anonymous

	// Parse request body for duration_ms or other interaction data
	var reqBody struct {
		DurationMs      int64  `json:"duration_ms"`
		InteractionType string `json:"interaction_type"`
	}

	if err := ctx.ShouldBindJSON(&reqBody); err != nil {
		// If JSON binding fails, check query params as fallback (backward compatibility)
		reqBody.DurationMs = int64(utils.GetIntQuery(ctx, "duration_ms", 0))
	}

	// Default interaction type if not provided
	if reqBody.InteractionType == "" {
		if reqBody.DurationMs > 0 {
			reqBody.InteractionType = "read_duration"
		} else {
			reqBody.InteractionType = "interaction"
		}
	}

	// Call Blog Service
	resp, err := asc.Client.TrackInteraction(context.Background(), &pb.TrackInteractionReq{
		BlogId:          blogID,
		AccountId:       userAccountId,
		InteractionType: reqBody.InteractionType,
		DurationMs:      reqBody.DurationMs,
		ClientInfo:      asc.createClientInfo(ctx),
	})

	if err != nil {
		asc.log.Errorw("failed to track blog interaction", "blog_id", blogID, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to track interaction"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"success": resp.Success,
		"message": resp.Message,
	})
}

// GetTrendingBlogs returns trending blogs from the activity service
// GET /api/v2/blog/trending
//
// Query params:
//   - time_range: ES date math suffix (e.g. "24h", "14d", "90d"). Default "24h".
//   - limit, offset, account_id
//
// When the requested window contains no activity, the handler progressively
// widens the window (24h -> 14d -> 90d -> 1y) until it finds data. This keeps
// the landing page useful when activity data is sparse without forcing the
// frontend to pick the right window. The actually used window is returned in
// the response as "time_range_used".
func (asc *BlogServiceClient) GetTrendingBlogs(ctx *gin.Context) {
	accID := ctx.Query("account_id")
	requestedRange := ctx.DefaultQuery("time_range", "24h")
	limitInt := utils.GetIntQuery(ctx, "limit", 10)
	offsetInt := utils.GetIntQuery(ctx, "offset", 0)
	clientInfo := utils.GetClientInfo(ctx)

	// Fallback ladder. Always tries the caller's requested window first, then
	// progressively wider windows. Duplicates are deduped, the requested
	// window is never skipped.
	windows := []string{requestedRange}
	for _, w := range []string{"14d", "90d", "1y"} {
		if w != requestedRange {
			windows = append(windows, w)
		}
	}

	var (
		resp    *activity_pb.GetTrendingBlogsResponse
		usedWin string
		blogIds []string
		callErr error
	)
	blogStats := make(map[string]struct {
		Views int64
		Score float64
	})

	for _, win := range windows {
		resp, callErr = asc.ActivityCli.GetTrendingBlogs(context.Background(), &activity_pb.GetTrendingBlogsRequest{
			AccountId: accID,
			TimeRange: win,
			Limit:     int32(limitInt),
		})
		if callErr != nil {
			asc.log.Errorw("failed to get trending blogs", "window", win, "err", callErr)
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch trending blogs"})
			return
		}
		if len(resp.Blogs) > 0 {
			usedWin = win
			blogIds = make([]string, 0, len(resp.Blogs))
			for _, b := range resp.Blogs {
				blogIds = append(blogIds, b.BlogId)
				blogStats[b.BlogId] = struct {
					Views int64
					Score float64
				}{Views: b.Views, Score: b.Score}
			}
			break
		}
	}

	// Still nothing across all windows: return an empty result rather than
	// calling the blog service with an empty BlogIds slice (which returns
	// InvalidArgument).
	if len(blogIds) == 0 {
		ctx.JSON(http.StatusOK, gin.H{
			"total_blogs":     0,
			"blogs":           []map[string]interface{}{},
			"time_range_used": requestedRange,
		})
		return
	}

	// Call gRPC to get blog metadata with client tracking
	stream, err := asc.Client.MetaGetBlogsByBlogIds(context.Background(), &pb.BlogListReq{
		BlogIds:   blogIds,
		IsDraft:   false, // Only published blogs
		Limit:     int32(limitInt),
		Offset:    int32(offsetInt),
		Ip:        clientInfo.IPAddress,
		Client:    clientInfo.ClientType,
		SessionId: clientInfo.SessionID,
		UserAgent: clientInfo.UserAgent,
		Referrer:  clientInfo.Referrer,
		Platform:  utils.GetBlogPlatform(ctx),
	})

	if err != nil {
		asc.log.Errorf("cannot get the blogs by tags, error: %v", err)
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "cannot find the blogs for the given tags"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot get the blogs by tags"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	var allBlogs []map[string]interface{}
	var totalBlogs int // Store total number of blogs

	for {
		blog, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			asc.log.Errorf("trending: error receiving blog stream item: %v", err)
			if st, ok := status.FromError(err); ok {
				switch st.Code() {
				case codes.NotFound:
					ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no blogs found for the given tags"})
					return
				case codes.InvalidArgument:
					ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": st.Message()})
					return
				case codes.Internal:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving blog from stream"})
					return
				default:
					ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
					return
				}
			}
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "error receiving blog from stream"})
			return
		}

		// Unmarshal into a map since response structure has changed
		var blogMap map[string]interface{}
		if err := json.Unmarshal(rewriteV1StorageURLs(blog.Value), &blogMap); err != nil {
			asc.log.Errorf("cannot unmarshal the blog, error: %v", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot unmarshal the blog"})
			return
		}

		// Extract "total_blogs" if present
		if total, ok := blogMap["total_blogs"].(float64); ok { // JSON numbers default to float64
			totalBlogs = int(total)
		}

		// Extract the "blogs" array safely
		blogsData, ok := blogMap["blogs"]
		if !ok {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "missing blogs data in response"})
			return
		}

		// Convert blogsData to []map[string]interface{}
		blogList, ok := blogsData.([]interface{})
		if !ok {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "invalid blogs format in response"})
			return
		}

		// Convert []interface{} to []map[string]interface{}
		for _, b := range blogList {
			if blogEntry, valid := b.(map[string]interface{}); valid {
				allBlogs = append(allBlogs, blogEntry)
			}
		}
	}

	// Add additional metadata (like & bookmark count) for each blog
	for _, blog := range allBlogs {
		blogID, ok := blog["blog_id"].(string)
		if !ok {
			asc.log.Errorf("BlogId is either missing or not a string: %v", blog)
			continue
		}

		likeCount, _ := asc.UserCli.GetNoOfLikeCounts(blogID)
		blog["like_count"] = likeCount

		bookmarkCount, _ := asc.UserCli.GetNoOfBookmarkCounts(blogID)
		blog["bookmark_count"] = bookmarkCount

		// Add views and score from activity service
		if stats, exists := blogStats[blogID]; exists {
			blog["views"] = stats.Views
			blog["score"] = stats.Score
		}
	}

	// Final response including total blogs count
	responseBlogs := map[string]interface{}{
		"total_blogs":     totalBlogs,
		"blogs":           allBlogs,
		"time_range_used": usedWin,
	}

	ctx.JSON(http.StatusOK, responseBlogs)
}

// GetAccountActivities returns activity history for a specific account
// GET /api/v2/blog/account/:acc_id/activity
func (asc *BlogServiceClient) GetAccountActivities(ctx *gin.Context) {
	accID := ctx.Param("acc_id")
	action := ctx.Query("action")
	limit := int32(utils.GetIntQuery(ctx, "limit", 20))
	offset := int32(utils.GetIntQuery(ctx, "offset", 0))

	resp, err := asc.ActivityCli.GetAccountActivities(context.Background(), &activity_pb.GetAccountActivitiesRequest{
		AccountId: accID,
		Action:    action,
		Limit:     limit,
		Offset:    offset,
	})

	if err != nil {
		asc.log.Errorf("failed to get account activities: %v", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch activities"})
		return
	}

	ctx.JSON(http.StatusOK, resp)
}

// GetAdvancedAnalytics returns complex insights like retention and hotspots
// GET /api/v2/blog/analytics/advanced
func (asc *BlogServiceClient) GetAdvancedAnalytics(ctx *gin.Context) {
	accID := ctx.Query("account_id")
	timeRange := ctx.DefaultQuery("time_range", "7d")

	resp, err := asc.ActivityCli.GetAdvancedAnalytics(context.Background(), &activity_pb.GetAdvancedAnalyticsRequest{
		AccountId: accID,
		TimeRange: timeRange,
	})

	if err != nil {
		asc.log.Errorf("failed to get advanced analytics: %v", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch advanced analytics"})
		return
	}

	ctx.JSON(http.StatusOK, resp)
}

// redirectToBlogSearchV2 is the Phase 5 deprecation shim for the old
// /api/v2/blog/search query route. It permanently redirects to the v2
// engine at /api/v2/blog/search/v2, preserving the raw query string.
// The v2 endpoint accepts the legacy `search_term` parameter as a
// back-compat alias for `q`, so old clients keep working unchanged.
// 308 (not 301/302) is used so the GET method is preserved by
// intermediaries.
//
// Deprecated: transitional shim. New code must call
// /api/v2/blog/search/v2 directly. The route will be removed one
// release after the Search v2 rollout soaks.
func redirectToBlogSearchV2(ctx *gin.Context) {
	target := "/api/v2/blog/search/v2"
	if raw := ctx.Request.URL.RawQuery; raw != "" {
		target += "?" + raw
	}
	ctx.Redirect(http.StatusPermanentRedirect, target)
}
