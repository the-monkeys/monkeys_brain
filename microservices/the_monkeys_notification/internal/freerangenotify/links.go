package freerangenotify

import (
	"net/url"
	"os"
	"regexp"
	"strings"
)

const defaultAppPublicURL = "https://monkeys.com.co"

var (
	nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)
	multiHyphen  = regexp.MustCompile(`-+`)
)

var profileNameKeys = [][2]string{
	{"actor_name", "actor_url"},
	{"follower_name", "follower_url"},
	{"liker_name", "liker_url"},
	{"inviter_name", "inviter_url"},
	{"coauthor_name", "coauthor_url"},
	{"remover_name", "remover_url"},
	{"publisher_name", "publisher_url"},
	{"commenter_name", "commenter_url"},
}

// AppPublicURL is the origin used in email hrefs. APP_PUBLIC_URL wins, then
// SEO_BASE_URL, then production.
func AppPublicURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_PUBLIC_URL")), "/"); v != "" {
		return v
	}
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("SEO_BASE_URL")), "/"); v != "" {
		return v
	}
	return defaultAppPublicURL
}

// EnrichData adds clickable Monkeys URLs for Freerange templates. Empty slugs
// stay omitted so in-app payloads do not invent links.
func EnrichData(data map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(data)+20)
	for k, v := range data {
		out[k] = v
	}

	base := AppPublicURL()
	out["home_url"] = base
	out["settings_url"] = base + "/settings"
	out["events_home_url"] = base + "/events"
	out["groups_home_url"] = base + "/groups"
	out["notifications_url"] = base + "/notifications"

	for _, pair := range profileNameKeys {
		if name := stringVal(out[pair[0]]); name != "" {
			out[pair[1]] = profileURL(base, name)
		}
	}
	if slug := stringVal(out["event_slug"]); slug != "" {
		out["event_url"] = joinPath(base, "/events/"+slug)
	}
	if slug := stringVal(out["group_slug"]); slug != "" {
		out["group_url"] = joinPath(base, "/groups/"+slug)
	}
	if id := stringVal(out["blog_id"]); id != "" {
		out["blog_url"] = blogURL(base, stringVal(out["blog_title"]), id)
	}
	return out
}

func stringVal(v interface{}) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func profileURL(base, username string) string {
	handle := strings.TrimPrefix(username, "@")
	if handle == "" {
		return ""
	}
	return joinPath(base, "/"+handle)
}

func blogURL(base, title, id string) string {
	if title != "" && title != id {
		return joinPath(base, "/blog/"+slugify(title)+"-"+id)
	}
	return joinPath(base, "/blog/"+id)
}

func joinPath(base, path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u, err := url.Parse(base + path)
	if err != nil {
		return base + path
	}
	return u.String()
}

func slugify(title string) string {
	s := strings.ToLower(strings.ReplaceAll(title, "’", "'"))
	s = strings.ReplaceAll(s, "'s", "")
	s = strings.ReplaceAll(s, "'", "")
	s = nonSlugChars.ReplaceAllString(s, "-")
	s = multiHyphen.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
