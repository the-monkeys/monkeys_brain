package admin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	userpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type AdminServiceClient struct {
	Client userpb.UserServiceClient
	Events pb.EventServiceClient
	Blogs  blogpb.BlogServiceClient
	Groups grouppb.GroupServiceClient
	logger *zap.SugaredLogger
}

func NewAdminServiceClient(cfg *config.Config, log *zap.SugaredLogger) userpb.UserServiceClient {
	userService := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysUser, cfg.Microservices.UserPort)
	cc, err := grpc.NewClient(userService, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Errorf("cannot dial to grpc user server for admin: %v", err)
	}
	log.Infof("✅ admin service is dialing to user rpc server at: %v", cfg.Microservices.TheMonkeysUser)
	return userpb.NewUserServiceClient(cc)
}

// LocalNetworkMiddleware restricts access to RFC1918 / loopback peers.
// A public TCP peer cannot spoof access with X-Forwarded-For.
func LocalNetworkMiddleware(log *zap.SugaredLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if RequestIsLocal(c) {
			c.Next()
			return
		}
		log.Warnf("Admin access attempt from non-local IP: peer=%s client=%s", c.Request.RemoteAddr, c.ClientIP())
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "Access denied: Admin API only accessible from local network",
		})
	}
}

// RequestIsLocal reports whether this request originated on a private network.
// If the TCP peer is a public address, that peer is used (so a public client
// cannot pass by sending X-Forwarded-For: 127.0.0.1). If the peer is already
// private (loopback or docker nginx), gin's ClientIP / forwarded header is used.
func RequestIsLocal(c *gin.Context) bool {
	return isLocalNetwork(requestIP(c))
}

func requestIP(c *gin.Context) net.IP {
	peer := parseIPHost(c.Request.RemoteAddr)
	if peer != nil && !isLocalNetwork(peer) {
		return peer
	}
	if ip := net.ParseIP(c.ClientIP()); ip != nil {
		return ip
	}
	return peer
}

func parseIPHost(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return net.ParseIP(host)
}

// isLocalNetwork checks if IP is from private/loopback ranges. CGNAT
// (100.64.0.0/10) and public unicast are not local.
func isLocalNetwork(ip net.IP) bool {
	if ip == nil {
		return false
	}
	localRanges := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"::1/128",
		"fc00::/7",
	}

	for _, cidr := range localRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// AdminKeyMiddleware validates admin key from header
func AdminKeyMiddleware(adminKey string, log *zap.SugaredLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		providedKey := c.GetHeader("X-Admin-Key")
		if providedKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Admin key required",
			})
			return
		}

		if providedKey != adminKey {
			log.Warnf("Invalid admin key attempt from IP: %s", c.ClientIP())
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid admin key",
			})
			return
		}

		c.Next()
	}
}

