package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/models"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

// computeContentFingerprint creates a deterministic hash of blog content,
// excluding volatile fields (timestamps) that change on every auto-save tick
// even when the actual content hasn't changed. This prevents unnecessary
// Elasticsearch writes from the streaming auto-save loop.
func computeContentFingerprint(req map[string]interface{}) [32]byte {
	content := make(map[string]interface{}, 4)
	for _, key := range []string{"blog", "tags", "slug", "owner_account_id", "group_slug", "audience"} {
		if v, ok := req[key]; ok {
			content[key] = v
		}
	}
	stripTimeFields(content)
	bs, _ := json.Marshal(content)
	return sha256.Sum256(bs)
}

// stripTimeFields recursively removes "time" keys from maps/slices
// so that auto-save tick timestamps don't affect the content fingerprint.
func stripTimeFields(v interface{}) {
	switch val := v.(type) {
	case map[string]interface{}:
		delete(val, "time")
		for _, child := range val {
			stripTimeFields(child)
		}
	case []interface{}:
		for _, item := range val {
			stripTimeFields(item)
		}
	}
}

func (blog *BlogService) DraftBlogV2(stream grpc.BidiStreamingServer[anypb.Any, anypb.Any]) error {
	// Track the last content fingerprint to skip redundant ES writes
	// when the auto-save loop fires with unchanged content.
	var lastFingerprint [32]byte

	// Track blog existence across the stream lifetime. We only query ES
	// once on the first message — after that, the existence state is
	// known (either it already existed or we just created it).
	var blogExists bool
	var existenceChecked bool

	for {
		// Receive a message from the client
		reqAny, err := stream.Recv()
		if err == io.EOF {
			// Client has closed the stream
			blog.logger.Debug("client closed stream")
			// Send message to the user service to update the blog status
			return nil
		}
		if err != nil {
			blog.logger.Errorw("stream recv failed", "err", err)
			return status.Errorf(codes.Internal, "error receiving message")
		}

		// Unmarshal the incoming Any message into a struct
		reqStruct := &structpb.Struct{}
		if err := anypb.UnmarshalTo(reqAny, reqStruct, proto.UnmarshalOptions{}); err != nil {
			blog.logger.Errorf("Error unmarshaling message: %v", err)
			return status.Errorf(codes.InvalidArgument, "Invalid message format: %v", err)
		}

		// Convert the struct to a map for further processing
		req := reqStruct.AsMap()

		blogId, _ := req["blog_id"].(string)
		ownerAccountId, _ := req["owner_account_id"].(string)

		req["is_draft"] = true

		var ip, client string
		// Check both "Ip" and "ip" for backward/forward compatibility
		if v, ok := req["ip"]; ok && v != nil {
			ip, _ = v.(string)
		} else if v, ok := req["Ip"]; ok && v != nil {
			ip, _ = v.(string)
		}

		if v, ok := req["client"]; ok && v != nil {
			client, _ = v.(string)
		} else if v, ok := req["Client"]; ok && v != nil {
			client, _ = v.(string)
		}

		tagsInterface, ok := req["tags"].([]interface{})
		if !ok {
			blog.logger.Debugf("Tags field is missing or not of type []interface{}, using default")
			tagsInterface = []interface{}{"untagged"}
		}
		tags := make([]string, len(tagsInterface))
		for i, v := range tagsInterface {
			tags[i], ok = v.(string)
			if !ok {
				blog.logger.Errorf("Tag value at index %d is not of type string: %v", i, v)
				return status.Errorf(codes.InvalidArgument, "Tag value is not of type string")
			}
		}

		// Compute a content fingerprint to detect whether the blog content
		// actually changed. The client auto-save loop sends updates every ~15s
		// even when the user hasn't typed anything — the only difference is the
		// per-block "time" field. Skipping unchanged saves reduces ES writes
		// and eliminates version bumps that cause 409 conflicts on publish.
		fingerprint := computeContentFingerprint(req)

		// Check blog existence and draft status only once per stream.
		// After the first message we know whether the blog exists and
		// whether it's editable, so subsequent auto-save ticks skip
		// the ES GET entirely — one fewer round-trip per message.
		if !existenceChecked {
			exists, blogInfo, err := blog.osClient.DoesBlogExist(stream.Context(), blogId)
			if err != nil {
				blog.logger.Errorf("Error checking blog existence for %s: %v", blogId, err)
			}
			blogExists = exists
			existenceChecked = true

			// If blog exists, validate it is still a draft. Published or
			// archived blogs must be moved back to draft before editing.
			if exists && blogInfo != nil {
				isDraft := true
				switch v := blogInfo["is_draft"].(type) {
				case bool:
					isDraft = v
				case string:
					isDraft = v != "false" && v != "False"
				}
				if !isDraft {
					blog.logger.Warnw("DraftBlogV2: blog is not a draft, rejecting edit",
						"blog_id", blogId)
					return status.Errorf(codes.FailedPrecondition,
						"blog must be moved to draft before editing")
				}
			}
		}

		// If blog already exists and content hasn't changed, skip the ES write
		// but still respond to the client so the WebSocket doesn't stall.
		if blogExists && fingerprint == lastFingerprint {
			blog.logger.Debugw("DraftBlogV2: content unchanged, skipping save", "blog_id", blogId)
			anyMsg, err := anypb.New(reqStruct)
			if err != nil {
				blog.logger.Errorf("Error wrapping structpb.Struct in anypb.Any: %v", err)
				return status.Errorf(codes.Internal, "Failed to wrap struct in Any: %v", err)
			}
			if err := stream.Send(anyMsg); err != nil {
				blog.logger.Errorf("Error sending response: %v", err)
				return status.Errorf(codes.Internal, "Failed to send response: %v", err)
			}
			continue
		}

		if blogExists {
			blog.logger.Debugw("DraftBlogV2: updating existing blog", "blog_id", blogId)
		} else {
			blog.logger.Infow("DraftBlogV2: creating new blog", "blog_id", blogId, "owner", ownerAccountId)
			msg := models.InterServiceMessage{
				AccountId:  ownerAccountId,
				BlogId:     blogId,
				Action:     constants.BLOG_CREATE,
				BlogStatus: constants.BlogStatusDraft,
				IpAddress:  ip,
				Client:     client,
			}
			if slug, ok := req["group_slug"].(string); ok {
				msg.GroupSlug = slug
			}
			if aud, ok := req["audience"].(string); ok {
				msg.Audience = aud
			}
			blog.logger.Debugw("DraftBlogV2: marshalling RabbitMQ message",
				"blog_id", blogId,
				"owner", ownerAccountId,
				"action", constants.BLOG_CREATE,
				"exchange", blog.config.RabbitMQ.Exchange,
				"routing_key", blog.config.RabbitMQ.RoutingKeys[1],
			)
			bx, err := json.Marshal(msg)
			if err != nil {
				blog.logger.Errorf("Cannot marshal the message for blog: %s, error: %v", blogId, err)
				return status.Errorf(codes.Internal, "Something went wrong while drafting a blog")
			}
			if len(tags) == 0 {
				req["tags"] = []string{"untagged"}
			}

			// Publish synchronously before writing to Elasticsearch.
			// If the broker rejects the message after all retries, we return an
			// error to the client and skip the ES write entirely, so the blog
			// never ends up orphaned in ES without a matching Postgres row.
			blog.logger.Debugw("DraftBlogV2: publishing blog_create to RabbitMQ",
				"blog_id", blogId,
				"owner", ownerAccountId,
				"exchange", blog.config.RabbitMQ.Exchange,
				"routing_key", blog.config.RabbitMQ.RoutingKeys[1],
				"message_size", len(bx),
			)
			if err := blog.qConn.PublishReliable(blog.config.RabbitMQ.Exchange, blog.config.RabbitMQ.RoutingKeys[1], bx, blog.config.RabbitMQ.MaxRetries); err != nil {
				blog.logger.Errorf("DraftBlogV2: failed to publish blog_create to RabbitMQ, aborting ES write: blog_id=%s, error=%v", blogId, err)
				return status.Errorf(codes.Internal, "Failed to create blog, please try again")
			}
			blog.logger.Infow("DraftBlogV2: blog_create message published to RabbitMQ",
				"blog_id", blogId,
				"owner", ownerAccountId,
			)

			go blog.trackBlogActivity(ownerAccountId, constants.BLOG_CREATE, "blog", blogId, req)

			// Mark as existing so subsequent messages use the update path.
			blogExists = true
		}

		saveResp, err := blog.osClient.SaveBlog(stream.Context(), req)
		if err != nil {
			blog.logger.Errorf("DraftBlogV2: Cannot store draft into opensearch for blog %s: %v", blogId, err)
			return status.Errorf(codes.Internal, "Failed to store draft: %v", err)
		}

		if saveResp.IsError() {
			blog.logger.Errorf("DraftBlogV2: OpenSearch save error for blog %s: %v", blogId, saveResp.String())
		}

		// Update fingerprint after successful write
		lastFingerprint = fingerprint

		// // Respond back to the client
		// resp := &pb.BlogResponse{
		//  Blog: req.Blog,
		// }
		// respAny, err := anypb.New(resp)
		// if err != nil {
		//  blog.logger.Errorf("Error marshalling response: %v", err)
		//  return status.Errorf(codes.Internal, "Failed to create response: %v", err)
		// }

		// TODO: Change the return data to the actual response
		anyMsg, err := anypb.New(reqStruct)
		if err != nil {
			blog.logger.Errorf("Error wrapping structpb.Struct in anypb.Any: %v", err)
			return status.Errorf(codes.Internal, "Failed to wrap struct in Any: %v", err)
		}

		if err := stream.Send(anyMsg); err != nil {
			blog.logger.Errorf("Error sending response: %v", err)
			return status.Errorf(codes.Internal, "Failed to send response: %v", err)
		}
	}
}

