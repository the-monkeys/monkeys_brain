package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/utils"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type ServiceClient struct {
	Client            pb.AuthServiceClient
	Log               *zap.SugaredLogger
	googleOauthConfig *oauth2.Config
}

// createClientInfo creates a comprehensive ClientInfo protobuf message from gin context
func (asc *ServiceClient) createClientInfo(ctx *gin.Context) *pb.ClientInfo {
	// Get comprehensive client information using enhanced utility function
	clientInfo := utils.GetClientInfo(ctx)

	// Get platform enum for protobuf
	platform := utils.GetAuthPlatform(ctx)

	ci := &pb.ClientInfo{
		// Basic client information
		IpAddress: clientInfo.IPAddress,
		Client:    clientInfo.ClientType,
		SessionId: clientInfo.SessionID,
		VisitorId: clientInfo.VisitorID,
		UserAgent: clientInfo.UserAgent,
		Referrer:  clientInfo.Referrer,
		Platform:  platform,

		// Enhanced Browser fingerprinting
		AcceptLanguage:   clientInfo.AcceptLanguage,
		AcceptEncoding:   clientInfo.AcceptEncoding,
		Dnt:              clientInfo.DNT,
		Timezone:         clientInfo.Timezone,
		ScreenResolution: clientInfo.ScreenResolution,
		ColorDepth:       clientInfo.ColorDepth,
		DeviceMemory:     clientInfo.DeviceMemory,
		Languages:        clientInfo.Languages,
		Origin:           clientInfo.Origin,
		DeviceType:       clientInfo.DeviceType,
		Browser:          clientInfo.Browser,
		Accept:           clientInfo.Accept,
		Os:               clientInfo.Os,
		ForwardedFor:     clientInfo.ForwardedFor,
		ForwardedHost:    clientInfo.ForwardedHost,
		ForwardedProto:   clientInfo.ForwardedProto,

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
		FirstSeen:   clientInfo.FirstSeen,
		LastSeen:    clientInfo.LastSeen,
		CollectedAt: clientInfo.CollectedAt,
	}
	return ci
}

// InitServiceClient initializes the gRPC connection to the auth service.
func InitServiceClient(cfg *config.Config, log *zap.SugaredLogger) pb.AuthServiceClient {
	authService := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysAuthz, cfg.Microservices.AuthzPort)
	cc, err := grpc.NewClient(authService, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Errorf("cannot dial to grpc auth server: %v", err)
		return nil
	}

	log.Infof("✅ the monkeys gateway is dialing to the auth rpc server at: %v", authService)
	return pb.NewAuthServiceClient(cc)
}

