// Package searchcrawler verifies server-attested search indexing requests.
// It never trusts a user agent or a forwarding header as authorization by itself.
package searchcrawler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var detailPath = regexp.MustCompile(`^/api/v2/blog/[A-Za-z0-9_]+$`)
var reserved = map[string]bool{"meta-feed": true, "homepage": true, "feed": true, "search": true, "trending": true, "following-feed": true, "my-published": true, "my-drafts": true}
var agents = []struct {
	name  string
	match *regexp.Regexp
}{
	{"google", regexp.MustCompile(`(?i)(?:^|[ ;(])(?:Googlebot(?:-News|-Image|-Video)?|Google-InspectionTool)/[0-9]`)},
	{"openai", regexp.MustCompile(`(?i)(?:^|[ ;(])OAI-SearchBot/[0-9]`)},
	{"perplexity", regexp.MustCompile(`(?i)(?:^|[ ;(])PerplexityBot/[0-9]`)},
}

type ipList struct {
	ranges  []netip.Prefix
	expires time.Time
	retry   time.Time
}
type Verifier struct {
	key     []byte
	enabled bool
	now     func() time.Time
	client  *http.Client
	urls    map[string]string
	mu      sync.Mutex
	lists   map[string]ipList
}

func New(enabled bool, key string) *Verifier {
	return &Verifier{
		enabled: enabled && len(key) >= 32, key: []byte(key), now: time.Now,
		client: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("crawler list redirect rejected") }},
		urls: map[string]string{
			"google":     "https://developers.google.com/static/crawling/ipranges/common-crawlers.json",
			"openai":     "https://openai.com/searchbot.json",
			"perplexity": "https://www.perplexity.ai/perplexitybot.json",
		}, lists: make(map[string]ipList),
	}
}
func FromEnvironment() *Verifier {
	return New(os.Getenv("NSFW_SEO_ENABLED") == "1", os.Getenv("SEO_CRAWLER_SIGNING_KEY"))
}
func (v *Verifier) Enabled() bool { return v != nil && v.enabled }
func (v *Verifier) Verify(r *http.Request) bool {
	if !v.Enabled() || r.Method != "GET" || r.URL.RawQuery != "" || !detailPath.MatchString(r.URL.Path) || reserved[strings.TrimPrefix(r.URL.Path, "/api/v2/blog/")] || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
		return false
	}
	ipText := r.Header.Get("X-Monkeys-Crawler-IP")
	ip, err := netip.ParseAddr(ipText)
	if err != nil || ip.Zone() != "" {
		return false
	}
	agent := r.Header.Get("X-Monkeys-Crawler-Agent")
	if len(agent) > 512 {
		return false
	}
	provider := ""
	for _, a := range agents {
		if a.match.MatchString(agent) {
			provider = a.name
			break
		}
	}
	if provider == "" {
		return false
	}
	tsText := r.Header.Get("X-Monkeys-Crawler-Time")
	ts, err := strconv.ParseInt(tsText, 10, 64)
	if err != nil || strconv.FormatInt(ts, 10) != tsText {
		return false
	}
	age := v.now().Unix() - ts
	if age < -5 || age > 60 {
		return false
	}
	signature, err := hex.DecodeString(r.Header.Get("X-Monkeys-Crawler-Proof"))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte("v1\nGET\n" + r.URL.Path + "\n" + ipText + "\n" + agent + "\n" + tsText))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	return v.contains(r, provider, ip.Unmap())
}
func (v *Verifier) contains(r *http.Request, provider string, ip netip.Addr) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	list := v.lists[provider]
	if !now.Before(list.expires) {
		if now.Before(list.retry) {
			return false
		}
		// Never use an expired list after a failed refresh.
		next := ipList{retry: now.Add(time.Minute)}
		v.lists[provider] = next
		req, err := http.NewRequestWithContext(r.Context(), "GET", v.urls[provider], nil)
		if err != nil {
			return false
		}
		response, err := v.client.Do(req)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false
		}
		var data struct {
			Prefixes []struct {
				V4 string `json:"ipv4Prefix"`
				V6 string `json:"ipv6Prefix"`
			} `json:"prefixes"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&data) != nil {
			return false
		}
		for _, entry := range data.Prefixes {
			for _, raw := range []string{entry.V4, entry.V6} {
				if raw == "" {
					continue
				}
				prefix, err := netip.ParsePrefix(raw)
				if err != nil || prefix.Bits() == 0 {
					return false
				}
				next.ranges = append(next.ranges, prefix)
			}
		}
		if len(next.ranges) == 0 {
			return false
		}
		next.expires = now.Add(time.Hour)
		list = next
		v.lists[provider] = next
	}
	for _, prefix := range list.ranges {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