func (blog *BlogService) BlogsOfFollowingAccounts(req *pb.FollowingAccounts, stream pb.BlogService_BlogsOfFollowingAccountsServer) error {
	blog.logger.Debugf("BlogsOfFollowingAccounts: Received request for following accounts: %v", req.AccountIds)

	if len(req.AccountIds) == 0 {
		return status.Errorf(codes.InvalidArgument, "No account ids provided")
	}

	blogs, err := blog.osClient.GetBlogsOfUsersByAccountIds(stream.Context(), req.AccountIds, req.Limit, req.Offset)
	if err != nil {
		blog.logger.Errorf("Error fetching blogs of following accounts: %v", err)
		return status.Errorf(codes.Internal, "Error fetching blogs of following accounts: %v", err)
	}

	// TODO: remove a key from here blogs blogs = []map[string]interface{}
	removeKeyFromBlogs(blogs, "action")
	removeKeyFromBlogs(blogs, "Action")
	removeKeyFromBlogs(blogs, "Ip")
	removeKeyFromBlogs(blogs, "Client")

	blogBytes, err := json.Marshal(blogs)
	if err != nil {
		blog.logger.Errorf("Error marshalling blogs: %v", err)
		return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
	}

	// Send the packed message over the stream
	if err := stream.Send(&anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}); err != nil {
		return err
	}

	return nil
}