func RegisterAuthRouter(router *gin.Engine, cfg *config.Config, log *zap.SugaredLogger) *ServiceClient {

	asc := &ServiceClient{
		Client: InitServiceClient(cfg, log),
		Log:    log,
		googleOauthConfig: &oauth2.Config{
			RedirectURL:  cfg.GoogleOAuth2.RedirectURL,
			ClientID:     cfg.GoogleOAuth2.ClientID,     // Replace with your Google Client ID
			ClientSecret: cfg.GoogleOAuth2.ClientSecret, // Replace with your Google Client Secret
			Scopes:       cfg.GoogleOAuth2.Scope,
			Endpoint:     google.Endpoint,
		},
	}
	// fmt.Printf("asc.googleOauthConfig: %+v\n", asc.googleOauthConfig)

	routes := router.Group("/api/v1/auth")

	// SSO Google Login
	routes.GET("/google/login", asc.HandleGoogleLogin)
	routes.GET("/google/callback", asc.HandleGoogleCallback)

	routes.POST("/register", asc.Register)
	routes.POST("/login", asc.Login)

	// OTP-based registration flow
	routes.POST("/register/initiate", asc.InitiateRegistration)
	routes.POST("/register/verify-otp", asc.VerifyRegistrationOTP)
	routes.POST("/register/resend-otp", asc.ResendRegistrationOTP)

	// OTP-based password reset verification
	routes.POST("/verify-reset-otp", asc.VerifyResetOTP)

	routes.GET("/is-authenticated", asc.IsUserAuthenticated)
	routes.GET("/logout", asc.Logout)

	routes.POST("/forgot-pass", asc.ForgotPassword)
	routes.GET("/reset-password", asc.PasswordResetEmailVerification)
	routes.POST("/update-password", asc.UpdatePassword)
	routes.GET("/verify-email", asc.VerifyEmail)
	routes.POST("/refresh", asc.RefreshToken)

	routes.GET("/validate-session", asc.ValidateSession)

	// Authentication Point
	mware := InitAuthMiddleware(asc, log)
	routes.Use(mware.AuthRequired)

	routes.POST("/req-email-verification", asc.ReqEmailVerification)
	routes.PUT("/settings/username/:username", asc.UpdateUserName)
	routes.PUT("/settings/password/:username", asc.ChangePasswordWithCurrentPassword)

	// OTP-based email change flow (authenticated) — replaces the old PUT /settings/email/:username
	routes.POST("/settings/email/initiate/:username", asc.InitiateEmailChange)
	routes.POST("/settings/email/verify-otp/:username", asc.VerifyEmailChangeOTP)
	routes.POST("/settings/email/resend-otp/:username", asc.ResendEmailChangeOTP)

	// User verification checkmark (authenticated)
	routes.POST("/verification/request", asc.RequestUserVerification)
	routes.GET("/verification/status/:username", asc.GetUserVerificationStatus)
	routes.POST("/verification/review", asc.ReviewUserVerification) // Admin-only: review verification requests

	// Roles for blog
	routes.GET("/roles", asc.GetRoles)
	routes.GET("/role/:id", asc.GetRoles)
	routes.GET("/roles/:user_id", asc.GetRoles)
	routes.POST("/roles/:blog_id", asc.GetRoles)

	return asc
}

