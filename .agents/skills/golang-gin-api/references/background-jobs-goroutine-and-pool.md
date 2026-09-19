# Background Jobs — Goroutine Pattern & Worker Pool

See also: `background-jobs-queue-and-shutdown.md`

## Common Use Cases

| Use Case | Recommended Pattern |
|----------|-------------------|
| Send email after signup | Simple goroutine |
| Generate PDF report | Worker pool |
| Process payment webhook | DB-backed queue |
| Sync data with external API | External queue (asynq) |

## Simple Goroutine Pattern

Use `c.Copy()` before passing `*gin.Context` to a goroutine. The original context is returned to the pool when the request ends — using it after that causes a data race.

```go
// internal/handler/user_handler.go
func (h *UserHandler) Create(c *gin.Context) {
    var req CreateUserRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
        return
    }

    user, err := h.userSvc.Create(c.Request.Context(), req)
    if err != nil {
        h.logger.Error("create user failed", "err", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create user"})
        return
    }

    // c.Copy() returns a shallow copy safe to use outside the request lifecycle
    cCopy := c.Copy()
    go func() {
        if err := h.emailSvc.SendWelcome(cCopy.Request.Context(), user); err != nil {
            h.logger.Error("welcome email failed", "userID", user.ID, "err", err)
        }
    }()

    c.JSON(http.StatusCreated, user)
}
```

**Warning:** fire-and-forget goroutines have no retry, no persistence, and are lost on process crash. Use only for non-critical side effects.

---

## Worker Pool Pattern

A buffered channel acts as a bounded job queue. Prevents unbounded goroutine spawning under load.

```go
// internal/worker/worker.go
type Job func(ctx context.Context)

type Worker struct {
    jobs   chan Job
    logger *slog.Logger
}

func NewWorker(bufferSize int, logger *slog.Logger) *Worker {
    return &Worker{
        jobs:   make(chan Job, bufferSize),
        logger: logger,
    }
}

// Submit enqueues a job. Returns error if the queue is full (non-blocking).
func (w *Worker) Submit(job Job) error {
    select {
    case w.jobs <- job:
        return nil
    default:
        return fmt.Errorf("worker queue full")
    }
}

// Start processes jobs until ctx is cancelled. Run in a goroutine.
func (w *Worker) Start(ctx context.Context) {
    for {
        select {
        case job := <-w.jobs:
            job(ctx)
        case <-ctx.Done():
            // Drain remaining jobs before exit
            for {
                select {
                case job := <-w.jobs:
                    job(ctx)
                default:
                    w.logger.Info("worker pool stopped")
                    return
                }
            }
        }
    }
}
```

Usage in a handler — submit without blocking the request:

```go
// internal/handler/report_handler.go
func (h *ReportHandler) Generate(c *gin.Context) {
    var req GenerateReportRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
        return
    }

    reportID := uuid.NewString()

    if err := h.worker.Submit(func(ctx context.Context) {
        if err := h.reportSvc.Generate(ctx, reportID, req); err != nil {
            h.logger.Error("report generation failed", "reportID", reportID, "err", err)
        }
    }); err != nil {
        c.JSON(http.StatusServiceUnavailable, gin.H{"error": "server busy, try again later"})
        return
    }

    c.JSON(http.StatusAccepted, gin.H{"report_id": reportID})
}
```