func removeKeyFromBlogs(blogs []map[string]interface{}, key string) {
	for _, blog := range blogs {
		delete(blog, key)
	}
}

func (blog *BlogService) GetBlogs(req *pb.GetBlogsReq, stream pb.BlogService_GetBlogsServer) error {
	blog.logger.Debugf("Received request for blogs: %v", req)

	var blogs []map[string]interface{}
	var err error

	if req.GetGroupSlug() != "" {
		blog.logger.Debug("Fetching published blogs by group slug")
		blogs, err = blog.osClient.GetPublishedBlogsByGroupSlug(stream.Context(), req.GetGroupSlug(), req.GetIncludeGroupOnly(), req.Limit, req.Offset)
		if err != nil {
			blog.logger.Errorf("Error fetching blogs by group: %v", err)
			return status.Errorf(codes.Internal, "Error fetching blogs by group: %v", err)
		}

		removeKeyFromBlogs(blogs, "action")
		removeKeyFromBlogs(blogs, "Ip")
		removeKeyFromBlogs(blogs, "Client")

		blogBytes, err := json.Marshal(blogs)
		if err != nil {
			blog.logger.Errorf("Error marshalling blogs: %v", err)
			return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
		}

		if err := stream.Send(&anypb.Any{
			TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
			Value:   blogBytes,
		}); err != nil {
			return err
		}

		return nil
	}

	// Check if specific tags are requested
	if len(req.Tags) > 0 {
		if req.IsDraft {
			blog.logger.Debug("Fetching draft blogs by tags")
		} else {
			blog.logger.Debug("Fetching published blogs by tags")
			blogs, err = blog.osClient.GetBlogsByTags(stream.Context(), req.Tags, false, req.Limit, req.Offset)
			if err != nil {
				blog.logger.Errorf("Error fetching blogs by tags: %v", err)
				return status.Errorf(codes.Internal, "Error fetching blogs by tags: %v", err)
			}
		}

		// TODO: remove a key from here blogs blogs = []map[string]interface{}
		removeKeyFromBlogs(blogs, "action")
		removeKeyFromBlogs(blogs, "Ip")
		removeKeyFromBlogs(blogs, "Client")

		blogBytes, err := json.Marshal(blogs)
		if err != nil {
			blog.logger.Errorf("Error marshalling blogs: %v", err)
			return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
		}

		// Send the packed message over the stream
		if err := stream.Send(&anypb.Any{
			TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
			Value:   blogBytes,
		}); err != nil {
			return err
		}

		return nil
	}

	// No tags provided, handle based on draft or published status
	if req.IsDraft {
		blog.logger.Debug("Fetching draft blogs by account ID")
		blogs, err = blog.osClient.GetBlogsByAccountId(stream.Context(), req.AccountId, true, false, req.Limit, req.Offset)
		if err != nil {
			blog.logger.Errorf("Error fetching draft blogs by account ID: %v", err)
			return status.Errorf(codes.Internal, "Error fetching draft blogs by account ID: %v", err)
		}
	} else {
		blog.logger.Debug("Fetching published blogs by account ID")
		blogs, err = blog.osClient.GetBlogsByAccountId(stream.Context(), req.AccountId, false, req.GetExcludeGroupOnly(), req.Limit, req.Offset)
		if err != nil {
			blog.logger.Errorf("Error fetching published blogs by account ID: %v", err)
			return status.Errorf(codes.Internal, "Error fetching published blogs by account ID: %v", err)
		}
	}

	// TODO: remove a key from here blogs blogs = []map[string]interface{}
	removeKeyFromBlogs(blogs, "action")
	removeKeyFromBlogs(blogs, "Ip")
	removeKeyFromBlogs(blogs, "Client")

	blogBytes, err := json.Marshal(blogs)
	if err != nil {
		blog.logger.Errorf("Error marshalling blogs: %v", err)
		return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
	}

	// Send the packed message over the stream
	if err := stream.Send(&anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}); err != nil {
		return err
	}

	return nil
}

