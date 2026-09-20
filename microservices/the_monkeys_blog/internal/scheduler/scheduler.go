package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"github.com/the-monkeys/the_monkeys/common/audience"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/models"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/seo"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const (
	// DefaultPollingInterval is the interval between scheduler checks
	DefaultPollingInterval = 30 * time.Second
	// MaxRetries for publishing a blog
	MaxRetries = 3
	// RetryDelay between retries
	RetryDelay = 5 * time.Second
	// MaxFailedCycles is the maximum number of polling cycles a blog can fail
	// before it is excluded from future scheduler queries.
	// After this threshold, the blog requires manual intervention.
	MaxFailedCycles = 5
)

type GroupAuthorizer interface {
	Authorize(ctx context.Context, in *grouppb.AuthorizeGroupReq, opts ...grpc.CallOption) (*grouppb.AuthorizeGroupResp, error)
}

// Scheduler handles the automatic publishing of scheduled blogs
type Scheduler struct {
	db         database.ElasticsearchStorage
	seoManager seo.SEOManager
	qConn      *rabbitmq.ConnManager
	config     *config.Config
	logger     *zap.SugaredLogger
	groups     GroupAuthorizer
	stopCh     chan struct{}
	wg         sync.WaitGroup
	mu         sync.Mutex
	running    bool
}

// NewScheduler creates a new blog scheduler
func NewScheduler(
	db database.ElasticsearchStorage,
	seoManager seo.SEOManager,
	qConn *rabbitmq.ConnManager,
	cfg *config.Config,
	logger *zap.SugaredLogger,
	groups GroupAuthorizer,
) *Scheduler {
	return &Scheduler{
		db:         db,
		seoManager: seoManager,
		qConn:      qConn,
		config:     cfg,
		logger:     logger,
		groups:     groups,
		stopCh:     make(chan struct{}),
	}
}

// Start begins the scheduler background worker
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		s.logger.Warn("Scheduler is already running")
		return
	}
	s.running = true
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run()

	s.logger.Info("📅 Blog Scheduler started - checking for scheduled blogs every 30 seconds")
}

// Stop gracefully stops the scheduler
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stopCh)
	s.wg.Wait()
	s.logger.Info("📅 Blog Scheduler stopped")
}

// run is the main scheduler loop
func (s *Scheduler) run() {
	defer s.wg.Done()

	ticker := time.NewTicker(DefaultPollingInterval)
	defer ticker.Stop()

	// Run immediately on start
	s.processScheduledBlogs()

	for {
		select {
		case <-s.stopCh:
			s.logger.Debug("Scheduler received stop signal")
			return
		case <-ticker.C:
			s.processScheduledBlogs()
		}
	}
}

// processScheduledBlogs fetches and publishes all due scheduled blogs
func (s *Scheduler) processScheduledBlogs() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Get current time in UTC for consistent comparison
	// Note: schedule_time is stored in UTC in Elasticsearch
	currentTime := time.Now().UTC()

	s.logger.Debugf("Scheduler: checking for scheduled blogs due at %s", currentTime.Format(time.RFC3339))

	// Fetch due scheduled blogs, excluding those that have exceeded max failed attempts
	dueBlogs, err := s.db.GetDueScheduledBlogs(ctx, currentTime, MaxFailedCycles)
	if err != nil {
		s.logger.Errorf("Scheduler: failed to fetch due scheduled blogs: %v", err)
		return
	}

	if len(dueBlogs) == 0 {
		s.logger.Debug("Scheduler: no scheduled blogs due")
		return
	}

	s.logger.Infof("Scheduler: found %d scheduled blogs ready to publish", len(dueBlogs))

	// Process each blog
	for _, dueBlog := range dueBlogs {
		blogId, _ := dueBlog.Source["blog_id"].(string)
		accountId, _ := dueBlog.Source["owner_account_id"].(string)

		if blogId == "" {
			s.logger.Warn("Scheduler: skipping blog with empty blog_id")
			continue
		}

		// Publish with retry logic and optimistic concurrency control
		if err := s.publishBlogWithRetry(ctx, blogId, accountId, dueBlog); err != nil {
			if errors.Is(err, database.ErrVersionConflict) {
				s.logger.Infof("Scheduler: blog %s already processed by another instance, skipping", blogId)
				continue
			}

			s.logger.Errorf("Scheduler: failed to publish blog %s after %d retries: %v", blogId, MaxRetries, err)

			// Increment failed attempts counter to prevent infinite retry loop
			if incErr := s.db.IncrementScheduleFailedAttempts(ctx, blogId, err.Error()); incErr != nil {
				s.logger.Errorf("Scheduler: failed to increment failed attempts for blog %s: %v", blogId, incErr)
			}
			continue
		}

		s.logger.Infof("Scheduler: successfully published scheduled blog %s", blogId)
	}
}