func (asc *ServiceClient) Register(ctx *gin.Context) {
	body := RegisterRequestBody{}

	if err := ctx.BindJSON(&body); err != nil {
		asc.Log.Errorf("json body is not correct, error: %v", err)
		_ = ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}

	// Check for google login
	var loginMethod pb.RegisterUserRequest_LoginMethod
	switch body.LoginMethod {
	case "google-oauth2":
		loginMethod = pb.RegisterUserRequest_GOOGLE_ACC
	case "the-monkeys":
		loginMethod = pb.RegisterUserRequest_The_MONKEYS
	}

	// Get comprehensive client information using helper function
	clientInfo := asc.createClientInfo(ctx)

	// Todo: Remove this after testing
	fmt.Printf("clientInfo: %v\n", clientInfo)

	// Log registration attempt with enhanced tracking
	asc.Log.Debug("Registration attempt from IP: %s, User-Agent: %s, Platform: %s, SessionID: %s",
		clientInfo.IpAddress, clientInfo.UserAgent, clientInfo.Platform, clientInfo.SessionId)

	res, err := asc.Client.RegisterUser(context.Background(), &pb.RegisterUserRequest{

		FirstName:   body.FirstName,
		LastName:    body.LastName,
		Email:       body.Email,
		Password:    body.Password,
		LoginMethod: loginMethod,
		ClientInfo:  clientInfo,
	})

	if err != nil {
		// Check for gRPC error code
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.InvalidArgument:
				ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "incomplete request, please provide first name, last name, email and password"})
				return
			case codes.AlreadyExists:
				ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"message": "user with this email already exists"})
				return
			case codes.Aborted:
				ctx.AbortWithStatusJSON(http.StatusPartialContent, gin.H{"message": "Registration done but token is not created, try logging in"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot register the user, something went wrong"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	utils.SetMonkeysAuthCookie(ctx, res.Token)
	utils.SetMonkeysRefreshCookie(ctx, res.RefreshToken)

	registerRespJson, _ := json.Marshal(&res)

	// Convert to map to safely delete private fields
	var registerRespMap map[string]interface{}
	_ = json.Unmarshal(registerRespJson, &registerRespMap)

	delete(registerRespMap, "token")
	delete(registerRespMap, "refresh_token")

	ctx.JSON(int(res.StatusCode), &registerRespMap)
}

// InitiateRegistration handles POST /register/initiate — step 1 of OTP registration.
func (asc *ServiceClient) InitiateRegistration(ctx *gin.Context) {
	var body InitiateRegistrationBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := asc.Client.InitiateRegistration(context.Background(), &pb.InitiateRegistrationReq{
		FirstName:  body.FirstName,
		LastName:   body.LastName,
		Email:      body.Email,
		Password:   body.Password,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	ctx.JSON(int(res.StatusCode), gin.H{
		"message":    res.Message,
		"expires_in": res.ExpiresIn,
	})
}

// VerifyRegistrationOTP handles POST /register/verify-otp — step 2, creates the account.
func (asc *ServiceClient) VerifyRegistrationOTP(ctx *gin.Context) {
	var body VerifyRegistrationOTPBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := asc.Client.VerifyRegistrationOTP(context.Background(), &pb.VerifyRegistrationOTPReq{
		Email:      body.Email,
		OtpCode:    body.OTPCode,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	utils.SetMonkeysAuthCookie(ctx, res.Token)
	utils.SetMonkeysRefreshCookie(ctx, res.RefreshToken)

	respJson, _ := json.Marshal(res)
	var respMap map[string]interface{}
	_ = json.Unmarshal(respJson, &respMap)
	delete(respMap, "token")
	delete(respMap, "refresh_token")

	ctx.JSON(int(res.StatusCode), respMap)
}

// ResendRegistrationOTP handles POST /register/resend-otp.
func (asc *ServiceClient) ResendRegistrationOTP(ctx *gin.Context) {
	var body ResendOTPBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := asc.Client.ResendRegistrationOTP(context.Background(), &pb.ResendRegistrationOTPReq{
		Email:      body.Email,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	ctx.JSON(int(res.StatusCode), gin.H{
		"message":    res.Message,
		"expires_in": res.ExpiresIn,
	})
}

// VerifyResetOTP handles POST /verify-reset-otp — verifies password reset OTP, returns reset token.
func (asc *ServiceClient) VerifyResetOTP(ctx *gin.Context) {
	var body VerifyResetOTPBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := asc.Client.VerifyResetOTP(context.Background(), &pb.VerifyResetOTPReq{
		Email:      body.Email,
		OtpCode:    body.OTPCode,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	ctx.JSON(int(res.StatusCode), gin.H{
		"token": res.Token,
	})
}

// handleGRPCError translates gRPC status codes to HTTP responses.
func (asc *ServiceClient) handleGRPCError(ctx *gin.Context, err error) {
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.InvalidArgument:
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": st.Message()})
		case codes.AlreadyExists:
			ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"message": st.Message()})
		case codes.NotFound:
			ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": st.Message()})
		case codes.DeadlineExceeded:
			ctx.AbortWithStatusJSON(http.StatusGone, gin.H{"message": st.Message()})
		case codes.ResourceExhausted:
			ctx.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"message": st.Message()})
		case codes.Aborted:
			ctx.AbortWithStatusJSON(http.StatusPartialContent, gin.H{"message": st.Message()})
		default:
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
		}
		return
	}
	ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
}

func (asc *ServiceClient) Login(ctx *gin.Context) {
	body := LoginRequestBody{}

	asc.Log.Infof("traffic is coming from ip: %v", ctx.ClientIP())

	if err := ctx.BindJSON(&body); err != nil {
		asc.Log.Errorf("json body is not correct, error: %v", err)
		_ = ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}

	body.Email = strings.TrimSpace(body.Email)

	res, err := asc.Client.Login(context.Background(), &pb.LoginUserRequest{
		Email:      body.Email,
		Password:   body.Password,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		// Check for gRPC error code
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "email/password is incorrect"})
				return
			case codes.Unauthenticated:
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "email/password is incorrect"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "cannot generate the token"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "unknown error"})
				return
			}
		}
	}

	utils.SetMonkeysAuthCookie(ctx, res.Token)
	utils.SetMonkeysRefreshCookie(ctx, res.RefreshToken)

	loginRespJson, _ := json.Marshal(&res)

	// Convert to map to safely delete private fields
	var loginRespMap map[string]interface{}
	_ = json.Unmarshal(loginRespJson, &loginRespMap)

	delete(loginRespMap, "token")
	delete(loginRespMap, "refresh_token")

	ctx.JSON(http.StatusOK, &loginRespMap)
}

