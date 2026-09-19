# WebSocket — Auth Before Upgrade & Ping/Pong Keepalive

See also: `websocket-setup-and-echo.md`, `websocket-hub-and-client.md`, `websocket-shutdown-and-messages.md`, `websocket-testing.md`

## Auth Before Upgrade

WebSocket has no standard mechanism for sending auth headers after the handshake. Authenticate during the HTTP upgrade request — before calling `upgrader.Upgrade()`. If auth fails, return a normal HTTP error response.

```go
// internal/ws/auth_handler.go
package ws

import (
    "log/slog"
    "net/http"

    "github.com/gin-gonic/gin"
)

// AuthChatHandler validates a JWT from the query param before upgrading.
// Browsers cannot set custom headers on WebSocket connections — query params
// are the standard workaround. Keep tokens short-lived (< 60 s) when using
// them in URLs to reduce exposure in server logs.
func AuthChatHandler(hub *Hub, validateToken func(string) (string, error)) gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.Query("token")
        if token == "" {
            c.JSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
            return
        }

        userID, err := validateToken(token)
        if err != nil {
            slog.Warn("ws auth failed", "err", err)
            c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
            return
        }

        conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
        if err != nil {
            slog.Error("ws upgrade failed", "err", err)
            return
        }

        _ = userID // attach to Client struct as needed

        client := &Client{
            hub:  hub,
            conn: conn,
            send: make(chan []byte, 256),
        }
        hub.register <- client

        go client.writePump()
        go client.readPump()
    }
}
```

**Route registration — rate limiting applies to upgrade request:**

```go
wsGroup := r.Group("/ws")
wsGroup.Use(middleware.RateLimit())
{
    wsGroup.GET("/chat", ws.AuthChatHandler(hub, tokenSvc.ValidateToken))
}
```

**Alternative — `Sec-WebSocket-Protocol` header:** Some clients send the token as a subprotocol name. This avoids URL exposure but requires echoing the subprotocol in the upgrade response header, which is more complex. Query param is simpler for most use cases.

---

## Ping/Pong Keepalive

Without keepalive, idle connections time out silently at the load balancer or OS level. gorilla/websocket supports WebSocket ping/pong control frames natively.

The `writePump` ticker in `websocket-hub-and-client.md` already sends pings. Here is the pattern isolated for clarity:

```go
// In readPump — reset read deadline when a pong arrives
conn.SetReadDeadline(time.Now().Add(pongWait)) // initial deadline
conn.SetPongHandler(func(appData string) error {
    // Extend deadline: client is alive
    conn.SetReadDeadline(time.Now().Add(pongWait))
    return nil
})

// In writePump ticker case — send a ping
conn.SetWriteDeadline(time.Now().Add(writeWait))
if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
    return // connection broken; writePump exits, triggering cleanup
}
```

**Deadline chain:** writePump sends ping every `pingPeriod` (54 s). Client must reply with pong before `pongWait` (60 s) expires. If pong never arrives, `ReadMessage` returns a deadline error and `readPump` exits, triggering `hub.unregister` and `conn.Close()`.

**Why `pingPeriod < pongWait`:** The ping must be sent before the read deadline fires. Using `(pongWait * 9) / 10` gives a 10% safety margin.
