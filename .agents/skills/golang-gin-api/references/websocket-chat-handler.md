# WebSocket — ChatHandler (Hub + Client Registration)

See also: `websocket-hub-and-client.md`, `websocket-setup-and-echo.md`

## ChatHandler

Upgrades the HTTP connection and registers the new `Client` with the `Hub`. Launches `readPump` and `writePump` goroutines.

```go
// internal/handler/ws_chat_handler.go
package handler

import (
    "log/slog"

    "github.com/gin-gonic/gin"
    "github.com/gorilla/websocket"
    "myapp/pkg/ws"
)

// ChatHandler upgrades the connection and registers the client with the hub.
func ChatHandler(hub *ws.Hub) gin.HandlerFunc {
    return func(c *gin.Context) {
        conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
        if err != nil {
            slog.Error("ws upgrade failed", "err", err)
            return
        }

        // c.Copy() is required when passing *gin.Context to goroutines.
        // Capture request-scoped values before the handler returns.
        cpy := c.Copy()
        _ = cpy // use cpy.GetString("userID") etc. if needed

        client := &ws.Client{
            Hub:  hub,
            Conn: conn,
            Send: make(chan []byte, 256),
        }
        hub.Register <- client

        go client.WritePump()
        go client.ReadPump()
    }
}
```

**Why `c.Copy()` before goroutines:** Gin recycles `*gin.Context` objects via `sync.Pool` after the handler returns. `c.Copy()` creates a snapshot safe to use after the handler returns.

## Route Registration

```go
hub := ws.NewHub()
go hub.Run()

r.GET("/ws/chat", middleware.Auth(cfg, logger), handler.ChatHandler(hub))
```
