package user_service

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
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"

	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/middleware"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/utils"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	activity_pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/activity"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/cache/searchcache"
)

type UserServiceClient struct {
	Client      pb.UserServiceClient
	ActivityCli activity_pb.ActivityServiceClient
	log         *zap.SugaredLogger
	cache       *searchcache.Cache
}

func NewUserServiceClient(cfg *config.Config, lg *zap.SugaredLogger) pb.UserServiceClient {
	userService := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysUser, cfg.Microservices.UserPort)
	cc, err := grpc.NewClient(userService, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		lg.Errorw("dial user gRPC failed", "err", err, "addr", userService)
	}
	lg.Debugw("dialing user service", "addr", userService)
	return pb.NewUserServiceClient(cc)
}

func RegisterUserRouter(router *gin.Engine, cfg *config.Config, authClient *auth.ServiceClient, log *zap.SugaredLogger, cache *searchcache.Cache) *UserServiceClient {
	mware := auth.InitAuthMiddleware(authClient, log)

	usc := &UserServiceClient{
		Client:      NewUserServiceClient(cfg, log),
		ActivityCli: activity.NewActivityServiceClient(cfg, log),
		log:         log,
		cache:       cache,
	}
	routes := router.Group("/api/v1/user")
	routes.GET("/topics", usc.GetAllTopics)
	routes.GET("/category", usc.GetAllCategories)
	routes.GET("/public/:id", usc.GetUserPublicProfile)
	routes.GET("/public/account/:acc_id", usc.GetUserDetailsByAccId)
	routes.GET("/connection-count/:username", usc.ConnectionCount)

	routes.Use(mware.AuthRequired)

	{
		// Account verification (blue-check)
		routes.POST("/verification", middleware.RateLimiterMiddleware("10-H"), usc.SubmitVerification)
		routes.GET("/verification/me", usc.GetMyVerification)
		routes.DELETE("/verification/:request_id", usc.CancelVerification)
	}

	{
		routes.PUT("/:id", usc.UpdateUserProfile)
		routes.PATCH("/:id", usc.UpdateUserProfile)
		routes.GET("/:id", usc.GetUserProfile)
		routes.DELETE("/:id", usc.DeleteUserProfile)
		routes.GET("/followers", usc.GetFollowers)
		routes.GET("/following", usc.GetFollowing)
	}

	{
		routes.GET("/activities/:user_name", usc.GetUserActivities)
		routes.PUT("/follow-topics/:user_name", usc.FollowTopic)
		routes.PUT("/un-follow-topics/:user_name", usc.UnFollowTopic)
		routes.POST("/follow/:username", usc.FollowUser)
		routes.POST("/unfollow/:username", usc.UnfollowUser)
		routes.GET("/is-followed/:username", usc.IsUserFollowed)
		// DEPRECATED route — Search v2 deprecation (Phase 5): v1 user
		// search now permanently redirects to /api/v2/user/search. 308
		// preserves the GET method and the query string (search_term,
		// limit, offset are identical between v1 and v2), so existing
		// clients keep working without code changes. Will be removed
		// entirely after one release. Do not add new callers.
		routes.GET("/search", redirectToV2UserSearch)
	}

	// Invite and un invite as coauthor
	{
		routes.POST("/invite/:blog_id/", mware.AuthzRequired, usc.InviteCoAuthor)
		routes.POST("/revoke-invite/:blog_id/", mware.AuthzRequired, usc.RevokeInviteCoAuthor)
		routes.GET("/all-blogs/:username", usc.GetBlogsByUserName)
		routes.POST("/bookmark/:blog_id", usc.BookMarkABlog)
		routes.POST("/remove-bookmark/:blog_id", usc.RemoveBookMarkFromABlog)
		routes.GET("/count-bookmarks/:blog_id", usc.CountBookMarks)
		routes.GET("/is-bookmarked/:blog_id", usc.IsBlogBookMarked)
		routes.POST("/like/:blog_id", usc.LikeABlog)
		routes.POST("/unlike/:blog_id", usc.UnlikeABlog)
		routes.GET("/count-likes/:blog_id", usc.CountLikes)
		routes.GET("/is-liked/:blog_id", usc.IsBlogLiked)
	}

	{
		routes.POST("/topics", usc.CreateNewTopics)
	}

	// Business cards — owned by the authenticated account.
	{
		routes.POST("/cards", usc.CreateBusinessCard)
		routes.GET("/cards", usc.ListBusinessCards)
		routes.GET("/cards/:card_id", usc.GetBusinessCard)
		routes.PUT("/cards/:card_id", usc.UpdateBusinessCard)
		routes.PATCH("/cards/:card_id", usc.UpdateBusinessCard)
		routes.DELETE("/cards/:card_id", usc.DeleteBusinessCard)
	}

	// Private addresses — owned by the authenticated account (never public).
	{
		routes.POST("/addresses", usc.CreateUserAddress)
		routes.GET("/addresses", usc.ListUserAddresses)
		routes.GET("/addresses/:address_id", usc.GetUserAddress)
		routes.PUT("/addresses/:address_id", usc.UpdateUserAddress)
		routes.PATCH("/addresses/:address_id", usc.UpdateUserAddress)
		routes.DELETE("/addresses/:address_id", usc.DeleteUserAddress)
	}

	routesV2 := router.Group("/api/v2/user")
	rateLimiter := middleware.RateLimiterMiddleware("100-S")
	{
		routesV2.GET("/active-users", rateLimiter, usc.GetActiveUsers)
		// Search-v2 (Phase 1): index-backed, ranked, Active-only people
		// search. Rate limited to keep cheap typo-tolerant queries from
		// being weaponised into a CPU DoS against pg_trgm.
		routesV2.GET("/search", rateLimiter, usc.SearchUserV2)
	}

	return usc
}