func (blog *BlogService) GetBlogsBySlice(req *pb.GetBlogsBySliceReq, stream pb.BlogService_GetBlogsBySliceServer) error {
	blog.logger.Debugf("Received request for blogs by slice: %v", req)

	if len(req.BlogIds) == 0 {
		return status.Errorf(codes.InvalidArgument, "No blog ids provided")
	}

	blogsIds := []string{}
	for _, blogId := range req.BlogIds {
		exist, blogInfo, err := blog.osClient.DoesBlogExist(stream.Context(), blogId)
		if err != nil {
			blog.logger.Errorf("Error checking if blog exists: %v", err)
			return status.Errorf(codes.Internal, "Error checking if blog exists: %v", err)
		}

		idDraft, ok := blogInfo["is_draft"].(bool)
		if !ok {
			idDraft = false
		}

		if exist && !idDraft {
			blogsIds = append(blogsIds, blogId)
		}
	}

	blogs, err := blog.osClient.GetBlogsByBlogIdsV2(stream.Context(), blogsIds, req.Limit, req.Offset)
	if err != nil {
		blog.logger.Errorf("Error fetching blogs by slice: %v", err)
		return status.Errorf(codes.Internal, "Error fetching blogs by slice: %v", err)
	}

	// TODO: remove a key from here blogs blogs = []map[string]interface{}
	removeKeyFromBlogs(blogs, "action")
	removeKeyFromBlogs(blogs, "Action")
	removeKeyFromBlogs(blogs, "Ip")
	removeKeyFromBlogs(blogs, "Client")

	blogBytes, err := json.Marshal(blogs)
	if err != nil {
		blog.logger.Errorf("Error marshalling blogs: %v", err)
		return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
	}

	// Send the packed message over the stream
	if err := stream.Send(&anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}); err != nil {
		return err
	}

	return nil
}

