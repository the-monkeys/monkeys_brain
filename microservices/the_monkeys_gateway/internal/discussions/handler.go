package discussions

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_discussion/pb"
	"google.golang.org/grpc/metadata"
)

func accountID(ctx *gin.Context) string {
	id, _ := ctx.Get("accountId")
	s, _ := id.(string)
	return s
}

type fileBody struct {
	StorageKey  string `json:"storage_key"`
	ContentType string `json:"content_type"`
}

type createBody struct {
	Body     string     `json:"body"`
	Audience string     `json:"audience"`
	Files    []fileBody `json:"files"`
}

type replyBody struct {
	Body          string `json:"body"`
	ParentReplyID string `json:"parent_reply_id"`
}

type editBody struct {
	Body string `json:"body"`
}

func (c *Client) CreateDiscussion(ctx *gin.Context) {
	c.create(ctx, "")
}

func (c *Client) CreateGroupDiscussion(ctx *gin.Context) {
	c.create(ctx, ctx.Param("slug"))
}

func (c *Client) create(ctx *gin.Context, slug string) {
	var body createBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Send a discussion body."})
		return
	}
	files := make([]*pb.DiscussionFile, 0, len(body.Files))
	for _, f := range body.Files {
		files = append(files, &pb.DiscussionFile{StorageKey: f.StorageKey, ContentType: f.ContentType})
	}
	res, err := c.svc.CreateDiscussion(ctx, &pb.CreateDiscussionReq{
		AccountId: accountID(ctx),
		GroupSlug: slug,
		Body:      body.Body,
		Audience:  body.Audience,
		Files:     files,
	})
	if c.fail(ctx, err, "create discussion") {
		return
	}
	ctx.JSON(http.StatusCreated, res.GetDiscussion())
}

func (c *Client) ListDiscussions(ctx *gin.Context) {
	c.list(ctx, "")
}

func (c *Client) ListGroupDiscussions(ctx *gin.Context) {
	c.list(ctx, ctx.Param("slug"))
}

func (c *Client) list(ctx *gin.Context, slug string) {
	res, err := c.svc.ListDiscussions(ctx, &pb.ListDiscussionsReq{
		AccountId:      accountID(ctx),
		GroupSlug:      slug,
		BeforePublicId: ctx.Query("before_id"),
	})
	if c.fail(ctx, err, "list discussions") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"discussions": res.GetDiscussions()})
}

func (c *Client) GetDiscussion(ctx *gin.Context) {
	res, err := c.svc.GetDiscussion(ctx, &pb.DiscussionActionReq{
		AccountId: accountID(ctx),
		PublicId:  ctx.Param("id"),
	})
	if c.fail(ctx, err, "get discussion") {
		return
	}
	ctx.JSON(http.StatusOK, res.GetDiscussion())
}

func (c *Client) DeleteDiscussion(ctx *gin.Context) {
	c.remove(ctx, false)
}

func (c *Client) HideDiscussion(ctx *gin.Context) {
	c.remove(ctx, true)
}

func (c *Client) remove(ctx *gin.Context, hide bool) {
	callCtx := ctx.Request.Context()
	if hide {
		callCtx = metadata.AppendToOutgoingContext(callCtx, discussionActionHeader, discussionActionHide)
	}
	res, err := c.svc.DeleteDiscussion(callCtx, &pb.DiscussionActionReq{
		AccountId: accountID(ctx),
		PublicId:  ctx.Param("id"),
	})
	action := "delete discussion"
	if hide {
		action = "hide discussion"
	}
	if c.fail(ctx, err, action) {
		return
	}
	ctx.JSON(http.StatusOK, res.GetDiscussion())
}

func (c *Client) ReplyToDiscussion(ctx *gin.Context) {
	var body replyBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Send a reply."})
		return
	}
	res, err := c.svc.ReplyToDiscussion(ctx, &pb.ReplyDiscussionReq{
		AccountId:      accountID(ctx),
		PublicId:       ctx.Param("id"),
		ParentPublicId: body.ParentReplyID,
		Body:           body.Body,
	})
	if c.fail(ctx, err, "reply to discussion") {
		return
	}
	ctx.JSON(http.StatusCreated, res.GetReply())
}

func (c *Client) LikeDiscussion(ctx *gin.Context) {
	res, err := c.svc.LikeDiscussion(ctx, &pb.DiscussionActionReq{
		AccountId: accountID(ctx),
		PublicId:  ctx.Param("id"),
	})
	if c.fail(ctx, err, "like discussion") {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"liked": res.GetLiked()})
}

func (c *Client) EditDiscussion(ctx *gin.Context) {
	var body editBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Send the updated discussion."})
		return
	}
	res, err := c.svc.EditDiscussion(ctx, &pb.EditDiscussionReq{
		AccountId: accountID(ctx),
		PublicId:  ctx.Param("id"),
		Body:      body.Body,
	})
	if c.fail(ctx, err, "edit discussion") {
		return
	}
	ctx.JSON(http.StatusOK, res.GetDiscussion())
}
