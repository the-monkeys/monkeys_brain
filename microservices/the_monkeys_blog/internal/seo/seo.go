package seo

import (
	"context"

	"github.com/the-monkeys/the_monkeys/config"
	"go.uber.org/zap"
)

type SEOManager interface {
	HandleSEOForBlog(ctx context.Context, blogId, slug string) error
	ShareBlogToTelegram(ctx context.Context, blogId, slug string) error
	ShareBlogToDiscord(ctx context.Context, blogId, slug string) error
}

type seoManager struct {
	log    *zap.SugaredLogger
	config *config.Config
}

func NewSEOManager(log *zap.SugaredLogger, config *config.Config) SEOManager {
	return &seoManager{
		log:    log,
		config: config,
	}
}

// HandleSEOForBlog preserves the publishing hook without submitting ordinary posts
// to Google's restricted Indexing API or the retired sitemap ping endpoint.
func (s *seoManager) HandleSEOForBlog(ctx context.Context, blogId, slug string) error {
	if !s.config.SEO.Enabled {
		s.log.Debugw("SEO disabled", "blog_id", blogId)
		return nil
	}

	s.log.Debugw("Skipping external indexing submission for ordinary blog posts; discovery relies on crawlable links and sitemaps",
		"blog_id", blogId, "slug", slug)
	return nil
}

func (s *seoManager) ShareBlogToTelegram(ctx context.Context, blogId, slug string) error {
	// Placeholder implementation
	s.log.Infof("Sharing blog %s to Telegram with slug: %s", blogId, slug)
	// Implement actual Telegram sharing logic here
	return nil
}

func (s *seoManager) ShareBlogToDiscord(ctx context.Context, blogId, slug string) error {
	// Placeholder implementation
	s.log.Infof("Sharing blog %s to Discord with slug: %s", blogId, slug)
	// Implement actual Discord sharing logic here
	return nil
}
