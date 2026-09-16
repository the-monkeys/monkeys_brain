# WebSocket — Graceful Shutdown & JSON Messages

See also: `websocket-setup-and-echo.md`, `websocket-hub-and-client.md`, `websocket-auth-and-keepalive.md`, `websocket-testing.md`

## Graceful Shutdown

On server shutdown, close all WebSocket connections cleanly so clients can reconnect rather than hang.

```go
// internal/ws/hub.go — add shutdown support
type Hub struct {
    clients    map[*Client]bool
    broadcast  chan []byte
    register   chan *Client
    unregister chan *Client
    shutdown   chan struct{}
}

func NewHub() *Hub {
    return &Hub{
        clients:    make(map[*Client]bool),
        broadcast:  make(chan []byte, 256),
        register:   make(chan *Client),
        unregister: make(chan *Client),
        shutdown:   make(chan struct{}),
    }
}

func (h *Hub) Run() {
    for {
        select {
        case <-h.shutdown:
            for client := range h.clients {
                client.conn.WriteMessage(
                    websocket.CloseMessage,
                    websocket.FormatCloseMessage(websocket.CloseServiceRestart, "server shutdown"),
                )
                close(client.send)
                delete(h.clients, client)
            }
            return

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
                    close(client.send)
                    delete(h.clients, client)
                }
            }
        }
    }
}

func (h *Hub) Shutdown() { close(h.shutdown) }
```

Wire into server shutdown:

```go
hub := ws.NewHub()
go hub.Run()

srv := &http.Server{Addr: ":8080", Handler: r}

quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit

hub.Shutdown() // close WebSocket connections first

ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
srv.Shutdown(ctx)
```

**Why close WebSocket before HTTP shutdown:** `srv.Shutdown` stops accepting new requests and waits for in-flight HTTP handlers. WebSocket handlers return immediately after spawning goroutines — those goroutines run outside Gin's request lifecycle. Shutting the hub first ensures client goroutines exit before the process terminates.

---

## JSON Messages

Use typed message envelopes with `ReadJSON`/`WriteJSON` to make the wire protocol explicit.

```go
// internal/ws/message.go
package ws

import (
    "log/slog"
    "time"

    "github.com/gorilla/websocket"
)

type MessageType string

const (
    MsgChat   MessageType = "chat"
    MsgSystem MessageType = "system"
    MsgError  MessageType = "error"
)

type Envelope struct {
    Type    MessageType `json:"type"`
    Payload any         `json:"payload"`
    SentAt  time.Time   `json:"sent_at"`
}

type ChatPayload struct {
    UserID  string `json:"user_id"`
    Content string `json:"content"`
}

func readJSON(conn *websocket.Conn, dst any) error {
    conn.SetReadDeadline(time.Now().Add(pongWait))
    return conn.ReadJSON(dst)
}

func writeJSON(conn *websocket.Conn, src any) error {
    conn.SetWriteDeadline(time.Now().Add(writeWait))
    return conn.WriteJSON(src)
}
```

**Why `any` in `Envelope.Payload`:** The envelope type is fixed, but the payload varies per message type. Unmarshal into `Envelope` first, then use `json.Unmarshal` on the re-encoded payload for strict typing:

```go
import "encoding/json"

raw, err := json.Marshal(env.Payload)
if err != nil { return }
var chat ChatPayload
if err := json.Unmarshal(raw, &chat); err != nil {
    slog.Warn("invalid chat payload", "err", err)
    return
}
```