func RegisterAdminRouter(router *gin.Engine, cfg *config.Config, authClient *auth.ServiceClient, eventClient pb.EventServiceClient, blogClient blogpb.BlogServiceClient, groupClient grouppb.GroupServiceClient, logg *zap.SugaredLogger) *AdminServiceClient {
	asc := &AdminServiceClient{
		Client: NewAdminServiceClient(cfg, logg),
		Events: eventClient,
		Blogs:  blogClient,
		Groups: groupClient,
		logger: logg,
	}
	mware := auth.InitAuthMiddleware(authClient, logg)

	staff := router.Group("/api/v1/admin")
	staff.Use(LocalNetworkMiddleware(logg))
	staff.Use(mware.AuthRequired)

	pay := staff.Group("/payments", RequireRole(logg, constants.RoleAdmin, constants.RoleCommunity))
	pay.GET("/events", asc.ListEventPayments)
	pay.GET("/events/:slug", asc.GetEventPayments)
	pay.POST("/events/:slug/settlements", asc.CreateSettlement)
	pay.POST("/settlements/:id/mark-paid", asc.MarkSettlementPaid)

	// Community can search and take takedown actions. Role/flag/orphan stay Admin.
	catalog := staff.Group("", RequireRole(logg, constants.RoleAdmin, constants.RoleCommunity))
	catalog.GET("/users", asc.ListUsers)
	catalog.DELETE("/users/:id", asc.DeleteUserJWT)
	catalog.GET("/blogs", asc.ListBlogs)
	catalog.POST("/blogs/:blog_id/unpublish", asc.UnpublishBlog)
	catalog.DELETE("/blogs/:blog_id", asc.DeleteBlog)
	catalog.GET("/events", asc.ListAdminEvents)
	catalog.POST("/events/:slug/cancel", asc.CancelAdminEvent)
	catalog.POST("/events/:slug/unpublish", asc.UnpublishAdminEvent)
	catalog.DELETE("/events/:slug", asc.DeleteAdminEvent)
	catalog.GET("/groups", asc.ListAdminGroups)
	catalog.POST("/groups/:slug/suspend", asc.SuspendGroup)
	catalog.DELETE("/groups/:slug", asc.DeleteAdminGroup)

	cat := staff.Group("", RequireRole(logg, constants.RoleAdmin))
	cat.POST("/users/:id/role", asc.SetUserRole)
	cat.POST("/users/:id/flag", asc.FlagUser)
	cat.POST("/users/:id/unflag", asc.UnflagUserJWT)
	cat.POST("/users/:id/suspend", asc.SuspendUser)
	cat.GET("/blogs/orphans", asc.ListOrphanBlogs)

	stats := staff.Group("", RequireRole(logg, constants.RoleAdmin, constants.RoleSupport, constants.RoleCommunity))
	stats.GET("/stats", asc.GetStats)

	ver := staff.Group("", RequireRole(logg, constants.RoleAdmin, constants.RoleSupport, constants.RoleCommunity))
	ver.GET("/verifications", asc.ListVerifications)
	ver.POST("/verifications/:id/review", asc.ReviewVerification)

	mod := staff.Group("", RequireRole(logg, constants.RoleAdmin, constants.RoleCommunity))
	mod.POST("/events/:slug/nsfw", asc.FlagNsfw("event"))
	mod.POST("/blogs/:blog_id/nsfw", asc.FlagNsfw("blog"))
	mod.POST("/groups/:slug/nsfw", asc.FlagNsfw("group"))
	mod.POST("/users/:id/nsfw", asc.FlagNsfw("user"))
	mod.POST("/events/:slug/comments/:id/hide", asc.HideEventComment)
	mod.POST("/events/:slug/questions/:id/hide", asc.HideEventQuestion)

	adminRoutes := router.Group("/api/v1/admin")
	adminRoutes.Use(LocalNetworkMiddleware(logg))
	adminRoutes.Use(AdminKeyMiddleware(cfg.Keys.AdminSecretKey, logg))

	{
		adminRoutes.DELETE("/users/bulk", asc.BulkDeleteUsers)
		adminRoutes.GET("/users/suspicious", asc.GetSuspiciousUsers)
		adminRoutes.GET("/users/flagged", asc.GetFlaggedUsers)
	}

	// System health and monitoring
	{
		adminRoutes.GET("/health", asc.AdminHealthCheck)
		adminRoutes.GET("/system/stats", asc.GetSystemStats)
	}

	// Backup operations
	{
		adminRoutes.POST("/backup/execute", asc.ExecuteBackup)
	}

	// New admin apis
	// GEt all the registered ussers with numbers of users
	// GEt all blogs, published blogs, draft blogs, orphan blogs(available in elasticsearch but not present in postgres)
	// Get VErification requests with stats of pending review, approved, rejected including pics available (make sure pics and videos are available local to admin and doesn't display on the homepage)
	// Approve verification from daashboard itself
	// CAn mark posts/images/accounts/events NSFW
	// Add more looking at he db in postgrsss and elasticsearch in docker-compose.yaml

	return asc
}

// ForceDeleteUser deletes a user without normal authorization checks
func (asc *AdminServiceClient) ForceDeleteUser(ctx *gin.Context) {
	userID := ctx.Param("id")
	reason := ctx.Query("reason")
	if reason == "" {
		reason = "Admin deletion"
	}

	asc.logger.Infof("Admin force deleting user: %s, reason: %s, from IP: %s", userID, reason, ctx.ClientIP())

	res, err := asc.Client.DeleteUserAccount(context.Background(), &userpb.DeleteUserProfileReq{
		Username: userID,
	})

	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error":   "User not found",
				"user_id": userID,
			})
			return
		} else {
			asc.logger.Errorf("Failed to delete user %s: %v", userID, err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to delete user",
			})
			return
		}
	}

	asc.logger.Infof("Successfully deleted user: %s", userID)
	ctx.JSON(http.StatusOK, gin.H{
		"message":    "User successfully deleted",
		"user_id":    userID,
		"reason":     reason,
		"deleted_by": "admin",
		"result":     res,
	})
}

