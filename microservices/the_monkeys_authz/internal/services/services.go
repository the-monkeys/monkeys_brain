package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	activitypb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/service_types"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/db"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/models"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/utils"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type AuthzSvc struct {
	dbConn  db.AuthDBHandler
	otpRepo *db.OTPRepository
	jwt     utils.JwtWrapper
	config  *config.Config
	logger  *zap.SugaredLogger
	qConn   *rabbitmq.ConnManager
	pb.UnimplementedAuthServiceServer
}

func NewAuthzSvc(dbCli db.AuthDBHandler, otpRepo *db.OTPRepository, jwt utils.JwtWrapper, config *config.Config, qConn *rabbitmq.ConnManager, logger *zap.SugaredLogger) *AuthzSvc {
	return &AuthzSvc{
		dbConn:  dbCli,
		otpRepo: otpRepo,
		jwt:     jwt,
		config:  config,
		logger:  logger,
		qConn:   qConn,
	}
}

// Helper method to generate session ID
func (as *AuthzSvc) generateSessionID() string {
	return fmt.Sprintf("session_%d_%s", time.Now().UnixNano(), utils.GenerateGUID()[:8])
}

// ComprehensiveClientInfo represents all client information extracted from requests
type ComprehensiveClientInfo struct {
	// Basic client information
	IPAddress string
	Client    string
	SessionID string
	UserAgent string
	Referrer  string
	Platform  pb.Platform
	Origin    string

	// Enhanced Browser fingerprinting
	AcceptLanguage   string
	AcceptEncoding   string
	DNT              string
	Timezone         string
	ScreenResolution string
	ColorDepth       string
	DeviceMemory     string
	Languages        []string

	// Location & Geographic hints
	Country        string
	TimezoneOffset string

	// Marketing & UTM tracking
	UTMSource   string
	UTMMedium   string
	UTMCampaign string
	UTMContent  string
	UTMTerm     string

	// Behavioral indicators
	IsBot        bool
	TrustScore   float64
	RequestCount int32

	// Technical environment
	IsSecureContext   bool
	ConnectionType    string
	BrowserEngine     string
	JavaScriptEnabled bool
	Connection        string
	Os                string
	DeviceType        string
	Accept            string
	XForwardedHost    string
	XForwardedFor     string
	XForwardedProto   string
	XRealIp           string
	Browser           string
	VisitorID         string

	// Timestamps
	FirstSeen   string
	LastSeen    string
	CollectedAt string
}

// Helper method to extract comprehensive client info from any request type
func (as *AuthzSvc) extractClientInfo(req interface{}) *ComprehensiveClientInfo {
	var clientInfo *pb.ClientInfo
	// Extract ClientInfo from different request types
	switch r := req.(type) {
	case *pb.RegisterUserRequest:
		clientInfo = r.GetClientInfo()
	case *pb.LoginUserRequest:
		clientInfo = r.GetClientInfo()
	case *pb.ForgotPasswordReq:
		clientInfo = r.GetClientInfo()
	case *pb.ResetPasswordReq:
		clientInfo = r.GetClientInfo()
	case *pb.UpdatePasswordReq:
		clientInfo = r.GetClientInfo()
	case *pb.EmailVerificationReq:
		clientInfo = r.GetClientInfo()
	case *pb.VerifyEmailReq:
		clientInfo = r.GetClientInfo()
	case *pb.UpdateUsernameReq:
		clientInfo = r.GetClientInfo()
	case *pb.UpdatePasswordWithPasswordReq:
		clientInfo = r.GetClientInfo()
	case *pb.UpdateEmailIdReq:
		clientInfo = r.GetClientInfo()
	default:
		// Fallback for unknown request types
		return &ComprehensiveClientInfo{
			SessionID: as.generateSessionID(),
			Platform:  pb.Platform_PLATFORM_UNSPECIFIED,
		}
	}

	// Handle nil ClientInfo
	if clientInfo == nil {
		return &ComprehensiveClientInfo{
			SessionID: as.generateSessionID(),
			Platform:  pb.Platform_PLATFORM_UNSPECIFIED,
		}
	}

	// Generate session ID if not provided
	sessionID := clientInfo.GetSessionId()
	if sessionID == "" {
		sessionID = as.generateSessionID()
	}

	// Convert to comprehensive structure
	return &ComprehensiveClientInfo{
		// Basic client information
		IPAddress:       clientInfo.GetIpAddress(),
		Client:          clientInfo.GetClient(),
		SessionID:       sessionID,
		UserAgent:       clientInfo.GetUserAgent(),
		Referrer:        clientInfo.GetReferrer(),
		Platform:        clientInfo.GetPlatform(),
		Browser:         clientInfo.GetBrowser(),
		DeviceType:      clientInfo.GetDeviceType(),
		Connection:      clientInfo.GetConnection(),
		Origin:          clientInfo.GetOrigin(),
		XRealIp:         clientInfo.GetRealIp(),
		XForwardedFor:   clientInfo.GetForwardedFor(),
		XForwardedProto: clientInfo.GetForwardedProto(),
		XForwardedHost:  clientInfo.GetForwardedHost(),
		Accept:          clientInfo.GetAccept(),
		Os:              clientInfo.GetOs(),
		VisitorID:       clientInfo.GetVisitorId(),

		// Enhanced Browser fingerprinting
		AcceptLanguage:   clientInfo.GetAcceptLanguage(),
		AcceptEncoding:   clientInfo.GetAcceptEncoding(),
		DNT:              clientInfo.GetDnt(),
		Timezone:         clientInfo.GetTimezone(),
		ScreenResolution: clientInfo.GetScreenResolution(),
		ColorDepth:       clientInfo.GetColorDepth(),
		DeviceMemory:     clientInfo.GetDeviceMemory(),
		Languages:        clientInfo.GetLanguages(),

		// Location & Geographic hints
		Country:        clientInfo.GetCountry(),
		TimezoneOffset: clientInfo.GetTimezoneOffset(),

		// Marketing & UTM tracking
		UTMSource:   clientInfo.GetUtmSource(),
		UTMMedium:   clientInfo.GetUtmMedium(),
		UTMCampaign: clientInfo.GetUtmCampaign(),
		UTMContent:  clientInfo.GetUtmContent(),
		UTMTerm:     clientInfo.GetUtmTerm(),

		// Behavioral indicators
		IsBot:        clientInfo.GetIsBot(),
		TrustScore:   clientInfo.GetTrustScore(),
		RequestCount: clientInfo.GetRequestCount(),

		// Technical environment
		IsSecureContext:   clientInfo.GetIsSecureContext(),
		ConnectionType:    clientInfo.GetConnectionType(),
		BrowserEngine:     clientInfo.GetBrowserEngine(),
		JavaScriptEnabled: clientInfo.GetJavascriptEnabled(),

		// Timestamps
		FirstSeen:   clientInfo.GetFirstSeen(),
		LastSeen:    clientInfo.GetLastSeen(),
		CollectedAt: clientInfo.GetCollectedAt(),
	}
}

