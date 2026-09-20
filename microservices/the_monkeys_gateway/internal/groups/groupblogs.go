package groups

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func isActiveMember(authz *pb.AuthorizeGroupResp) bool {
	return authz != nil && authz.GetIsMember() && authz.GetMemberStatus() == "active"
}

// ListGroupBlogs returns published blogs attached to the group.
// Private and unlisted groups 404 for anyone who is not an active member,
// same as a missing group. Strangers on a public group see only public posts.
func (gsc *GroupServiceClient) ListGroupBlogs(ctx *gin.Context) {
	slug := ctx.Param("slug")
	if gsc.Client == nil || gsc.Blogs == nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
		return
	}

	authz, err := gsc.Client.Authorize(ctx.Request.Context(), &pb.AuthorizeGroupReq{
		AccountId: accountID(ctx),
		GroupSlug: slug,
	})
	if err != nil {
		gsc.fail(ctx, err, "list group blogs")
		return
	}
	if authz == nil || !authz.GetGroupExists() {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	active := isActiveMember(authz)
	vis := authz.GetGroupVisibility()
	if !active && (vis == "private" || vis == "unlisted" || authz.GetGroupStatus() != "published") {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	limit, offset := page(ctx)
	stream, err := gsc.Blogs.GetBlogs(ctx.Request.Context(), &blogpb.GetBlogsReq{
		GroupSlug:        slug,
		IncludeGroupOnly: active,
		Limit:            limit,
		Offset:           offset,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			ctx.JSON(http.StatusOK, gin.H{"blogs": []any{}})
			return
		}
		gsc.log.Errorw("list group blogs failed", "slug", slug, "err", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
		return
	}

	var allBlogs []map[string]interface{}
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			gsc.log.Errorw("list group blogs stream failed", "slug", slug, "err", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
			return
		}
		var blogs []map[string]interface{}
		if err := json.Unmarshal(msg.Value, &blogs); err != nil {
			gsc.log.Errorw("list group blogs unmarshal failed", "slug", slug, "err", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
			return
		}
		allBlogs = append(allBlogs, blogs...)
	}
	if allBlogs == nil {
		allBlogs = []map[string]interface{}{}
	}
	ctx.JSON(http.StatusOK, gin.H{"blogs": allBlogs})
}
