package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/utils"
	"go.uber.org/zap"
)

type UserDbConn struct {
	dbConn database.UserDb
	log    *zap.SugaredLogger
	config *config.Config
}

func NewUserDb(dbConn database.UserDb, log *zap.SugaredLogger, config *config.Config) *UserDbConn {
	return &UserDbConn{dbConn: dbConn, log: log, config: config}
}

// ConsumeFromQueue connects to RabbitMQ queue2 and processes messages.
// It automatically reconnects if the channel/connection drops, using
// exponential backoff (1s → 2s → 4s … 30s cap).
// Messages are manually acked on success; on processing failure they are
// nacked without requeue so RabbitMQ routes them to the dead letter queue.
func ConsumeFromQueue(mgr *rabbitmq.ConnManager, conf *config.Config, log *zap.SugaredLogger, dbConn database.UserDb) {
	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Debug("Received termination signal. Closing connection and exiting gracefully.")
		os.Exit(0)
	}()

	userCon := NewUserDb(dbConn, log, conf)
	backoff := time.Second

	for {
		msgs, err := mgr.Channel().Consume(
			conf.RabbitMQ.Queues[1], // queue
			"",                      // consumer
			false,                   // auto-ack OFF — we ack/nack manually
			false,                   // exclusive
			false,                   // no-local
			false,                   // no-wait
			nil,                     // args
		)
		if err != nil {
			log.Errorf("Failed to register a consumer, reconnecting in %v: %v", backoff, err)
			time.Sleep(backoff)
			backoff = nextBackoff(backoff)
			mgr.Reconnect()
			continue
		}

		// Reset backoff on successful consume registration
		backoff = time.Second
		log.Info("Consumer registered on queue: ", conf.RabbitMQ.Queues[1])

		// Process messages until the channel closes
		for d := range msgs {
			if err := processMessage(userCon, log, d.Body); err != nil {
				// Processing failed — nack without requeue so the message goes to the DLQ
				log.Errorf("Message processing failed, sending to DLQ: %v", err)
				if nackErr := d.Nack(false, false); nackErr != nil {
					log.Errorf("Failed to nack message: %v", nackErr)
				}
			} else {
				// Processing succeeded — ack the message
				if ackErr := d.Ack(false); ackErr != nil {
					log.Errorf("Failed to ack message: %v", ackErr)
				}
			}
		}

		// If we reach here, the channel was closed — reconnect
		log.Warn("RabbitMQ channel closed, reconnecting in ", backoff)
		time.Sleep(backoff)
		backoff = nextBackoff(backoff)
		mgr.Reconnect()
	}
}

// nextBackoff doubles the backoff duration, capping at 30 seconds.
func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > 30*time.Second {
		return 30 * time.Second
	}
	return next
}

// processMessage unmarshals and handles a single RabbitMQ delivery.
// Returns nil on success; returns an error if processing fails so the
// caller can nack the message to the dead letter queue.
func processMessage(userCon *UserDbConn, log *zap.SugaredLogger, body []byte) error {
	user := models.TheMonkeysMessage{}
	if err := json.Unmarshal(body, &user); err != nil {
		log.Errorf("Failed to unmarshal message from RabbitMQ, sending to DLQ: %v", err)
		return fmt.Errorf("unmarshal failed: %w", err)
	}

	log.Debugf("consumer received message: %+v", user)
	userLog := &models.UserLogs{AccountId: user.AccountId, IpAddress: user.IpAddress, Client: user.Client}
	userLog.IpAddress, userLog.Client = utils.IpClientConvert(userLog.IpAddress, userLog.Client)

	switch user.Action {
	case constants.BLOG_CREATE:
		log.Debugf("Creating blog: %+v", user)
		if err := userCon.dbConn.AddBlogWithId(user); err != nil {
			log.Errorf("Error creating blog: %v", err)
			return fmt.Errorf("AddBlogWithId failed for %s: %w", user.BlogId, err)
		}

	case constants.BLOG_UPDATE:
		usr, err := userCon.dbConn.GetBlogByBlogId(user.BlogId)
		if err != nil {
			log.Errorf("Error getting blog: %v", err)
			return fmt.Errorf("GetBlogByBlogId failed for %s: %w", user.BlogId, err)
		}
		if usr.BlogStatus != user.BlogStatus {
			if err := userCon.dbConn.UpdateBlogStatusToDraft(user.BlogId, user.BlogStatus); err != nil {
				log.Errorf("Can't update blog status to draft: %v", err)
				return fmt.Errorf("UpdateBlogStatusToDraft failed for %s: %w", user.BlogId, err)
			}
		}

	case constants.BLOG_PUBLISH:
		log.Infof("User published a blog: blogId=%s, accountId=%s", user.BlogId, user.AccountId)
		if err := userCon.dbConn.UpdateBlogStatusToPublish(user); err != nil {
			log.Errorf("Can't update blog status to publish: %v", err)
			return fmt.Errorf("UpdateBlogStatusToPublish failed for %s: %w", user.BlogId, err)
		}
		for _, tag := range user.Tags {
			if err := userCon.dbConn.InsertTopicWithCategory(context.Background(), tag, "General"); err != nil {
				log.Errorf("Can't insert topic for publish: %v", err)
				// Non-critical: don't send to DLQ for tag insertion failures
			}
		}

	case constants.BLOG_DELETE:
		if err := userCon.dbConn.DeleteBlogAndReferences(user.BlogId); err != nil {
			log.Errorf("Can't delete blog %s from user service: %v", user.BlogId, err)
			return fmt.Errorf("DeleteBlogAndReferences failed for %s: %w", user.BlogId, err)
		}

	case constants.BLOG_SCHEDULE:
		log.Infof("User scheduled a blog: blogId=%s, accountId=%s", user.BlogId, user.AccountId)
		if err := userCon.dbConn.UpdateBlogStatusToPublish(user); err != nil {
			log.Errorf("Can't update blog status to schedule: %v", err)
			return fmt.Errorf("UpdateBlogStatusToPublish (schedule) failed for %s: %w", user.BlogId, err)
		}
		for _, tag := range user.Tags {
			if err := userCon.dbConn.InsertTopicWithCategory(context.Background(), tag, "General"); err != nil {
				log.Errorf("Can't insert topic for schedule: %v", err)
			}
		}

	case constants.GROUP_AUDIENCE_COERCE:
		if user.GroupSlug == "" {
			log.Warn("GROUP_AUDIENCE_COERCE missing group slug")
			return nil
		}
		if err := userCon.dbConn.CoerceBlogAudienceByGroupSlug(user.GroupSlug); err != nil {
			log.Errorf("Can't coerce blog audience for group %s: %v", user.GroupSlug, err)
			return fmt.Errorf("CoerceBlogAudienceByGroupSlug failed for %s: %w", user.GroupSlug, err)
		}

	default:
		log.Errorf("Unknown action: %s", user.Action)
		return fmt.Errorf("unknown action: %s", user.Action)
	}

	return nil
}
