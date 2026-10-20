package storage_v2

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	authpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	userpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const contentRequestID = "ba15d17e-172f-43a2-a28e-23aa7ee73b42"
const contentChecksum = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type contentAuthClient struct{ authpb.AuthServiceClient }

func (c *contentAuthClient) Validate(_ context.Context, req *authpb.ValidateRequest, _ ...grpc.CallOption) (*authpb.ValidateResponse, error) {
	if req.Token != "synthetic-token" {
		return nil, status.Error(codes.Unauthenticated, "fixture authentication rejected")
	}
	return &authpb.ValidateResponse{UserName: "reviewer", AccountId: "fixture-account", Role: constants.RoleAdmin}, nil
}

type contentUserClient struct {
	userpb.UserServiceClient
	request *userpb.VerificationRequest
	err     error
	calls   int
}

func (c *contentUserClient) GetMyVerification(_ context.Context, in *userpb.GetVerificationReq, _ ...grpc.CallOption) (*userpb.VerificationRequest, error) {
	c.calls++
	if in.RequestId != contentRequestID {
		return nil, status.Error(codes.NotFound, "fixture request missing")
	}
	return c.request, c.err
}

// Real MinIO SDK against an in-process synthetic S3 server, never a real bucket.
func contentFixture(t *testing.T, body []byte, objectStatus int, policy string) (*verificationHandler, *contentUserClient, *observer.ObservedLogs, *int) {
	t.Helper()
	reads := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			t.Errorf("document viewing attempted a storage mutation: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if _, ok := r.URL.Query()["policy"]; ok {
			if strings.HasPrefix(policy, "error:") {
				w.Header().Set("Content-Type", "application/xml")
				code := strings.TrimPrefix(policy, "error:")
				if code == "NoSuchBucketPolicy" {
					w.WriteHeader(http.StatusNotFound)
				} else {
					w.WriteHeader(http.StatusForbidden)
				}
				_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>private-storage-secret-fixture</Message></Error>", code)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(policy))
			return
		}
		if r.URL.Path == "/the-monkeys-verification/" || r.URL.Path == "/the-monkeys-verification" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/the-monkeys-verification/verifications/sha256/"+contentChecksum {
			t.Errorf("unexpected object path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		*reads++
		if objectStatus != http.StatusOK {
			code := "AccessDenied"
			if objectStatus == http.StatusNotFound {
				code = "NoSuchKey"
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(objectStatus)
			_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>private-storage-secret-fixture</Message></Error>", code)
			return
		}
		// An untrusted stored content type must never be used to serve active content.
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Header().Set("ETag", `"fixture-etag"`)
		w.Header().Set("Last-Modified", "Sun, 18 Oct 2026 20:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	mc, err := minio.New(u.Host, &minio.Options{Secure: false, Region: "us-east-1", Creds: credentials.NewStaticV4("fixture-access", "fixture-secret", "")})
	if err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zapcore.InfoLevel)
	users := &contentUserClient{request: &userpb.VerificationRequest{Id: contentRequestID, Username: "applicant", SelfieChecksum: contentChecksum}}
	return &verificationHandler{Service: &Service{mc: mc, log: zap.New(core).Sugar()}, userCli: users, bucket: "the-monkeys-verification"}, users, logs, reads
}

type contentResponseWriter struct {
	*httptest.ResponseRecorder
	mu          sync.Mutex
	deadlines   []time.Time
	writeEvents []string
	block       bool
	started     chan struct{}
	unblocked   chan struct{}
	startOnce   sync.Once
	unblockOnce sync.Once
}

func (w *contentResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	w.deadlines = append(w.deadlines, deadline)
	if deadline.IsZero() {
		w.writeEvents = append(w.writeEvents, "reset")
	} else {
		w.writeEvents = append(w.writeEvents, "deadline")
	}
	w.mu.Unlock()
	if !deadline.IsZero() && !deadline.After(time.Now()) {
		w.unblock()
	}
	return nil
}

func (w *contentResponseWriter) Flush() {
	w.mu.Lock()
	w.writeEvents = append(w.writeEvents, "flush")
	w.mu.Unlock()
	w.ResponseRecorder.Flush()
}

func requireFlushBeforeReset(t *testing.T, w *contentResponseWriter) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	flushed, reset := false, false
	for _, event := range w.writeEvents {
		if event == "flush" {
			if reset {
				t.Fatalf("flush after resetting deadline: %v", w.writeEvents)
			}
			flushed = true
		}
		if event == "reset" {
			if !flushed {
				t.Fatalf("reset before flushing response: %v", w.writeEvents)
			}
			reset = true
		}
	}
	if !flushed || !reset {
		t.Fatalf("missing flush/reset: %v", w.writeEvents)
	}
}

func (w *contentResponseWriter) unblock() { w.unblockOnce.Do(func() { close(w.unblocked) }) }

func (w *contentResponseWriter) Write(body []byte) (int, error) {
	if !w.block {
		return w.ResponseRecorder.Write(body)
	}
	w.startOnce.Do(func() { close(w.started) })
	<-w.unblocked
	return 0, os.ErrDeadlineExceeded
}

func contentContext(username, role, peer, kind string) (*gin.Context, *contentResponseWriter) {
	gin.SetMode(gin.TestMode)
	w := &contentResponseWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), unblocked: make(chan struct{})}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/storage/verifications/"+contentRequestID+"/"+kind+"/content", nil)
	c.Request.RemoteAddr = peer
	c.Params = gin.Params{{Key: "request_id", Value: contentRequestID}, {Key: "kind", Value: kind}}
	c.Set("userName", username)
	c.Set("user_role", role)
	return c, w
}

