package consumer

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/models"
	"go.uber.org/zap"
)

func ConsumeFromQueue(mgr *rabbitmq.ConnManager, conf config.RabbitMQ, log *zap.SugaredLogger, db database.ElasticsearchStorage) {

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Debug("Graceful shutdown initiated - blog consumer exiting")
		os.Exit(0)
	}()

	// Consume from queue[3] with reconnect loop
	go consumeQueue(mgr, conf.Queues[3], log, db)

	select {}
}

// consumeQueue registers a consumer on the given queue and processes messages.
// On channel close it reconnects via the shared ConnManager and re-registers,
// so consumption never permanently stops due to a broker restart or blip.
func consumeQueue(mgr *rabbitmq.ConnManager, queueName string, log *zap.SugaredLogger, db database.ElasticsearchStorage) {
	backoff := time.Second

	for {
		msgs, err := mgr.Channel().Consume(
			queueName,
			"",    // consumer tag
			true,  // auto-ack
			false, // exclusive
			false, // no-local
			false, // no-wait
			nil,
		)
		if err != nil {
			log.Errorf("Blog consumer: failed to register on queue '%s', reconnecting in %v: %v", queueName, backoff, err)
			time.Sleep(backoff)
			backoff = nextBackoff(backoff)
			mgr.Reconnect()
			continue
		}

		backoff = time.Second
		log.Infof("Blog consumer: registered on queue '%s'", queueName)

		for d := range msgs {
			user := models.InterServiceMessage{}
			if err := json.Unmarshal(d.Body, &user); err != nil {
				log.Errorf("Blog consumer: message deserialization failed: %v", err)
				continue
			}
			log.Debugw("Blog consumer: message received",
				"queue", queueName,
				"action", user.Action,
				"account_id", user.AccountId,
				"blog_id", user.BlogId,
				"blog_ids_count", len(user.BlogIds),
			)
			handleUserAction(user, log, db)
		}

		// Channel closed — reconnect and loop
		log.Warn("Blog consumer: channel closed, reconnecting...")
		mgr.Reconnect()
	}
}

// nextBackoff doubles the duration, capped at 30 seconds.
func nextBackoff(d time.Duration) time.Duration {
	if d *= 2; d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

func handleUserAction(user models.InterServiceMessage, log *zap.SugaredLogger, db database.ElasticsearchStorage) {
	switch user.Action {
	case constants.USER_ACCOUNT_DELETE:
		log.Debugw("=== BLOG: USER_ACCOUNT_DELETE received ===",
			"account_id", user.AccountId,
			"blog_ids_count", len(user.BlogIds),
			"blog_ids", user.BlogIds,
		)
		resp, err := db.DeleteBlogsByOwnerAccountID(context.Background(), user.AccountId)
		if err != nil {
			log.Errorw("Blog ES deletion failed", "account_id", user.AccountId, "err", err)
			return
		}
		if resp == nil {
			log.Debugw("=== BLOG: USER_ACCOUNT_DELETE complete — no blogs found in ES ===", "account_id", user.AccountId)
		} else {
			log.Debugw("=== BLOG: USER_ACCOUNT_DELETE complete — blogs removed from ES ===",
				"account_id", user.AccountId,
				"status_code", resp.StatusCode,
			)
		}

	case constants.GROUP_DELETE:
		slug := strings.TrimSpace(user.GroupSlug)
		if slug == "" {
			log.Warn("GROUP_DELETE missing group slug")
			return
		}
		log.Debugw("=== BLOG: GROUP_DELETE received ===", "group_slug", slug)
		if err := db.DetachBlogsFromGroup(context.Background(), slug); err != nil {
			log.Errorw("Blog ES detach from group failed", "group_slug", slug, "err", err)
			return
		}
		log.Debugw("=== BLOG: GROUP_DELETE complete — blogs detached in ES ===", "group_slug", slug)

	case constants.GROUP_AUDIENCE_COERCE:
		slug := strings.TrimSpace(user.GroupSlug)
		if slug == "" {
			log.Warn("GROUP_AUDIENCE_COERCE missing group slug")
			return
		}
		if err := db.CoerceBlogsToGroupOnly(context.Background(), slug); err != nil {
			log.Errorw("Blog ES audience coerce failed", "group_slug", slug, "err", err)
			return
		}
		log.Debugw("=== BLOG: GROUP_AUDIENCE_COERCE complete ===", "group_slug", slug)

	default:
		log.Warnf("Received unsupported action type: %s", user.Action)
	}
}
