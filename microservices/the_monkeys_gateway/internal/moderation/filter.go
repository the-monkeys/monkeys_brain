// Package moderation applies server-owned ratings to the gateway's existing DTOs.
package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/searchcrawler"
	"strings"
	"time"
)

const maxJSON = 32 << 20

// MediaAuthorizationKey is populated only from a successful policy RPC, never HTTP headers.
const MediaAuthorizationKey = "moderation.media_authorized"

type Service struct {
	client  pb.ModerationServiceClient
	crawler crawlerVerifier
}

func New(client pb.ModerationServiceClient) *Service {
	return &Service{client: client, crawler: searchcrawler.FromEnvironment()}
}

type document struct {
	value  map[string]any
	target *pb.Target
}
type capture struct {
	gin.ResponseWriter
	body     bytes.Buffer
	status   int
	overflow bool
}

func (w *capture) WriteHeader(code int) { w.status = code }
func (w *capture) Write(p []byte) (int, error) {
	if w.body.Len()+len(p) > maxJSON {
		w.overflow = true
		return len(p), nil
	}
	return w.body.Write(p)
}
func (w *capture) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *capture) WriteHeaderNow() {
	if w.status == 0 {
		w.status = 200
	}
}
func (w *capture) Status() int {
	if w.status == 0 {
		return 200
	}
	return w.status
}
func (w *capture) Size() int     { return w.body.Len() }
func (w *capture) Written() bool { return w.status != 0 || w.body.Len() > 0 }

type privateWriter struct{ gin.ResponseWriter }