// BulkDeleteUsers deletes multiple users at once
func (asc *AdminServiceClient) BulkDeleteUsers(ctx *gin.Context) {
	var req struct {
		UserIDs []string `json:"user_ids" binding:"required"`
		Reason  string   `json:"reason"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}

	if len(req.UserIDs) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "At least one user ID is required",
		})
		return
	}

	if len(req.UserIDs) > 100 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Cannot delete more than 100 users at once",
		})
		return
	}

	if req.Reason == "" {
		req.Reason = "Bulk admin deletion"
	}

	asc.logger.Infof("Admin bulk deleting %d users, reason: %s, from IP: %s", len(req.UserIDs), req.Reason, ctx.ClientIP())

	results := make(map[string]interface{})
	successCount := 0
	failureCount := 0

	for _, userID := range req.UserIDs {
		_, err := asc.Client.DeleteUserAccount(context.Background(), &userpb.DeleteUserProfileReq{
			Username: userID,
		})

		if err != nil {
			failureCount++
			results[userID] = gin.H{
				"status": "failed",
				"error":  err.Error(),
			}
			asc.logger.Errorf("Failed to delete user %s: %v", userID, err)
		} else {
			successCount++
			results[userID] = gin.H{
				"status": "success",
			}
		}
	}

	asc.logger.Infof("Bulk deletion completed: %d successful, %d failed", successCount, failureCount)

	ctx.JSON(http.StatusOK, gin.H{
		"message":      "Bulk deletion completed",
		"total_users":  len(req.UserIDs),
		"successful":   successCount,
		"failed":       failureCount,
		"reason":       req.Reason,
		"results":      results,
		"processed_by": "admin",
	})
}

// GetSuspiciousUsers returns users that might be bots or fake accounts
func (asc *AdminServiceClient) GetSuspiciousUsers(ctx *gin.Context) {
	// This would typically involve complex logic to identify suspicious patterns
	// For now, returning a placeholder response

	ctx.JSON(http.StatusOK, gin.H{
		"message": "Suspicious users detection not yet implemented",
		"note":    "This endpoint would analyze user patterns, disposable emails, etc.",
	})
}

// FlagUserAsBotOrFake flags a user as suspicious
func (asc *AdminServiceClient) FlagUserAsBotOrFake(ctx *gin.Context) {
	userID := ctx.Param("id")

	var req struct {
		Reason string `json:"reason" binding:"required"`
		Type   string `json:"type" binding:"required"` // "bot", "fake", "spam"
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}

	validTypes := []string{"bot", "fake", "spam"}
	isValidType := false
	for _, t := range validTypes {
		if req.Type == t {
			isValidType = true
			break
		}
	}

	if !isValidType {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Invalid type. Must be one of: bot, fake, spam",
		})
		return
	}

	asc.logger.Infof("Admin flagging user %s as %s, reason: %s, from IP: %s", userID, req.Type, req.Reason, ctx.ClientIP())

	// Here you would implement the actual flagging logic
	// This might involve updating a user_flags table or similar

	ctx.JSON(http.StatusOK, gin.H{
		"message":    "User flagged successfully",
		"user_id":    userID,
		"flag_type":  req.Type,
		"reason":     req.Reason,
		"flagged_by": "admin",
	})
}

// UnflagUser removes flags from a user
func (asc *AdminServiceClient) UnflagUser(ctx *gin.Context) {
	userID := ctx.Param("id")
	reason := ctx.Query("reason")
	if reason == "" {
		reason = "Admin unflag"
	}

	asc.logger.Infof("Admin unflagging user %s, reason: %s, from IP: %s", userID, reason, ctx.ClientIP())

	ctx.JSON(http.StatusOK, gin.H{
		"message":      "User unflagged successfully",
		"user_id":      userID,
		"reason":       reason,
		"unflagged_by": "admin",
	})
}

// GetFlaggedUsers returns all flagged users
func (asc *AdminServiceClient) GetFlaggedUsers(ctx *gin.Context) {
	flagType := ctx.Query("type") // optional filter by flag type

	ctx.JSON(http.StatusOK, gin.H{
		"message":     "Flagged users retrieval not yet implemented",
		"note":        "This endpoint would return users flagged as bots/fake/spam",
		"filter_type": flagType,
	})
}

// GetUserStats returns user statistics for admin monitoring
func (asc *AdminServiceClient) GetUserStats(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"message": "User statistics not yet implemented",
		"note":    "This would return user registration patterns, suspicious activity, etc.",
	})
}

// AdminHealthCheck provides health status for admin monitoring
func (asc *AdminServiceClient) AdminHealthCheck(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"service":   "admin-api",
		"timestamp": "2024-01-01T00:00:00Z",
		"access_ip": ctx.ClientIP(),
	})
}

// GetSystemStats returns system-wide statistics
func (asc *AdminServiceClient) GetSystemStats(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"message": "System statistics not yet implemented",
		"note":    "This would return system health, performance metrics, etc.",
	})
}

type ReturnMessage struct {
	Message string `json:"message"`
}

// BackupRequest represents the request body for backup operations
type BackupRequest struct {
	Servers []ServerConfig `json:"servers" binding:"required"`
	Timeout int            `json:"timeout"` // timeout in seconds, default 300
}

// ServerConfig represents a server configuration for SSH backup
type ServerConfig struct {
	Host     string   `json:"host" binding:"required"`
	Port     int      `json:"port"` // default 22
	User     string   `json:"user" binding:"required"`
	KeyPath  string   `json:"key_path"` // SSH private key path, optional
	Password string   `json:"password"` // SSH password, optional if key_path provided
	Commands []string `json:"commands" binding:"required"`
	Name     string   `json:"name"`     // friendly name for the server
	UseSudo  bool     `json:"use_sudo"` // prefix commands with sudo
}

// BackupResult represents the result of a backup operation on a server
type BackupResult struct {
	Server    string `json:"server"`
	Name      string `json:"name"`
	Success   bool   `json:"success"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Duration  string `json:"duration"`
	Timestamp string `json:"timestamp"`
}

// ExecuteBackup handles SSH backup operations across multiple servers
func (asc *AdminServiceClient) ExecuteBackup(ctx *gin.Context) {
	var req BackupRequest

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	if len(req.Servers) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "At least one server configuration is required",
		})
		return
	}

	if req.Timeout == 0 {
		req.Timeout = 300 // default 5 minutes
	}

	asc.logger.Infof("Admin initiating backup on %d servers from IP: %s", len(req.Servers), ctx.ClientIP())

	// Execute backups concurrently
	var wg sync.WaitGroup
	results := make([]BackupResult, len(req.Servers))

	for i, server := range req.Servers {
		wg.Add(1)
		go func(index int, srv ServerConfig) {
			defer wg.Done()
			results[index] = asc.executeServerBackup(srv, req.Timeout)
		}(i, server)
	}

	wg.Wait()

	// Count successes and failures
	successCount := 0
	failureCount := 0
	for _, result := range results {
		if result.Success {
			successCount++
		} else {
			failureCount++
		}
	}

	asc.logger.Infof("Backup operation completed: %d successful, %d failed", successCount, failureCount)

	statusCode := http.StatusOK
	if successCount == 0 {
		statusCode = http.StatusInternalServerError
	} else if failureCount > 0 {
		statusCode = http.StatusPartialContent
	}

	ctx.JSON(statusCode, gin.H{
		"message":       "Backup operation completed",
		"total_servers": len(req.Servers),
		"successful":    successCount,
		"failed":        failureCount,
		"results":       results,
		"executed_by":   "admin",
		"timestamp":     time.Now().Format(time.RFC3339),
	})
}