// publishBlogWithRetry attempts to publish a blog with exponential backoff retry.
// Version conflicts (from concurrent instance processing) are not retried.
func (s *Scheduler) publishBlogWithRetry(ctx context.Context, blogId, accountId string, dueBlog database.DueScheduledBlog) error {
	var lastErr error

	for attempt := 1; attempt <= MaxRetries; attempt++ {
		err := s.publishBlog(ctx, blogId, accountId, dueBlog)
		if err == nil {
			return nil
		}

		// Version conflict means another instance already published this blog — don't retry
		if errors.Is(err, database.ErrVersionConflict) {
			return err
		}

		lastErr = err
		s.logger.Warnf("Scheduler: publish attempt %d/%d failed for blog %s: %v", attempt, MaxRetries, blogId, err)

		if attempt < MaxRetries {
			// Exponential backoff
			backoffDuration := RetryDelay * time.Duration(attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoffDuration):
				// Continue to next retry
			}
		}
	}

	return lastErr
}

// publishBlog handles the actual publishing of a single blog.
// Uses optimistic concurrency control to prevent duplicate publishes across instances.
func (s *Scheduler) publishBlog(ctx context.Context, blogId, accountId string, dueBlog database.DueScheduledBlog) error {
	seqNo := dueBlog.SeqNo
	primaryTerm := dueBlog.PrimaryTerm

	groupSlug := audience.DocGroupSlug(dueBlog.Source)
	clearGroup, err := s.mustClearScheduledGroup(ctx, accountId, groupSlug)
	if err != nil {
		return err
	}
	if clearGroup {
		groupSlug = ""
	}

	_, err = s.db.PublishScheduledBlog(ctx, blogId, &seqNo, &primaryTerm, clearGroup)
	if err != nil {
		return err
	}

	// Extract tags from blog
	var tags []string
	if tagsInterface, ok := dueBlog.Source["tags"].([]interface{}); ok {
		for _, t := range tagsInterface {
			if tag, ok := t.(string); ok {
				tags = append(tags, tag)
			}
		}
	}

	// Send RabbitMQ message for downstream services
	msg := models.InterServiceMessage{
		AccountId:  accountId,
		BlogId:     blogId,
		Action:     constants.BLOG_PUBLISH,
		BlogStatus: constants.BlogStatusPublished,
		Tags:       tags,
		GroupSlug:  groupSlug,
		Audience:   audience.DocAudience(dueBlog.Source),
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		s.logger.Errorf("Scheduler: failed to marshal RabbitMQ message for blog %s: %v", blogId, err)
		// Don't fail the publish - blog is already published in ES
	} else {
		if err := s.qConn.PublishReliable(s.config.RabbitMQ.Exchange, s.config.RabbitMQ.RoutingKeys[1], msgBytes, s.config.RabbitMQ.MaxRetries); err != nil {
			s.logger.Errorf("Scheduler: failed to publish RabbitMQ message for blog %s: %v", blogId, err)
			// Don't fail the publish - blog is already published in ES
		}
	}

	// Handle SEO (async, non-blocking)
	go func() {
		if audience.IsGroupOnly(audience.DocAudience(dueBlog.Source)) {
			s.logger.Infof("Scheduler: skipping SEO for group-only blog %s", blogId)
			return
		}
		slug := ""
		if s, ok := dueBlog.Source["slug"].(string); ok {
			slug = s
		}
		if slug == "" {
			slug = "blog-" + blogId
		}

		if err := s.seoManager.HandleSEOForBlog(context.Background(), blogId, slug); err != nil {
			s.logger.Errorf("Scheduler: SEO handling failed for blog %s: %v", blogId, err)
		}
	}()

	return nil
}

func (s *Scheduler) mustClearScheduledGroup(ctx context.Context, accountID, groupSlug string) (bool, error) {
	if groupSlug == "" {
		return false, nil
	}
	if s.groups == nil {
		return false, fmt.Errorf("groups client not configured")
	}
	authz, err := s.groups.Authorize(ctx, &grouppb.AuthorizeGroupReq{
		AccountId: accountID,
		GroupSlug: groupSlug,
	})
	if err != nil {
		return false, err
	}
	exists := authz != nil && authz.GetGroupExists()
	member := authz != nil && authz.GetIsMember() && authz.GetMemberStatus() == "active"
	return audience.ShouldDetachScheduledGroup(groupSlug, exists, member), nil
}
