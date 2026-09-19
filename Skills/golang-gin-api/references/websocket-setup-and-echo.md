# WebSocket — Upgrader Setup & Basic Echo Handler

See also: `websocket-hub-and-client.md`, `websocket-auth-and-keepalive.md`, `websocket-shutdown-and-messages.md`, `websocket-testing.md`

## Overview

Gin handles the HTTP upgrade handshake through its normal routing and middleware chain. After upgrade, all communication is raw WebSocket — Gin is no longer involved.

## Upgrader Setup

`websocket.Upgrader` converts an HTTP connection to a WebSocket connection. Configure it once at the package level — it is safe to use concurrently.

```go
// internal/ws/upgrader.go
package ws

import (
    "net/http"

    "github.com/gorilla/websocket"
)

// upgrader is the shared upgrader for all WebSocket endpoints.
// ReadBufferSize / WriteBufferSize tune internal I/O buffers — not message
// size limits. 1024 bytes is a safe default for typical JSON payloads.
var upgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,

    // CheckOrigin gates which origins may open a WebSocket connection.
    // Never return true unconditionally in production — that allows
    // cross-site WebSocket hijacking (CSWSH).
    CheckOrigin: func(r *http.Request) bool {
        origin := r.Header.Get("Origin")
        if origin == "" {
            return true
        }
        allowed := map[string]bool{
            "https://example.com":     true,
            "https://app.example.com": true,
        }
        return allowed[origin]
    },
}
```

**Why `CheckOrigin` matters:** Browsers automatically send the `Origin` header on WebSocket requests. Without validation, any website can open a WebSocket to your server using the visitor's credentials (cookies/sessions).

**`SetReadLimit`** — set a per-connection message size limit to prevent memory exhaustion:

```go
// After upgrading, before the read loop:
conn.SetReadLimit(512 * 1024) // 512 KB max per message
```

---

## Basic Echo Handler

The simplest handler: upgrade the connection, read messages in a loop, write each one back.

```go
// internal/ws/echo.go
package ws

import (
    "log/slog"

    "github.com/gin-gonic/gin"
    "github.com/gorilla/websocket"
)

// EchoHandler upgrades the HTTP connection and echoes every message back.
func EchoHandler(c *gin.Context) {
    conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil {
        // Upgrade already wrote a 400 response on failure; just log.
        slog.Error("websocket upgrade failed", "err", err)
        return
    }
    defer conn.Close()

    conn.SetReadLimit(512 * 1024)

    for {
        msgType, msg, err := conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err,
                websocket.CloseGoingAway,
                websocket.CloseNormalClosure,
            ) {
                slog.Warn("websocket read error", "err", err)
            }
            return
        }

        if err := conn.WriteMessage(msgType, msg); err != nil {
            slog.Warn("websocket write error", "err", err)
            return
        }
    }
}
```

Register the handler like any Gin route — middleware runs during the HTTP upgrade request:

```go
r := gin.New()
r.Use(middleware.Logger(), middleware.Recovery())
r.GET("/ws/echo", ws.EchoHandler)
```

**Why `IsUnexpectedCloseError`:** When a client disconnects cleanly, gorilla/websocket returns `CloseGoingAway`. Logging that as an error is noise. `IsUnexpectedCloseError` filters out expected close codes so you only log genuine errors.
