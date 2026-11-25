package moderation

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func NewClient(cfg *config.Config) (pb.ModerationServiceClient, error) {
	conn, err := grpc.NewClient(fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysEvents, cfg.Microservices.EventsPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return pb.NewModerationServiceClient(conn), nil
}
func (s *Service) RegisterRoutes(r *gin.Engine, mw auth.AuthMiddlewareConfig) {
	r.GET("/api/v1/moderation/media", mw.AuthOptional, s.Media)
	r.GET("/api/v1/moderation/:kind/:id", mw.AuthOptional, middleware.RateLimiterMiddleware("120-M"), s.Access)
	write := r.Group("/api/v1/moderation", mw.AuthRequired, middleware.RateLimiterMiddleware("30-M"))
	write.POST("/:kind/:id/report", s.Report)
	write.POST("/:kind/:id/reveal", s.Reveal)
	write.PUT("/:kind/:id/label", s.Label)
}
func actor(c *gin.Context) *pb.Actor {
	return &pb.Actor{AccountId: c.GetString("accountId"), Ip: c.ClientIP()}
}
func target(c *gin.Context) *pb.Target { return &pb.Target{Kind: c.Param("kind"), Id: c.Param("id")} }
func fail(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	code := http.StatusServiceUnavailable
	switch status.Code(err) {
	case codes.InvalidArgument:
		code = 400
	case codes.Unauthenticated:
		code = 401
	case codes.PermissionDenied:
		code = 403
	case codes.NotFound:
		code = 404
	case codes.AlreadyExists, codes.Aborted:
		code = 409
	}
	messages := map[int]string{400: "Please check the submitted information.", 401: "Log in to continue.", 403: "You cannot perform this action.", 404: "Content not found.", 409: "The content changed. Please refresh.", 503: "Moderation is temporarily unavailable. Please try again."}
	c.AbortWithStatusJSON(code, gin.H{"error": messages[code]})
	return true
}
func (s *Service) Report(c *gin.Context) {
	var body struct {
		Reason  string `json:"reason"`
		Details string `json:"details"`
	}
	if c.ShouldBindJSON(&body) != nil {
		c.JSON(400, gin.H{"error": "A report reason is required."})
		return
	}
	res, err := s.client.ReportContent(c.Request.Context(), &pb.ActionRequest{Actor: actor(c), Target: target(c), Reason: body.Reason, Details: body.Details})
	if !fail(c, err) {
		c.Header("Cache-Control", "private, no-store")
		c.JSON(200, res)
	}
}
func (s *Service) Reveal(c *gin.Context) {
	res, err := s.client.Reveal(c.Request.Context(), &pb.ActionRequest{Actor: actor(c), Target: target(c)})
	if !fail(c, err) {
		c.Header("Cache-Control", "private, no-store")
		c.JSON(200, res)
	}
}
func (s *Service) Label(c *gin.Context) {
	var body struct {
		IsNSFW *bool `json:"is_nsfw"`
	}
	if c.ShouldBindJSON(&body) != nil || body.IsNSFW == nil {
		c.JSON(400, gin.H{"error": "Choose whether the content is NSFW."})
		return
	}
	res, err := s.client.SetAuthorLabel(c.Request.Context(), &pb.ActionRequest{Actor: actor(c), Target: target(c), IsNsfw: *body.IsNSFW})
	if !fail(c, err) {
		c.Header("Cache-Control", "private, no-store")
		c.JSON(200, res)
	}
}
func (s *Service) ListReports(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	res, err := s.client.ListReports(c.Request.Context(), &pb.ListRequest{Actor: actor(c), Status: c.DefaultQuery("status", "pending"), Limit: int32(limit), Offset: int32(offset)})
	if !fail(c, err) {
		c.Header("Cache-Control", "private, no-store")
		c.JSON(200, res)
	}
}
func (s *Service) Review(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(400, gin.H{"error": "Invalid report."})
		return
	}
	var body struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if c.ShouldBindJSON(&body) != nil {
		c.JSON(400, gin.H{"error": "Choose a review action."})
		return
	}
	res, err := s.client.ReviewReport(c.Request.Context(), &pb.ReviewRequest{Actor: actor(c), ReportId: id, Action: body.Action, Note: body.Note})
	if !fail(c, err) {
		c.Header("Cache-Control", "private, no-store")
		c.JSON(200, res)
	}
}

// WriteError sanitizes errors for legacy adapters using the moderation service.
func WriteError(c *gin.Context, err error) bool { return fail(c, err) }

func (s *Service) Access(c *gin.Context) {
	res, err := s.client.Check(c.Request.Context(), &pb.CheckRequest{AccountId: c.GetString("accountId"), Targets: []*pb.Target{target(c)}})
	if fail(c, err) {
		return
	}
	if res == nil || len(res.Items) != 1 || res.Items[0] == nil {
		c.JSON(503, gin.H{"error": "Content checks are temporarily unavailable."})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Cookie, Authorization")
	c.JSON(200, res.Items[0])
}

func (s *Service) Media(c *gin.Context) {
	path := c.Query("path")
	supported := false
	for _, prefix := range []string{"/api/v2/storage/", "/api/storage/", "/api/v1/files/post/", "/api/v1.1/files/post/"} {
		if strings.HasPrefix(path, prefix) {
			supported = true
			break
		}
	}
	if !supported || len(path) > 4096 {
		c.JSON(400, gin.H{"error": "Invalid media path."})
		return
	}
	call, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	res, err := s.client.CheckMedia(call, &pb.MediaRequest{AccountId: c.GetString("accountId"), Path: path})
	if fail(c, err) {
		return
	}
	if res == nil {
		c.JSON(503, gin.H{"error": "Content checks are temporarily unavailable."})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Cookie, Authorization")
	// Do not expose whether a private target exists or which reference is restricted.
	c.JSON(200, gin.H{"allowed": res.Allowed && res.Resolved})
}