// Helper method to detect platform from user agent or request platform
func (as *AuthzSvc) detectPlatform(userAgent string, reqPlatform pb.Platform) activitypb.Platform {
	// If platform is provided in request, convert it
	switch reqPlatform {
	case pb.Platform_PLATFORM_WEB:
		return activitypb.Platform_PLATFORM_WEB
	case pb.Platform_PLATFORM_MOBILE:
		return activitypb.Platform_PLATFORM_MOBILE
	case pb.Platform_PLATFORM_TABLET:
		return activitypb.Platform_PLATFORM_TABLET
	case pb.Platform_PLATFORM_API:
		return activitypb.Platform_PLATFORM_API
	case pb.Platform_PLATFORM_DESKTOP:
		return activitypb.Platform_PLATFORM_DESKTOP
	default:
		// Detect from user agent if platform not specified
		if userAgent != "" {
			userAgent = strings.ToLower(userAgent)
			if strings.Contains(userAgent, "mobile") || strings.Contains(userAgent, "android") || strings.Contains(userAgent, "iphone") {
				return activitypb.Platform_PLATFORM_MOBILE
			}
			if strings.Contains(userAgent, "tablet") || strings.Contains(userAgent, "ipad") {
				return activitypb.Platform_PLATFORM_TABLET
			}
		}
		return activitypb.Platform_PLATFORM_WEB
	}
}

// Helper method to convert device type string to activitypb.DeviceType enum
func (as *AuthzSvc) convertDeviceType(deviceType string) activitypb.DeviceType {
	// Convert string device type to enum
	switch strings.ToLower(strings.TrimSpace(deviceType)) {
	case "desktop":
		return activitypb.DeviceType_DEVICE_TYPE_DESKTOP
	case "mobile":
		return activitypb.DeviceType_DEVICE_TYPE_MOBILE
	case "tablet":
		return activitypb.DeviceType_DEVICE_TYPE_TABLET
	case "bot":
		return activitypb.DeviceType_DEVICE_TYPE_BOT
	case "unknown":
		return activitypb.DeviceType_DEVICE_TYPE_UNKNOWN
	default:
		return activitypb.DeviceType_DEVICE_TYPE_UNSPECIFIED
	}
}

// Helper method to send activity tracking message to RabbitMQ
func (as *AuthzSvc) sendActivityTrackingMessage(activityReq *activitypb.TrackActivityRequest) {
	go func() {
		// Create activity tracking message
		activityMsg, err := json.Marshal(activityReq)
		if err != nil {
			as.logger.Errorf("failed to marshal activity tracking message: %v", err)
			return
		}

		// Send to activity tracking queue via RabbitMQ
		err = as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, "activity.track", activityMsg)
		if err != nil {
			as.logger.Errorf("failed to publish activity tracking message: %v", err)
			return
		}

		as.logger.Debugf("activity tracking message sent for user %s, action %s", activityReq.UserId, activityReq.Action)
	}()
}

// Helper method to track auth activities with comprehensive client information
func (as *AuthzSvc) trackAuthActivity(user *models.TheMonkeysUser, action string, req interface{}) {
	// Extract comprehensive client information
	clientInfo := as.extractClientInfo(req)

	color_depth, err := strconv.ParseInt(clientInfo.ColorDepth, 10, 16)
	if err != nil {
		color_depth = -1
	}

	resolution := strings.Split(clientInfo.ScreenResolution, "x")
	var screen_width, screen_height int64
	if len(resolution) == 2 {
		screen_width, _ = strconv.ParseInt(resolution[0], 10, 16)
		screen_height, _ = strconv.ParseInt(resolution[1], 10, 16)
	}
	timezone_offset, _ := strconv.ParseInt(clientInfo.TimezoneOffset, 10, 16)

	// Create comprehensive ClientInfo for activity tracking
	activityClientInfo := &activitypb.ClientInfo{
		IpAddress:         clientInfo.IPAddress,
		UserAgent:         clientInfo.UserAgent,
		AcceptLanguage:    clientInfo.AcceptLanguage,
		AcceptEncoding:    clientInfo.AcceptEncoding,
		Dnt:               clientInfo.DNT,
		Referer:           clientInfo.Referrer,
		Platform:          as.detectPlatform(clientInfo.UserAgent, clientInfo.Platform),
		Country:           clientInfo.Country,
		IsBot:             clientInfo.IsBot,
		TrustScore:        clientInfo.TrustScore,
		BrowserEngine:     clientInfo.BrowserEngine,
		UtmSource:         clientInfo.UTMSource,
		UtmMedium:         clientInfo.UTMMedium,
		UtmCampaign:       clientInfo.UTMCampaign,
		UtmTerm:           clientInfo.UTMTerm,
		UtmContent:        clientInfo.UTMContent,
		Timezone:          clientInfo.Timezone,
		Languages:         clientInfo.Languages,
		ColorDepth:        int32(color_depth),
		ScreenWidth:       int32(screen_width),
		ScreenHeight:      int32(screen_height),
		CfIpcountry:       clientInfo.Country,
		TimezoneOffset:    int32(timezone_offset),
		JavascriptEnabled: clientInfo.JavaScriptEnabled,
		RequestCount:      clientInfo.RequestCount,
		DeviceType:        as.convertDeviceType(clientInfo.DeviceType),
		Browser:           clientInfo.Browser,
		Os:                clientInfo.Os,
		Accept:            clientInfo.Accept,
		VisitorId:         clientInfo.VisitorID,
		// XClientId:  clientInfo.Client, // TODO: Extract if available
		XSessionId: clientInfo.SessionID,
		// Additional fields that can be populated from comprehensive client info
		Connection:      clientInfo.ConnectionType,
		Origin:          clientInfo.Origin, // Use Referrer as Origin since it's available in ClientInfo
		XForwardedFor:   clientInfo.XForwardedFor,
		XForwardedHost:  clientInfo.XForwardedHost,
		XForwardedProto: clientInfo.XForwardedProto,
		XRealIp:         clientInfo.XRealIp,
	}

	// Create enhanced activity tracking request with comprehensive client data
	activityReq := &activitypb.TrackActivityRequest{
		UserId:     user.AccountId,
		AccountId:  user.AccountId,
		SessionId:  clientInfo.SessionID,
		Category:   activitypb.ActivityCategory_CATEGORY_AUTHENTICATION,
		Action:     action,
		Resource:   "user",
		ResourceId: user.AccountId,
		ClientInfo: activityClientInfo,
		Success:    true,
		DurationMs: 0, // TODO: Add timing if needed
	}

	// Log comprehensive client tracking information for debugging
	as.logger.Debugf("Tracking %s activity for user %s - IP: %s, Platform: %s, UserAgent: %s, Country: %s, UTM Source: %s, Trust Score: %f, Browser: %s",
		action, user.AccountId, clientInfo.IPAddress, clientInfo.Platform,
		clientInfo.UserAgent, clientInfo.Country, clientInfo.UTMSource,
		clientInfo.TrustScore, clientInfo.BrowserEngine)

	// Send activity tracking message
	as.sendActivityTrackingMessage(activityReq)

	// TODO: Send additional comprehensive tracking data to enhanced activity service
	// This could include UTM parameters, browser fingerprinting, behavioral indicators, etc.
	// For now, we're logging the comprehensive data for debugging purposes
	as.logger.Debugf("Comprehensive client data - UTM: [%s/%s/%s], Browser: [%s, %s], Behavioral: [Bot: %t, Trust: %f], Technical: [Secure: %t, Connection: %s]",
		clientInfo.UTMSource, clientInfo.UTMMedium, clientInfo.UTMCampaign,
		clientInfo.BrowserEngine, clientInfo.AcceptLanguage,
		clientInfo.IsBot, clientInfo.TrustScore,
		clientInfo.IsSecureContext, clientInfo.ConnectionType)
}

