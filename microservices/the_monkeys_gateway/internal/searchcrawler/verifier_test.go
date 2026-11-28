package searchcrawler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testKey = "test-only-crawler-signing-key-123456789"

func signedRequest(path, ip, agent string, at time.Time) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	ts := strconv.FormatInt(at.Unix(), 10)
	r.Header.Set("X-Monkeys-Crawler-IP", ip)
	r.Header.Set("X-Monkeys-Crawler-Agent", agent)
	r.Header.Set("X-Monkeys-Crawler-Time", ts)
	mac := hmac.New(sha256.New, []byte(testKey))
	mac.Write([]byte("v1\nGET\n" + path + "\n" + ip + "\n" + agent + "\n" + ts))
	r.Header.Set("X-Monkeys-Crawler-Proof", hex.EncodeToString(mac.Sum(nil)))
	return r
}
func fixtureVerifier() *Verifier {
	v := New(true, testKey)
	v.now = func() time.Time { return time.Unix(1800000000, 0) }
	v.lists["google"] = ipList{ranges: []netip.Prefix{netip.MustParsePrefix("66.249.66.0/24"), netip.MustParsePrefix("2001:4860:4801::/48")}, expires: v.now().Add(time.Hour)}
	return v
}
func TestVerifiedCrawlerNeedsAllEvidence(t *testing.T) {
	for _, ip := range []string{"66.249.66.1", "2001:4860:4801::1"} {
		v := fixtureVerifier()
		if !v.Verify(signedRequest("/api/v2/blog/restricted", ip, "Googlebot/2.1", v.now())) {
			t.Fatal("valid crawler denied", ip)
		}
	}
}
func TestCrawlerRejectsForgeryAndReplay(t *testing.T) {
	cases := map[string]func(*http.Request){
		"wrong ip":          func(r *http.Request) { r.Header.Set("X-Monkeys-Crawler-IP", "66.249.66.2") },
		"wrong target":      func(r *http.Request) { r.URL.Path = "/api/v2/blog/other" },
		"method":            func(r *http.Request) { r.Method = "POST" },
		"query":             func(r *http.Request) { r.URL.RawQuery = "reveal=true" },
		"missing signature": func(r *http.Request) { r.Header.Del("X-Monkeys-Crawler-Proof") },
		"wrong signature":   func(r *http.Request) { r.Header.Set("X-Monkeys-Crawler-Proof", strings.Repeat("0", 64)) },
		"cookie":            func(r *http.Request) { r.Header.Set("Cookie", "mat=user") },
		"authorization":     func(r *http.Request) { r.Header.Set("Authorization", "Bearer user") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := fixtureVerifier()
			r := signedRequest("/api/v2/blog/restricted", "66.249.66.1", "Googlebot/2.1", v.now())
			mutate(r)
			if v.Verify(r) {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	v := fixtureVerifier()
	for _, delta := range []time.Duration{-61 * time.Second, 6 * time.Second} {
		if v.Verify(signedRequest("/api/v2/blog/restricted", "66.249.66.1", "Googlebot/2.1", v.now().Add(delta))) {
			t.Fatal("stale or future request accepted")
		}
	}
	for _, path := range []string{"/api/v2/blog/meta-feed", "/api/v2/blog/restricted/stats", "/api/v1/events/restricted", "/api/v2/blog/../restricted"} {
		if v.Verify(signedRequest(path, "66.249.66.1", "Googlebot/2.1", v.now())) {
			t.Fatal("non-detail accepted", path)
		}
	}
}
func TestSignedNonCrawlerAndUserFetchersStayBlocked(t *testing.T) {
	v := fixtureVerifier()
	for _, agent := range []string{"Mozilla/5.0", "ChatGPT-User/1.0", "GPTBot/1.0", "Perplexity-User/1.0"} {
		if v.Verify(signedRequest("/api/v2/blog/restricted", "66.249.66.1", agent, v.now())) {
			t.Fatal(agent)
		}
	}
	r := signedRequest("/api/v2/blog/restricted", "192.0.2.4", "Googlebot/2.1", v.now())
	if v.Verify(r) {
		t.Fatal("signed non-provider IP accepted")
	}
}
func TestMissingConfigurationKeepsOldBehavior(t *testing.T) {
	for _, v := range []*Verifier{New(false, testKey), New(true, ""), New(true, "short")} {
		if v.Enabled() || v.Verify(signedRequest("/api/v2/blog/restricted", "66.249.66.1", "Googlebot/2.1", time.Now())) {
			t.Fatal("incomplete config enabled")
		}
	}
}
func TestProviderListFetchIsBoundedAndFailsClosed(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
	defer server.Close()
	v := fixtureVerifier()
	v.urls = map[string]string{"google": server.URL}
	v.client = server.Client()
	v.lists["google"] = ipList{}
	for i := 0; i < 2; i++ {
		if v.Verify(signedRequest("/api/v2/blog/restricted", "66.249.66.1", "Googlebot/2.1", v.now())) {
			t.Fatal("failed list accepted")
		}
	}
	if calls != 1 {
		t.Fatal("negative cache missing", calls)
	}
}
func TestProviderListParsingAndExpiry(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"prefixes":[{"ipv4Prefix":"66.249.66.0/24"},{"ipv6Prefix":"2001:4860:4801::/48"}]}`))
	}))
	defer server.Close()
	v := fixtureVerifier()
	v.lists["google"] = ipList{}
	v.urls = map[string]string{"google": server.URL}
	v.client = server.Client()
	if !v.Verify(signedRequest("/api/v2/blog/restricted", "66.249.66.1", "Googlebot/2.1", v.now())) {
		t.Fatal("official-shaped list not loaded")
	}
	if v.Verify(signedRequest("/api/v2/blog/restricted", "66.249.67.1", "Googlebot/2.1", v.now())) {
		t.Fatal("out-of-range IP allowed")
	}
}

func TestGoogleInspectionToolRequiresSameVerifiedGoogleProvenance(t *testing.T) {
	v := fixtureVerifier()
	if !v.Verify(signedRequest("/api/v2/blog/restricted", "66.249.66.1", "Google-InspectionTool/1.0", v.now())) {
		t.Fatal("verified Search Console inspector denied")
	}
	if v.Verify(signedRequest("/api/v2/blog/restricted", "192.0.2.1", "Google-InspectionTool/1.0", v.now())) {
		t.Fatal("spoofed inspector accepted")
	}
	if v.Verify(signedRequest("/api/v2/blog/restricted", "66.249.66.1", "GoogleOther/1.0", v.now())) {
		t.Fatal("unrelated common crawler accepted")
	}
}
