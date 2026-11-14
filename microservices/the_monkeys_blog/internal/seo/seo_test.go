package seo

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/the-monkeys/the_monkeys/config"
	"go.uber.org/zap"
)

type rejectingSEOTransport struct {
	requests atomic.Int64
}

func (r *rejectingSEOTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.requests.Add(1)
	return nil, errors.New("outbound HTTP disabled in SEO regression tests")
}

func rejectSEOHTTP(t *testing.T) *rejectingSEOTransport {
	t.Helper()
	transport := &rejectingSEOTransport{}
	previousTransport, previousClient := http.DefaultTransport, http.DefaultClient
	http.DefaultTransport = transport
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() {
		http.DefaultTransport, http.DefaultClient = previousTransport, previousClient
		if got := transport.requests.Load(); got != 0 {
			t.Errorf("SEO attempted %d outbound HTTP requests; want zero", got)
		}
	})
	return transport
}

func TestHandleSEOForBlogSkipsExternalSubmission(t *testing.T) {
	// These tests change HTTP globals and must not run in parallel.
	rejectSEOHTTP(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancelExpired()

	legacy := config.SEO{
		Enabled:                 true,
		GoogleIndexingAPIKey:    "unused-test-value",
		GoogleIndexingAPI:       "https://indexing.example.invalid/publish",
		SearchConsoleSitemapURL: "https://posts.example.invalid/sitemap.xml",
		BaseURL:                 "https://posts.example.invalid",
		// An empty path cannot read an actual credential file, including on RED.
		GoogleCredentialsFile: "",
	}
	disabled := legacy
	disabled.Enabled = false

	for _, tc := range []struct {
		name string
		seo  config.SEO
		ctx  context.Context
		slug string
	}{
		{"zero_config", config.SEO{}, context.Background(), "ordinary-post"},
		{"disabled_with_legacy_settings", disabled, context.Background(), "ordinary-post"},
		{"enabled_without_credentials", config.SEO{Enabled: true}, context.Background(), "ordinary-post"},
		{"enabled_with_legacy_settings", legacy, context.Background(), "ordinary-post"},
		{"empty_slug", legacy, context.Background(), ""},
		{"canceled_context", legacy, canceled, "ordinary-post"},
		{"expired_context", legacy, expired, "ordinary-post"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := NewSEOManager(zap.NewNop().Sugar(), &config.Config{SEO: tc.seo})
			if err := manager.HandleSEOForBlog(tc.ctx, "post-123", tc.slug); err != nil {
				t.Fatalf("ordinary-post discovery must not require external submission: %v", err)
			}
		})
	}
}

func TestHandleSEOForBlogConcurrentCalls(t *testing.T) {
	rejectSEOHTTP(t)
	manager := NewSEOManager(zap.NewNop().Sugar(), &config.Config{SEO: config.SEO{Enabled: true}})
	const calls = 32
	results := make(chan error, calls)
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- manager.HandleSEOForBlog(context.Background(), "post-123", "ordinary-post")
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent SEO call must remain a successful no-op: %v", err)
		}
	}
}

func TestSEOManagerSharingMethodsRemainAvailable(t *testing.T) {
	rejectSEOHTTP(t)
	manager := NewSEOManager(zap.NewNop().Sugar(), &config.Config{})
	if err := manager.ShareBlogToTelegram(context.Background(), "post-123", "ordinary-post"); err != nil {
		t.Errorf("Telegram sharing: %v", err)
	}
	if err := manager.ShareBlogToDiscord(context.Background(), "post-123", "ordinary-post"); err != nil {
		t.Errorf("Discord sharing: %v", err)
	}
}