// executeServerBackup executes backup commands on a single server via SSH
func (asc *AdminServiceClient) executeServerBackup(server ServerConfig, timeout int) BackupResult {
	result := BackupResult{
		Server:    server.Host,
		Name:      server.Name,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if server.Name == "" {
		result.Name = server.Host
	}

	port := server.Port
	if port == 0 {
		port = 22
	}

	startTime := time.Now()
	defer func() {
		result.Duration = time.Since(startTime).String()
	}()

	// Combine all commands into a single SSH session
	combinedCommands := strings.Join(server.Commands, " && ")

	// Prefix with sudo if requested
	if server.UseSudo {
		combinedCommands = "sudo " + combinedCommands
		asc.logger.Infof("Commands will be executed with sudo on %s", server.Host)
	}

	// Build SSH command
	var sshArgs []string
	sshArgs = append(sshArgs, "-p", fmt.Sprintf("%d", port))

	// Add SSH options
	sshArgs = append(sshArgs,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", fmt.Sprintf("ConnectTimeout=%d", timeout),
	)

	// Add key-based authentication if provided
	if server.KeyPath != "" {
		sshArgs = append(sshArgs, "-i", server.KeyPath)
	}

	// Add user and host
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", server.User, server.Host))

	// Add the command to execute
	sshArgs = append(sshArgs, combinedCommands)

	asc.logger.Infof("Executing SSH backup on %s (%s)", server.Host, result.Name)

	// Create command with timeout context
	ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctxTimeout, "ssh", sshArgs...)

	// If password is provided (for sshpass usage)
	if server.Password != "" && server.KeyPath == "" {
		// Use sshpass for password authentication
		asc.logger.Warn("Password-based SSH authentication is less secure. Consider using key-based authentication.")
		// Prepend sshpass command
		sshpassArgs := []string{"-p", server.Password, "ssh"}
		sshpassArgs = append(sshpassArgs, sshArgs...)
		cmd = exec.CommandContext(ctxTimeout, "sshpass", sshpassArgs...)
	}

	// Execute command and capture output
	output, err := cmd.CombinedOutput()
	result.Output = string(output)

	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("SSH command failed: %v", err)
		asc.logger.Errorf("Backup failed on %s (%s): %v\nOutput: %s", server.Host, result.Name, err, result.Output)
		return result
	}

	result.Success = true
	asc.logger.Infof("Backup successful on %s (%s)", server.Host, result.Name)
	return result
}