func (asc *ServiceClient) RefreshToken(ctx *gin.Context) {
	refreshCookie, err := ctx.Request.Cookie("mrt")
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}

	res, err := asc.Client.RefreshToken(context.Background(), &pb.RefreshTokenReq{
		RefreshToken: refreshCookie.Value,
	})

	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "cannot refresh token"})
		return
	}

	utils.SetMonkeysAuthCookie(ctx, res.Token)
	utils.SetMonkeysRefreshCookie(ctx, res.RefreshToken)

	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (asc *ServiceClient) ForgotPassword(ctx *gin.Context) {
	body := GetEmail{}

	if err := ctx.BindJSON(&body); err != nil {
		asc.Log.Errorf("json body is not correct, error: %v", err)
		_ = ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}

	res, err := asc.Client.ForgotPassword(context.Background(), &pb.ForgotPasswordReq{
		Email:      body.Email,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		// Check for gRPC error code
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusOK, gin.H{"message": "If the account is registered with this email, you’ll receive an email verification link to reset your password."})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "sent password reset link to the email", "status": res.StatusCode})
}

// TODO: Rename it to Password Reset Email Verification
func (asc *ServiceClient) PasswordResetEmailVerification(ctx *gin.Context) {
	userAny := ctx.Query("user")
	secretAny := ctx.Query("evpw")

	res, err := asc.Client.ResetPassword(context.Background(), &pb.ResetPasswordReq{
		Username:   userAny,
		Token:      secretAny,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		// Check for gRPC error code
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "User not found"})
				return
			case codes.Unauthenticated:
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "Token expired/incorrect"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "Internal server error"})
				return
			}
		}
	}

	utils.SetMonkeysAuthCookie(ctx, res.Token)
	// ResetPassword might not return a refresh token yet, but we'll set it if it does
	// In my AuthZ implementation, I updated ResetPassword to return it.
	// However, pb.ResetPasswordRes doesn't have it yet. Let's check the proto.
	// Wait, I updated pb.RegisterUserResponse and pb.LoginUserResponse, NOT ResetPasswordRes.
	// I should probably update ResetPasswordRes too if we want a fresh session after reset.

	respJson, _ := json.Marshal(&res)

	// Convert to map to safely delete private fields
	var respMap map[string]interface{}
	_ = json.Unmarshal(respJson, &respMap)

	delete(respMap, "token")
	// Standardize username field for UI if needed (pb uses UserName, but UI might expect username)
	if val, ok := respMap["user_name"]; ok {
		respMap["username"] = val
	}

	ctx.JSON(http.StatusOK, &respMap)
}

func (asc *ServiceClient) UpdatePassword(ctx *gin.Context) {
	authorization := ctx.Request.Header.Get("Authorization")
	if authorization == "" {
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	token := strings.Split(authorization, "Bearer ")

	if len(token) < 2 {
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	res, err := asc.Client.Validate(context.Background(), &pb.ValidateRequest{
		Token: token[1],
	})

	if err != nil || res.StatusCode != http.StatusOK {
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	pass := UpdatePassword{}
	if err := ctx.BindJSON(&pass); err != nil {
		asc.Log.Errorf("json body is not correct, error: %v", err)
		_ = ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}

	resp, err := asc.Client.UpdatePassword(context.Background(), &pb.UpdatePasswordReq{
		Password:   pass.NewPassword,
		Username:   res.UserName,
		Email:      res.Email,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "User not found"})
				return
			case codes.Unauthenticated:
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "Token expired/incorrect"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "Internal server error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, resp)
}

func (asc *ServiceClient) ReqEmailVerification(ctx *gin.Context) {
	var vrEmail VerifyEmail

	if err := ctx.BindJSON(&vrEmail); err != nil {
		asc.Log.Errorf("json body is not correct, error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, IncorrectReqBody{Error: "Invalid json body"})
		return
	}

	res, err := asc.Client.RequestForEmailVerification(context.Background(), &pb.EmailVerificationReq{
		Email:      vrEmail.Email,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "user not found"})
				return
			case codes.Internal:
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "error occurred while updating email verification token"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
				return
			}
		}
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "An email verification link has been sent to your registered email",
		"status":  res.StatusCode,
	})
}

