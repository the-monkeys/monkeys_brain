package blog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	activitypb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_activity/pb"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_blog/pb"
	"github.com/the-monkeys/the_monkeys/common/homepage"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const homepageDeadline = 10 * time.Second
const homepageTrendingCeiling = 10000

type homepageCatalog struct {
	Blogs     []map[string]interface{} `json:"blogs"`
	HasMore   bool                     `json:"has_more"`
	Version   string                   `json:"homepage_policy_version"`
	TimeRange string                   `json:"time_range_used,omitempty"`
}

// GetHomepageBlogs is deliberately separate from the default and personalized
// feeds. Only this handler selects the internal homepage metadata contract.
func (asc *BlogServiceClient) GetHomepageBlogs(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "12"))
	offset, offsetErr := strconv.Atoi(c.DefaultQuery("offset", "0"))
	order := c.DefaultQuery("sort", "latest")
	if err != nil || offsetErr != nil || limit < 1 || limit > 50 || offset < 0 || offset > 1000 || (order != "latest" && order != "trending") {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid homepage pagination or sort"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), homepageDeadline)
	defer cancel()
	var page homepageCatalog
	if order == "trending" {
		page, err = asc.homepageTrending(ctx, c, limit, offset)
	} else {
		page, err = asc.homepageMetadata(ctx, c, limit, offset, nil)
	}
	if err != nil || ctx.Err() != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "homepage catalog is unavailable"})
		return
	}
	c.JSON(http.StatusOK, page)
}