func (as *AuthzSvc) RegisterUser(ctx context.Context, req *pb.RegisterUserRequest) (*pb.RegisterUserResponse, error) {
	as.logger.Debugf("got the request data for : %+v", req.Email)

	// Extract comprehensive client information
	clientInfo := req.ClientInfo

	// Cleanup request data
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	user := &models.TheMonkeysUser{}

	if err := utils.ValidateRegisterUserRequest(req); err != nil {
		as.logger.Errorf("incomplete request body provided for email %s, error: %+v", req.Email, err)
		return nil, status.Errorf(codes.InvalidArgument, "Incomplete request body provided for email %s", req.Email)
	}

	// Check if the user exists with the same email id return conflict
	_, err := as.dbConn.CheckIfEmailExist(req.Email)
	if err == nil {
		if err == sql.ErrNoRows {
			as.logger.Debugf("creating a new user with email: %v", req.Email)
		} else {
			return nil, status.Errorf(codes.AlreadyExists, "The user with email %s already exists", req.Email)
		}
	}

	hash := string(utils.GenHash())
	encHash := utils.HashPassword(hash)

	// Create a userId and username
	user.AccountId = utils.GenerateGUID()
	user.Username = utils.GenerateGUID()
	user.FirstName = req.FirstName
	user.LastName = req.GetLastName()
	user.Email = req.GetEmail()
	user.Password = utils.HashPassword(req.Password)
	user.UserStatus = "active"
	user.EmailVerificationToken = encHash
	user.EmailVerificationTimeout = sql.NullTime{
		Time:  time.Now().Add(time.Hour * 1),
		Valid: true,
	}
	if req.LoginMethod.String() == pb.RegisterUserRequest_LoginMethod_name[0] {
		user.LoginMethod = "the-monkeys"
	}

	// Set client information from comprehensive ClientInfo
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IpAddress, clientInfo.Client)

	as.logger.Debugf("registering the user with email %v", req.Email)
	userId, err := as.dbConn.RegisterUser(user)
	if err != nil {
		as.logger.Errorf("cannot register the user %s, error: %v", user.Email, err)
		return nil, status.Errorf(codes.Internal, "cannot register the user, something went wrong")
	}

	// Send email verification mail as a routine else the register api gets slower
	emailBody := utils.EmailVerificationHTML(user.FirstName, user.LastName, user.Username, hash)
	go func() {
		err := as.SendMail(user.Email, emailBody)
		if err != nil {
			log.Printf("Failed to send mail post registration: %v", err)
		}
		as.logger.Debug("Email Sent!")
	}()

	// Track authentication activity
	as.trackAuthActivity(user, "register", req)

	as.logger.Debugf("user %s is successfully registered.", user.Email)

	// Generate and return token
	token, refreshToken, err := as.jwt.GenerateToken(user)
	if err != nil {
		as.logger.Errorf("failed to generate token for user %s: %v", user.Email, err)
		return nil, status.Errorf(codes.Aborted, "The user with email %s is successfully registered, try to log in", user.Email)
	}

	bx, err := json.Marshal(models.TheMonkeysMessage{
		Username:     user.Username,
		AccountId:    user.AccountId,
		FirstName:    user.FirstName,
		LastName:     user.LastName,
		Email:        user.Email,
		Action:       constants.USER_REGISTER,
		Notification: constants.NotificationRegister,
	})
	if err != nil {
		as.logger.Errorf("failed to marshal message, error: %v", err)
	}

	go func() {
		err = as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[0], bx)
		if err != nil {
			as.logger.Errorf("failed to publish message for user: %s, error: %v", user.Username, err)
		}

		err = as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], bx)
		if err != nil {
			as.logger.Errorf("failed to publish message for notification service for user: %s, error: %v", user.Username, err)
		}
	}()

	return &pb.RegisterUserResponse{
		StatusCode:              http.StatusCreated,
		Token:                   token,
		RefreshToken:            refreshToken,
		EmailVerified:           false,
		Username:                user.Username,
		Email:                   user.Email,
		UserId:                  userId,
		FirstName:               user.FirstName,
		LastName:                user.LastName,
		AccountId:               user.AccountId,
		EmailVerificationStatus: user.EmailVerificationStatus,
	}, nil
}