// To verify email
func (asc *ServiceClient) VerifyEmail(ctx *gin.Context) {
	username := ctx.Query("user")
	evSecret := ctx.Query("evpw")

	// Verify Headers
	res, err := asc.Client.VerifyEmail(context.Background(), &pb.VerifyEmailReq{
		Username:   username,
		Token:      evSecret,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		// Check for gRPC error code
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "User not found"})
				return
			case codes.Unauthenticated:
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "Token expired/incorrect"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "Internal server error"})
				return
			}
		}
	}

	// If user is logged in then update the session token
	authCookie, err := ctx.Request.Cookie("mat")
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "Token expired/incorrect"})
	}
	if authCookie != nil {
		if _, err := asc.Client.Validate(context.Background(), &pb.ValidateRequest{Token: authCookie.Value}); err != nil {
			utils.SetMonkeysAuthCookie(ctx, res.Token)
		}
	}

	// Return success response
	ctx.JSON(http.StatusOK, gin.H{"message": "email verified", "status": res.StatusCode})
}

func (asc *ServiceClient) IsUserAuthenticated(ctx *gin.Context) {
	authorization := ctx.Request.Header.Get("Authorization")

	if authorization == "" {
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	token := strings.Split(authorization, "Bearer ")

	if len(token) < 2 {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "unauthorized"})
		return
	}
	user := ctx.Request.Header.Get("Username")
	if user == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "unauthorized"})
		return
	}
	res, err := asc.Client.Validate(context.Background(), &pb.ValidateRequest{
		Token: token[1],
	})
	if err != nil || res.StatusCode != http.StatusOK {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "unauthorized"})
		return
	}

	if res.UserName != user {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "unauthorized"})
		return
	}

	ctx.JSON(http.StatusOK, struct {
		Authorized bool `json:"authorized"`
	}{Authorized: true})
}

func (asc *ServiceClient) GetRoles(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, "administrator")
}

func (asc *ServiceClient) UpdateUserName(ctx *gin.Context) {
	currentUsername := ctx.Param("username")

	if currentUsername != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "you aren't authorized to perform this action"})
		return
	}

	var newUsername UpdateUsername

	if err := ctx.BindJSON(&newUsername); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if newUsername.Username == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "username cannot be empty"})
		return
	}

	resp, err := asc.Client.UpdateUsername(context.Background(), &pb.UpdateUsernameReq{
		CurrentUsername: currentUsername,
		NewUsername:     newUsername.Username,
		ClientInfo:      asc.createClientInfo(ctx),
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "user not found"})
			return
		} else if status.Code(err) == codes.AlreadyExists {
			ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"message": "Username is already taken. Please try another."})
			return
		} else if status.Code(err) == codes.InvalidArgument {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": status.Convert(err).Message()})
			return
		} else {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "couldn't update username"})
			return
		}
	}

	utils.SetMonkeysAuthCookie(ctx, resp.Token)

	response, _ := json.Marshal(&resp)

	// Convert to map to safely delete private fields
	var responseMap map[string]interface{}
	_ = json.Unmarshal(response, &responseMap)

	delete(responseMap, "token")

	ctx.JSON(http.StatusOK, responseMap)
}

