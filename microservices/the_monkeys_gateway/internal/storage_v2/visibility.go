package storage_v2

import (
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"
	"github.com/the-monkeys/the_monkeys/constants"
)

func blogStatusAllowsPublic(status string) bool {
	return status == constants.BlogStatusPublished
}

func decideAssetRead(allowPublic, verificationOnly, hasJWT, anyDraftAccess bool) bool {
	if verificationOnly {
		return false
	}
	if allowPublic {
		return true
	}
	return hasJWT && anyDraftAccess
}

// allowPostFileRead is the unpublished 404 gate for GET/HEAD/list. Denied
// reads are always 404 (never 403) so draft existence is not leaked.
func allowPostFileRead(res *pb.ResolveAssetReadResp, hasJWT, hasBlogAccess bool) bool {
	if res == nil {
		return false
	}
	anyDraft := false
	if !res.GetAllowPublic() && hasJWT {
		anyDraft = hasBlogAccess
	}
	return decideAssetRead(res.GetAllowPublic(), res.GetVerificationOnly(), hasJWT, anyDraft)
}

func unpublishedPostReadStatus(allowPublic, hasJWT, hasBlogAccess bool) int {
	res := &pb.ResolveAssetReadResp{AllowPublic: allowPublic}
	if allowPostFileRead(res, hasJWT, hasBlogAccess) {
		return http.StatusOK
	}
	return http.StatusNotFound
}

func checksumFromAssetObjectName(objectName string) (string, bool) {
	base := path.Base(objectName)
	sum := strings.TrimSuffix(base, path.Ext(base))
	if len(sum) != 64 {
		return "", false
	}
	for i := 0; i < len(sum); i++ {
		c := sum[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return "", false
		}
	}
	return strings.ToLower(sum), true
}

func (s *Service) abortNotFound(ctx *gin.Context) {
	ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "file not found"})
}

func (s *Service) gatePostFileRead(ctx *gin.Context) bool {
	blogID := ctx.Param("id")
	if s.storageCli == nil {
		s.abortNotFound(ctx)
		return false
	}
	res, err := s.storageCli.ResolveAssetRead(ctx.Request.Context(), &pb.ResolveAssetReadReq{BlogId: blogID})
	if err != nil || res == nil {
		s.abortNotFound(ctx)
		return false
	}
	hasJWT := ctx.GetString("accountId") != ""
	hasAccess := false
	if !res.GetAllowPublic() && hasJWT && s.authz != nil {
		hasAccess = s.authz.HasBlogAccess(ctx, blogID)
	}
	if !allowPostFileRead(res, hasJWT, hasAccess) {
		s.abortNotFound(ctx)
		return false
	}
	return true
}

func (s *Service) gateAssetFileRead(ctx *gin.Context, objectName string) bool {
	sum, ok := checksumFromAssetObjectName(objectName)
	if !ok || s.storageCli == nil {
		s.abortNotFound(ctx)
		return false
	}
	res, err := s.storageCli.ResolveAssetRead(ctx.Request.Context(), &pb.ResolveAssetReadReq{Checksum: sum})
	if err != nil || res == nil || res.GetVerificationOnly() {
		s.abortNotFound(ctx)
		return false
	}
	if res.GetAllowPublic() {
		return true
	}
	hasJWT := ctx.GetString("accountId") != ""
	if !hasJWT || s.authz == nil {
		s.abortNotFound(ctx)
		return false
	}
	for _, id := range res.GetUnpublishedBlogIds() {
		if s.authz.HasBlogAccess(ctx, id) {
			return true
		}
	}
	s.abortNotFound(ctx)
	return false
}

func cacheControlForPublishedBlog(allowPublic bool) string {
	if allowPublic {
		return "public, max-age=31536000"
	}
	return "private, no-store"
}

func (s *Service) postUploadCacheControl(ctx *gin.Context, blogID string) string {
	if s.storageCli == nil {
		return cacheControlForPublishedBlog(false)
	}
	res, err := s.storageCli.ResolveAssetRead(ctx.Request.Context(), &pb.ResolveAssetReadReq{BlogId: blogID})
	if err != nil || res == nil {
		return cacheControlForPublishedBlog(false)
	}
	return cacheControlForPublishedBlog(res.GetAllowPublic())
}