func (asc *UserServiceClient) GetUserProfile(ctx *gin.Context) {
	username := ctx.Param("id")
	var isPrivate bool
	if username == ctx.GetString("userName") {
		isPrivate = true
	}

	res, err := asc.Client.GetUserProfile(context.Background(), &pb.UserProfileReq{
		Username: username,
		// Email:     email,
		IsPrivate: isPrivate,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, &res)
}

func (asc *UserServiceClient) GetUserPublicProfile(ctx *gin.Context) {
	username := ctx.Param("id")
	var isPrivate bool

	res, err := asc.Client.GetUserProfile(context.Background(), &pb.UserProfileReq{
		Username:  username,
		IsPrivate: isPrivate,
	})

	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, ReturnMessage{Message: "user not found"})
			return
		} else {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage{Message: "something went wrong"})
			return
		}
	}

	ctx.JSON(http.StatusAccepted, &res)
}

func (asc *UserServiceClient) GetUserActivities(ctx *gin.Context) {
	username := ctx.Param("user_name")
	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are unauthorized to perform this action"})
		return
	}

	res, err := asc.Client.GetUserActivities(ctx, &pb.UserActivityReq{
		UserName: username,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, ReturnMessage{Message: "no user/activity found"})
			return
		} else {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage{Message: "couldn't get the user's activities"})
			return
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (usc *UserServiceClient) UpdateUserProfile(ctx *gin.Context) {
	username := ctx.Param("id")
	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are unauthorized to perform this action"})
		return
	}

	ipAddress := ctx.Request.Header.Get("IP")
	client := ctx.Request.Header.Get("Client")

	var req UpdateUserProfileRequest

	if err := ctx.ShouldBindJSON(&req); err != nil {
		usc.log.Errorw("update user profile bind json failed", "err", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	body := req.Values
	var isPartial bool
	if ctx.Request.Method == http.MethodPatch {
		isPartial = true
	}

	res, err := usc.Client.UpdateUserProfile(context.Background(), &pb.UpdateUserProfileReq{
		Username:      username,
		FirstName:     body.FirstName,
		LastName:      body.LastName,
		DateOfBirth:   body.DateOfBirth,
		Bio:           body.Bio,
		Address:       body.Address,
		ContactNumber: body.ContactNumber,
		Twitter:       body.Twitter,
		Instagram:     body.Instagram,
		Linkedin:      body.LinkedIn,
		Github:        body.Github,
		Ip:            ipAddress,
		Client:        client,
		Partial:       isPartial,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, ReturnMessage{Message: "user not found"})
			return
		} else {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage{Message: "couldn't update user informations"})
			return
		}
	}
	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) DeleteUserProfile(ctx *gin.Context) {
	username := ctx.Param("id")
	tokenUsername := ctx.GetString("userName")

	if username != tokenUsername {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, ReturnMessage{Message: "you are unauthorized to perform this action"})
		return
	}

	res, err := asc.Client.DeleteUserAccount(context.Background(), &pb.DeleteUserProfileReq{
		Username: username,
	})

	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, ReturnMessage{Message: "no user/activity found"})
			return
		} else {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage{Message: "couldn't get the user's activities"})
			return
		}
	}

	ctx.SetCookie("mat", "", -1, "/", "", true, true)
	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) GetAllTopics(ctx *gin.Context) {
	res, err := asc.Client.GetAllTopics(context.Background(), &pb.GetTopicsRequests{})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get the list of topics"})
		return
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) GetAllCategories(ctx *gin.Context) {
	res, err := asc.Client.GetAllCategories(context.Background(), &pb.GetAllCategoriesReq{})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get the all the Categories"})
		return
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) GetUserDetailsByAccId(ctx *gin.Context) {
	accId := ctx.Param("acc_id")

	// Fetch user details by AccountId
	res, err := asc.Client.GetUserDetails(context.Background(), &pb.UserDetailReq{
		AccountId: accId,
	})
	if err != nil {
		switch status.Code(err) {
		case codes.NotFound:
			ctx.AbortWithStatusJSON(http.StatusNotFound, ReturnMessage{Message: "no user found"})
		case codes.Internal:
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage{Message: "couldn't get the user info due to internal error"})
		default:
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage{Message: "an unexpected error occurred"})
		}
		return
	}

	// Fetch followers and following counts
	connCount, err := asc.Client.GetFollowersFollowingCounts(context.Background(), &pb.UserDetailReq{
		Username: res.Username,
	})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage{Message: "couldn't fetch connection counts"})
		return
	}

	// Return the JSON response
	ctx.JSON(http.StatusOK, struct {
		User      *pb.UserDetailsResp `json:"user"`
		Followers int64               `json:"followers"`
		Following int64               `json:"following"`
	}{
		User:      res,
		Followers: connCount.Followers,
		Following: connCount.Following,
	})
}