// Validate user runs to check to validate the user. It checks
// If the token is correct
// If the token is expired
// Is the token belongs to the user
// Is the user existing in the db or an active user
func (as *AuthzSvc) Validate(ctx context.Context, req *pb.ValidateRequest) (*pb.ValidateResponse, error) {
	claims, err := as.jwt.ValidateToken(req.Token)
	if err != nil {
		as.logger.Errorf("cannot validate the auth token, error: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "couldn't validate auth token")
	}

	// Check if the email exists
	user, err := as.dbConn.CheckIfEmailExist(claims.Email)
	if err != nil {
		as.logger.Errorf("cannot validate token as the email %s doesn't exist, error: %+v", claims.Email, err)
		return nil, status.Errorf(codes.NotFound, "email does not exist")
	}

	if claims.TokenType != "" && claims.TokenType != "access" && claims.TokenType != "password_reset" {
		as.logger.Errorf("invalid token type: expected access or password_reset, got %s", claims.TokenType)
		return nil, status.Errorf(codes.Unauthenticated, "invalid token type")
	}

	as.logger.Debugf("User with email %s successfully verified!", claims.Email)
	return &pb.ValidateResponse{
		StatusCode: http.StatusOK,
		UserId:     user.Id,
		Email:      claims.Email,
		UserName:   user.Username,
		AccountId:  claims.AccountId,
	}, nil
}

func (as *AuthzSvc) DecodeSignedJWT(ctx context.Context, req *pb.DecodeSignedJWTRequest) (*pb.DecodeSignedJWTResponse, error) {
	claims, err := as.jwt.ValidateToken(req.Token)
	if err != nil {
		as.logger.Errorf("cannot validate the auth token, error: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "couldn't validate auth token")
	}

	// Check if the email exists
	user, err := as.dbConn.CheckIfEmailExist(claims.Email)
	if err != nil {
		as.logger.Errorf("cannot validate token as the email %s doesn't exist, error: %+v", claims.Email, err)
		return nil, status.Errorf(codes.NotFound, "email does not exist")
	}

	if claims.TokenType != "" && claims.TokenType != "access" {
		as.logger.Errorf("invalid token type: expected access, got %s", claims.TokenType)
		return nil, status.Errorf(codes.Unauthenticated, "invalid token type")
	}

	return &pb.DecodeSignedJWTResponse{
		StatusCode:              http.StatusOK,
		Email:                   user.Email,
		FirstName:               user.FirstName,
		LastName:                user.LastName,
		Username:                user.Username,
		EmailVerificationStatus: user.EmailVerificationStatus,
		AccountId:               user.AccountId,
	}, nil
}

func (as *AuthzSvc) CheckAccessLevel(ctx context.Context, req *pb.AccessCheckReq) (*pb.AccessCheckRes, error) {
	as.logger.Debugf("checking access of user %s for blog %s", req.AccountId, req.BlogId)
	if req.AccountId == "" || req.BlogId == "" {
		return nil, status.Errorf(codes.Unauthenticated, "unauthorized to perform this action")
	}

	resp, role, err := as.dbConn.GetUserAccessForABlog(req.AccountId, req.BlogId)
	if err == sql.ErrNoRows {
		as.logger.Errorf("blog with id %s not found", req.BlogId)
		return &pb.AccessCheckRes{
			Access:     []string{constants.PermissionCreate},
			StatusCode: http.StatusOK,
		}, nil
	} else if err != nil {
		as.logger.Errorf("error in access level: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "unauthorized to perform this action")
	}

	as.logger.Debugf("user %s has the following permissions: %v", req.AccountId, resp)
	// Return the access level
	return &pb.AccessCheckRes{
		Access:     resp,
		StatusCode: http.StatusOK,
		Role:       role,
	}, nil
}

func (as *AuthzSvc) Login(ctx context.Context, req *pb.LoginUserRequest) (*pb.LoginUserResponse, error) {
	as.logger.Debugf("user has requested to login with email: %s", req.Email)

	// Extract comprehensive client information
	clientInfo := as.extractClientInfo(req)

	// Check if the user is existing the db or not
	user, err := as.dbConn.CheckIfEmailExist(req.Email)
	if err != nil {
		as.logger.Errorf("cannot find user with email %s, error: %v", req.Email, err)
		return nil, status.Errorf(codes.NotFound, "email does not exist")
	}

	// Check if the password match with the password hash
	if !utils.CheckPasswordHash(req.Password, user.Password) {
		as.logger.Errorf("password incorrect for email %s, error: %v", req.Email, err)
		return nil, status.Errorf(codes.Unauthenticated, "email/password incorrect")
	}

	token, refreshToken, err := as.jwt.GenerateToken(user)
	if err != nil {
		as.logger.Error(service_types.CannotCreateToken(req.Email, err))
		return nil, status.Errorf(codes.Internal, "cannot generate the token: %v", err)
	}

	// Set client information from comprehensive ClientInfo
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	// Track authentication activity
	as.trackAuthActivity(user, "login", req)

	// Publish login notification
	notifMsg, err := json.Marshal(models.TheMonkeysMessage{
		Username:    user.Username,
		AccountId:   user.AccountId,
		Action:      constants.LOGIN_DETECTED,
		IpAddress:   clientInfo.IPAddress,
		Client:      clientInfo.Client,
		LoginMethod: constants.AuthTheMonkeys,
	})
	if err != nil {
		as.logger.Errorf("failed to marshal login notification: %v", err)
	} else {
		go func() {
			if err := as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], notifMsg); err != nil {
				as.logger.Errorf("failed to publish login notification: %v", err)
			}
		}()
	}

	resp := &pb.LoginUserResponse{
		StatusCode:              http.StatusOK,
		Token:                   token,
		RefreshToken:            refreshToken,
		EmailVerificationStatus: user.EmailVerificationStatus,
		Username:                user.Username,
		Email:                   user.Email,
		UserId:                  user.Id,
		FirstName:               user.FirstName,
		LastName:                user.LastName,
		AccountId:               user.AccountId,
	}
	return resp, nil
}

func (as *AuthzSvc) RefreshToken(ctx context.Context, req *pb.RefreshTokenReq) (*pb.RefreshTokenRes, error) {
	claims, err := as.jwt.ValidateToken(req.RefreshToken)
	if err != nil {
		as.logger.Errorf("cannot validate the refresh token, error: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "couldn't validate refresh token")
	}

	if claims.TokenType != "refresh" {
		as.logger.Errorf("invalid token type for refresh: expected refresh, got %s", claims.TokenType)
		return nil, status.Errorf(codes.Unauthenticated, "invalid token type")
	}

	user, err := as.dbConn.CheckIfAccountIdExist(claims.AccountId)
	if err != nil {
		as.logger.Errorf("cannot refresh token as the account %s doesn't exist, error: %+v", claims.AccountId, err)
		return nil, status.Errorf(codes.NotFound, "user does not exist")
	}

	token, refreshToken, err := as.jwt.GenerateToken(user)
	if err != nil {
		as.logger.Errorf("failed to generate new tokens for user %s: %v", user.Email, err)
		return nil, status.Errorf(codes.Internal, "cannot generate tokens")
	}

	return &pb.RefreshTokenRes{
		StatusCode:   http.StatusOK,
		Token:        token,
		RefreshToken: refreshToken,
	}, nil
}

func (as *AuthzSvc) ForgotPassword(ctx context.Context, req *pb.ForgotPasswordReq) (*pb.ForgotPasswordRes, error) {
	as.logger.Debugf("User %s has forgotten their password", req.Email)

	// Extract comprehensive client information
	clientInfo := as.extractClientInfo(req)

	// Check if the user exists in the database
	user, err := as.dbConn.CheckIfEmailExist(req.Email)
	if err != nil {
		as.logger.Errorf("Error checking if username exists in the database: %v", err)
		if err == sql.ErrNoRows {
			// Don't reveal whether user exists - always return success message
			return &pb.ForgotPasswordRes{
				StatusCode: 200,
				Message:    "If the account is registered with this email, you will receive a verification code.",
			}, nil
		}
		return nil, status.Errorf(codes.Internal, "Something went wrong while getting user")
	}

	// Generate 6-digit OTP (crypto/rand, secure)
	otpCode, err := utils.GenerateOTP(6)
	if err != nil {
		as.logger.Errorf("failed to generate reset OTP for %s: %v", req.Email, err)
		return nil, status.Errorf(codes.Internal, "Something went wrong")
	}

	// Store OTP hash in Redis with 10-min TTL
	resetData := &db.ResetOTPData{
		Email:    req.Email,
		OTPHash:  utils.HashPassword(otpCode),
		Attempts: 0,
		CreateAt: time.Now(),
	}
	if err := as.otpRepo.StoreResetOTP(ctx, resetData); err != nil {
		as.logger.Errorf("failed to store reset OTP for %s: %v", req.Email, err)
		return nil, status.Errorf(codes.Internal, "Something went wrong")
	}

	// Send OTP email
	emailBody := utils.PasswordResetOTPEmailHTML(user.FirstName, user.LastName, otpCode)
	go func() {
		if err := as.SendMail(req.Email, emailBody); err != nil {
			as.logger.Errorf("Failed to send OTP mail for password recovery: %v", err)
		}
	}()

	// Set client information from comprehensive ClientInfo
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	// Track authentication activity
	as.trackAuthActivity(user, "forgot_password", req)

	// Publish password reset requested notification
	notifMsg, err := json.Marshal(models.TheMonkeysMessage{
		Username:  user.Username,
		AccountId: user.AccountId,
		Action:    constants.PASSWORD_RESET_REQUESTED,
		IpAddress: user.IpAddress,
		Client:    user.Client,
	})
	if err != nil {
		as.logger.Errorf("failed to marshal password reset requested notification: %v", err)
	} else {
		go func() {
			if err := as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], notifMsg); err != nil {
				as.logger.Errorf("failed to publish password reset requested notification: %v", err)
			}
		}()
	}

	return &pb.ForgotPasswordRes{
		StatusCode: 200,
		Message:    "If the account is registered with this email, you will receive a verification code.",
	}, nil
}

