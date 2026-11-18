package idempotency

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Opt-in only: never read application Redis config or contact a service host.
// Supply a disposable loopback Redis (or miniredis with Lua) explicitly.
func TestRedisIntegration(t *testing.T) {
	addr := os.Getenv("MONKEYS_IDEMPOTENCY_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set MONKEYS_IDEMPOTENCY_TEST_REDIS_ADDR to a disposable loopback Redis")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("integration Redis must be an explicit loopback IP:port")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	s := &redisStore{client: client}
	key := "idempotency:test:" + uuid.NewString()
	defer client.Del(context.Background(), key)
	before := record{Fingerprint: "fingerprint", Owner: "original-owner"}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, e := s.reserve(ctx, key, before, time.Minute)
			if e != nil {
				t.Error(e)
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("SET NX admitted %d owners", wins.Load())
	}
	ttl, err := client.PTTL(ctx, key).Result()
	if err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("missing bounded TTL: %s %v", ttl, err)
	}
	after := before
	after.Status = 201
	after.Body = []byte(`{"id":"same-resource"}`)
	if err = s.complete(ctx, key, before, after); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, key)
	if err != nil || loaded.Status != 201 || string(loaded.Body) != `{"id":"same-resource"}` {
		t.Fatalf("replay not persisted: %+v %v", loaded, err)
	}
	remaining, err := client.PTTL(ctx, key).Result()
	if err != nil || remaining <= 0 || remaining > ttl {
		t.Fatalf("completion changed expiry: before=%v after=%v err=%v", ttl, remaining, err)
	}
	// Simulate expiration followed by a new reservation. A late old worker must
	// not overwrite that owner's record or refresh its TTL.
	if err = client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	next := record{Fingerprint: "new-body", Owner: "new-owner"}
	if ok, e := s.reserve(ctx, key, next, time.Minute); e != nil || !ok {
		t.Fatalf("new owner: %v %v", ok, e)
	}
	if err = s.complete(ctx, key, before, after); err == nil {
		t.Fatal("late old owner overwrote new reservation")
	}
	loaded, err = s.load(ctx, key)
	if err != nil || loaded.Owner != "new-owner" || loaded.Status != 0 {
		t.Fatalf("new reservation corrupted: %+v %v", loaded, err)
	}

	// Separate client pools model separate gateway processes. The actual Redis
	// reservation, load and Lua completion participate in HTTP concurrency.
	other := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	defer other.Close()
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	handler := func(c *gin.Context) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		c.JSON(201, gin.H{"id": "created-once"})
	}
	one, two := testRouter(New(client), handler), testRouter(New(other), handler)
	intent := uuid.NewString()
	account := uuid.NewString()
	done := make(chan struct{}, 1)
	go func() {
		defer func() { done <- struct{}{} }()
		w := request(one, "POST", "/api/v1/events", account, intent, `{}`)
		if w.Code != 201 {
			t.Errorf("first create: %d %s", w.Code, w.Body)
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first create did not enter")
	}
	assertCode(t, request(two, "POST", "/api/v1/events", account, intent, `{}`), 409, "idempotency_in_progress")
	unblock()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("create did not finish")
	}
	replay := request(two, "POST", "/api/v1/events", account, intent, `{}`)
	if replay.Code != 201 || replay.Header().Get("Idempotency-Replayed") != "true" || !strings.Contains(replay.Body.String(), "created-once") || calls.Load() != 1 {
		t.Fatalf("distributed replay: %d %s calls=%d", replay.Code, replay.Body, calls.Load())
	}
	redisKey := "idempotency:v1:" + digest([]string{account, "POST", "/api/v1/events", "/api/v1/events", intent})
	defer client.Del(context.Background(), redisKey)
	assertCode(t, request(two, "POST", "/api/v1/events", account, intent, `{"changed":true}`), 409, "idempotency_key_reused")
}

func TestRedisNetworkFailureNeverRunsCreation(t *testing.T) {
	// A local socket is explicitly closed; no configured or production address is used.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1, DialTimeout: 50 * time.Millisecond, ReadTimeout: 50 * time.Millisecond, WriteTimeout: 50 * time.Millisecond})
	defer client.Close()
	var calls atomic.Int32
	r := testRouter(New(client), func(c *gin.Context) { calls.Add(1); c.JSON(201, gin.H{"id": "unexpected"}) })
	assertCode(t, request(r, "POST", "/api/v1/events", "alice", "same", `{}`), 503, "idempotency_unavailable")
	if calls.Load() != 0 {
		t.Fatal("creation ran during Redis outage")
	}
}
