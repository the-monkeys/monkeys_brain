package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	userpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
)

func (asc *AdminServiceClient) ListBlogs(ctx *gin.Context) {
	limit, offset := queryLimitOffset(ctx)
	res, err := asc.Client.AdminListBlogs(ctx.Request.Context(), &userpb.AdminListBlogsReq{
		Limit: limit, Offset: offset, Query: strings.TrimSpace(ctx.Query("q")), Status: strings.TrimSpace(ctx.Query("status")),
	})
	if asc.failRPC(ctx, err, "list blogs") {
		return
	}
	blogs := make([]gin.H, 0, len(res.Blogs))
	for _, b := range res.Blogs {
		created := ""
		if b.CreatedAt != nil {
			created = b.CreatedAt.AsTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		blogs = append(blogs, gin.H{"blog_id": b.BlogId, "username": b.Username, "status": b.Status, "created_at": created})
	}
	ctx.JSON(http.StatusOK, gin.H{"blogs": blogs, "total": res.Total})
}

func (asc *AdminServiceClient) ListOrphanBlogs(ctx *gin.Context) {
	limit, offset := queryLimitOffset(ctx)
	es, err := asc.Blogs.AdminListESBlogIds(ctx.Request.Context(), &blogpb.AdminListESBlogIdsReq{Limit: limit, Offset: offset})
	if asc.failRPC(ctx, err, "list es blogs") {
		return
	}
	missing, err := asc.Client.AdminMissingBlogIds(ctx.Request.Context(), &userpb.AdminMissingBlogIdsReq{BlogIds: es.BlogIds})
	if asc.failRPC(ctx, err, "compare blog ids") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"blog_ids": missing.GetBlogIds(), "es_page_total": es.Total})
}

func (asc *AdminServiceClient) UnpublishBlog(ctx *gin.Context) {
	id := ctx.Param("blog_id")
	_, err := asc.Blogs.MoveBlogToDraftStatus(ctx.Request.Context(), blogReq(ctx, id))
	if asc.failRPC(ctx, err, "unpublish blog") {
		return
	}
	asc.audit(ctx, "blog.unpublish", "blog", id, nil)
	ctx.JSON(http.StatusOK, gin.H{"message": "unpublished", "blog_id": id})
}

func (asc *AdminServiceClient) DeleteBlog(ctx *gin.Context) {
	id := strings.TrimSpace(ctx.Param("blog_id"))
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "blog_id required"})
		return
	}
	_, err := asc.Blogs.DeleteABlogByBlogId(ctx.Request.Context(), &blogpb.DeleteBlogReq{
		BlogId: id,
		Ip:     ctx.ClientIP(),
		Client: "admin",
	})
	if asc.failRPC(ctx, err, "delete blog") {
		return
	}
	asc.audit(ctx, "blog.delete", "blog", id, nil)
	ctx.JSON(http.StatusOK, gin.H{"message": "deleted", "blog_id": id})
}
