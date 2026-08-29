package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/utils"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamp "google.golang.org/protobuf/types/known/timestamppb"
)

type UserSvc struct {
	dbConn database.UserDb
	log    *zap.SugaredLogger
	config *config.Config
	qConn  *rabbitmq.ConnManager
	pb.UnimplementedUserServiceServer
}

func NewUserSvc(dbConn database.UserDb, log *zap.SugaredLogger, config *config.Config, qConn *rabbitmq.ConnManager) *UserSvc {
	return &UserSvc{dbConn: dbConn, log: log, config: config, qConn: qConn}
}

func (us *UserSvc) GetUserProfile(ctx context.Context, req *pb.UserProfileReq) (*pb.UserProfileRes, error) {
	us.log.Debugf("profile info has been requested for user: %s.", req.Username)
	if !req.IsPrivate {
		userProfile, err := us.dbConn.GetUserProfile(req.Username)
		if err != nil {
			us.log.Errorf("error while fetching the public profile for user %s, err: %v", req.Username, err)
			if err == sql.ErrNoRows {
				return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
			}
			return nil, status.Errorf(codes.Internal, "cannot get the user profile")
		}
		return &pb.UserProfileRes{
			Username:   userProfile.UserName,
			FirstName:  userProfile.FirstName,
			LastName:   userProfile.LastName,
			Bio:        userProfile.Bio.String,
			AvatarUrl:  userProfile.AvatarUrl.String,
			CreatedAt:  timestamp.New(userProfile.CreatedAt.Time),
			Address:    userProfile.Address.String,
			Linkedin:   userProfile.LinkedIn.String,
			Instagram:  userProfile.Instagram.String,
			Twitter:    userProfile.Twitter.String,
			Github:     userProfile.Github.String,
			Topics:     userProfile.Interests,
			IsVerified: userProfile.IsVerified,
		}, nil

	}

	_, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while fetching the private profile for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	userDetails, err := us.dbConn.GetMyProfile(req.Username)
	if err != nil {
		us.log.Errorf("error while fetching the profile for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "profile for user: %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	return &pb.UserProfileRes{
		AccountId:     userDetails.AccountId,
		Username:      userDetails.Username,
		FirstName:     userDetails.FirstName,
		LastName:      userDetails.LastName,
		DateOfBirth:   userDetails.DateOfBirth.Time.String(),
		Bio:           userDetails.Bio.String,
		AvatarUrl:     userDetails.AvatarUrl.String,
		CreatedAt:     timestamp.New(userDetails.CreatedAt.Time),
		UpdatedAt:     timestamp.New(userDetails.UpdatedAt.Time),
		Address:       userDetails.Address.String,
		ContactNumber: userDetails.ContactNumber.String,
		UserStatus:    userDetails.UserStatus,
		Linkedin:      userDetails.LinkedIn.String,
		Instagram:     userDetails.Instagram.String,
		Twitter:       userDetails.Twitter.String,
		Github:        userDetails.Github.String,
		Topics:        userDetails.Interests,
	}, err
}

func (us *UserSvc) GetUserActivities(ctx context.Context, req *pb.UserActivityReq) (*pb.UserActivityResp, error) {
	us.log.Debugf("Retrieving activities for: %v", req.UserName)
	// Check if username exits or not
	user, err := us.dbConn.CheckIfUsernameExist(req.UserName)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.UserName, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.UserName)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	return us.dbConn.GetUserActivities(user.Id)
}

func (us *UserSvc) UpdateUserProfile(ctx context.Context, req *pb.UpdateUserProfileReq) (*pb.UpdateUserProfileRes, error) {
	us.log.Debugf("user %s is updating the profile.", req.Username)
	us.log.Debugf("req: %+v", req)

	// Check if the user exists
	userDetails, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	// Check if the method isPartial true
	var dbUserInfo = &models.UserProfileRes{}
	if req.Partial {
		// If isPartial is true fetch the remaining data from the db
		dbUserInfo, err = us.dbConn.GetMyProfile(req.Username)
		if err != nil {
			us.log.Errorf("error while fetching the profile for user %s, err: %v", req.Username, err)
			if err == sql.ErrNoRows {
				return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
			}
			return nil, status.Errorf(codes.Internal, "cannot get the user profile")
		}
		// Map the user
		dbUserInfo = utils.MapUserUpdateDataPatch(req, dbUserInfo)
	} else {
		dbUserInfo = utils.MapUserUpdateDataPut(req, dbUserInfo)
	}

	// Update the user
	err = us.dbConn.UpdateUserProfile(req.Username, dbUserInfo)
	if err != nil {
		us.log.Errorf("error while updating the profile for user %s, err: %v", req.Username, err)
		return nil, status.Errorf(codes.Internal, "cannot update the user profile")
	}

	userLog := &models.UserLogs{
		AccountId: userDetails.AccountId,
	}

	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	return &pb.UpdateUserProfileRes{
		Username: dbUserInfo.Username,
	}, err
}

// TODO: Design a pipeline
// 1. Delete all the blogs of the user
// 2. Delete all the comments of the user
// 3. Delete all the likes of the user
// 4. Delete all the user interests
// 5. Delete the topics of the user
// 6. Send User a mail
func (us *UserSvc) DeleteUserAccount(ctx context.Context, req *pb.DeleteUserProfileReq) (*pb.DeleteUserProfileRes, error) {
	us.log.Debugw("=== ACCOUNT DELETION STARTED ===", "username", req.Username)

	// Step 1: Verify user exists and capture their details (email, account_id)
	user, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("cannot verify user existence for %s: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}
	us.log.Debugw("User verified", "username", user.Username, "account_id", user.AccountId, "email", user.Email)

	// Step 2: Fetch blog IDs BEFORE deletion (rows disappear after TX commits)
	blogIDs, err := us.dbConn.GetOwnedBlogIDs(req.Username)
	if err != nil {
		us.log.Errorf("failed to fetch owned blog IDs for %s: %v", req.Username, err)
		// Non-fatal: proceed with deletion but blog files in MinIO may be orphaned
		blogIDs = nil
	}
	us.log.Debugw("Owned blogs collected", "username", req.Username, "blog_count", len(blogIDs), "blog_ids", blogIDs)

	// Step 3: Delete from Postgres (all 16 tables in a single transaction)
	err = us.dbConn.DeleteUserProfile(req.Username)
	if err != nil {
		us.log.Errorf("postgres deletion failed for %s: %v", req.Username, err)
		return nil, status.Errorf(codes.Internal, "cannot delete the user")
	}
	us.log.Debugw("Postgres deletion complete", "username", req.Username)

	// Build the RabbitMQ message with blog IDs and email for downstream consumers
	msg := models.TheMonkeysMessage{
		Username:  user.Username,
		AccountId: user.AccountId,
		Email:     user.Email,
		Action:    constants.USER_ACCOUNT_DELETE,
		BlogIds:   blogIDs,
	}
	bx, err := json.Marshal(msg)
	if err != nil {
		us.log.Errorf("failed to marshal delete message: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to prepare deletion events")
	}
	us.log.Debugw("RabbitMQ message prepared",
		"username", user.Username,
		"account_id", user.AccountId,
		"email", user.Email,
		"blog_ids_count", len(blogIDs),
		"routing_keys", []string{
			us.config.RabbitMQ.RoutingKeys[0],
			us.config.RabbitMQ.RoutingKeys[3],
			us.config.RabbitMQ.RoutingKeys[4],
		},
	)

	// Step 4: Publish to all consumers using PublishReliable (broker-confirmed delivery)
	exchange := us.config.RabbitMQ.Exchange
	maxRetries := us.config.RabbitMQ.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	// Storage consumer: deletes profile folder + blog files from MinIO and FS
	go func() {
		rk := us.config.RabbitMQ.RoutingKeys[0]
		us.log.Debugw("Publishing to storage consumer", "exchange", exchange, "routing_key", rk, "username", user.Username)
		if err := us.qConn.PublishReliable(exchange, rk, bx, maxRetries); err != nil {
			us.log.Errorw("FAILED to publish to storage consumer", "routing_key", rk, "username", user.Username, "err", err)
		} else {
			us.log.Debugw("Published to storage consumer (broker confirmed)", "routing_key", rk, "username", user.Username)
		}
	}()

	// Blog consumer: bulk-deletes blog documents from Elasticsearch
	go func() {
		rk := us.config.RabbitMQ.RoutingKeys[3]
		us.log.Debugw("Publishing to blog consumer", "exchange", exchange, "routing_key", rk, "username", user.Username)
		if err := us.qConn.PublishReliable(exchange, rk, bx, maxRetries); err != nil {
			us.log.Errorw("FAILED to publish to blog consumer", "routing_key", rk, "username", user.Username, "err", err)
		} else {
			us.log.Debugw("Published to blog consumer (broker confirmed)", "routing_key", rk, "username", user.Username)
		}
	}()

	// Notification consumer: sends farewell notification + deletes user from FRN
	go func() {
		rk := us.config.RabbitMQ.RoutingKeys[4]
		us.log.Debugw("Publishing to notification consumer", "exchange", exchange, "routing_key", rk, "username", user.Username)
		if err := us.qConn.PublishReliable(exchange, rk, bx, maxRetries); err != nil {
			us.log.Errorw("FAILED to publish to notification consumer", "routing_key", rk, "username", user.Username, "err", err)
		} else {
			us.log.Debugw("Published to notification consumer (broker confirmed)", "routing_key", rk, "username", user.Username)
		}
	}()

	// Step 5: Send farewell email directly via SMTP (best-effort, fire-and-forget)
	go func() {
		if user.Email == "" {
			us.log.Warn("No email address for deleted user, skipping farewell email", "username", user.Username)
			return
		}
		us.log.Debugw("Sending farewell email", "username", user.Username, "email", user.Email)
		emailBody := utils.AccountDeletedEmailHTML(user.FirstName, user.LastName, user.Username)
		if err := utils.SendMail(us.config, user.Email, emailBody); err != nil {
			us.log.Errorw("Failed to send farewell email", "username", user.Username, "email", user.Email, "err", err)
		} else {
			us.log.Debugw("Farewell email sent", "username", user.Username, "email", user.Email)
		}
	}()

	us.log.Debugw("=== ACCOUNT DELETION COMPLETED ===", "username", req.Username, "account_id", user.AccountId)

	return &pb.DeleteUserProfileRes{
		Success: "user has been deleted successfully",
		Status:  "200",
	}, nil
}

func (us *UserSvc) GetAllTopics(context.Context, *pb.GetTopicsRequests) (*pb.GetTopicsResponse, error) {
	us.log.Debug("getting all the topics")

	res, err := us.dbConn.GetAllTopicsFromDb()
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			us.log.Errorf("cannot find the topics in the database: %v", err)
		}
		us.log.Errorf("error while querying the topics: %v", err)
		return nil, errors.New("error while querying the topics")
	}

	return res, err
}