func (asc *UserServiceClient) FollowTopic(ctx *gin.Context) {
	username := ctx.Param("user_name")
	ipAddress := ctx.Request.Header.Get("IP")
	client := ctx.Request.Header.Get("Client")

	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allow to perform this action"})
		return
	}

	var req FollowTopic
	if err := ctx.ShouldBindJSON(&req); err != nil {
		asc.log.Errorw("error while getting the update data: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	res, err := asc.Client.FollowTopics(context.Background(), &pb.TopicActionReq{
		Username: username,
		Topic:    req.Topics,
		Ip:       ipAddress,
		Client:   client,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.InvalidArgument:
				ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
				return
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, &res)
}

func (asc *UserServiceClient) UnFollowTopic(ctx *gin.Context) {
	username := ctx.Param("user_name")
	ipAddress := ctx.Request.Header.Get("IP")
	client := ctx.Request.Header.Get("Client")

	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allow to perform this action"})
		return
	}

	var req FollowTopic
	if err := ctx.ShouldBindJSON(&req); err != nil {
		asc.log.Errorw("error while getting the update data: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	res, err := asc.Client.UnFollowTopics(context.Background(), &pb.TopicActionReq{
		Username: username,
		Topic:    req.Topics,
		Ip:       ipAddress,
		Client:   client,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.InvalidArgument:
				ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
				return
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, &res)
}

