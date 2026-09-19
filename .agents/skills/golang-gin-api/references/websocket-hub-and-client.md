# WebSocket — Hub Pattern & Client readPump/writePump

See also: `websocket-setup-and-echo.md`, `websocket-auth-and-keepalive.md`, `websocket-shutdown-and-messages.md`, `websocket-testing.md`, `websocket-chat-handler.md`

## Hub Pattern

For broadcasting to multiple clients (chat rooms, live feeds), a central hub serializes all register/unregister/broadcast operations through a single goroutine — avoiding mutex-protected maps.

```go
// internal/ws/hub.go
package ws

type Hub struct {
    clients    map[*Client]bool
    broadcast  chan []byte
    register   chan *Client
    unregister chan *Client
}

func NewHub() *Hub {
    return &Hub{
        clients:    make(map[*Client]bool),
        broadcast:  make(chan []byte, 256),
        register:   make(chan *Client),
        unregister: make(chan *Client),
    }
}

// Run processes hub events. Call this in a dedicated goroutine.
func (h *Hub) Run() {
    for {
        select {
        case client := <-h.register:
            h.clients[client] = true

        case client := <-h.unregister:
            if _, ok := h.clients[client]; ok {
                delete(h.clients, client)
                close(client.send)
            }

        case message := <-h.broadcast:
            for client := range h.clients {
                select {
                case client.send <- message:
                default:
                    // send buffer full — client is too slow; drop and disconnect.
                    close(client.send)
                    delete(h.clients, client)
                }
            }
        }
    }
}
```

**Why channel-based (not mutex):** The hub goroutine owns the `clients` map exclusively. No locking needed. The `default` branch in broadcast prevents a slow client from blocking the entire broadcast loop.

---

## Client Struct with readPump / writePump

Two goroutines per client: `readPump` reads from the WebSocket, `writePump` writes to it. This avoids concurrent writes to `*websocket.Conn` (gorilla/websocket does not allow them).

```go
// internal/ws/client.go
package ws

import (
    "log/slog"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/gorilla/websocket"
)

const (
    writeWait      = 10 * time.Second
    pongWait       = 60 * time.Second
    pingPeriod     = (pongWait * 9) / 10
    maxMessageSize = 512 * 1024
)

type Client struct {
    hub  *Hub
    conn *websocket.Conn
    send chan []byte
}

func (cl *Client) readPump() {
    defer func() {
        cl.hub.unregister <- cl
        cl.conn.Close()
    }()

    cl.conn.SetReadLimit(maxMessageSize)
    cl.conn.SetReadDeadline(time.Now().Add(pongWait))
    cl.conn.SetPongHandler(func(string) error {
        cl.conn.SetReadDeadline(time.Now().Add(pongWait))
        return nil
    })

    for {
        _, msg, err := cl.conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err,
                websocket.CloseGoingAway,
                websocket.CloseNormalClosure,
            ) {
                slog.Warn("ws read error", "err", err)
            }
            return
        }
        cl.hub.broadcast <- msg
    }
}

func (cl *Client) writePump() {
    ticker := time.NewTicker(pingPeriod)
    defer func() {
        ticker.Stop()
        cl.conn.Close()
    }()

    for {
        select {
        case msg, ok := <-cl.send:
            cl.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if !ok {
                cl.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            if err := cl.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
                slog.Warn("ws write error", "err", err)
                return
            }

        case <-ticker.C:
            cl.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if err := cl.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
                return
            }
        }
    }
}

```

See also: `websocket-chat-handler.md` for `ChatHandler` wiring.
