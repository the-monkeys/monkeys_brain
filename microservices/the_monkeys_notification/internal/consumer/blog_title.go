package consumer

import (
	"context"
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"time"

	blogpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_notification/internal/models"
	"go.uber.org/zap"
)

// BlogTitleFn looks up a post title by blog id. Nil is safe to call through resolveBlogTitle.
type BlogTitleFn func(ctx context.Context, blogID string) string

var htmlTag = regexp.MustCompile(`<[^>]+>`)

func NewBlogTitleFn(client blogpb.BlogServiceClient, log *zap.SugaredLogger) BlogTitleFn {
	if client == nil {
		return nil
	}
	return func(ctx context.Context, blogID string) string {
		if blogID == "" {
			return ""
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		for _, draft := range []bool{false, true} {
			resp, err := client.GetBlog(lookupCtx, &blogpb.BlogReq{
				BlogId:  blogID,
				IsDraft: draft,
			})
			if err != nil || resp == nil || len(resp.Value) == 0 {
				continue
			}
			var doc map[string]interface{}
			if err := json.Unmarshal(resp.Value, &doc); err != nil {
				log.Debugw("blog title: unmarshal failed", "blog_id", blogID, "err", err)
				continue
			}
			if title := extractBlogTitle(doc); title != "" {
				return title
			}
		}
		return ""
	}
}

func resolveBlogTitle(ctx context.Context, user models.TheMonkeysMessage, lookup BlogTitleFn) string {
	if user.BlogTitle != "" && user.BlogTitle != user.BlogId {
		return user.BlogTitle
	}
	if lookup != nil && user.BlogId != "" {
		if title := lookup(ctx, user.BlogId); title != "" && title != user.BlogId {
			return title
		}
	}
	return user.BlogTitle
}

func extractBlogTitle(doc map[string]interface{}) string {
	if doc == nil {
		return ""
	}
	if title := cleanTitle(asString(doc["title"])); title != "" {
		return title
	}

	var blocks []interface{}
	if blog, ok := doc["blog"].(map[string]interface{}); ok {
		if title := cleanTitle(asString(blog["title"])); title != "" {
			return title
		}
		blocks, _ = blog["blocks"].([]interface{})
		if blocks == nil {
			if content, ok := blog["content"].(map[string]interface{}); ok {
				blocks, _ = content["blocks"].([]interface{})
			}
		}
	}
	if blocks == nil {
		blocks, _ = doc["blocks"].([]interface{})
	}

	var firstHeader string
	for _, raw := range blocks {
		block, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if asString(block["type"]) != "header" {
			continue
		}
		data, _ := block["data"].(map[string]interface{})
		text := cleanTitle(asString(data["text"]))
		if text == "" {
			continue
		}
		if level, _ := data["level"].(float64); level == 1 {
			return text
		}
		if firstHeader == "" {
			firstHeader = text
		}
	}
	return firstHeader
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func cleanTitle(s string) string {
	s = html.UnescapeString(htmlTag.ReplaceAllString(s, ""))
	return strings.TrimSpace(s)
}
