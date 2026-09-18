package social_post

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type importVerifierStub struct {
	visible bool
}

func (v importVerifierStub) VerifySocialAssetReference(*gin.Context, string) bool {
	return v.visible
}

func TestVerifyImportSourceRejectsUnverifiableReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	if verifyImportSource(ctx, importVerifierStub{visible: false}, "cross-user-ref") {
		t.Fatal("unverifiable source asset was accepted")
	}
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unverifiable source asset returned %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestVerifyImportSourceAcceptsVisibleReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	if !verifyImportSource(ctx, importVerifierStub{visible: true}, "owned-ref") {
		t.Fatal("visible source asset was rejected")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("visible source asset wrote status %d, want untouched recorder status %d", recorder.Code, http.StatusOK)
	}
}