func (asc *ServiceClient) ChangePasswordWithCurrentPassword(ctx *gin.Context) {
	username := ctx.Param("username")

	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "you aren't authorized to perform this action"})
		return
	}

	var updatePass UpdatePassword

	if err := ctx.BindJSON(&updatePass); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	resp, err := asc.Client.UpdatePasswordWithPassword(context.Background(), &pb.UpdatePasswordWithPasswordReq{
		Username:        username,
		CurrentPassword: updatePass.CurrentPassword,
		NewPassword:     updatePass.NewPassword,
		ClientInfo:      asc.createClientInfo(ctx),
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "user not found"})
			return
		} else if status.Code(err) == codes.Unauthenticated {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "password incorrect"})
			return
		} else {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "couldn't update password"})
			return
		}
	}

	ctx.SetSameSite(http.SameSiteNoneMode)
	ctx.SetCookie("mat", "", -1, "/", "", true, true)
	ctx.JSON(http.StatusOK, gin.H{"message": "successfully updated password", "status": resp.StatusCode})
}

func (asc *ServiceClient) UpdateEmailAddress(ctx *gin.Context) {
	username := ctx.Param("username")

	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "you aren't authorized to perform this action"})
		return
	}

	var emailBody GetEmail

	if err := ctx.BindJSON(&emailBody); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	resp, err := asc.Client.UpdateEmailId(context.Background(), &pb.UpdateEmailIdReq{
		Username:   username,
		NewEmail:   emailBody.Email,
		ClientInfo: asc.createClientInfo(ctx),
	})

	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "user not found"})
			return
		} else if status.Code(err) == codes.AlreadyExists {
			ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"message": "the email is already in use"})
			return
		} else {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "couldn't update email"})
			return
		}
	}

	utils.SetMonkeysAuthCookie(ctx, resp.Token)

	response, _ := json.Marshal(&resp)

	// Convert to map to safely delete private fields
	var responseMap map[string]interface{}
	_ = json.Unmarshal(response, &responseMap)

	delete(responseMap, "token")
	ctx.JSON(http.StatusOK, responseMap)
}

func (asc *ServiceClient) HandleGoogleLogin(c *gin.Context) {
	// Redirect to Google's OAuth 2.0 server
	url := asc.googleOauthConfig.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("url: %v\n", url)
	c.Redirect(http.StatusTemporaryRedirect, url)
}

func (asc *ServiceClient) HandleGoogleCallback(c *gin.Context) {
	// Exchange the code for a token
	code := c.Query("code")
	token, err := asc.googleOauthConfig.Exchange(context.Background(), code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Code exchange failed"})
		return
	}

	// Retrieve user information
	client := asc.googleOauthConfig.Client(context.Background(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user info"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			asc.Log.Errorf("Failed to close response body: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to close response body"})
		}
	}()

	// Process user info (you could store it in a database or create a session)
	var userInfo GoogleUser
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse user info"})
		return
	}

	loginResp, err := asc.Client.GoogleLogin(context.Background(), &pb.RegisterUserRequest{
		Email:       userInfo.Email,
		LoginMethod: pb.RegisterUserRequest_GOOGLE_ACC,
		FirstName:   userInfo.GivenName,
		LastName:    userInfo.FamilyName,
		ClientInfo:  asc.createClientInfo(c),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to login using google"})
		return
	}

	utils.SetMonkeysAuthCookie(c, loginResp.Token)
	utils.SetMonkeysRefreshCookie(c, loginResp.RefreshToken)

	loginRespJson, _ := json.Marshal(&loginResp)

	// Convert to map to safely delete private fields
	var loginRespMap map[string]interface{}
	_ = json.Unmarshal(loginRespJson, &loginRespMap)

	delete(loginRespMap, "token")
	delete(loginRespMap, "refresh_token")

	c.JSON(http.StatusOK, &loginRespMap)
}

func (asc *ServiceClient) Logout(ctx *gin.Context) {
	ctx.SetSameSite(http.SameSiteNoneMode)
	ctx.SetCookie("mat", "", -1, "/", "", true, true)
	ctx.SetCookie("mrt", "", -1, "/", "", true, true)
	ctx.JSON(http.StatusOK, gin.H{})
}

func (asc *ServiceClient) ValidateSession(ctx *gin.Context) {
	authCookie, err := ctx.Request.Cookie("mat")

	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "unauthorized"})
		return
	}

	resp, err := asc.Client.DecodeSignedJWT(context.Background(), &pb.DecodeSignedJWTRequest{Token: authCookie.Value})

	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "unauthorized"})
		return
	}

	ctx.JSON(http.StatusOK, resp)
}