func (blog *BlogService) GetBlog(ctx context.Context, req *pb.BlogReq) (*anypb.Any, error) {
	blogId := req.GetBlogId()
	accountId := req.GetAccountId()
	isDraft := req.GetIsDraft()

	// Track blog reading activity
	action := constants.READ_BLOG
	if isDraft {
		action = constants.READ_DRAFT
	}
	go blog.trackBlogActivity(accountId, action, "blog", blogId, req)

	blogData, err := blog.osClient.GetBlogByBlogId(ctx, blogId, isDraft)
	if err != nil {
		blog.logger.Errorf("GetBlog: Error fetching blog %s from OpenSearch: %v", blogId, err)
		return nil, status.Errorf(codes.Internal, "Error fetching blog: %v", err)
	}

	if blogData == nil {
		blog.logger.Warnf("GetBlog: Blog %s not found in OpenSearch", blogId)
		return nil, status.Errorf(codes.NotFound, "Blog not found")
	}

	delete(blogData, "action")
	delete(blogData, "Action")
	delete(blogData, "Ip")
	delete(blogData, "Client")

	blogBytes, err := json.Marshal(blogData)
	if err != nil {
		blog.logger.Errorf("GetBlog: Error marshalling blog %s: %v", blogId, err)
		return nil, status.Errorf(codes.Internal, "Error marshalling blog: %v", err)
	}

	return &anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}, nil
}

func (blog *BlogService) MetaGetFeedBlogs(req *pb.BlogListReq, stream pb.BlogService_MetaGetFeedBlogsServer) error {
	var blogs []map[string]interface{}
	// Find blog by tags
	if len(req.Tags) > 0 {
		blog.logger.Debug("Fetching published blogs by tags")
		blogs, err := blog.osClient.GetBlogsByTags(stream.Context(), req.Tags, false, req.Limit, req.Offset)
		if err != nil {
			blog.logger.Errorf("Error fetching blogs by tags: %v", err)
			return status.Errorf(codes.Internal, "Error fetching blogs by tags: %v", err)
		}
		// TODO: remove a key from here blogs blogs = []map[string]interface{}
		removeKeyFromBlogs(blogs, "action")
		removeKeyFromBlogs(blogs, "Ip")
		removeKeyFromBlogs(blogs, "Client")

		blogBytes, err := json.Marshal(blogs)
		if err != nil {
			blog.logger.Errorf("Error marshalling blogs: %v", err)
			return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
		}

		// Send the packed message over the stream
		if err := stream.Send(&anypb.Any{
			TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
			Value:   blogBytes,
		}); err != nil {
			return err
		}

		return nil
	}

	blog.logger.Debug("Fetching feed blogs")
	blogs, err := blog.osClient.GetAllPublishedBlogsLatestFirst(stream.Context(), int(req.Limit), int(req.Offset))
	if err != nil {
		blog.logger.Errorf("Error fetching blogs by tags: %v", err)
		return status.Errorf(codes.Internal, "Error fetching blogs by tags: %v", err)
	}

	removeKeyFromBlogs(blogs, "action")
	removeKeyFromBlogs(blogs, "Action")
	removeKeyFromBlogs(blogs, "Ip")
	removeKeyFromBlogs(blogs, "Client")

	blogBytes, err := json.Marshal(blogs)
	if err != nil {
		blog.logger.Errorf("Error marshalling blogs: %v", err)
		return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
	}

	// Send the packed message over the stream
	if err := stream.Send(&anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}); err != nil {
		return err
	}

	return nil
}