func (as *AuthzSvc) ResetPassword(ctx context.Context, req *pb.ResetPasswordReq) (*pb.ResetPasswordRes, error) {
	as.logger.Debugf("user %s has requested to reset their password", req.Username)

	// Extract comprehensive client information
	clientInfo := as.extractClientInfo(req)

	user, err := as.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		as.logger.Errorf("Error checking if username exists in the database: %v", err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "username not found")
		}
		return nil, status.Errorf(codes.Internal, "Something went wrong while getting user")
	}

	// timeTill, err := time.Parse(time.RFC3339, user.PasswordVerificationTimeout.Time.String())
	timeTill, err := time.Parse(time.RFC3339, user.PasswordVerificationTimeout.Time.Format(time.RFC3339))
	if err != nil {
		as.logger.Errorf("timeout couldn't be verified: %v", err)
		return nil, status.Errorf(codes.Internal, "timeout couldn't be verified")
	}

	if timeTill.Before(time.Now()) {
		as.logger.Errorf("the token has already expired, error: %+v", err)
		return nil, status.Errorf(codes.Unauthenticated, "token expired already")
	}

	// Verify reset token
	if ok := utils.CheckPasswordHash(req.Token, user.PasswordVerificationToken.String); !ok {
		as.logger.Errorf("the token didn't match, error: %+v", err)
		return nil, status.Errorf(codes.Unauthenticated, "token didn't match")
	}

	as.logger.Debugf("Assigning a token to the user: %s having email: %s to reset their password", user.Username, user.Email)
	// Generate and return token
	token, _, err := as.jwt.GenerateToken(user)
	if err != nil {
		as.logger.Error(service_types.CannotCreateToken(req.Email, err))
		return nil, status.Errorf(codes.Internal, "could not create token")
	}

	// Set client information from comprehensive ClientInfo
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	// Track authentication activity
	as.trackAuthActivity(user, "reset_password", req)

	// Publish password changed notification (reset via token)
	notifMsg, err := json.Marshal(models.TheMonkeysMessage{
		Username:  user.Username,
		AccountId: user.AccountId,
		Action:    constants.PASSWORD_CHANGED,
		IpAddress: user.IpAddress,
		Client:    user.Client,
	})
	if err != nil {
		as.logger.Errorf("failed to marshal password reset notification: %v", err)
	} else {
		go func() {
			if err := as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], notifMsg); err != nil {
				as.logger.Errorf("failed to publish password reset notification: %v", err)
			}
		}()
	}

	return &pb.ResetPasswordRes{
		StatusCode: http.StatusOK,
		Token:      token,
		// EmailVerified: false,
		UserName:  user.Username,
		Email:     user.Email,
		UserId:    user.Id,
		FirstName: user.FirstName,
		LastName:  user.LastName,
	}, nil
}

func (as *AuthzSvc) UpdatePassword(ctx context.Context, req *pb.UpdatePasswordReq) (*pb.UpdatePasswordRes, error) {
	as.logger.Debugf("updating password for: %+v", req)

	// Extract comprehensive client information
	clientInfo := as.extractClientInfo(req)

	// Check if the username exists in the database
	user, err := as.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		as.logger.Errorf("Error checking if username exists in the database: %v", err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "User doesn't exist")
		}
		return nil, status.Errorf(codes.Internal, "Something went wrong while verifying user")
	}

	encHash := utils.HashPassword(req.Password)

	if err := as.dbConn.UpdatePassword(encHash, &models.TheMonkeysUser{
		Id:       user.Id,
		Email:    req.Email,
		Username: req.Username,
	}); err != nil {
		as.logger.Errorf("could not update password for user %v, err: %v", req.Username, err)
		return nil, status.Errorf(codes.Internal, "could not update the password")
	}

	as.logger.Debugf("updated password for: %+v", req.Email)

	// Set client information from comprehensive ClientInfo
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	// Track authentication activity
	as.trackAuthActivity(user, "update_password", req)

	// Publish notification for password change
	notifMsg, err := json.Marshal(models.TheMonkeysMessage{
		Username:     user.Username,
		AccountId:    user.AccountId,
		Action:       constants.PASSWORD_CHANGED,
		Notification: "Your password was changed",
	})
	if err != nil {
		as.logger.Errorf("failed to marshal password changed notification: %v", err)
	} else {
		go func() {
			if err := as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], notifMsg); err != nil {
				as.logger.Errorf("failed to publish password changed notification: %v", err)
			}
		}()
	}

	return &pb.UpdatePasswordRes{
		StatusCode: http.StatusOK,
	}, nil
}

