package storage_v2

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"
)

func TestVerifySocialAssetReferenceAllowsPublicAsset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/media/import", nil)
	ctx.Set("accountId", "requester")
	svc := &Service{storageCli: &resolveStub{resp: &pb.ResolveAssetReadResp{AllowPublic: true}}}

	if !svc.VerifySocialAssetReference(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("public asset was rejected")
	}
}

func TestVerifySocialAssetReferenceRejectsNotVisibleAsset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/media/import", nil)
	ctx.Set("accountId", "requester")
	svc := &Service{storageCli: &resolveStub{resp: &pb.ResolveAssetReadResp{
		UnpublishedBlogIds: []string{"owner-blog"},
	}}}

	if svc.VerifySocialAssetReference(ctx, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Fatal("cross-user asset was accepted")
	}
}

func TestSocialAssetVisibleAllowsBlogAccessGrantedAsset(t *testing.T) {
	res := &pb.ResolveAssetReadResp{UnpublishedBlogIds: []string{"owner-blog"}}
	if !socialAssetVisible(res, true, func(blogID string) bool {
		return blogID == "owner-blog"
	}) {
		t.Fatal("asset visible through caller blog access was rejected")
	}
}