func (blog *BlogService) GetBlogsMetadata(req *pb.BlogListReq, stream pb.BlogService_GetBlogsMetadataServer) error {
	returnData := make(map[string]interface{})
	var blogs []map[string]interface{}

	// Track blog activity - determine action based on request type
	action := "browse_feed"
	resource := "feed"
	resourceId := "global_feed"

	if len(req.Tags) > 0 {
		action = "browse_by_tags"
		resource = "tags"
		resourceId = strings.Join(req.Tags, ",")
	} else {
		// Track the activity
		blog.trackBlogActivity(req.AccountId, action, resource, resourceId, req)
	}

	if req.AccountId != "" {
		if req.IsDraft {
			action = "browse_drafts"
			resource = "user_drafts"
		} else {
			action = "browse_user_blogs"
			resource = "user_blogs"
		}
		resourceId = req.AccountId
	}

	if len(req.BlogIds) > 0 {
		action = "browse_bookmarks"
		resource = "bookmarks"
		resourceId = strings.Join(req.BlogIds, ",")
	}

	// Find blog by tags
	if len(req.Tags) > 0 {
		blog.logger.Debug("Fetching published blogs by tags")
		blogs, count, err := blog.osClient.GetBlogsMetadataByTags(stream.Context(), req.Tags, false, req.Limit, req.Offset)
		if err != nil {
			blog.logger.Errorf("Error fetching blogs by tags: %v", err)
			return status.Errorf(codes.Internal, "Error fetching blogs by tags: %v", err)
		}

		returnData["total_blogs"] = count
		returnData["blogs"] = blogs

		blogBytes, err := json.Marshal(returnData)
		if err != nil {
			blog.logger.Errorf("Error marshalling blogs: %v", err)
			return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
		}

		// Send the packed message over the stream
		if err := stream.Send(&anypb.Any{
			TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
			Value:   blogBytes,
		}); err != nil {
			return err
		}

		return nil
	}

	blog.logger.Debug("Fetching feed metadata")
	blogs, count, err := blog.osClient.GetAllPublishedBlogsMetadata(stream.Context(), int(req.Limit), int(req.Offset))
	if err != nil {
		blog.logger.Errorf("Error fetching blogs by tags: %v", err)
		return status.Errorf(codes.Internal, "Error fetching blogs by tags: %v", err)
	}

	returnData["total_blogs"] = count
	returnData["blogs"] = blogs

	blogBytes, err := json.Marshal(returnData)
	if err != nil {
		blog.logger.Errorf("Error marshalling blogs: %v", err)
		return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
	}

	// Send the packed message over the stream
	if err := stream.Send(&anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}); err != nil {
		return err
	}

	return nil
}

func (blog *BlogService) SearchBlogsMetadata(req *pb.SearchReq, stream pb.BlogService_SearchBlogsMetadataServer) error {
	blog.logger.Debugf("Searching blogs with query: %s, limit: %d, offset: %d", req.Query, req.Limit, req.Offset)

	// Track search activity with user context
	blog.trackBlogActivity("", "search_blogs", "search", req.Query, req)
	// TODO: Add AccountId field to SearchReq protobuf to track which user is searching

	returnData := make(map[string]interface{})
	var searchTerms = []string{}

	// Check if the req.Query has "double quote" to determine if it's a full-text search
	if strings.TrimSpace(req.Query) == "" {
		blog.logger.Errorf("Search query is empty")
		return status.Errorf(codes.InvalidArgument, "Search query cannot be empty")
	}

	if strings.HasPrefix(req.Query, "\"") && strings.HasSuffix(req.Query, "\"") {
		searchTerms = []string{strings.TrimSpace(req.Query)}
	} else {
		// Split the query into words for a more flexible search
		searchTerms = strings.Fields(req.Query)
	}

	blogs, count, err := blog.osClient.GetBlogsMetadataByQuery(stream.Context(), searchTerms, false, req.Limit, req.Offset)
	if err != nil {
		blog.logger.Errorf("Error fetching blogs by query: %v", err)
		return status.Errorf(codes.Internal, "Error fetching blogs by query: %v", err)
	}

	returnData["total_blogs"] = count
	returnData["blogs"] = blogs

	blogBytes, err := json.Marshal(returnData)
	if err != nil {
		blog.logger.Errorf("Error marshalling blogs: %v", err)
		return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
	}

	// Send the packed message over the stream
	if err := stream.Send(&anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}); err != nil {
		return err
	}

	return nil

}