// --- OTP-based email change handlers ---

func (asc *ServiceClient) InitiateEmailChange(ctx *gin.Context) {
	username := ctx.Param("username")

	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "you aren't authorized to perform this action"})
		return
	}

	var body InitiateEmailChangeBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := asc.Client.InitiateEmailChange(context.Background(), &pb.InitiateEmailChangeReq{
		Username:   username,
		NewEmail:   body.NewEmail,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	ctx.JSON(int(res.StatusCode), gin.H{
		"message":    res.Message,
		"expires_in": res.ExpiresIn,
	})
}

func (asc *ServiceClient) VerifyEmailChangeOTP(ctx *gin.Context) {
	username := ctx.Param("username")

	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "you aren't authorized to perform this action"})
		return
	}

	var body VerifyEmailChangeOTPBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := asc.Client.VerifyEmailChangeOTP(context.Background(), &pb.VerifyEmailChangeOTPReq{
		Username:   username,
		NewEmail:   body.NewEmail,
		OtpCode:    body.OTPCode,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	utils.SetMonkeysAuthCookie(ctx, res.Token)

	response, _ := json.Marshal(&res)
	var responseMap map[string]interface{}
	_ = json.Unmarshal(response, &responseMap)
	delete(responseMap, "token")

	ctx.JSON(int(res.StatusCode), responseMap)
}

func (asc *ServiceClient) ResendEmailChangeOTP(ctx *gin.Context) {
	username := ctx.Param("username")

	if username != ctx.GetString("userName") {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "you aren't authorized to perform this action"})
		return
	}

	var body ResendEmailChangeOTPBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := asc.Client.ResendEmailChangeOTP(context.Background(), &pb.ResendEmailChangeOTPReq{
		Username:   username,
		NewEmail:   body.NewEmail,
		ClientInfo: asc.createClientInfo(ctx),
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	ctx.JSON(int(res.StatusCode), gin.H{
		"message":    res.Message,
		"expires_in": res.ExpiresIn,
	})
}

// --- User verification checkmark handlers ---

func (asc *ServiceClient) RequestUserVerification(ctx *gin.Context) {
	username := ctx.GetString("userName")
	if username == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var body RequestUserVerificationBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := asc.Client.RequestUserVerification(context.Background(), &pb.RequestUserVerificationReq{
		Username:         username,
		VerificationType: body.VerificationType,
		ProofUrls:        body.ProofURLs,
		AdditionalInfo:   body.AdditionalInfo,
		ClientInfo:       asc.createClientInfo(ctx),
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	ctx.JSON(int(res.StatusCode), gin.H{
		"message":    res.Message,
		"request_id": res.RequestId,
	})
}

func (asc *ServiceClient) GetUserVerificationStatus(ctx *gin.Context) {
	username := ctx.Param("username")
	if username == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	res, err := asc.Client.GetUserVerificationStatus(context.Background(), &pb.GetUserVerificationStatusReq{
		Username: username,
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	ctx.JSON(int(res.StatusCode), gin.H{
		"is_verified":         res.IsVerified,
		"verification_status": res.VerificationStatus,
		"verified_at":         res.VerifiedAt,
	})
}

func (asc *ServiceClient) ReviewUserVerification(ctx *gin.Context) {
	reviewerUsername := ctx.GetString("userName")
	if reviewerUsername == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var body ReviewUserVerificationBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := asc.Client.ReviewUserVerification(context.Background(), &pb.ReviewUserVerificationReq{
		RequestId:        body.RequestID,
		ReviewerUsername: reviewerUsername,
		Approved:         body.Approved,
		RejectionReason:  body.RejectionReason,
		ClientInfo:       asc.createClientInfo(ctx),
	})
	if err != nil {
		asc.handleGRPCError(ctx, err)
		return
	}

	ctx.JSON(int(res.StatusCode), gin.H{
		"message": res.Message,
	})
}