func (as *AuthzSvc) RequestForEmailVerification(ctx context.Context, req *pb.EmailVerificationReq) (*pb.EmailVerificationRes, error) {
	if req.Email == "" {
		return nil, constants.ErrBadRequest
	}
	as.logger.Debugf("user %v has requested for email verification", req.Email)

	// Extract comprehensive client information
	clientInfo := as.extractClientInfo(req)

	user, err := as.dbConn.CheckIfEmailExist(req.Email)
	if err != nil {
		as.logger.Errorf("user %v doesn't exist, error: %v", req.Email, err)
		return nil, status.Errorf(codes.NotFound, "User doesn't exist")
	}

	as.logger.Debugf("generating verification email token for: %s", req.GetEmail())
	hash := string(utils.GenHash())
	encHash := utils.HashPassword(hash)

	user.EmailVerificationToken = encHash
	user.EmailVerificationTimeout = sql.NullTime{
		Time:  time.Now().Add(time.Minute * 5),
		Valid: true, // Valid is true if Time is not NULL
	}

	if err := as.dbConn.UpdateEmailVerificationToken(user); err != nil {
		as.logger.Errorf("error occurred while updating email verification token: %v", err)
		return nil, status.Errorf(codes.Internal, "error occurred while updating email verification token")
	}

	emailBody := utils.EmailVerificationHTML(user.FirstName, user.LastName, user.Username, hash)
	as.logger.Debugf("Sending verification email to: %s", req.GetEmail())

	// TODO: Handle error of the go routine
	go func() {
		err := as.SendMail(user.Email, emailBody)
		if err != nil {
			// Handle error
			log.Printf("Failed to send mail for password recovery: %v", err)
		}
		as.logger.Debug("Email Sent!")
	}()

	// Set client information from comprehensive ClientInfo
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	// Track authentication activity
	as.trackAuthActivity(user, "request_email_verification", req)

	return &pb.EmailVerificationRes{
		StatusCode: http.StatusOK,
	}, nil
}

func (as *AuthzSvc) VerifyEmail(ctx context.Context, req *pb.VerifyEmailReq) (*pb.VerifyEmailRes, error) {
	// Check if the username exists in the database
	user, err := as.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		as.logger.Errorf("Error checking if username exists in the database: %v", err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "User doesn't exist")
		}
		return nil, status.Errorf(codes.Internal, "Something went wrong while verifying user")
	}

	// Parse the email verification timeout from the user
	timeTill, err := time.Parse(time.RFC3339, user.EmailVerificationTimeout.Time.Format(time.RFC3339))
	if err != nil {
		as.logger.Errorf("Failed to parse email verification timeout: %v", err)
		return nil, status.Errorf(codes.Unauthenticated, "Failed to parse email verification timeout: %v", err)
	}

	// Check if the email verification timeout has expired
	if timeTill.Before(time.Now()) {
		as.logger.Errorf("Email verification token expired already for %s, error: %v", req.Email, err)
		return nil, status.Errorf(codes.Unauthenticated, "Email verification token expired already or incorrect token")
	}

	// Verify reset token
	if ok := utils.CheckPasswordHash(req.Token, user.EmailVerificationToken); !ok {
		as.logger.Errorf("The token didn't match, error: %+v", err)
		return nil, status.Errorf(codes.Unauthenticated, "Email verification token expired already or incorrect token")
	}

	// Update email verification status
	err = as.dbConn.UpdateEmailVerificationStatus(user)
	if err != nil {
		as.logger.Errorf("Cannot update the verification details for %s, error: %v", req.Email, err)
		return nil, status.Errorf(codes.Internal, "Couldn't update email verification token")
	}

	as.logger.Debugf("Verified email: %s", user.Email)

	// Extract comprehensive client information and set client data
	clientInfo := as.extractClientInfo(req)
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	// Track authentication activity
	as.trackAuthActivity(user, "verify_email", req)

	// Publish notification for email verification
	notifMsg, err := json.Marshal(models.TheMonkeysMessage{
		Username:     user.Username,
		AccountId:    user.AccountId,
		Email:        user.Email,
		Action:       constants.EMAIL_VERIFIED,
		Notification: "Your email has been verified",
	})
	if err != nil {
		as.logger.Errorf("failed to marshal email verified notification: %v", err)
	} else {
		go func() {
			if err := as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], notifMsg); err != nil {
				as.logger.Errorf("failed to publish email verified notification: %v", err)
			}
		}()
	}

	user.Email = req.Email
	token, _, err := as.jwt.GenerateToken(user)
	if err != nil {
		as.logger.Errorf("Unable to generate new token for existing user %s, error %v", user.Email, err)
	}

	// Return a success response with status code 200
	return &pb.VerifyEmailRes{
		StatusCode: 200,
		Token:      token,
	}, nil
}

