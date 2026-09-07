package storage_v2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"
	"google.golang.org/grpc"
)

type resolveStub struct {
	pb.UploadBlogFileClient
	resp *pb.ResolveAssetReadResp
}

func (r *resolveStub) ResolveAssetRead(ctx context.Context, in *pb.ResolveAssetReadReq, opts ...grpc.CallOption) (*pb.ResolveAssetReadResp, error) {
	return r.resp, nil
}

func testPostCtx(method, path, blogID, fileName, accountID string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil)
	c.Params = gin.Params{{Key: "id", Value: blogID}}
	if fileName != "" {
		c.Params = append(c.Params, gin.Param{Key: "fileName", Value: fileName})
	}
	if accountID != "" {
		c.Set("accountId", accountID)
	}
	return c, w
}

func TestHeadAndListUnpublishedAnonymous404(t *testing.T) {
	s := &Service{storageCli: &resolveStub{resp: &pb.ResolveAssetReadResp{AllowPublic: false}}}

	c, w := testPostCtx(http.MethodHead, "/api/v2/storage/posts/draft/a.jpg", "draft", "a.jpg", "")
	s.HeadPostFile(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("HEAD unpublished anonymous got %d want 404", w.Code)
	}

	c, w = testPostCtx(http.MethodGet, "/api/v2/storage/posts/draft", "draft", "", "")
	s.ListPostFiles(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("list unpublished anonymous got %d want 404", w.Code)
	}
}

func TestHeadAndListUnpublishedJWTWithoutAccess404(t *testing.T) {
	s := &Service{storageCli: &resolveStub{resp: &pb.ResolveAssetReadResp{AllowPublic: false}}}
	c, w := testPostCtx(http.MethodHead, "/api/v2/storage/posts/draft/a.jpg", "draft", "a.jpg", "acct-1")
	s.HeadPostFile(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("HEAD stranger JWT got %d want 404 not 403", w.Code)
	}
	c, w = testPostCtx(http.MethodGet, "/api/v2/storage/posts/draft", "draft", "", "acct-1")
	s.ListPostFiles(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("list stranger JWT got %d want 404 not 403", w.Code)
	}
}

func TestHeadListAndGetShareGate(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, name := range []string{"func (s *Service) HeadPostFile", "func (s *Service) ListPostFiles", "func (s *Service) GetPostFile"} {
		start := strings.Index(text, name)
		if start < 0 {
			t.Fatalf("missing %s", name)
		}
		rest := text[start:]
		end := strings.Index(rest[1:], "\nfunc ")
		body := rest[:end+1]
		if !strings.Contains(body, "gatePostFileRead") {
			t.Fatalf("%s must use gatePostFileRead", name)
		}
	}
	if !strings.Contains(text, `v2.HEAD("/posts/:id/:fileName", mw.AuthOptional, svc.HeadPostFile)`) {
		t.Fatal("HEAD must be AuthOptional so unpublished anonymous is 404 not 401")
	}
	if !strings.Contains(text, `v2.GET("/posts/:id", mw.AuthOptional, svc.ListPostFiles)`) {
		t.Fatal("list must be AuthOptional so unpublished anonymous is 404 not 401")
	}
}