func TestVerificationContentBoundsAndResetsResponseWriteDeadline(t *testing.T) {
	h, _, _, _ := contentFixture(t, []byte("\x89PNG\r\n\x1a\nsynthetic-image"), 200, "")
	c, w := contentContext("reviewer", "Admin", "192.168.1.10:1234", "selfie")
	start := time.Now()
	h.GetVerificationAssetContent(c)
	requireFlushBeforeReset(t, w)
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.Flushed {
		t.Fatal("private response buffer was not flushed while the write deadline was active")
	}
	if len(w.deadlines) < 2 || w.deadlines[0].Before(start) || w.deadlines[0].After(start.Add(21*time.Second)) || !w.deadlines[len(w.deadlines)-1].IsZero() {
		t.Fatalf("missing bounded response deadline/reset: %v", w.deadlines)
	}
}

func TestVerificationContentErrorResponsesFlushBeforeDeadlineReset(t *testing.T) {
	for _, storageStatus := range []int{403, 404} {
		h, _, _, _ := contentFixture(t, []byte("fixture"), storageStatus, "")
		c, w := contentContext("reviewer", "Admin", "192.168.1.10:1234", "selfie")
		h.GetVerificationAssetContent(c)
		if w.Code < 400 {
			t.Fatal("expected controlled failure")
		}
		requireFlushBeforeReset(t, w)
	}
}

func TestVerificationContentAuthenticatedHTTPRoundTrip(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	h, users, _, _ := contentFixture(t, encoded.Bytes(), 200, "error:NoSuchBucketPolicy")
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	middleware := auth.InitAuthMiddleware(&auth.ServiceClient{Client: &contentAuthClient{}}, h.log)
	router.GET("/api/v2/storage/verifications/:request_id/:kind/content", middleware.AuthRequired, h.GetVerificationAssetContent)
	server := httptest.NewServer(router)
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v2/storage/verifications/"+contentRequestID+"/selfie/content", nil)
	request.Header.Set("Authorization", "Bearer synthetic-token")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !bytes.Equal(body, encoded.Bytes()) {
		t.Fatalf("authenticated download status=%d", response.StatusCode)
	}
	decoded, err := png.Decode(bytes.NewReader(body))
	if err != nil || decoded.Bounds().Dx() != 2 {
		t.Fatal("response is not the intact synthetic raster image")
	}
	request.Header.Del("Authorization")
	denied, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Body.Close()
	if denied.StatusCode != http.StatusUnauthorized || users.calls != 1 {
		t.Fatal("unauthenticated request reached private evidence lookup")
	}
}

func TestVerificationContentCancellationUnblocksStalledWrite(t *testing.T) {
	h, _, _, _ := contentFixture(t, []byte("\x89PNG\r\n\x1a\nsynthetic-image"), 200, "")
	c, w := contentContext("reviewer", "Admin", "192.168.1.10:1234", "selfie")
	w.block = true
	requestContext, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(requestContext)
	done := make(chan struct{})
	go func() { defer close(done); h.GetVerificationAssetContent(c) }()
	select {
	case <-w.started:
	case <-time.After(2 * time.Second):
		w.unblock()
		<-done
		t.Fatal("fixture did not reach response write")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		w.unblock()
		<-done
		t.Fatal("request cancellation did not interrupt the blocked response write")
	}
	requireFlushBeforeReset(t, w)
}

func TestVerificationContentPrivateStream(t *testing.T) {
	for _, role := range []string{constants.RoleAdmin, constants.RoleSupport, constants.RoleCommunity} {
		t.Run(role, func(t *testing.T) {
			body := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("synthetic-image"), 80)...)
			h, _, _, _ := contentFixture(t, body, http.StatusOK, "")
			c, w := contentContext("reviewer", role, "192.168.1.10:1234", "selfie")
			h.GetVerificationAssetContent(c)
			if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), body) {
				t.Fatalf("status=%d, body length=%d", w.Code, w.Body.Len())
			}
			for key, want := range map[string]string{"Content-Type": "image/png", "Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff"} {
				if w.Header().Get(key) != want {
					t.Errorf("%s=%q want %q", key, w.Header().Get(key), want)
				}
			}
			if w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "X-Amz-") {
				t.Fatal("must stream bytes, not redirect or issue signed URLs")
			}
		})
	}
}

func TestVerificationContentAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, user, role, peer, kind string
		want                         int
	}{
		{"anonymous", "", "", "192.168.1.10:1234", "selfie", 401},
		{"unrelated viewer", "stranger", "Viewer", "192.168.1.10:1234", "selfie", 403},
		{"public staff", "reviewer", "Admin", "203.0.113.10:1234", "selfie", 403},
		{"public staff forged forwarding", "reviewer", "Support", "203.0.113.10:1234", "selfie", 403},
		{"invalid kind", "reviewer", "Admin", "192.168.1.10:1234", "arbitrary", 400},
		{"missing document", "reviewer", "Admin", "192.168.1.10:1234", "id_back", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, reads := contentFixture(t, []byte("fixture"), http.StatusOK, "")
			c, w := contentContext(tc.user, tc.role, tc.peer, tc.kind)
			c.Request.Header.Set("X-Forwarded-For", "127.0.0.1")
			h.GetVerificationAssetContent(c)
			if w.Code != tc.want {
				t.Fatalf("status=%d want %d", w.Code, tc.want)
			}
			if *reads != 0 {
				t.Fatal("denied request read private object")
			}
		})
	}
}

func TestVerificationContentOwnerRemainsAllowed(t *testing.T) {
	body := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 1, 2, 3, 4, 5, 6}
	h, _, _, _ := contentFixture(t, body, http.StatusOK, "")
	c, w := contentContext("applicant", "Viewer", "203.0.113.10:1234", "selfie")
	h.GetVerificationAssetContent(c)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("owner status=%d", w.Code)
	}
}

func TestVerificationContentValidatesRequestAndStoredReference(t *testing.T) {
	for _, invalidID := range []bool{true, false} {
		h, users, _, reads := contentFixture(t, []byte("fixture"), 200, "")
		c, w := contentContext("reviewer", "Admin", "192.168.1.10:1234", "selfie")
		if invalidID {
			c.Params[0].Value = "not-a-request-id"
		} else {
			users.request.SelfieChecksum = "../../other-bucket/private"
		}
		h.GetVerificationAssetContent(c)
		want := 503
		if invalidID {
			want = 400
		}
		if w.Code != want || *reads != 0 {
			t.Fatalf("status=%d want=%d reads=%d", w.Code, want, *reads)
		}
		if invalidID && users.calls != 0 {
			t.Fatal("invalid request ID reached user service")
		}
	}
}

func TestVerificationContentRejectsOversizedDocument(t *testing.T) {
	body := make([]byte, constants.VerificationAssetMaxBytes+1)
	copy(body, []byte("\x89PNG\r\n\x1a\n"))
	h, _, _, _ := contentFixture(t, body, 200, "")
	c, w := contentContext("reviewer", "Admin", "192.168.1.10:1234", "selfie")
	h.GetVerificationAssetContent(c)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want=413", w.Code)
	}
}

func TestVerificationContentStorageFailuresAreSafe(t *testing.T) {
	for _, tc := range []struct {
		name                string
		storageStatus, want int
		body                []byte
		policy              string
	}{
		{"missing object", 404, 404, []byte("fixture"), ""},
		{"storage permission", 403, 503, []byte("fixture"), ""},
		{"active content", 200, 415, []byte("<html>private-storage-secret-fixture</html>"), ""},
		{"public bucket rejected", 200, 503, []byte("fixture"), `{"Statement":[{"Effect":"Allow"}]}`},
		{"policy cannot be checked", 200, 503, []byte("fixture"), "error:AccessDenied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, logs, _ := contentFixture(t, tc.body, tc.storageStatus, tc.policy)
			c, w := contentContext("reviewer", "Admin", "192.168.1.10:1234", "selfie")
			h.GetVerificationAssetContent(c)
			if w.Code != tc.want {
				t.Fatalf("status=%d want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private-storage-secret-fixture") || strings.Contains(fmt.Sprint(logs.All()), "private-storage-secret-fixture") {
				t.Fatal("private storage details leaked")
			}
		})
	}
}

func TestVerificationContentUnavailableRequestDoesNotReadStorage(t *testing.T) {
	h, users, _, reads := contentFixture(t, []byte("fixture"), 200, "")
	users.err = status.Error(codes.NotFound, "private user service error")
	c, w := contentContext("reviewer", "Admin", "192.168.1.10:1234", "selfie")
	h.GetVerificationAssetContent(c)
	if w.Code != 404 || *reads != 0 {
		t.Fatalf("status=%d reads=%d", w.Code, *reads)
	}
}

func TestVerificationLegacySignedURLStillAvailable(t *testing.T) {
	h, _, _, _ := contentFixture(t, []byte("fixture"), 200, "")
	c, w := contentContext("reviewer", "Admin", "192.168.1.10:1234", "selfie")
	h.GetVerificationAssetURL(c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"expires_in_seconds":600`) || !strings.Contains(w.Body.String(), "X-Amz-Signature") {
		t.Fatalf("legacy URL contract changed: status=%d", w.Code)
	}
}