func (asc *UserServiceClient) InviteCoAuthor(ctx *gin.Context) {
	blogId := ctx.Param("blog_id")
	userName := ctx.GetString("userName")

	ipAddress := ctx.Request.Header.Get("IP")
	client := ctx.Request.Header.Get("Client")

	// Check permissions:
	if !utils.CheckUserRoleInContext(ctx, constants.RoleOwner) {
		asc.log.Errorw("user does not have the permission to invite a co-author")
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}
	// accId := ctx.GetString("accountId")

	var req CoAuthor
	if err := ctx.ShouldBindJSON(&req); err != nil {
		asc.log.Errorw("error while getting the update data: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	res, err := asc.Client.InviteCoAuthor(context.Background(), &pb.CoAuthorAccessReq{
		AccountId:         req.AccountId,
		Username:          req.Username,
		Email:             req.Email,
		Ip:                ipAddress,
		Client:            client,
		BlogOwnerUsername: userName,
		BlogId:            blogId,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.InvalidArgument:
				ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
				return
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user/blog does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, &res)
}

func (asc *UserServiceClient) RevokeInviteCoAuthor(ctx *gin.Context) {
	blogId := ctx.Param("blog_id")
	userName := ctx.GetString("userName")

	ipAddress := ctx.Request.Header.Get("IP")
	client := ctx.Request.Header.Get("Client")

	// Check permissions:
	if !utils.CheckUserRoleInContext(ctx, constants.RoleOwner) {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allowed to perform this action"})
		return
	}
	// accId := ctx.GetString("accountId")

	var req CoAuthor
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	res, err := asc.Client.RevokeCoAuthorAccess(context.Background(), &pb.CoAuthorAccessReq{
		AccountId:         req.AccountId,
		Username:          req.Username,
		Email:             req.Email,
		Ip:                ipAddress,
		Client:            client,
		BlogOwnerUsername: userName,
		BlogId:            blogId,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.InvalidArgument:
				ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
				return
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user/blog does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, &res)
}

func (asc *UserServiceClient) GetBlogsByUserName(ctx *gin.Context) {
	username := ctx.Param("username")

	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "you are not allow to perform this action"})
		return
	}

	res, err := asc.Client.GetBlogsByUserIds(context.Background(), &pb.BlogsByUserIdsReq{
		Username: username,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			case codes.AlreadyExists:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user already has the blog permission"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, &res)
}

func (asc *UserServiceClient) CreateNewTopics(ctx *gin.Context) {
	userName := ctx.GetString("userName")

	var req Topics

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	res, err := asc.Client.CreateNewTopics(context.Background(), &pb.CreateTopicsReq{
		Topics:   req.Topics,
		Category: req.Category,
		Username: userName,
		Ip:       ctx.Request.Header.Get("Ip"),
		Client:   ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.InvalidArgument:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "invalid request body"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}
	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) BookMarkABlog(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	blogId := ctx.Param("blog_id")

	res, err := asc.Client.BookMarkBlog(context.Background(), &pb.BookMarkReq{
		Username: userName,
		BlogId:   blogId,
		Ip:       ctx.Request.Header.Get("Ip"),
		Client:   ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.AlreadyExists:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog already bookmarked"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) RemoveBookMarkFromABlog(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	blogId := ctx.Param("blog_id")

	res, err := asc.Client.RemoveBookMark(context.Background(), &pb.BookMarkReq{
		Username: userName,
		BlogId:   blogId,
		Ip:       ctx.Request.Header.Get("Ip"),
		Client:   ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.AlreadyExists:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog already removed from bookmarked"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) FollowUser(ctx *gin.Context) {
	username := ctx.Param("username")
	followerUsername := ctx.GetString("userName")

	resp, err := asc.Client.FollowUser(context.Background(), &pb.UserFollowReq{
		Username:         username,
		FollowerUsername: followerUsername,
		Ip:               ctx.Request.Header.Get("IP"),
		Client:           ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}
	ctx.JSON(http.StatusOK, resp)
}

func (asc *UserServiceClient) UnfollowUser(ctx *gin.Context) {
	username := ctx.Param("username")
	followerUsername := ctx.GetString("userName")

	resp, err := asc.Client.UnFollowUser(context.Background(), &pb.UserFollowReq{
		Username:         username,
		FollowerUsername: followerUsername,
		Ip:               ctx.Request.Header.Get("IP"),
		Client:           ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}
	ctx.JSON(http.StatusOK, resp)
}

func (asc *UserServiceClient) GetFollowers(ctx *gin.Context) {
	username := ctx.GetString("userName")

	resp, err := asc.Client.GetFollowers(context.Background(), &pb.UserDetailReq{
		Username: username,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}
	ctx.JSON(http.StatusOK, resp)
}

func (asc *UserServiceClient) GetFollowing(ctx *gin.Context) {
	username := ctx.GetString("userName")

	resp, err := asc.Client.GetFollowing(context.Background(), &pb.UserDetailReq{
		Username: username,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}
	ctx.JSON(http.StatusOK, resp)
}

func (asc *UserServiceClient) LikeABlog(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	blogId := ctx.Param("blog_id")

	res, err := asc.Client.LikeBlog(context.Background(), &pb.BookMarkReq{
		Username: userName,
		BlogId:   blogId,
		Ip:       ctx.Request.Header.Get("Ip"),
		Client:   ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.AlreadyExists:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog already like"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) UnlikeABlog(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	blogId := ctx.Param("blog_id")

	res, err := asc.Client.UnlikeBlog(context.Background(), &pb.BookMarkReq{
		Username: userName,
		BlogId:   blogId,
		Ip:       ctx.Request.Header.Get("Ip"),
		Client:   ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.AlreadyExists:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog already removed from like"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) IsBlogLiked(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	blogId := ctx.Param("blog_id")

	res, err := asc.Client.GetIfBlogLiked(context.Background(), &pb.BookMarkReq{
		Username: userName,
		BlogId:   blogId,
		Ip:       ctx.Request.Header.Get("Ip"),
		Client:   ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.AlreadyExists:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog already removed from like"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) IsUserFollowed(ctx *gin.Context) {
	username := ctx.GetString("userName")
	followedUsername := ctx.Param("username")

	res, err := asc.Client.GetIfIFollowedUser(context.Background(), &pb.UserFollowReq{
		Username:         followedUsername,
		FollowerUsername: username,
		Ip:               ctx.Request.Header.Get("IP"),
		Client:           ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}
	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) CountBookMarks(ctx *gin.Context) {
	blogId := ctx.Param("blog_id")

	res, err := asc.Client.GetBookMarkCounts(context.Background(), &pb.BookMarkReq{
		BlogId: blogId,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}
	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) IsBlogBookMarked(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	blogId := ctx.Param("blog_id")

	res, err := asc.Client.GetIfBlogBookMarked(context.Background(), &pb.BookMarkReq{
		Username: userName,
		BlogId:   blogId,
		Ip:       ctx.Request.Header.Get("Ip"),
		Client:   ctx.Request.Header.Get("Client"),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			case codes.AlreadyExists:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog already bookmarked"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) CountLikes(ctx *gin.Context) {
	blogId := ctx.Param("blog_id")

	res, err := asc.Client.GetLikeCounts(context.Background(), &pb.BookMarkReq{
		BlogId: blogId,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the blog does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}
	ctx.JSON(http.StatusOK, res)
}

func (asc *UserServiceClient) SearchUser(ctx *gin.Context) {
	// Deprecated: the v1 streaming search handler is no longer
	// registered on any route. The /api/v1/user/search path now issues a
	// 308 redirect to /api/v2/user/search (see redirectToV2UserSearch).
	// This stub remains only because the bidi gRPC method on the user
	// service is still wired for potential internal callers. Do not add
	// new callers — use FindUsersV2 / /api/v2/user/search instead.
	ctx.Redirect(http.StatusPermanentRedirect, buildRedirectTarget(ctx, "/api/v2/user/search"))
}

// redirectToV2UserSearch issues a 308 Permanent Redirect from the
// deprecated /api/v1/user/search to /api/v2/user/search, preserving the
// raw query string. 308 (vs 301/302) guarantees the GET method and the
// body — if any — are not mutated by intermediaries.
//
// Deprecated: this handler exists only as a transitional shim. New
// code must call /api/v2/user/search directly. The route will be
// removed one release after the Search v2 rollout soaks.
func redirectToV2UserSearch(ctx *gin.Context) {
	ctx.Redirect(http.StatusPermanentRedirect, buildRedirectTarget(ctx, "/api/v2/user/search"))
}

// buildRedirectTarget appends the inbound query string (if any) to the
// new path so that ?search_term=…&limit=…&offset=… is forwarded
// untouched. We do NOT pass through the host — Gin's Redirect will
// emit a relative Location which the client resolves against the
// originating origin, which is what we want behind the load balancer.
func buildRedirectTarget(ctx *gin.Context, newPath string) string {
	if raw := ctx.Request.URL.RawQuery; raw != "" {
		return newPath + "?" + raw
	}
	return newPath
}

func (asc *UserServiceClient) ConnectionCount(ctx *gin.Context) {
	username := ctx.Param("username")

	res, err := asc.Client.GetFollowersFollowingCounts(context.Background(), &pb.UserDetailReq{
		Username: username,
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "the user does not exist"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}
	ctx.JSON(http.StatusOK, res)
}

// GetActiveUsers returns the count of active users
// GET /api/v2/user/active-users
//
// Implements a progressive time-range fallback: if the requested window
// yields zero active users (common on quiet dev / staging databases), we
// widen the window through 7d → 30d → 1y until we find any. The window
// actually used is surfaced as `time_range_used` so the client can react.
func (usc *UserServiceClient) GetActiveUsers(ctx *gin.Context) {
	accID := ctx.Query("account_id")
	requested := ctx.DefaultQuery("time_range", "3h")

	// Fallback ladder — ordered narrow → wide. Requested window goes first
	// so a fresh, populated index returns the natural answer in one call.
	candidates := []string{requested, "7d", "30d", "1y"}
	seen := make(map[string]struct{}, len(candidates))

	var (
		resp      *activity_pb.GetActiveUsersResponse
		usedRange string
		lastErr   error
	)

	for _, tr := range candidates {
		if _, dup := seen[tr]; dup {
			continue
		}
		seen[tr] = struct{}{}

		r, err := usc.ActivityCli.GetActiveUsers(ctx.Request.Context(), &activity_pb.GetActiveUsersRequest{
			AccountId: accID,
			TimeRange: tr,
		})
		if err != nil {
			lastErr = err
			usc.log.Errorw("active users call failed", "time_range", tr, "err", err)
			continue
		}

		resp = r
		usedRange = tr
		if len(r.GetUserList()) > 0 {
			break
		}
	}

	if resp == nil {
		// All attempts errored — return the last error to the caller.
		usc.log.Errorf("failed to get active users after fallback: %v", lastErr)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch active users"})
		return
	}

	// Collect account ids from the active users list.
	accIds := make([]string, 0, len(resp.UserList))
	for _, user := range resp.UserList {
		if user.UserId != "" {
			accIds = append(accIds, user.UserId)
		}
	}

	var userDetails []*pb.UserDetailsResp
	if len(accIds) > 0 {
		usersResp, err := usc.Client.GetBatchUserDetails(ctx.Request.Context(), &pb.GetBatchUserDetailsReq{
			AccountIds: accIds,
		})

		if err != nil {
			usc.log.Errorf("failed to get user details: %v", err)
			// We don't return here to allow the count to be returned even if details fail
		} else {
			userDetails = usersResp.Users
		}
	}

	ctx.JSON(http.StatusOK, gin.H{
		"active_users":    resp.ActiveUsers,
		"user_details":    userDetails,
		"status_code":     resp.StatusCode,
		"time_range_used": usedRange,
	})
}

// searchV2QueryMaxLen bounds the user-supplied query string to prevent
// a malicious caller from passing a multi-MB string into pg_trgm where
// the trigram extraction cost grows linearly with input size.
const (
	searchV2QueryMaxLen = 128
	searchV2LimitMax    = 50
	searchV2LimitDflt   = 10
	searchV2Timeout     = 500 * time.Millisecond
	searchV2CacheTTL    = 30 * time.Second
)

// SearchUserV2 is the search-v2 (Phase 1) HTTP handler for people search.
//
// Differences from the v1 handler:
//
//   - Strict input validation. Query length is bounded, limit is hard-
//     capped server-side, both numeric params reject negatives. v1
//     accepted unbounded limit values and forwarded them to the DB.
//   - Bounded gRPC deadline. v1 used context.Background() which meant a
//     slow user service could block the gateway forever. v2 uses
//     context.WithTimeout from the inbound request context so client
//     cancellations propagate.
//   - Read-through Redis cache. Identical queries within 30s return the
//     cached result without hitting Postgres at all. Cache key is a
//     hashed tuple of (query, limit, offset) — never the raw query, so
//     keys cannot leak PII through Redis MONITOR.
//   - Structured search_event log line for observability (zero-result
//     queries, cache hit ratio, latency). No raw PII in logs.
//   - Stable JSON response shape: {"users": [...], "limit", "offset"}.
//     Keeps the existing v1 frontend contract for the users array so
//     migration is incremental.
func (asc *UserServiceClient) SearchUserV2(ctx *gin.Context) {
	start := time.Now()

	rawQuery := strings.TrimSpace(ctx.Query("search_term"))
	if rawQuery == "" {
		// Empty query is a client error — every match would be returned
		// otherwise, which is both expensive and a privacy hazard.
		ctx.JSON(http.StatusBadRequest, gin.H{"message": "search_term is required"})
		return
	}
	if len(rawQuery) > searchV2QueryMaxLen {
		ctx.JSON(http.StatusBadRequest, gin.H{"message": "search_term too long"})
		return
	}

	limitInt, err := strconv.Atoi(ctx.DefaultQuery("limit", strconv.Itoa(searchV2LimitDflt)))
	if err != nil || limitInt <= 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"message": "Invalid limit parameter"})
		return
	}
	if limitInt > searchV2LimitMax {
		limitInt = searchV2LimitMax
	}

	offsetInt, err := strconv.Atoi(ctx.DefaultQuery("offset", "0"))
	if err != nil || offsetInt < 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"message": "Invalid offset parameter"})
		return
	}

	// Normalise the cache key on a lowercased query so "Alice" and "alice"
	// share a cache entry. The DB-side ranking is already case-insensitive.
	cacheKey := searchcache.CacheKeyInts("user:"+strings.ToLower(rawQuery), limitInt, offsetInt)

	cacheHit := false
	if cached, cErr := asc.cache.Get(ctx.Request.Context(), cacheKey); cErr == nil && cached != "" {
		cacheHit = true
		searchcache.LogSearchEvent(asc.log, searchcache.Event{
			Query:       rawQuery, // logged at debug-level only; see helper
			Type:        "user",
			Limit:       limitInt,
			Offset:      offsetInt,
			ResultCount: -1, // unknown from cache without re-parsing
			CacheHit:    true,
			Latency:     time.Since(start),
			UserID:      searchcache.HashUserID(ctx.GetString("accountId")),
		})
		ctx.Data(http.StatusOK, "application/json", []byte(cached))
		return
	}

	rpcCtx, cancel := context.WithTimeout(ctx.Request.Context(), searchV2Timeout)
	defer cancel()

	// Reuse the existing bidi-stream RPC. The user service's DB layer
	// now executes the v2 ranked query, so the wire shape is unchanged
	// but the result quality is upgraded transparently. A unary RPC is
	// a future refactor (see SEARCH_V2_IMPLEMENTATION_PLAN.md).
	stream, err := asc.Client.SearchUser(rpcCtx)
	if err != nil {
		asc.log.Errorw("v2 search: failed to open stream", "err", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"message": "search unavailable"})
		return
	}

	if err := stream.Send(&pb.UserDetailReq{
		SearchTerm: rawQuery,
		Limit:      int32(limitInt),
		Offset:     int32(offsetInt),
	}); err != nil {
		asc.log.Errorw("v2 search: failed to send request", "err", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"message": "search failed"})
		return
	}
	if err := stream.CloseSend(); err != nil {
		asc.log.Warnw("v2 search: close send failed", "err", err)
	}

	// Pre-allocate the slice to the cap to avoid grow churn in the
	// (common) full-page response case.
	results := make([]*pb.User, 0, limitInt)
	for {
		resp, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			if status.Code(recvErr) == codes.DeadlineExceeded {
				asc.log.Warnw("v2 search: deadline exceeded", "query_len", len(rawQuery))
				ctx.JSON(http.StatusGatewayTimeout, gin.H{"message": "search timed out"})
				return
			}
			asc.log.Errorw("v2 search: stream recv failed", "err", recvErr)
			ctx.JSON(http.StatusInternalServerError, gin.H{"message": "search failed"})
			return
		}
		results = append(results, resp.Users...)
	}

	payload := gin.H{
		"users":  results,
		"limit":  limitInt,
		"offset": offsetInt,
	}

	// Best-effort cache write. We marshal once for both the response
	// body and the cache value to avoid double encoding cost.
	if body, mErr := json.Marshal(payload); mErr == nil {
		// Cache.Set is fire-and-forget by design (errors logged inside)
		// so we never let a flaky Redis turn into a user-visible 5xx.
		asc.cache.Set(ctx.Request.Context(), cacheKey, string(body), searchV2CacheTTL)
		searchcache.LogSearchEvent(asc.log, searchcache.Event{
			Query:       rawQuery,
			Type:        "user",
			Limit:       limitInt,
			Offset:      offsetInt,
			ResultCount: len(results),
			CacheHit:    cacheHit,
			Latency:     time.Since(start),
			UserID:      searchcache.HashUserID(ctx.GetString("accountId")),
		})
		ctx.Data(http.StatusOK, "application/json", body)
		return
	}

	// Marshal fell back: still serve the response, just without caching.
	ctx.JSON(http.StatusOK, payload)
}
