package moderation

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
)

type crawlerVerifier interface {
	Enabled() bool
	Verify(*http.Request) bool
}

// This projection is independent of human reveal consent. The existing
// anonymous policy result establishes publication and ACL before it is used.
func indexableBlog(row map[string]any, a *pb.Access) bool {
	if a == nil || a.Target == nil || a.Target.Kind != "blog" || (a.Reason != "" && a.Reason != "login_required") {
		return false
	}
	for _, flag := range []string{"is_draft", "is_archived", "is_scheduled", "content_unavailable"} {
		if row[flag] == true {
			return false
		}
	}
	if audience, present := row["audience"]; present && audience != "public" {
		return false
	}
	published, _ := row["published_time"].(string)
	at, err := time.Parse(time.RFC3339Nano, published)
	return err == nil && !at.After(time.Now())
}
func (s *Service) project(c *gin.Context, d document, a *pb.Access, verified bool, detail bool) {
	delete(d.value, "seo_indexable")
	delete(d.value, "seo_crawler")
	enabled := s.crawler != nil && s.crawler.Enabled()
	public := enabled && c.GetString("accountId") == "" && indexableBlog(d.value, a)
	if public {
		// Legacy catalog DTOs omit audience. The successful anonymous policy
		// check, not the optional ES field, establishes public visibility.
		d.value["audience"] = "public"
	}
	if public && verified && detail && c.Request.URL.Path == "/api/v2/blog/"+d.target.Id {
		// Do not expose viewer-specific statistics, report data or incidental fields.
		allowed := map[string]bool{"blog_id": true, "owner_account_id": true, "blog": true, "title": true, "slug": true, "canonical_slug": true, "seo_description": true, "published_time": true, "content_modified_at": true, "is_draft": true, "audience": true, "tags": true}
		for key := range d.value {
			if !allowed[key] {
				delete(d.value, key)
			}
		}
		d.value["content_rating"] = a
		d.value["is_nsfw"] = a.IsNsfw
		d.value["seo_indexable"] = true
		d.value["seo_crawler"] = true
		return
	}
	applyRating(d.value, a)
	if public && (strings.HasPrefix(c.Request.URL.Path, "/api/v2/blog/")) {
		// Capability only: contains no original restricted title/body/thumbnail.
		d.value["seo_indexable"] = true
	}
}