func (us *UserSvc) GetAllCategories(ctx context.Context, req *pb.GetAllCategoriesReq) (*pb.GetAllCategoriesRes, error) {
	us.log.Debug("getting all the Description and Categories")

	res, err := us.dbConn.GetAllCategories()
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			us.log.Errorf("no Categories and Description found in the database: %v", err)
			return nil, errors.New("no Categories found")
		}
		us.log.Errorf("error while querying the Categories: %v", err)
		return nil, errors.New("error while querying the categories")
	}

	return res, nil
}

func (us *UserSvc) GetUserDetails(ctx context.Context, req *pb.UserDetailReq) (*pb.UserDetailsResp, error) {
	var (
		userInfo *models.TheMonkeysUser
		err      error
	)

	switch {
	case req.AccountId != "":
		us.log.Debugf("Profile info has been requested for account id: %s.", req.AccountId)
		userInfo, err = us.dbConn.CheckIfAccIdExist(req.AccountId)
	case req.Username != "":
		us.log.Debugf("Profile info has been requested for username: %s.", req.Username)
		userInfo, err = us.dbConn.CheckIfUsernameExist(req.Username)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "either AccountId or Username must be provided")
	}

	if err != nil {
		us.log.Errorf("Error fetching profile info: %v", err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user not found")
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	return &pb.UserDetailsResp{
		Username:   userInfo.Username,
		FirstName:  userInfo.FirstName,
		LastName:   userInfo.LastName,
		AccountId:  userInfo.AccountId,
		Bio:        userInfo.Bio.String,
		Location:   userInfo.Location.String,
		IsVerified: userInfo.IsVerified,
	}, nil
}

func (us *UserSvc) FollowTopics(ctx context.Context, req *pb.TopicActionReq) (*pb.TopicActionRes, error) {
	if len(req.Topic) == 0 {
		us.log.Errorf("user %s has entered no topic", req.Username)
		return nil, status.Errorf(codes.InvalidArgument, "there is no topic")
	}

	for i := range req.Topic {
		req.Topic[i] = strings.TrimSpace(req.Topic[i])
	}

	err := us.dbConn.AddUserInterest(req.Topic, req.Username)
	if err != nil {
		us.log.Errorf("Failed to update user interest for user %s, error: %v", req.Username, err)
		return nil, status.Errorf(codes.Internal, "Failed to update user interest")
	}

	// Check if the user exists
	dbUserInfo, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	userLog := &models.UserLogs{
		AccountId: dbUserInfo.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}

	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	return &pb.TopicActionRes{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("user's interest in the topics %v is updated successfully", req.Topic),
	}, nil
}

func (us *UserSvc) UnFollowTopics(ctx context.Context, req *pb.TopicActionReq) (*pb.TopicActionRes, error) {
	if len(req.Topic) == 0 {
		us.log.Errorf("user %s has entered no topic", req.Username)
		return nil, status.Errorf(codes.InvalidArgument, "there is no topic")
	}

	for i := range req.Topic {
		req.Topic[i] = strings.TrimSpace(req.Topic[i])
	}

	err := us.dbConn.RemoveUserInterest(req.Topic, req.Username)
	if err != nil {
		us.log.Errorf("Failed to remove user interest for user %s, error: %v", req.Username, err)
		return nil, status.Errorf(codes.Internal, "Failed to update user interest")
	}

	// Check if the user exists
	dbUserInfo, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	userLog := &models.UserLogs{
		AccountId: dbUserInfo.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}

	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	return &pb.TopicActionRes{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("user's un-followed the topics %v is updated successfully", req.Topic),
	}, nil
}

func (us *UserSvc) InviteCoAuthor(ctx context.Context, req *pb.CoAuthorAccessReq) (*pb.CoAuthorAccessRes, error) {
	us.log.Debugf("user %s has requested to invite %s as a co-author.", req.BlogOwnerUsername, req.Username)
	resp, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	// Invite the co-author
	if err := us.dbConn.AddPermissionToAUser(req.BlogId, resp.Id, req.BlogOwnerUsername, constants.RoleEditor); err != nil {
		us.log.Errorf("error while inviting the co-author: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	usa, _ := us.dbConn.CheckIfUsernameExist(req.BlogOwnerUsername)
	userLog := &models.UserLogs{
		AccountId: usa.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}

	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	// Publish notification event for co-author invite
	notifMsg, err := json.Marshal(models.TheMonkeysMessage{
		AccountId:    resp.AccountId,
		Username:     req.BlogOwnerUsername,
		NewUsername:  req.Username,
		Action:       constants.CO_AUTHOR_INVITE,
		BlogId:       req.BlogId,
		Notification: fmt.Sprintf("%s invited you as a co-author", req.BlogOwnerUsername),
	})
	if err != nil {
		us.log.Errorf("failed to marshal co-author invite notification: %v", err)
	} else {
		go func() {
			if err := us.qConn.PublishMessage(us.config.RabbitMQ.Exchange, us.config.RabbitMQ.RoutingKeys[4], notifMsg); err != nil {
				us.log.Errorf("failed to publish co-author invite notification: %v", err)
			}
		}()
	}

	return &pb.CoAuthorAccessRes{
		Message: fmt.Sprintf("%s has been invited as a co-author", req.Username),
	}, nil
}

func (us *UserSvc) RevokeCoAuthorAccess(ctx context.Context, req *pb.CoAuthorAccessReq) (*pb.CoAuthorAccessRes, error) {
	us.log.Debugf("user %s has requested to invite %s as a co-author.", req.BlogOwnerUsername, req.Username)
	resp, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	// Invite the co-author
	if err := us.dbConn.RevokeBlogPermissionFromAUser(req.BlogId, resp.Id, constants.RoleEditor); err != nil {
		us.log.Errorf("error while inviting the co-author: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	usa, _ := us.dbConn.CheckIfUsernameExist(req.BlogOwnerUsername)

	userLog := &models.UserLogs{
		AccountId: usa.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}

	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	// Publish notification event for co-author removal
	notifMsg, err := json.Marshal(models.TheMonkeysMessage{
		Username:     req.BlogOwnerUsername,
		NewUsername:  req.Username,
		Action:       constants.CO_AUTHOR_REMOVED,
		BlogId:       req.BlogId,
		Notification: fmt.Sprintf("You have been removed as co-author from blog %s", req.BlogId),
	})
	if err != nil {
		us.log.Errorf("failed to marshal co-author removed notification: %v", err)
	} else {
		go func() {
			if err := us.qConn.PublishMessage(us.config.RabbitMQ.Exchange, us.config.RabbitMQ.RoutingKeys[4], notifMsg); err != nil {
				us.log.Errorf("failed to publish co-author removal notification: %v", err)
			}
		}()
	}

	return &pb.CoAuthorAccessRes{
		Message: fmt.Sprintf("%s has been removed from co-author", req.Username),
	}, nil
}

func (us *UserSvc) GetBlogsByUserIds(ctx context.Context, req *pb.BlogsByUserIdsReq) (*pb.BlogsByUserNameRes, error) {
	us.log.Debugf("fetching blogs for user: %s", req.AccountId)

	switch req.Type {
	case "colab":
		resp, err := us.dbConn.GetCoAuthorBlogsByAccountId(req.AccountId)
		if err != nil {
			us.log.Errorf("error while fetching blogs for user %s, err: %v", req.Username, err)
			if err == sql.ErrNoRows {
				return nil, status.Errorf(codes.NotFound, "blogs for user %s doesn't exist", req.Username)
			}

			return nil, status.Errorf(codes.Internal, "something went wrong")
		}

		return resp, nil

	case "bookmark":
		resp, err := us.dbConn.GetBookmarkBlogsByAccountId(req.AccountId)
		if err != nil {
			us.log.Errorf("error while fetching blogs for user %s, err: %v", req.Username, err)
			if err == sql.ErrNoRows {
				return nil, status.Errorf(codes.NotFound, "blogs for user %s doesn't exist", req.Username)
			}

			return nil, status.Errorf(codes.Internal, "something went wrong")
		}

		return resp, nil
	default:
		return nil, status.Errorf(codes.Internal, "We don't support this operation")
	}
}

func (us *UserSvc) CreateNewTopics(ctx context.Context, req *pb.CreateTopicsReq) (*pb.CreateTopicsRes, error) {
	us.log.Debugf("fetching co-authors for user: %s", req.Username)
	if len(req.Topics) == 0 {
		us.log.Errorf("user %s has entered no topic", req.Username)
		return nil, status.Errorf(codes.InvalidArgument, "there is no topic")
	}

	err := us.dbConn.CreateNewTopics(req.Topics, req.Category, req.Username)
	if err != nil {
		us.log.Errorf("error while fetching co-authors for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "co-authors for user %s doesn't exist", req.Username)
		}

		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	usa, _ := us.dbConn.CheckIfUsernameExist(req.Username)

	userLog := &models.UserLogs{
		AccountId: usa.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}
	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	return &pb.CreateTopicsRes{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("topics %v has been created successfully", req.Topics),
	}, nil
}

func (us *UserSvc) BookMarkBlog(ctx context.Context, req *pb.BookMarkReq) (*pb.BookMarkRes, error) {
	user, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	err = us.dbConn.BookMarkABlog(req.BlogId, user.Id)
	if err != nil {
		us.log.Errorf("error while bookmarking the blog: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	userLog := &models.UserLogs{
		AccountId: user.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}
	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	return &pb.BookMarkRes{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("blog %v has been bookmarked successfully", req.BlogId),
	}, nil
}

func (us *UserSvc) RemoveBookMark(ctx context.Context, req *pb.BookMarkReq) (*pb.BookMarkRes, error) {
	user, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	err = us.dbConn.RemoveBookmarkFromBlog(req.BlogId, user.Id)
	if err != nil {
		us.log.Errorf("error while removing the bookmark from the blog: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	userLog := &models.UserLogs{
		AccountId: user.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}
	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	return &pb.BookMarkRes{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("blog %v has been removed from bookmark successfully", req.BlogId),
	}, nil
}

func (us *UserSvc) FollowUser(ctx context.Context, req *pb.UserFollowReq) (*pb.UserFollowRes, error) {
	us.log.Debugf("user %s has requested to follow %s.", req.FollowerUsername, req.Username)

	err := us.dbConn.FollowAUser(req.Username, req.FollowerUsername)
	if err != nil {
		us.log.Errorf("error while following the user: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	usa, _ := us.dbConn.CheckIfUsernameExist(req.FollowerUsername)

	userLog := &models.UserLogs{
		AccountId: usa.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}
	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	if req.FollowerUsername != req.Username {
		// Send a notification to the user
		bx, err := json.Marshal(models.TheMonkeysMessage{
			AccountId:    usa.AccountId,
			Username:     req.FollowerUsername,
			NewUsername:  req.Username,
			Action:       constants.USER_FOLLOWED,
			Notification: fmt.Sprintf("%s has followed you", req.FollowerUsername),
		})
		if err != nil {
			us.log.Errorf("failed to marshal message, error: %v", err)
		}

		go func() {
			err = us.qConn.PublishMessage(us.config.RabbitMQ.Exchange, us.config.RabbitMQ.RoutingKeys[4], bx)
			if err != nil {
				us.log.Errorf("failed to publish message for notification service for user: %s, error: %v", req.Username, err)
			}
		}()
	}

	return &pb.UserFollowRes{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("%s has been followed successfully", req.Username),
	}, nil
}

func (us *UserSvc) UnFollowUser(ctx context.Context, req *pb.UserFollowReq) (*pb.UserFollowRes, error) {
	us.log.Debugf("user %s has requested to un-follow %s.", req.FollowerUsername, req.Username)

	err := us.dbConn.UnFollowAUser(req.Username, req.FollowerUsername)
	if err != nil {
		us.log.Errorf("error while un-following the user: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	usa, _ := us.dbConn.CheckIfUsernameExist(req.FollowerUsername)

	userLog := &models.UserLogs{
		AccountId: usa.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}
	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	return &pb.UserFollowRes{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("%s has been un-followed successfully", req.Username),
	}, nil
}

func (us *UserSvc) GetFollowers(ctx context.Context, req *pb.UserDetailReq) (*pb.FollowerFollowingResp, error) {
	us.log.Debugf("fetching followers for user: %s", req.Username)
	resp, err := us.dbConn.GetFollowers(req.Username)
	if err != nil {
		us.log.Errorf("error while fetching followers for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "followers for user %s doesn't exist", req.Username)
		}

		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	var followers []*pb.User
	for _, r := range resp {
		followers = append(followers, &pb.User{
			Username:  r.Username,
			FirstName: r.FirstName,
			LastName:  r.LastName,
		})
	}

	return &pb.FollowerFollowingResp{
		Users: followers,
	}, nil
}

func (us *UserSvc) GetFollowing(ctx context.Context, req *pb.UserDetailReq) (*pb.FollowerFollowingResp, error) {
	us.log.Debugf("fetching following for user: %s", req.Username)
	resp, err := us.dbConn.GetFollowings(req.Username)
	if err != nil {
		us.log.Errorf("error while fetching following for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "following for user %s doesn't exist", req.Username)
		}

		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	var followings []*pb.User
	for _, r := range resp {
		followings = append(followings, &pb.User{
			Username:  r.Username,
			FirstName: r.FirstName,
			LastName:  r.LastName,
			AccountId: r.AccountId,
		})
	}

	return &pb.FollowerFollowingResp{
		Users: followings,
	}, nil
}

func (us *UserSvc) LikeBlog(ctx context.Context, req *pb.BookMarkReq) (*pb.BookMarkRes, error) {
	user, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	// Check if the user has already liked this blog
	alreadyLiked, err := us.dbConn.IsBlogLikedByUser(req.Username, req.BlogId)
	if err != nil {
		us.log.Errorf("error checking like status for user %s on blog %s: %v", req.Username, req.BlogId, err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}
	if alreadyLiked {
		return &pb.BookMarkRes{
			Status:  http.StatusOK,
			Message: "you have already liked this blog",
		}, nil
	}

	err = us.dbConn.LikeBlog(req.Username, req.BlogId)
	if err != nil {
		us.log.Errorf("error while liking the blog: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	userLog := &models.UserLogs{
		AccountId: user.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}
	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)
	// go cache.AddUserLog(us.dbConn, userLog, fmt.Sprintf(constants.LikeBlog, req.BlogId), constants.ServiceUser, constants.EventBlogLike, us.log)

	// Send a notification to the user
	blog, err := us.dbConn.GetBlogsByBlogId(req.BlogId)
	if err != nil {
		us.log.Errorf("error while getting the blog: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	fmt.Printf("blog)): %v\n", blog)

	if blog.AccountId != user.AccountId {
		bx, err := json.Marshal(models.TheMonkeysMessage{
			Username:     blog.Username,
			AccountId:    blog.AccountId,
			NewUsername:  user.Username,
			BlogId:       blog.BlogId,
			Action:       constants.BLOG_LIKE,
			Notification: fmt.Sprintf("%s liked your blog: %s", user.Username, blog.BlogId),
		})
		if err != nil {
			us.log.Errorf("failed to marshal message, error: %v", err)
		}

		go func() {
			err = us.qConn.PublishMessage(us.config.RabbitMQ.Exchange, us.config.RabbitMQ.RoutingKeys[4], bx)
			if err != nil {
				us.log.Errorf("failed to publish message for notification service for user: %s, error: %v", user.Username, err)
			}
			us.log.Debugf("message published successfully for user: %s", user.Username)
		}()
	}

	return &pb.BookMarkRes{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("blog %v has been liked successfully", req.BlogId),
	}, nil
}

func (us *UserSvc) UnlikeBlog(ctx context.Context, req *pb.BookMarkReq) (*pb.BookMarkRes, error) {
	user, err := us.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		us.log.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.Username)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	err = us.dbConn.UnlikeBlog(req.Username, req.BlogId)
	if err != nil {
		us.log.Errorf("error while unliking the blog: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	userLog := &models.UserLogs{
		AccountId: user.AccountId,
		IpAddress: req.Ip,
		Client:    req.Client,
	}
	userLog.IpAddress, userLog.Client = utils.IpClientConvert(req.Ip, req.Client)

	return &pb.BookMarkRes{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("blog %v has been unliked successfully", req.BlogId),
	}, nil
}

func (us *UserSvc) GetIfIFollowedUser(ctx context.Context, req *pb.UserFollowReq) (*pb.UserFollowRes, error) {
	us.log.Debugf("user %s has requested to follow %s.", req.FollowerUsername, req.Username)

	isFollowing, err := us.dbConn.IsUserFollowing(req.FollowerUsername, req.Username)
	if err != nil {
		us.log.Errorf("error while following the user: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	return &pb.UserFollowRes{
		Status:      http.StatusOK,
		IsFollowing: isFollowing,
	}, nil
}

func (us *UserSvc) GetIfBlogLiked(ctx context.Context, req *pb.BookMarkReq) (*pb.BookMarkRes, error) {
	us.log.Debugf("user %s has requested to like blog %s.", req.Username, req.BlogId)

	isLiked, err := us.dbConn.IsBlogLikedByUser(req.Username, req.BlogId)
	if err != nil {
		us.log.Errorf("error while liking the blog: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	return &pb.BookMarkRes{
		Status:  http.StatusOK,
		IsLiked: isLiked,
	}, nil
}

// GetIfBlogBookMarked checks if a blog is bookmarked by the user
func (s *UserSvc) GetIfBlogBookMarked(ctx context.Context, req *pb.BookMarkReq) (*pb.BookMarkRes, error) {
	s.log.Debugf("Checking if blog %s is bookmarked by user %s", req.BlogId, req.Username)

	isBookmark, err := s.dbConn.IsBlogBookmarkedByUser(req.Username, req.BlogId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to check if blog is bookmarked")
	}
	// Return the bookmarked status
	return &pb.BookMarkRes{
		Status:     http.StatusOK,
		Message:    "Blog bookmark status retrieved successfully",
		BookMarked: isBookmark,
	}, nil
}

// GetBookMarkCounts returns the total number of bookmarks for a blog
func (s *UserSvc) GetBookMarkCounts(ctx context.Context, req *pb.BookMarkReq) (*pb.CountResp, error) {
	s.log.Debugf("Getting bookmark count for blog %s", req.GetBlogId())
	count, err := s.dbConn.CountBlogBookmarks(req.BlogId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to get bookmark count")
	}

	return &pb.CountResp{
		Status: 200,
		Count:  int32(count),
	}, nil
}

// GetLikeCounts returns the total number of likes for a blog
func (s *UserSvc) GetLikeCounts(ctx context.Context, req *pb.BookMarkReq) (*pb.CountResp, error) {
	s.log.Debugf("Getting like count for blog %s", req.GetBlogId())
	count, err := s.dbConn.GetBlogLikeCount(req.BlogId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to get like count")
	}

	return &pb.CountResp{
		Status: 200,
		Count:  int32(count),
	}, nil
}

func (us *UserSvc) SearchUser(stream pb.UserService_SearchUserServer) error {
	us.log.Debug("SearchUser stream initiated")

	for {
		// Receive data from the stream
		req, err := stream.Recv()
		if err == io.EOF {
			us.log.Debug("SearchUser stream closed by client")
			return nil
		}
		if err != nil {
			us.log.Errorf("Error receiving data from stream: %v", err)
			return status.Errorf(codes.Internal, "Error reading stream: %v", err)
		}

		us.log.Debugf("Received search request for username: %s or account_id: %s", req.GetUsername(), req.GetAccountId())

		// Search v2 (Phase 1): index-backed, ranked, Active-only.
		// Bounded server-side context keeps a slow query from holding
		// the gRPC connection forever even if the caller forgets a
		// deadline.
		searchCtx, cancel := context.WithTimeout(stream.Context(), 500*time.Millisecond)
		users, err := us.dbConn.FindUsersV2(searchCtx, req.GetSearchTerm(), int(req.GetLimit()), int(req.GetOffset()))
		cancel()
		if err != nil {
			us.log.Errorf("Error searching user: %v", err)
			return status.Errorf(codes.Internal, "Failed to search users: %v", err)
		}

		// Send matching users back to the client
		for _, user := range users {
			resp := &pb.FollowerFollowingResp{
				Users: []*pb.User{
					{
						AccountId: user.AccountId,
						Username:  user.UserName,
						FirstName: user.FirstName,
						LastName:  user.LastName,
						Bio:       user.Bio.String,
						AvatarUrl: user.AvatarUrl.String,
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				us.log.Errorf("Error sending response to stream: %v", err)
				return status.Errorf(codes.Internal, "Error sending response: %v", err)
			}
		}
	}
}

func (us *UserSvc) GetFollowersFollowingCounts(ctx context.Context, req *pb.UserDetailReq) (*pb.FollowerFollowingCountsResp, error) {
	us.log.Debugf("fetching followers and following count for user: %s", req.Username)
	followers, following, err := us.dbConn.GetFollowersAndFollowingsCounts(req.Username)
	if err != nil {
		us.log.Errorf("error while fetching followers and following count for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "followers and following count for user %s doesn't exist", req.Username)
		}

		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	return &pb.FollowerFollowingCountsResp{
		Followers: int64(followers),
		Following: int64(following),
		Status:    http.StatusOK,
	}, nil
}

func (us *UserSvc) GetBookMarks(ctx context.Context, req *pb.BookMarkReq) (*pb.BookMarkRes, error) {
	us.log.Debugf("fetching bookmarks for user: %s", req.Username)

	resp, err := us.dbConn.GetBookmarkBlogsByUsername(req.Username)
	if err != nil {
		us.log.Errorf("error while fetching bookmarks for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "bookmarks for user %s doesn't exist", req.Username)
		}

		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	var blogIds []string
	for _, r := range resp {
		blogIds = append(blogIds, r.BlogId)
	}

	return &pb.BookMarkRes{
		Status:  http.StatusOK,
		BlogIds: blogIds,
	}, nil
}

func (us *UserSvc) GetBatchUserDetails(ctx context.Context, req *pb.GetBatchUserDetailsReq) (*pb.GetBatchUserDetailsRes, error) {
	if len(req.AccountIds) == 0 {
		return &pb.GetBatchUserDetailsRes{}, nil
	}

	users, err := us.dbConn.GetUsersByAccountIds(req.AccountIds)
	if err != nil {
		us.log.Errorf("failed to fetch batch user details: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to fetch user details")
	}

	var res []*pb.UserDetailsResp
	for _, u := range users {
		res = append(res, &pb.UserDetailsResp{
			AccountId: u.AccountId,
			Username:  u.Username,
			FirstName: u.FirstName,
			LastName:  u.LastName,
			Bio:       u.Bio.String,
			AvatarUrl: u.AvatarUrl.String,
			Location:  u.Address.String,
		})
	}

	return &pb.GetBatchUserDetailsRes{
		Users: res,
	}, nil
}