func (asc *BlogServiceClient) homepageMetadata(ctx context.Context, c *gin.Context, limit, offset int, ids []string) (homepageCatalog, error) {
	var page homepageCatalog
	if err := ctx.Err(); err != nil {
		return page, err
	}
	if asc.Client == nil {
		return page, fmt.Errorf("blog service unavailable")
	}
	// Never forward an arbitrary HTTP policy header or account/article selectors.
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(homepage.MetadataKey, homepage.Version)
	ctx = metadata.NewOutgoingContext(ctx, md)
	client := utils.GetClientInfo(c)
	stream, err := asc.Client.GetBlogsMetadata(ctx, &pb.BlogListReq{
		Limit: int32(limit), Offset: int32(offset), BlogIds: ids,
		Ip: client.IPAddress, Client: client.ClientType, SessionId: client.SessionID, UserAgent: client.UserAgent,
		Referrer: client.Referrer, Platform: utils.GetBlogPlatform(c), ClientInfo: createClientInfo(client),
	}, grpc.MaxCallRecvMsgSize(32<<20))
	if err != nil {
		return page, err
	}
	message, err := stream.Recv()
	if err != nil {
		return page, err
	}
	// Bound a malformed service response too; the valid contract is one envelope.
	if message == nil || len(message.Value) > 32<<20 {
		return page, fmt.Errorf("invalid homepage envelope")
	}
	var envelope struct {
		Blogs   json.RawMessage `json:"blogs"`
		HasMore *bool           `json:"has_more"`
		Version string          `json:"homepage_policy_version"`
	}
	if err := json.Unmarshal(rewriteV1StorageURLs(message.Value), &envelope); err != nil {
		return page, err
	}
	if envelope.Version != homepage.Version || envelope.HasMore == nil || len(envelope.Blogs) == 0 {
		return page, fmt.Errorf("homepage service policy mismatch")
	}
	if err := json.Unmarshal(envelope.Blogs, &page.Blogs); err != nil || page.Blogs == nil || len(page.Blogs) > limit {
		return page, fmt.Errorf("invalid homepage articles")
	}
	seen := make(map[string]bool, len(page.Blogs))
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	for _, row := range page.Blogs {
		id, _ := row["blog_id"].(string)
		title, _ := row["title"].(string)
		assessment, _ := row["homepage_eligibility"].(map[string]interface{})
		if strings.TrimSpace(id) == "" || strings.TrimSpace(title) == "" || seen[id] || (len(ids) > 0 && !allowed[id]) || assessment["eligible"] != true || assessment["version"] != homepage.Version {
			return page, fmt.Errorf("unmarked homepage article")
		}
		seen[id] = true
	}
	if *envelope.HasMore && len(page.Blogs) != limit {
		return page, fmt.Errorf("incomplete homepage page")
	}
	if _, err := stream.Recv(); err != io.EOF {
		return page, fmt.Errorf("incomplete homepage stream")
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	page.HasMore = *envelope.HasMore
	page.Version = homepage.Version
	return page, nil
}

func (asc *BlogServiceClient) homepageTrending(ctx context.Context, c *gin.Context, limit, offset int) (homepageCatalog, error) {
	empty := homepageCatalog{Blogs: []map[string]interface{}{}, Version: homepage.Version, TimeRange: "24h"}
	if asc.ActivityCli == nil {
		return empty, fmt.Errorf("activity service unavailable")
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(homepage.MetadataKey, homepage.Version)
	ctx = metadata.NewOutgoingContext(ctx, md)
	for _, window := range []string{"24h", "14d", "90d", "1y"} {
		// The activity service's literal global 24h cache contains only ten rows
		// and ignores Limit. Equivalent ES date math bypasses that truncated cache.
		activityWindow := window
		if window == "24h" {
			activityWindow = "1d"
		}
		for candidates := 100; candidates <= homepageTrendingCeiling; candidates += 100 {
			if err := ctx.Err(); err != nil {
				return empty, err
			}
			activity, err := asc.ActivityCli.GetTrendingBlogs(ctx, &activitypb.GetTrendingBlogsRequest{TimeRange: activityWindow, Limit: int32(candidates)})
			if err != nil {
				return empty, err
			}
			if activity == nil || activity.StatusCode != http.StatusOK || activity.Error != nil || len(activity.Blogs) > candidates {
				return empty, fmt.Errorf("invalid homepage activity response")
			}
			if len(activity.Blogs) == 0 {
				break
			} // Never turn empty trending IDs into latest.
			ids := make([]string, 0, len(activity.Blogs))
			stats := make(map[string]*activitypb.TrendingBlog, len(activity.Blogs))
			for _, entry := range activity.Blogs {
				if entry == nil || strings.TrimSpace(entry.BlogId) == "" {
					return empty, fmt.Errorf("invalid trending article")
				}
				if _, ok := stats[entry.BlogId]; !ok {
					ids = append(ids, entry.BlogId)
					stats[entry.BlogId] = entry
				}
			}
			page, err := asc.homepageMetadata(ctx, c, limit, offset, ids)
			if err != nil {
				return empty, err
			}
			exhausted := len(activity.Blogs) < candidates
			if page.HasMore || exhausted {
				// An empty page beyond the last eligible article must not switch to a
				// different window. Probe only to distinguish that from no eligible posts.
				anyEligible := len(page.Blogs) > 0
				if !anyEligible && offset > 0 && exhausted {
					first, err := asc.homepageMetadata(ctx, c, 1, 0, ids)
					if err != nil {
						return empty, err
					}
					anyEligible = len(first.Blogs) > 0
				}
				if anyEligible {
					// Re-establish activity order defensively when receiving service metadata.
					byID := make(map[string]map[string]interface{}, len(page.Blogs))
					for _, row := range page.Blogs {
						byID[row["blog_id"].(string)] = row
					}
					ordered := make([]map[string]interface{}, 0, len(page.Blogs))
					for _, id := range ids {
						if row, ok := byID[id]; ok {
							row["views"] = stats[id].Views
							row["score"] = stats[id].Score
							ordered = append(ordered, row)
						}
					}
					page.Blogs = ordered
					page.TimeRange = window
					return page, nil
				}
				break
			}
			if candidates == homepageTrendingCeiling {
				return empty, fmt.Errorf("homepage activity candidate budget exhausted")
			}
		}
	}
	return empty, nil
}