func (as *AuthzSvc) UpdateUsername(ctx context.Context, req *pb.UpdateUsernameReq) (*pb.UpdateUsernameRes, error) {
	if req.CurrentUsername == req.NewUsername {
		return nil, status.Errorf(codes.InvalidArgument, "current username and new username cannot be the same")
	}

	if req.CurrentUsername == "" || req.NewUsername == "" {
		return nil, status.Errorf(codes.InvalidArgument, "current username or new username cannot be empty")
	}

	// Allow only letters, numbers, underscore and period in usernames.
	if !utils.IsValidUsername(req.NewUsername) {
		return nil, status.Errorf(codes.InvalidArgument, "Usernames can only contain letters, numbers, underscores, and periods.")
	}

	if utils.IsRestrictedUsername(req.NewUsername) {
		return nil, status.Errorf(codes.InvalidArgument, "the username %s is not allowed, please choose a different username", req.NewUsername)
	}

	// Check if the user exists
	user, err := as.dbConn.CheckIfUsernameExist(req.CurrentUsername)
	if err != nil {
		as.logger.Errorf("error while checking if the username exists for user %s, err: %v", req.CurrentUsername, err)
		if err == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "user %s doesn't exist", req.CurrentUsername)
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	// Ensure the new username isn't already taken by another user.
	// Without this pre-check the UPDATE below hits the unique constraint and
	// surfaces as a generic Internal error (HTTP 500) instead of a clear conflict.
	if _, err := as.dbConn.CheckIfUsernameExist(req.NewUsername); err == nil {
		return nil, status.Errorf(codes.AlreadyExists, "the username %s is already taken", req.NewUsername)
	} else if err != sql.ErrNoRows {
		as.logger.Errorf("error while checking availability of new username %s, err: %v", req.NewUsername, err)
		return nil, status.Errorf(codes.Internal, "cannot verify username availability")
	}

	// Update the username
	err = as.dbConn.UpdateUserName(req.CurrentUsername, req.NewUsername)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "could not update the username")
	}

	bx, err := json.Marshal(models.TheMonkeysMessage{
		Username:    user.Username,
		NewUsername: req.NewUsername,
		AccountId:   user.AccountId,
		Action:      constants.USERNAME_UPDATE,
	})
	if err != nil {
		as.logger.Errorf("error while marshalling the message queue data, err: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	// Marshal notification message BEFORE the goroutine to avoid data race
	// (user.Username gets overwritten to req.NewUsername after the goroutine is spawned)
	notifBx, err := json.Marshal(models.TheMonkeysMessage{
		Username:    user.Username,
		NewUsername: req.NewUsername,
		AccountId:   user.AccountId,
		Email:       user.Email,
		FirstName:   user.FirstName,
		LastName:    user.LastName,
		Action:      constants.USERNAME_CHANGED,
	})
	if err != nil {
		as.logger.Errorf("failed to marshal username changed notification: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	go func() {
		err = as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[0], bx)
		if err != nil {
			as.logger.Errorf("failed to publish message for user: %s for updating profile, error: %v", user.Username, err)
		}

		if err := as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], notifBx); err != nil {
			as.logger.Errorf("failed to publish username change notification for user: %s, error: %v", user.Username, err)
		}
	}()

	// Extract comprehensive client information and set client data
	clientInfo := as.extractClientInfo(req)
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	// Track authentication activity
	as.trackAuthActivity(user, "update_username", req)

	user.Username = req.NewUsername
	token, _, err := as.jwt.GenerateToken(user)
	if err != nil {
		as.logger.Error(service_types.CannotCreateToken(req.NewUsername, err))
		as.logger.Errorf("error while marshalling the message queue data, err: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	return &pb.UpdateUsernameRes{
		StatusCode:    http.StatusOK,
		Token:         token,
		EmailVerified: false,
		UserName:      req.NewUsername,
		Email:         user.Email,
		UserId:        user.Id,
		FirstName:     user.FirstName,
		LastName:      user.LastName,
		AccountId:     user.AccountId,
	}, nil
}

func (as *AuthzSvc) UpdatePasswordWithPassword(ctx context.Context, req *pb.UpdatePasswordWithPasswordReq) (*pb.UpdatePasswordWithPasswordRes, error) {
	as.logger.Debugf("updating password of user: %s", req.Username)

	// Check if the user exists
	user, err := as.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		as.logger.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("user %s doesn't exist", req.Username))
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	// Check if the password match with the password hash
	if !utils.CheckPasswordHash(req.CurrentPassword, user.Password) {
		return nil, status.Errorf(codes.Unauthenticated, "password didn't match, cannot update password")
	}

	// Hash the new password
	hash := utils.HashPassword(req.NewPassword)

	// update the password
	err = as.dbConn.UpdatePassword(hash, user)
	if err != nil {
		as.logger.Errorf("error while updating the password for user %s, err: %v", req.Username, err)
		return nil, status.Errorf(codes.Internal, "cannot update the password")
	}

	// Extract comprehensive client information and set client data
	clientInfo := as.extractClientInfo(req)
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	// Track authentication activity
	as.trackAuthActivity(user, "update_password_with_password", req)

	// Publish password changed notification
	notifMsg, err := json.Marshal(models.TheMonkeysMessage{
		Username:  user.Username,
		AccountId: user.AccountId,
		Action:    constants.PASSWORD_CHANGED,
	})
	if err != nil {
		as.logger.Errorf("failed to marshal password changed notification: %v", err)
	} else {
		go func() {
			if err := as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], notifMsg); err != nil {
				as.logger.Errorf("failed to publish password changed notification: %v", err)
			}
		}()
	}

	// Return
	return &pb.UpdatePasswordWithPasswordRes{
		StatusCode: http.StatusOK,
	}, nil
}

func (as *AuthzSvc) UpdateEmailId(ctx context.Context, req *pb.UpdateEmailIdReq) (*pb.UpdateEmailIdRes, error) {
	as.logger.Debugf("updating email of user: %s", req.Username)

	user, err := as.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		as.logger.Errorf("error while checking if the username exists for user %s, err: %v", req.Username, err)
		if err == sql.ErrNoRows {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("user %s doesn't exist", req.Username))
		}
		return nil, status.Errorf(codes.Internal, "cannot get the user profile")
	}

	// check if the email is already in use
	_, err = as.dbConn.CheckIfEmailExist(req.NewEmail)
	if err == nil {
		if err == sql.ErrNoRows {
			as.logger.Debugf("updating a new email: %v", req.NewEmail)
		} else {
			return nil, status.Errorf(codes.AlreadyExists, "The user with email %s already in use", req.NewEmail)
		}
	}

	hash := string(utils.GenHash())
	encHash := utils.HashPassword(hash)

	user.EmailVerificationToken = encHash
	user.EmailVerificationTimeout = sql.NullTime{
		Time:  time.Now().Add(time.Hour * 1),
		Valid: true,
	}

	// else update the email address
	err = as.dbConn.UpdateEmailId(req.NewEmail, user)
	if err != nil {
		as.logger.Errorf("error while updating the email for user %s, err: %v", req.Username, err)
		return nil, status.Errorf(codes.Internal, "cannot update the email")
	}

	// Send email verification mail as a routine else the register api gets slower
	emailBody := utils.EmailVerificationHTML(user.FirstName, user.LastName, user.Username, hash)
	go func() {
		err := as.SendMail(user.Email, emailBody)
		if err != nil {
			log.Printf("Failed to send mail post registration: %v", err)
		}
		as.logger.Debug("Email Sent!")
	}()

	// Extract comprehensive client information and set client data
	clientInfo := as.extractClientInfo(req)
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	// Track authentication activity
	as.trackAuthActivity(user, "update_email", req)

	// Publish email changed notification
	emailNotifMsg, err := json.Marshal(models.TheMonkeysMessage{
		Username:  user.Username,
		AccountId: user.AccountId,
		Email:     req.NewEmail,
		Action:    constants.EMAIL_CHANGED,
	})
	if err != nil {
		as.logger.Errorf("failed to marshal email changed notification: %v", err)
	} else {
		go func() {
			if err := as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], emailNotifMsg); err != nil {
				as.logger.Errorf("failed to publish email changed notification: %v", err)
			}
		}()
	}

	user.Email = req.NewEmail
	token, _, err := as.jwt.GenerateToken(user)
	if err != nil {
		as.logger.Error(service_types.CannotCreateToken(user.Username, err))
		as.logger.Errorf("error while marshalling the message queue data, err: %v", err)
		return nil, status.Errorf(codes.Internal, "something went wrong")
	}

	// TODO: Add token to the db with status valid

	return &pb.UpdateEmailIdRes{
		StatusCode:    http.StatusOK,
		Token:         token,
		EmailVerified: false,
		UserName:      user.Username,
		Email:         req.NewEmail,
		UserId:        user.Id,
		FirstName:     user.FirstName,
		LastName:      user.LastName,
		AccountId:     user.AccountId,
	}, nil
}

