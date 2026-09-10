package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_notification/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Consider restricting this based on your use case
	},
}

type Notification struct {
	ID      string `json:"id"`
	UserID  string `json:"user_id"`
	Message string `json:"message"`
	Status  string `json:"status"` // e.g., "sent", "read"
}

type NotificationServiceClient struct {
	Client      pb.NotificationServiceClient
	cfg         *config.Config
	mu          sync.Mutex
	connections map[string][]*websocket.Conn // Map user ID to WebSocket connections
	log         *zap.SugaredLogger
}

// NewNotificationServiceClient creates a new instance of NotificationServiceClient
func NewNotificationServiceClient(cfg *config.Config, lg *zap.SugaredLogger) pb.NotificationServiceClient {
	notificationSvc := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysNotification, cfg.Microservices.NotificationPort)
	cc, err := grpc.NewClient(notificationSvc, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		lg.Errorw("cannot dial gRPC notification server", "err", err, "addr", notificationSvc)
		return nil
	}
	lg.Debugw("dialing notification service", "addr", notificationSvc)
	return pb.NewNotificationServiceClient(cc)
}

// RegisterNotificationRoute sets up the notification routes
func RegisterNotificationRoute(router *gin.Engine, cfg *config.Config, authClient *auth.ServiceClient, lg *zap.SugaredLogger) *NotificationServiceClient {
	mware := auth.InitAuthMiddleware(authClient, lg)

	nsc := &NotificationServiceClient{
		Client:      NewNotificationServiceClient(cfg, lg),
		cfg:         cfg,
		connections: make(map[string][]*websocket.Conn), // Map of user ID to WebSocket connections
		log:         lg,
	}

	routes := router.Group("/api/v1/notification")
	routes.Use(mware.AuthRequired)

	routes.POST("/notifications", nsc.CreateNotification)      // Create notifications
	routes.GET("/notifications", nsc.GetNotifications)         // Get notifications
	routes.GET("/ws-notification", nsc.GetNotificationsStream) // WebSocket endpoint
	routes.PUT("/notifications", nsc.ViewNotification)         // Get notifications
	routes.GET("/sse-token", nsc.GetSSEToken)                  // FRN SSE token
	routes.GET("/frn", nsc.GetFRNNotifications)                // FRN notification list
	routes.POST("/frn/read-all", nsc.MarkAllFRNRead)           // Mark all as read

	return nsc
}

// CreateNotification handles the creation of notifications
func (nsc *NotificationServiceClient) CreateNotification(ctx *gin.Context) {
	var notification Notification
	if err := ctx.ShouldBindJSON(&notification); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Notify the user via WebSocket
	// nsc.NotifyUser(notification.UserID, notification)

	ctx.JSON(http.StatusCreated, notification)
}

