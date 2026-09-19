# WebSocket — Testing

See also: `websocket-setup-and-echo.md`, `websocket-hub-and-client.md`, `websocket-auth-and-keepalive.md`, `websocket-shutdown-and-messages.md`

## Testing WebSocket Handlers

WebSocket tests require a real TCP server — `httptest.NewServer` + `websocket.DefaultDialer.Dial`. You cannot use `httptest.NewRecorder` because the upgrade requires a hijackable connection.

```go
// internal/ws/echo_test.go
package ws_test

import (
    "net/http/httptest"
    "strings"
    "testing"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/gorilla/websocket"

    ws "github.com/yourorg/yourapp/internal/ws"
)

func newTestServer(t *testing.T) *httptest.Server {
    t.Helper()
    gin.SetMode(gin.TestMode)
    r := gin.New()
    r.GET("/ws/echo", ws.EchoHandler)
    return httptest.NewServer(r)
}

func wsURL(srv *httptest.Server, path string) string {
    return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

func TestEchoHandler_RoundTrip(t *testing.T) {
    srv := newTestServer(t)
    defer srv.Close()

    dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
    conn, resp, err := dialer.Dial(wsURL(srv, "/ws/echo"), nil)
    if err != nil {
        t.Fatalf("dial failed: %v (status %d)", err, resp.StatusCode)
    }
    defer conn.Close()

    want := "hello"
    if err := conn.WriteMessage(websocket.TextMessage, []byte(want)); err != nil {
        t.Fatalf("write failed: %v", err)
    }

    conn.SetReadDeadline(time.Now().Add(3 * time.Second))
    _, got, err := conn.ReadMessage()
    if err != nil {
        t.Fatalf("read failed: %v", err)
    }

    if string(got) != want {
        t.Errorf("got %q, want %q", got, want)
    }
}

func TestEchoHandler_ClientClose(t *testing.T) {
    srv := newTestServer(t)
    defer srv.Close()

    conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv, "/ws/echo"), nil)
    if err != nil {
        t.Fatalf("dial failed: %v", err)
    }

    err = conn.WriteMessage(
        websocket.CloseMessage,
        websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
    )
    if err != nil {
        t.Fatalf("close write failed: %v", err)
    }

    conn.SetReadDeadline(time.Now().Add(2 * time.Second))
    _, _, err = conn.ReadMessage()
    if err == nil {
        t.Error("expected error after close, got nil")
    }
}

func TestChatHandler_Broadcast(t *testing.T) {
    gin.SetMode(gin.TestMode)
    hub := ws.NewHub()
    go hub.Run()
    defer hub.Shutdown()

    r := gin.New()
    r.GET("/ws/chat", ws.ChatHandler(hub))
    srv := httptest.NewServer(r)
    defer srv.Close()

    dial := func() *websocket.Conn {
        conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv, "/ws/chat"), nil)
        if err != nil {
            t.Fatalf("dial failed: %v", err)
        }
        return conn
    }

    c1 := dial()
    c2 := dial()
    defer c1.Close()
    defer c2.Close()

    time.Sleep(50 * time.Millisecond) // let register events process

    want := "broadcast-test"
    if err := c1.WriteMessage(websocket.TextMessage, []byte(want)); err != nil {
        t.Fatalf("c1 write failed: %v", err)
    }

    for _, conn := range []*websocket.Conn{c1, c2} {
        conn.SetReadDeadline(time.Now().Add(2 * time.Second))
        _, msg, err := conn.ReadMessage()
        if err != nil {
            t.Fatalf("read failed: %v", err)
        }
        if string(msg) != want {
            t.Errorf("got %q, want %q", msg, want)
        }
    }
}
```

**Key testing patterns:**
- Use `httptest.NewServer` (not `httptest.NewRecorder`) — WebSocket requires a hijackable TCP connection.
- Convert `http://` to `ws://` with `strings.TrimPrefix`.
- Set `SetReadDeadline` in tests to prevent hangs on failure.
- Test both happy path and clean disconnect to verify server-side cleanup.
- `hub.Shutdown()` in defer ensures test goroutines exit cleanly.