func (blog *BlogService) MetaGetUsersBlogs(req *pb.BlogListReq, stream pb.BlogService_MetaGetUsersBlogsServer) error {
	blog.logger.Debugf("Received request for user's blogs: %v", req)

	returnData := make(map[string]interface{})
	var blogs []map[string]interface{}
	var count int
	if req.AccountId == "" {
		return status.Errorf(codes.InvalidArgument, "Account ID cannot be empty")
	}

	var err error

	if req.IsDraft {
		blog.logger.Debug("Fetching draft blogs by account ID")
		blogs, count, err = blog.osClient.GetBlogsMetaByAccountId(stream.Context(), req.AccountId, true, false, false, req.Limit, req.Offset)
	} else if req.IsScheduled {
		blog.logger.Debug("Fetching schedule blogs by account ID")
		blogs, count, err = blog.osClient.GetBlogsMetaByAccountId(stream.Context(), req.AccountId, true, true, false, req.Limit, req.Offset)
	} else {
		blog.logger.Debug("Fetching published blogs by account ID")
		blogs, count, err = blog.osClient.GetBlogsMetaByAccountId(stream.Context(), req.AccountId, false, false, req.GetExcludeGroupOnly(), req.Limit, req.Offset)
	}

	if err != nil {
		blog.logger.Errorf("Error fetching blogs by account ID: %v", err)
		return status.Errorf(codes.Internal, "Error fetching blogs by account ID: %v", err)
	}

	removeKeyFromBlogs(blogs, "action")
	removeKeyFromBlogs(blogs, "Ip")
	removeKeyFromBlogs(blogs, "Client")

	returnData["total_blogs"] = count
	returnData["blogs"] = blogs

	blogBytes, err := json.Marshal(returnData)
	if err != nil {
		blog.logger.Errorf("Error marshalling blogs: %v", err)
		return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
	}

	// Send the packed message over the stream
	if err := stream.Send(&anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}); err != nil {
		return err
	}

	return nil
}

func (blog *BlogService) MetaGetBlogsByBlogIds(req *pb.BlogListReq, stream pb.BlogService_MetaGetBlogsByBlogIdsServer) error {
	blog.logger.Debugf("Received request for blogs by slice: %v", req)

	if len(req.BlogIds) == 0 {
		return status.Errorf(codes.InvalidArgument, "No blog ids provided")
	}

	blogsIds := []string{}
	for _, blogId := range req.BlogIds {
		exist, blogInfo, err := blog.osClient.DoesBlogExist(stream.Context(), blogId)
		if err != nil {
			blog.logger.Errorf("Error checking if blog exists: %v", err)
			return status.Errorf(codes.Internal, "Error checking if blog exists: %v", err)
		}

		idDraft, ok := blogInfo["is_draft"].(bool)
		if !ok {
			idDraft = false
		}

		if exist && !idDraft {
			blogsIds = append(blogsIds, blogId)
		}
	}

	blogs, count, err := blog.osClient.GetBlogsMetaByBlogIdsV2(stream.Context(), blogsIds, false, req.Limit, req.Offset)
	if err != nil {
		blog.logger.Errorf("Error fetching blogs by slice: %v", err)
		return status.Errorf(codes.Internal, "Error fetching blogs by slice: %v", err)
	}

	removeKeyFromBlogs(blogs, "action")
	removeKeyFromBlogs(blogs, "Action")
	removeKeyFromBlogs(blogs, "Ip")
	removeKeyFromBlogs(blogs, "Client")

	returnData := make(map[string]interface{})
	returnData["total_blogs"] = count
	returnData["blogs"] = blogs

	blogBytes, err := json.Marshal(returnData)
	if err != nil {
		blog.logger.Errorf("Error marshalling blogs: %v", err)
		return status.Errorf(codes.Internal, "Error marshalling blogs: %v", err)
	}

	if err := stream.Send(&anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}); err != nil {
		return err
	}

	return nil
}

func (blog *BlogService) UsersBlogData(ctx context.Context, req *pb.BlogReq) (*anypb.Any, error) {
	blog.logger.Debugf("Received request for user blog data: %v", req)

	// Track user tag browsing activity
	blog.trackBlogActivity(req.AccountId, "browse_user_tags", "user_tags", req.AccountId, req)

	// Fetch user blog data from the database
	blogData, err := blog.osClient.GetAllTagsFromUserPublishedBlogs(ctx, req.AccountId)
	if err != nil {
		blog.logger.Errorf("Error fetching user blog data: %v", err)
		return nil, status.Errorf(codes.Internal, "Error fetching user blog data: %v", err)
	}

	// Count the no of unique tags into a map
	tagCount := make(map[string]int)
	for _, tag := range blogData {
		tagCount[tag]++

	}
	// Marshal the blog data into a protobuf Any message
	blogBytes, err := json.Marshal(tagCount)
	if err != nil {
		blog.logger.Errorf("Error marshalling blog data: %v", err)
		return nil, status.Errorf(codes.Internal, "Error marshalling blog data: %v", err)
	}

	return &anypb.Any{
		TypeUrl: "the-monkeys/the-monkeys/apis/serviceconn/gateway_blog/pb.BlogResponse",
		Value:   blogBytes,
	}, nil
}