// GetNotifications retrieves notifications for the user and pushes them via WebSocket if connected
func (nsc *NotificationServiceClient) GetNotifications(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	// get params like limit, offset, etc.
	limit := ctx.Query("limit")
	offset := ctx.Query("offset")

	// Convert to int64
	limitInt, err := strconv.ParseInt(limit, 10, 64)
	if err != nil {
		limitInt = 10
	}
	offsetInt, err := strconv.ParseInt(offset, 10, 64)
	if err != nil {
		offsetInt = 0
	}

	// Step 2: Fetch notifications from the database for the user (e.g., only unread ones)
	notifications, err := nsc.Client.GetNotification(context.Background(), &pb.GetNotificationReq{
		Username: userName,
		Limit:    limitInt,
		Offset:   offsetInt,
	})
	if err != nil {
		nsc.log.Errorw("fetch notifications failed", "user", userName, "err", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch notifications"})
		return
	}

	// Step 3: Send notifications via WebSocket if the user has an active connection
	if len(notifications.Notification) > 0 {
		nsc.NotifyUser(userName, notifications.Notification)
	}

	// Step 4: Return the notifications via HTTP as well
	ctx.JSON(http.StatusOK, gin.H{"notifications": notifications})
}

// NotifyUser sends notifications to a specific user via WebSocket
func (nsc *NotificationServiceClient) NotifyUser(userID string, notification []*pb.Notification) {
	nsc.mu.Lock()
	defer nsc.mu.Unlock()

	// Step 1: Check if the user has active WebSocket connections
	conns, ok := nsc.connections[userID]
	if !ok {
		nsc.log.Debugw("no active websocket connections", "user", userID)
		return
	}

	// Step 2: Send the notification to each active connection
	for _, conn := range conns {
		if err := conn.WriteJSON(notification); err != nil {
			nsc.log.Errorw("send ws notification failed", "user", userID, "err", err)
			if err := conn.Close(); err != nil {
				nsc.log.Errorw("close ws conn failed", "user", userID, "err", err)
			}
			// Remove closed connection from list
			nsc.connections[userID] = removeConn(nsc.connections[userID], conn)
		}
	}
}

// removeConn removes a WebSocket connection from the list
func removeConn(conns []*websocket.Conn, conn *websocket.Conn) []*websocket.Conn {
	for i, c := range conns {
		if c == conn {
			return append(conns[:i], conns[i+1:]...)
		}
	}
	return conns
}

func (nsc *NotificationServiceClient) ViewNotification(ctx *gin.Context) {
	var req pb.WatchNotificationReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Step 2: Mark the notification as seen in the database
	_, err := nsc.Client.NotificationSeen(context.Background(), &req)
	if err != nil {
		nsc.log.Errorw("mark notification seen failed", "err", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to mark notification as seen"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "Notification seen"})
}

func (nsc *NotificationServiceClient) GetNotificationsStream(ctx *gin.Context) {
	// Upgrade the HTTP connection to a WebSocket connection
	conn, err := upgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		nsc.log.Errorw("upgrade to websocket failed", "err", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to establish WebSocket connection"})
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			nsc.log.Errorw("close websocket failed", "err", err)
		}
	}()

	// Get the username from the context (assumes middleware sets this)
	userName := ctx.GetString("userName")
	if userName == "" {
		nsc.log.Errorw("unauthenticated websocket connection")
		_ = conn.WriteJSON(gin.H{"error": "Unauthorized"})
		return
	}
	nsc.log.Debugw("websocket connection established", "user", userName)

	// Track the last notification ID to avoid sending duplicates
	var lastNotificationID string

	for {
		// Call the gRPC streaming method
		stream, err := nsc.Client.GetNotificationStream(context.Background(), &pb.GetNotificationReq{
			Username: userName,
			Limit:    10, // Fetch 10 notifications at a time
			Offset:   0,
		})
		if err != nil {
			nsc.log.Errorw("create gRPC stream failed", "user", userName, "err", err)
			_ = conn.WriteJSON(gin.H{"error": "Failed to establish notification stream"})
			return
		}

		// Receive notifications from the gRPC stream
		for {
			notification, err := stream.Recv()
			if err != nil {
				nsc.log.Debugw("no new notifications or stream closed", "user", userName)
				break // Exit inner loop if no new notifications or error occurs
			}

			// Check if the notification is new
			if notification.Notification[0].Id != lastNotificationID {
				// Update the last notification ID
				lastNotificationID = notification.Notification[0].Id

				// Forward the notification to the WebSocket client
				if err := conn.WriteJSON(notification); err != nil {
					nsc.log.Errorw("forward notification to ws client failed", "user", userName, "err", err)
					return
				}
				nsc.log.Debugw("notification forwarded", "user", userName)
			}
		}

		// Wait for a short period before checking for new notifications again
		select {
		case <-time.After(5 * time.Second): // Poll every 5 seconds
			continue
		case <-ctx.Done(): // Exit if the client disconnects
			nsc.log.Debugw("websocket connection closed by user", "user", userName)
			return
		}
	}
}

// GetSSEToken returns a short-lived FRN SSE token for the authenticated user.
func (nsc *NotificationServiceClient) GetSSEToken(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	if userName == "" {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user identity"})
		return
	}

	frnCfg := nsc.cfg.FreeRangeNotify
	if frnCfg.BaseURL == "" {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "notification service not configured"})
		return
	}

	payload, err := json.Marshal(map[string]string{
		"user_id": userName,
	})
	if err != nil {
		nsc.log.Errorw("failed to marshal sse-token request", "err", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx.Request.Context(), 5*time.Second)
	defer cancel()

	url := frnCfg.BaseURL + "/sse/tokens"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		nsc.log.Errorw("failed to build sse-token request", "err", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+frnCfg.APIKey)
	req.Header.Set("X-API-Key", frnCfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		nsc.log.Errorw("failed to get sse token from FRN", "err", err, "user", userName)
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "notification service unavailable"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		nsc.log.Errorw("FRN returned non-200 for sse-token", "status", resp.StatusCode, "body", string(body), "user", userName)
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "failed to obtain sse token"})
		return
	}

	// FRN returns {sse_token, user_id, expires_in}. The browser must connect to
	// the same FRN that issued the token, so attach the public URL from config.
	var frnResp map[string]any
	if err := json.Unmarshal(body, &frnResp); err != nil {
		ctx.Data(http.StatusOK, "application/json", body)
		return
	}
	if pub := strings.TrimRight(frnCfg.SSEPublicURL, "/"); pub != "" {
		frnResp["sse_public_url"] = pub
	}
	out, err := json.Marshal(frnResp)
	if err != nil {
		ctx.Data(http.StatusOK, "application/json", body)
		return
	}
	ctx.Data(http.StatusOK, "application/json", out)
}

// GetFRNNotifications proxies the FRN notification list for the authenticated user.
func (nsc *NotificationServiceClient) GetFRNNotifications(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	if userName == "" {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user identity"})
		return
	}

	frnCfg := nsc.cfg.FreeRangeNotify
	if frnCfg.BaseURL == "" {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "notification service not configured"})
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx.Request.Context(), 5*time.Second)
	defer cancel()

	pageSize := ctx.DefaultQuery("page_size", "20")
	page := ctx.DefaultQuery("page", "1")
	channel := ctx.DefaultQuery("channel", "in_app")

	url := fmt.Sprintf("%s/notifications?user_id=%s&channel=%s&page_size=%s&page=%s",
		frnCfg.BaseURL, userName, channel, pageSize, page)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		nsc.log.Errorw("failed to build FRN list request", "err", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	req.Header.Set("X-API-Key", frnCfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		nsc.log.Errorw("failed to get notifications from FRN", "err", err, "user", userName)
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "notification service unavailable"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	ctx.Data(resp.StatusCode, "application/json", body)
}

// MarkAllFRNRead marks all notifications as read for the authenticated user.
func (nsc *NotificationServiceClient) MarkAllFRNRead(ctx *gin.Context) {
	userName := ctx.GetString("userName")
	if userName == "" {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user identity"})
		return
	}

	frnCfg := nsc.cfg.FreeRangeNotify
	if frnCfg.BaseURL == "" {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "notification service not configured"})
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx.Request.Context(), 5*time.Second)
	defer cancel()

	payload, err := json.Marshal(map[string]string{"user_id": userName})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	url := frnCfg.BaseURL + "/notifications/read-all"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", frnCfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		nsc.log.Errorw("failed to mark-all-read in FRN", "err", err, "user", userName)
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "notification service unavailable"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	ctx.Data(resp.StatusCode, "application/json", body)
}
