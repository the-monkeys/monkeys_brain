package blogacl

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"github.com/the-monkeys/the_monkeys/common/audience"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func isActiveGroupMember(authz *grouppb.AuthorizeGroupResp) bool {
	return authz != nil && authz.GetIsMember() && authz.GetMemberStatus() == "active"
}

func CanViewPublishedDoc(doc map[string]interface{}, viewerAccountID string, memberActive bool) bool {
	return audience.CanRead(audience.DocAudience(doc), audience.OwnerAccountID(doc), viewerAccountID, memberActive)
}

type Checker struct {
	Blogs  blogpb.BlogServiceClient
	Groups grouppb.GroupServiceClient
	Log    *zap.SugaredLogger
}

func (c *Checker) CanViewPublished(ctx *gin.Context, doc map[string]interface{}, viewerAccountID string) bool {
	if CanViewPublishedDoc(doc, viewerAccountID, false) {
		return true
	}
	slug := audience.DocGroupSlug(doc)
	if slug == "" || c == nil || c.Groups == nil {
		return false
	}
	authz, err := c.Groups.Authorize(ctx.Request.Context(), &grouppb.AuthorizeGroupReq{
		AccountId: viewerAccountID,
		GroupSlug: slug,
	})
	if err != nil {
		if c.Log != nil {
			c.Log.Errorf("authorize group %s for blog read: %v", slug, err)
		}
		return false
	}
	return CanViewPublishedDoc(doc, viewerAccountID, isActiveGroupMember(authz))
}

func (c *Checker) RequireCanViewPublished(ctx *gin.Context, blogID string) bool {
	if c == nil || c.Blogs == nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
		return false
	}
	resp, err := c.Blogs.GetBlog(context.Background(), &blogpb.BlogReq{
		BlogId:    blogID,
		AccountId: ctx.GetString("accountId"),
		IsDraft:   false,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "blog not found"})
			return false
		}
		if c.Log != nil {
			c.Log.Errorf("load blog %s for ACL: %v", blogID, err)
		}
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
		return false
	}
	var doc map[string]interface{}
	if resp == nil || len(resp.Value) == 0 {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "blog not found"})
		return false
	}
	if err := json.Unmarshal(resp.Value, &doc); err != nil || doc == nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "blog not found"})
		return false
	}
	if !c.CanViewPublished(ctx, doc, ctx.GetString("accountId")) {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "blog not found"})
		return false
	}
	return true
}