func (as *AuthzSvc) GoogleLogin(ctx context.Context, req *pb.RegisterUserRequest) (*pb.RegisterUserResponse, error) {
	as.logger.Debugf("google login: req from : %+v", req.Email)
	user := &models.TheMonkeysUser{}

	// Check if the user exists with the same email id return conflict
	existingUser, err := as.dbConn.CheckIfEmailExist(req.Email)
	if err == nil {
		if err == sql.ErrNoRows {
			as.logger.Debugf("google login: creating a new user with email: %v", req.Email)
		} else {
			as.logger.Debugf("google login: user with email %s already exists", req.Email)
			// Generate and return token
			token, refreshToken, err := as.jwt.GenerateToken(existingUser)
			if err != nil {
				as.logger.Errorf("google login: failed to generate token for user %s: %v", user.Email, err)
				return nil, status.Errorf(codes.Aborted, "user cannot login using google at this time")
			}

			// Extract client info for existing Google user login notification
			gClientInfo := as.extractClientInfo(req)

			// Publish login notification for existing Google user
			gLoginMsg, marshalErr := json.Marshal(models.TheMonkeysMessage{
				Username:    existingUser.Username,
				AccountId:   existingUser.AccountId,
				Action:      constants.LOGIN_DETECTED,
				IpAddress:   gClientInfo.IPAddress,
				Client:      gClientInfo.Client,
				LoginMethod: constants.AuthGoogleOauth2,
			})
			if marshalErr != nil {
				as.logger.Errorf("failed to marshal google login notification: %v", marshalErr)
			} else {
				go func() {
					if pubErr := as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], gLoginMsg); pubErr != nil {
						as.logger.Errorf("failed to publish google login notification: %v", pubErr)
					}
				}()
			}

			return &pb.RegisterUserResponse{
				StatusCode:              http.StatusOK,
				Token:                   token,
				RefreshToken:            refreshToken,
				EmailVerified:           false,
				Username:                existingUser.Username,
				Email:                   existingUser.Email,
				FirstName:               existingUser.FirstName,
				LastName:                existingUser.LastName,
				AccountId:               existingUser.AccountId,
				EmailVerificationStatus: existingUser.EmailVerificationStatus,
			}, nil
		}
	}

	// Since it's a google login, password will be empty hence add a random password
	req.Password = utils.RandomString(16)

	hash := string(utils.GenHash())
	encHash := utils.HashPassword(hash)

	// Create a userId and username
	user.AccountId = utils.GenerateGUID()
	user.Username = utils.GenerateGUID()
	user.FirstName = req.FirstName
	user.LastName = req.GetLastName()
	user.Email = req.GetEmail()
	user.Password = utils.HashPassword(req.Password)
	user.UserStatus = "active"
	user.EmailVerificationToken = encHash
	user.EmailVerificationTimeout = sql.NullTime{
		Time:  time.Now().Add(time.Hour * 1),
		Valid: true,
	}
	user.LoginMethod = constants.AuthGoogleOauth2

	// Extract comprehensive client information and set client data
	clientInfo := as.extractClientInfo(req)
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)

	as.logger.Debugf("registering the user with email %v", req.Email)
	userId, err := as.dbConn.RegisterUser(user)
	if err != nil {
		as.logger.Errorf("cannot register the user %s, error: %v", user.Email, err)
		return nil, status.Errorf(codes.Internal, "cannot register the user, something went wrong")
	}

	// Send email verification mail as a routine else the register api gets slower
	emailBody := utils.EmailVerificationHTML(user.FirstName, user.LastName, user.Username, hash)
	go func() {
		err := as.SendMail(user.Email, emailBody)
		if err != nil {
			as.logger.Errorf("Failed to send mail post registration: %v", err)
		}
		as.logger.Debugf("Email Sent!")
	}()

	// Track authentication activity
	as.trackAuthActivity(user, "google_login", req)

	as.logger.Debugf("user %s is successfully registered.", user.Email)

	// Generate and return token
	token, refreshToken, err := as.jwt.GenerateToken(user)
	if err != nil {
		as.logger.Errorf("failed to generate token for user %s: %v", user.Email, err)
		return nil, status.Errorf(codes.Aborted, "The user with email %s is successfully registered, try to log in", user.Email)
	}

	bx, err := json.Marshal(models.TheMonkeysMessage{
		Username:     user.Username,
		AccountId:    user.AccountId,
		FirstName:    user.FirstName,
		LastName:     user.LastName,
		Email:        user.Email,
		Action:       constants.USER_REGISTER,
		Notification: constants.NotificationRegister,
	})
	if err != nil {
		as.logger.Errorf("failed to marshal message, error: %v", err)
	}

	go func() {
		err = as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[0], bx)
		if err != nil {
			as.logger.Errorf("failed to publish message for user: %s, error: %v", user.Username, err)
		}

		err = as.qConn.PublishMessage(as.config.RabbitMQ.Exchange, as.config.RabbitMQ.RoutingKeys[4], bx)
		if err != nil {
			as.logger.Errorf("failed to publish message for notification service for user: %s, error: %v", user.Username, err)
		}
	}()

	return &pb.RegisterUserResponse{
		StatusCode:              http.StatusCreated,
		Token:                   token,
		RefreshToken:            refreshToken,
		EmailVerified:           false,
		Username:                user.Username,
		Email:                   user.Email,
		UserId:                  userId,
		FirstName:               user.FirstName,
		LastName:                user.LastName,
		AccountId:               user.AccountId,
		EmailVerificationStatus: user.EmailVerificationStatus,
	}, nil
}
