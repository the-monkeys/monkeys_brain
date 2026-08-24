package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type AuthMiddlewareConfig struct {
	svc *ServiceClient
	log *zap.SugaredLogger
}

func InitAuthMiddleware(svc *ServiceClient, log *zap.SugaredLogger) AuthMiddlewareConfig {
	return AuthMiddlewareConfig{svc, log}
}

// Extracts the token from the Authorization header or query parameter
func (c *AuthMiddlewareConfig) extractToken(ctx *gin.Context) (string, error) {
	authCookie, err := ctx.Request.Cookie("mat")
	if err == nil {
		return authCookie.Value, nil
	}

	authorization := ctx.Request.Header.Get("Authorization")

	if authorization == "" {
		tokenQuery := ctx.Query("token")
		if tokenQuery == "" {
			return "", fmt.Errorf("unauthorized")
		}
		authorization = "Bearer " + tokenQuery
	}

	tokenParts := strings.Split(authorization, "Bearer ")
	if len(tokenParts) < 2 {
		return "", fmt.Errorf("unauthorized")
	}

	return tokenParts[1], nil
}

// Validate the token and retrieve user information
func (c *AuthMiddlewareConfig) validateToken(ctx *gin.Context) (*pb.ValidateResponse, error) {
	token, err := c.extractToken(ctx)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "unauthorized"})
		return nil, err
	}

	res, err := c.svc.Client.Validate(context.Background(), &pb.ValidateRequest{Token: token})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "unauthorized"})
		return nil, err
	}

	ctx.Set("userName", res.UserName)
	ctx.Set("accountId", res.AccountId)
	return res, nil
}

// Middleware to check basic authorization
func (c *AuthMiddlewareConfig) AuthRequired(ctx *gin.Context) {
	if _, err := c.validateToken(ctx); err != nil {
		return
	}

	ctx.Next()
}

// AuthOptional identifies the caller when a valid token is present but lets
// anonymous requests through. Use it on public reads that render differently
// for a signed-in viewer.
func (c *AuthMiddlewareConfig) AuthOptional(ctx *gin.Context) {
	token, err := c.extractToken(ctx)
	if err != nil {
		ctx.Next()
		return
	}

	res, err := c.svc.Client.Validate(context.Background(), &pb.ValidateRequest{Token: token})
	if err != nil {
		ctx.Next()
		return
	}

	ctx.Set("userName", res.UserName)
	ctx.Set("accountId", res.AccountId)
	ctx.Next()
}

// Middleware to check authorization with specific access level
func (c *AuthMiddlewareConfig) AuthzRequired(ctx *gin.Context) {
	res, err := c.validateToken(ctx)
	if err != nil {
		return
	}

	blogID := ctx.Param("blog_id")
	userName := res.UserName
	email := res.Email // ✅ FIXED: Get email from JWT token, not URL params

	accessResp, err := c.svc.Client.CheckAccessLevel(context.Background(), &pb.AccessCheckReq{
		// Token:     res,
		Email:     email,
		AccountId: res.AccountId,
		UserName:  userName,
		BlogId:    blogID,
	})

	if err != nil || accessResp.StatusCode != http.StatusOK {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.Unauthenticated:
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "you are not authorized to perform this action now"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	// fmt.Printf("res: %+v\n", accessResp)

	ctx.Set("accountId", res.AccountId)
	ctx.Set("user_access_level", accessResp.Access)
	ctx.Set("user_role", accessResp.Role)

	// fmt.Printf("accessResp.Role: %v\n", accessResp.Role)
	ctx.Next()
}

// Middleware to check authorization with specific access level
func (c *AuthMiddlewareConfig) AuthorizationByID(ctx *gin.Context) {
	res, err := c.validateToken(ctx)
	if err != nil {
		return
	}

	blogID := ctx.Param("id")
	userName := res.UserName
	email := res.Email // ✅ FIXED: Get email from JWT token, not URL params
	accessResp, err := c.svc.Client.CheckAccessLevel(context.Background(), &pb.AccessCheckReq{
		// Token:     res,
		Email:     email,
		AccountId: res.AccountId,
		UserName:  userName,
		BlogId:    blogID,
	})

	if err != nil || accessResp.StatusCode != http.StatusOK {
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.Unauthenticated:
				ctx.AbortWithStatusJSON(http.StatusUnauthorized, Authorization{AuthorizationStatus: false, Error: "you are not authorized to perform this action now"})
				return
			default:
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "something went wrong"})
				return
			}
		}
	}

	// fmt.Printf("res: %+v\n", accessResp)

	ctx.Set("accountId", res.AccountId)
	ctx.Set("user_access_level", accessResp.Access)
	ctx.Set("user_role", accessResp.Role)

	// fmt.Printf("accessResp.Role: %v\n", accessResp.Role)
	ctx.Next()
}

func (c *AuthMiddlewareConfig) CheckWriteAccess(ctx *gin.Context) {
	// TODO: Check if the user can publish access
	c.log.Infof("The user has write/edit access to the blog!")
	ctx.Next()
}