func (w privateWriter) WriteHeader(code int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Del("Expires")
	w.ResponseWriter.WriteHeader(code)
}
func (w privateWriter) WriteHeaderNow() { w.WriteHeader(w.Status()); w.ResponseWriter.WriteHeaderNow() }
func (w privateWriter) Write(p []byte) (int, error) {
	w.WriteHeader(w.Status())
	return w.ResponseWriter.Write(p)
}
func (w privateWriter) WriteString(p string) (int, error) { return w.Write([]byte(p)) }
func contentPath(path string) bool {
	for _, p := range []string{"/api/v1/discussions", "/api/v1/events", "/api/v1/groups", "/api/v1/group-invites", "/api/v1/blog", "/api/v2/blog", "/api/v2/posts", "/api/v2/search/suggest"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
func mediaPath(path string) bool {
	for _, prefix := range []string{"/api/v2/storage/", "/api/storage/"} {
		if strings.HasPrefix(path, prefix) {
			rest := strings.TrimPrefix(path, prefix)
			for _, p := range []string{"posts/", "assets/", "events/", "groups/"} {
				if strings.HasPrefix(rest, p) {
					return true
				}
			}
		}
	}
	return false
}
func routeTarget(path string) *pb.Target {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		return nil
	}
	switch parts[2] {
	case "events", "groups":
		if parts[3] == "user" || parts[3] == "group" || parts[3] == "attending" {
			return nil
		}
		kind := "event"
		if parts[2] == "groups" {
			kind = "group"
		}
		return &pb.Target{Kind: kind, Id: parts[3]}
	case "discussions":
		if parts[3] != "following" {
			return &pb.Target{Kind: "discussion", Id: parts[3]}
		}
	}
	return nil
}
func (s *Service) check(ctx *gin.Context, targets []*pb.Target, anonymous bool) (*pb.CheckResponse, error) {
	if s.client == nil {
		return nil, errors.New("moderation unavailable")
	}
	account := ctx.GetString("accountId")
	if anonymous {
		account = ""
	}
	call, cancel := context.WithTimeout(ctx.Request.Context(), 5*time.Second)
	defer cancel()
	result := &pb.CheckResponse{}
	for start := 0; start < len(targets); start += 100 {
		end := start + 100
		if end > len(targets) {
			end = len(targets)
		}
		batch := targets[start:end]
		res, err := s.client.Check(call, &pb.CheckRequest{AccountId: account, Targets: batch})
		if err != nil || res == nil || len(res.Items) != len(batch) {
			return nil, errors.New("moderation unavailable")
		}
		for i, item := range res.Items {
			if item == nil || item.Target == nil || item.Target.Kind != batch[i].Kind || item.Target.Id != batch[i].Id {
				return nil, errors.New("invalid policy response")
			}
		}
		result.Items = append(result.Items, res.Items...)
	}
	return result, nil
}
func (s *Service) Middleware(identity gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		media := mediaPath(path) && (c.Request.Method == "GET" || c.Request.Method == "HEAD")
		content := contentPath(path)
		if !media && !content {
			c.Next()
			return
		}
		// Loads identity without advancing the handler chain. Invalid credentials remain anonymous.
		if identity != nil {
			identity(c)
		}
		c.Header("Cache-Control", "private, no-store")
		c.Header("Vary", "Cookie, Authorization")
		if media {
			if s.client == nil {
				c.AbortWithStatusJSON(503, gin.H{"error": "Content checks are temporarily unavailable."})
				return
			}
			call, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()
			res, err := s.client.CheckMedia(call, &pb.MediaRequest{AccountId: c.GetString("accountId"), Path: path})
			if err != nil || res == nil {
				c.AbortWithStatus(503)
				return
			}
			if !res.Allowed {
				c.AbortWithStatus(404)
				return
			}
			if res.Resolved {
				c.Set(MediaAuthorizationKey, true)
			}
			original := c.Writer
			c.Writer = privateWriter{original}
			defer func() { c.Writer = original }()
			c.Next()
			return
		}
		if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
			id := c.Param("blog_id")
			if id != "" {
				res, err := s.check(c, []*pb.Target{{Kind: "blog", Id: id}}, false)
				if err != nil {
					c.AbortWithStatus(503)
					return
				}
				if res.Items[0].IsNsfw && !res.Items[0].Revealed {
					c.AbortWithStatus(403)
					return
				}
			} else {
				c.AbortWithStatus(403)
				return
			}
			c.Next()
			return
		}
		// Non-JSON projections and child-resource reads need admission before bytes escape.
		// Detail/list reads may return a placeholder instead of an error.
		target := routeTarget(path)
		parts := strings.Split(strings.Trim(path, "/"), "/")
		child := target != nil && len(parts) > 4
		if child && c.Request.Method == "GET" {
			res, err := s.check(c, []*pb.Target{target}, strings.HasSuffix(path, "/share") || strings.HasSuffix(path, "/calendar"))
			if err != nil {
				c.AbortWithStatusJSON(503, gin.H{"error": "Content checks are temporarily unavailable."})
				return
			}
			if res.Items[0].IsNsfw && (!res.Items[0].Revealed || strings.HasSuffix(path, "/share") || strings.HasSuffix(path, "/calendar")) {
				if strings.HasSuffix(path, "/share") {
					c.AbortWithStatusJSON(200, gin.H{"og_title": "Sensitive content", "og_description": "Log in and confirm you are 18 or older to view this content.", "og_image": "", "content_rating": res.Items[0]})
					return
				}
				c.AbortWithStatusJSON(403, gin.H{"error": "Sensitive content is restricted.", "content_rating": res.Items[0]})
				return
			}
		}
		original := c.Writer
		w := &capture{ResponseWriter: original}
		c.Writer = w
		c.Next()
		c.Writer = original
		code := w.Status()
		body := w.body.Bytes()
		c.Header("Cache-Control", "private, no-store")
		c.Header("Vary", "Cookie, Authorization")
		c.Header("Content-Length", "")
		if w.overflow {
			original.Header().Set("Content-Type", "application/json")
			original.WriteHeader(503)
			_, _ = original.Write([]byte(`{"error":"Content response is too large."}`))
			return
		}
		if code >= 200 && code < 300 && strings.Contains(c.Writer.Header().Get("Content-Type"), "json") && len(body) > 0 {
			var root any
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.UseNumber()
			if err := dec.Decode(&root); err != nil {
				c.JSON(503, gin.H{"error": "Content response is invalid."})
				return
			}
			docs := collect(root, path)
			if len(docs) > 0 {
				targets := make([]*pb.Target, len(docs))
				for i, d := range docs {
					targets[i] = d.target
				}
				if len(targets) > 1000 {
					c.JSON(503, gin.H{"error": "Too many content items."})
					return
				}
				verified := s.crawler != nil && s.crawler.Verify(c.Request)
				res, err := s.check(c, targets, verified)
				if err != nil {
					c.JSON(503, gin.H{"error": "Content checks are temporarily unavailable."})
					return
				}
				for i, d := range docs {
					_, detail := root.(map[string]any)
					s.project(c, d, res.Items[i], verified, detail && len(docs) == 1)
				}
				body, err = json.Marshal(root)
				if err != nil {
					c.JSON(503, gin.H{"error": "Content response is invalid."})
					return
				}
			}
		}
		original.WriteHeader(code)
		_, _ = original.Write(body)
	}
}
func collect(root any, path string) []document {
	var docs []document
	add := func(value any, kind string) {
		row, ok := value.(map[string]any)
		if !ok {
			return
		}
		key := "slug"
		if kind == "blog" {
			key = "blog_id"
		}
		if kind == "discussion" {
			key = "public_id"
		}
		id, _ := row[key].(string)
		if id == "" {
			return
		}
		docs = append(docs, document{row, &pb.Target{Kind: kind, Id: id}})
	}
	if rows, ok := root.([]any); ok {
		for _, v := range rows {
			add(v, "blog")
		}
		return docs
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	// Only established DTO containers are traversed. Article blocks and arbitrary
	// user JSON are never interpreted as content identities.
	for key, kind := range map[string]string{"blogs": "blog", "hits": "blog", "latest_news": "blog", "trending_news": "blog", "drafts": "blog", "published": "blog", "events": "event", "groups": "group", "discussions": "discussion"} {
		if rows, ok := obj[key].([]any); ok {
			for _, v := range rows {
				add(v, kind)
			}
		}
	}
	for key, kind := range map[string]string{"event": "event", "group": "group", "discussion": "discussion"} {
		if obj[key] != nil {
			add(obj[key], kind)
		}
	}
	if _, ok := obj["blog_id"]; ok {
		add(obj, "blog")
	}
	if _, ok := obj["public_id"]; ok {
		add(obj, "discussion")
	}
	if len(docs) == 0 {
		if t := routeTarget(path); t != nil {
			add(obj, t.Kind)
		}
	}
	return docs
}
func applyRating(row map[string]any, a *pb.Access) {
	if a.Reason == "not_found" {
		for key := range row {
			delete(row, key)
		}
		switch a.Target.Kind {
		case "blog":
			row["blog_id"] = a.Target.Id
			row["title"] = "Content unavailable"
			row["blog"] = map[string]any{"blocks": []any{}}
		case "discussion":
			row["public_id"] = a.Target.Id
			row["body"] = "Content unavailable"
			row["files"] = []any{}
			row["replies"] = []any{}
		case "group":
			row["slug"] = a.Target.Id
			row["name"] = "Content unavailable"
		case "event":
			row["slug"] = a.Target.Id
			row["title"] = "Content unavailable"
		}
		row["content_unavailable"] = true
		return
	}
	row["content_rating"] = a
	row["is_nsfw"] = a.IsNsfw
	if !a.IsNsfw {
		return
	}
	if !a.Revealed {
		// The homepage client requires this policy to retain an eligible card
		// in its original slot. Keep only policy fields, never article content.
		var homepageAssessment map[string]any
		if a.Target.Kind == "blog" {
			if assessment, ok := row["homepage_eligibility"].(map[string]any); ok {
				homepageAssessment = make(map[string]any)
				for _, key := range []string{"eligible", "lead_eligible", "word_count", "minimum_words", "reasons", "version"} {
					if value, exists := assessment[key]; exists {
						homepageAssessment[key] = value
					}
				}
			}
		}
		safe := map[string]bool{"blog_id": true, "public_id": true, "id": true, "slug": true, "canonical_slug": true, "author_username": true, "owner_account_id": true, "organizer_username": true, "organizer_account_id": true, "created_at": true, "published_time": true, "start_time": true, "end_time": true, "timezone": true, "status": true, "audience": true, "like_count": true, "reply_count": true, "liked": true, "member_count": true}
		for key := range row {
			if !safe[key] {
				delete(row, key)
			}
		}
		if homepageAssessment != nil {
			row["homepage_eligibility"] = homepageAssessment
		}
		switch a.Target.Kind {
		case "blog":
			row["title"] = "Sensitive content"
			row["first_paragraph"] = ""
			row["first_image"] = ""
			row["blog"] = map[string]any{"blocks": []any{}}
		case "discussion":
			row["body"] = "Sensitive content"
			row["files"] = []any{}
			row["replies"] = []any{}
		case "event":
			row["title"] = "Sensitive event"
			row["description"] = ""
			row["cover_image"] = ""
		case "group":
			row["name"] = "Sensitive group"
			row["description"] = ""
			row["logo_image"] = ""
			row["cover_image"] = ""
		}
	}
	row["is_nsfw"] = true
	row["content_rating"] = a
}
