package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_social_post/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_social_post/internal/models"
	"go.uber.org/zap"
)

// Worker repeatedly claims and executes due publish jobs against the mock
// providers. It is intentionally simple: first-release scope is mock/internal
// accounts only, so "publishing" means validating and recording a
// deterministic provider reference, not calling a real network API.
type Worker struct {
	store    *Service
	id       string
	log      *zap.SugaredLogger
	lease    time.Duration
	batch    int
	interval time.Duration
}

// NewWorker builds a worker bound to svc's database store. workerID should be
// unique per process (e.g. hostname:pid) so lease ownership is unambiguous
// across replicas.
func NewWorker(svc *Service, workerID string, log *zap.SugaredLogger) *Worker {
	return &Worker{store: svc, id: workerID, log: log, lease: 30 * time.Second, batch: 10, interval: 2 * time.Second}
}

// Run polls for due jobs until ctx is cancelled. It never returns an error;
// transient claim/execution failures are logged and retried on the next tick
// so a single bad job cannot stall the whole worker loop.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.tick(ctx); err != nil {
				w.log.Errorw("social publish worker tick failed", "err", err)
			}
		}
	}
}

func (w *Worker) tick(ctx context.Context) error {
	jobs, err := database.ClaimDueJobs(ctx, w.store.store.DB, w.id, w.batch, w.lease)
	if err != nil {
		return fmt.Errorf("claim due social publish jobs: %w", err)
	}
	for _, job := range jobs {
		w.execute(ctx, job)
	}
	return nil
}

func (w *Worker) execute(ctx context.Context, job database.Job) {
	detail, err := database.MarkPublishing(ctx, w.store.store.DB, job.ID)
	if err != nil {
		w.log.Errorw("mark social publish job publishing", "job_id", job.ID, "err", err)
		return
	}
	attemptNumber := detail.AttemptCount + 1

	providerRef, mockErr := mockPublish(detail.Platform, detail.Text)
	if mockErr == nil {
		if err := database.CompleteJobSuccess(ctx, w.store.store.DB, job.ID, detail.RenditionID, detail.SocialAccountID,
			detail.PostID, w.id, attemptNumber, providerRef); err != nil {
			w.log.Errorw("complete social publish job success", "job_id", job.ID, "err", err)
		}
		return
	}
	if err := database.CompleteJobFailure(ctx, w.store.store.DB, job.ID, detail.RenditionID, detail.SocialAccountID,
		detail.PostID, w.id, attemptNumber, detail.MaxAttempts, "mock_publish_failed", mockErr.Error()); err != nil {
		w.log.Errorw("complete social publish job failure", "job_id", job.ID, "err", err)
	}
}

// mockPublish simulates a provider call. It enforces the same per-platform
// text policy the gateway validates at write time (defense in depth against
// a rendition mutated after validation, e.g. by a future bulk-edit path), and
// honors a "::force-transient-failure::" marker so retry/backoff/replay can
// be exercised deterministically in tests and demos without real providers.
func mockPublish(platform, text string) (string, error) {
	if strings.Contains(text, "::force-transient-failure::") {
		return "", fmt.Errorf("simulated transient provider error")
	}
	if v := models.ValidateText(platform, text); v != nil {
		return "", fmt.Errorf("%s: %s", v.RuleID, v.Message)
	}
	ref := make([]byte, 8)
	if _, err := rand.Read(ref); err != nil {
		return "", fmt.Errorf("generate mock provider reference: %w", err)
	}
	return fmt.Sprintf("mock:%s:%s", platform, hex.EncodeToString(ref)), nil
}
