# Background Jobs — DB-Backed Queue, External Queue & Graceful Shutdown

See also: `background-jobs-goroutine-and-pool.md`

## Database-Backed Job Queue

For persistence across restarts and guaranteed at-least-once processing.

**Jobs table (PostgreSQL):**

```sql
CREATE TABLE jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type         TEXT NOT NULL,
    payload      JSONB NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',  -- pending | running | done | failed
    attempts     INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_at    TIMESTAMPTZ
);

CREATE INDEX ON jobs (status, locked_at) WHERE status = 'pending';
```

**Polling worker — claims with atomic UPDATE to prevent double-processing:**

```go
// internal/worker/db_worker.go
func (w *DBWorker) claimNext(ctx context.Context) (*Job, error) {
    var job Job
    err := w.db.QueryRowContext(ctx, `
        UPDATE jobs SET status = 'running', locked_at = now(), attempts = attempts + 1
        WHERE id = (
            SELECT id FROM jobs
            WHERE status = 'pending'
              AND attempts < max_attempts
              AND locked_at IS NULL
            ORDER BY created_at
            FOR UPDATE SKIP LOCKED
            LIMIT 1
        )
        RETURNING id, type, payload, attempts
    `).Scan(&job.ID, &job.Type, &job.Payload, &job.Attempts)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    return &job, err
}

func (w *DBWorker) Start(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            job, err := w.claimNext(ctx)
            if err != nil {
                w.logger.Error("claim job failed", "err", err)
                continue
            }
            if job == nil {
                continue
            }
            go w.process(ctx, job)
        case <-ctx.Done():
            return
        }
    }
}
```

Retry with exponential backoff: on failure set `status = 'pending'`, `locked_at = NULL`. After `max_attempts` set `status = 'failed'` (dead letter).

---

## External Queue (Brief)

Use when you need multiple workers, guaranteed delivery, or complex retry logic.

**`github.com/hibiken/asynq`** — Redis-backed task queue, drop-in for most use cases:

```go
// Enqueue (in handler)
client := asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
task := asynq.NewTask("email:welcome", payload)
_, err = client.EnqueueContext(c.Request.Context(), task)

// Worker (in main.go)
srv := asynq.NewServer(asynq.RedisClientOpt{Addr: redisAddr}, asynq.Config{Concurrency: 10})
mux := asynq.NewServeMux()
mux.HandleFunc("email:welcome", emailHandler.ProcessWelcome)
srv.Run(mux)
```

Use NATS or RabbitMQ when you need fan-out, pub/sub, or cross-service messaging.

---

## Graceful Shutdown

Stop accepting new jobs and drain in-flight work before process exit.

```go
// cmd/api/main.go
ctx, cancel := context.WithCancel(context.Background())

go worker.Start(ctx)

quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit

slog.Info("shutting down, draining jobs...")
cancel()                        // signals worker to stop and drain
time.Sleep(5 * time.Second)    // give in-flight jobs time to finish
slog.Info("shutdown complete")
```

For the DB-backed worker, the drain loop finishes processing claimed jobs even after `ctx` is cancelled before releasing the process.
