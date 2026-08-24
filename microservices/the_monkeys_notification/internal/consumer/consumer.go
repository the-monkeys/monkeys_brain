package consumer

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_notification/internal/freerangenotify"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_notification/internal/models"
	"go.uber.org/zap"
)

func ConsumeFromQueue(mgr *rabbitmq.ConnManager, conf config.RabbitMQ, log *zap.SugaredLogger, frn *freerangenotify.Client) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Debug("Notification consumer: received termination signal, exiting")
		os.Exit(0)
	}()

	go consumeQueue(mgr, conf.Queues[4], log, frn)
	select {}
}

func consumeQueue(mgr *rabbitmq.ConnManager, queueName string, log *zap.SugaredLogger, frn *freerangenotify.Client) {
	backoff := time.Second

	for {
		msgs, err := mgr.Channel().Consume(
			queueName,
			"",
			true, // auto-ack
			false,
			false,
			false,
			nil,
		)
		if err != nil {
			log.Errorf("Notification consumer: failed to register on queue '%s', reconnecting in %v: %v", queueName, backoff, err)
			time.Sleep(backoff)
			if backoff *= 2; backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			mgr.Reconnect()
			continue
		}

		backoff = time.Second
		log.Info("Notification consumer: registered on queue: ", queueName)

		for d := range msgs {
			user := models.TheMonkeysMessage{}
			if err := json.Unmarshal(d.Body, &user); err != nil {
				log.Errorf("Failed to unmarshal user from RabbitMQ: %v", err)
				continue
			}
			log.Debugw("Notification consumer: message received",
				"queue", queueName,
				"action", user.Action,
				"username", user.Username,
				"account_id", user.AccountId,
				"email", user.Email,
			)
			handleUserAction(user, log, frn)
		}

		log.Warn("Notification consumer: channel closed, reconnecting...")
		mgr.Reconnect()
	}
}

func handleUserAction(user models.TheMonkeysMessage, log *zap.SugaredLogger, frn *freerangenotify.Client) {
	ctx := context.Background()

	switch user.Action {
	case constants.USER_REGISTER:
		log.Debugw("Processing user registration", "username", user.Username, "email", user.Email, "first_name", user.FirstName, "last_name", user.LastName, "account_id", user.AccountId)
		if err := frn.RegisterUser(ctx, user.Email, user.Username, user.FirstName, user.LastName); err != nil {
			log.Errorw("FRN user registration failed", "username", user.Username, "email", user.Email, "err", err)
		} else {
			log.Debugw("FRN user registered successfully", "username", user.Username, "external_id", user.Username)
		}

	case constants.USER_FOLLOWED:
		log.Debugf("Received user follow: %s → %s", user.Username, user.NewUsername)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.NewUsername,
			InAppTpl: constants.FRNTplNewFollowerInApp,
			SSETpl:   constants.FRNTplNewFollowerSSE,
			Priority: "normal",
			Category: constants.FRNCategorySocial,
			Data: map[string]interface{}{
				"follower_name": user.Username,
			},
		}, log); err != nil {
			log.Errorw("FRN follow notification failed", "follower", user.Username, "target", user.NewUsername, "err", err)
		}

	case constants.BLOG_LIKE:
		log.Debugf("Received blog like: %s liked %s", user.NewUsername, user.BlogId)
		blogTitle := user.BlogTitle
		if blogTitle == "" {
			blogTitle = user.BlogId // Fallback: publisher doesn't always include title
		}
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.Username,
			InAppTpl: constants.FRNTplBlogLikedInApp,
			SSETpl:   constants.FRNTplBlogLikedSSE,
			Priority: "low",
			Category: constants.FRNCategorySocial,
			Data: map[string]interface{}{
				"liker_name": user.NewUsername,
				"blog_id":    user.BlogId,
				"blog_title": blogTitle,
			},
		}, log); err != nil {
			log.Errorw("FRN like notification failed", "user", user.Username, "err", err)
		}

	case constants.CO_AUTHOR_INVITE:
		log.Debugf("Received co-author invite: %s invited %s for blog %s", user.Username, user.NewUsername, user.BlogId)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.NewUsername,
			InAppTpl: constants.FRNTplCoAuthorInviteInApp,
			SSETpl:   constants.FRNTplCoAuthorInviteSSE,
			EmailTpl: constants.FRNTplCoAuthorInviteEmail,
			Priority: "high",
			Category: constants.FRNCategoryCollaboration,
			Data: map[string]interface{}{
				"inviter_name": user.Username,
				"blog_title":   user.BlogTitle,
				"blog_id":      user.BlogId,
			},
		}, log); err != nil {
			log.Errorw("FRN co-author invite notification failed", "err", err)
		}

	case constants.CO_AUTHOR_ACCEPT:
		log.Debugf("Received co-author accept: %s accepted invite for blog %s", user.Username, user.BlogId)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.NewUsername,
			InAppTpl: constants.FRNTplCoAuthorAcceptInApp,
			SSETpl:   constants.FRNTplCoAuthorAcceptSSE,
			Priority: "normal",
			Category: constants.FRNCategoryCollaboration,
			Data: map[string]interface{}{
				"coauthor_name": user.Username,
				"blog_title":    user.BlogTitle,
				"blog_id":       user.BlogId,
			},
		}, log); err != nil {
			log.Errorw("FRN co-author accept notification failed", "err", err)
		}

	case constants.CO_AUTHOR_DECLINE:
		log.Debugf("Received co-author decline: %s declined invite for blog %s", user.Username, user.BlogId)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.NewUsername,
			InAppTpl: constants.FRNTplCoAuthorDeclineInApp,
			Priority: "normal",
			Category: constants.FRNCategoryCollaboration,
			Data: map[string]interface{}{
				"coauthor_name": user.Username,
				"blog_title":    user.BlogTitle,
				"blog_id":       user.BlogId,
			},
		}, log); err != nil {
			log.Errorw("FRN co-author decline notification failed", "err", err)
		}

	case constants.CO_AUTHOR_REMOVED:
		log.Debugf("Received co-author removed: %s removed %s from blog %s", user.Username, user.NewUsername, user.BlogId)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.NewUsername,
			InAppTpl: constants.FRNTplCoAuthorRemovedInApp,
			SSETpl:   constants.FRNTplCoAuthorRemovedSSE,
			Priority: "normal",
			Category: constants.FRNCategoryCollaboration,
			Data: map[string]interface{}{
				"remover_name": user.Username,
				"blog_title":   user.BlogTitle,
				"blog_id":      user.BlogId,
			},
		}, log); err != nil {
			log.Errorw("FRN co-author removed notification failed", "err", err)
		}

	case constants.CO_AUTHOR_BLOG_PUBLISHED:
		log.Debugf("Received co-author blog published: %s published blog %s", user.Username, user.BlogId)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.NewUsername,
			InAppTpl: constants.FRNTplBlogPublishedCoAuthorInApp,
			SSETpl:   constants.FRNTplBlogPublishedCoAuthorSSE,
			Priority: "normal",
			Category: constants.FRNCategoryContent,
			Data: map[string]interface{}{
				"publisher_name": user.Username,
				// "blog_title":     user.BlogTitle,
				"blog_id": user.BlogId,
			},
		}, log); err != nil {
			log.Errorw("FRN co-author blog published notification failed", "err", err)
		}

	case constants.PASSWORD_CHANGED:
		log.Debugf("Received password changed: %s", user.Username)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.Username,
			InAppTpl: constants.FRNTplPasswordChangedInApp,
			EmailTpl: constants.FRNTplPasswordChangedEmail,
			Priority: "high",
			Category: constants.FRNCategorySecurity,
			Data:     map[string]interface{}{},
		}, log); err != nil {
			log.Errorw("FRN password changed notification failed", "err", err)
		}

	case constants.EMAIL_VERIFIED:
		log.Debugf("Received email verified: %s", user.Username)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.Username,
			InAppTpl: constants.FRNTplEmailVerifiedInApp,
			Priority: "high",
			Category: constants.FRNCategorySecurity,
			Data:     map[string]interface{}{},
		}, log); err != nil {
			log.Errorw("FRN email verified notification failed", "err", err)
		}

	case constants.LOGIN_DETECTED:
		log.Debugf("Received login detected: %s", user.Username)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.Username,
			InAppTpl: constants.FRNTplLoginDetectedInApp,
			SSETpl:   constants.FRNTplLoginDetectedSSE,
			EmailTpl: constants.FRNTplLoginDetectedEmail,
			Priority: "high",
			Category: constants.FRNCategorySecurity,
			Data: map[string]interface{}{
				"ip_address":   user.IpAddress,
				"client":       user.Client,
				"login_method": user.LoginMethod,
			},
		}, log); err != nil {
			log.Errorw("FRN login detected notification failed", "user", user.Username, "err", err)
		}

	case constants.PASSWORD_RESET_REQUESTED:
		log.Debugf("Received password reset requested: %s", user.Username)
		// if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
		// 	UserID:   user.Username,
		// 	InAppTpl: constants.FRNTplPasswordResetReqInApp,
		// 	EmailTpl: constants.FRNTplPasswordResetReqEmail,
		// 	Priority: "high",
		// 	Category: constants.FRNCategorySecurity,
		// 	Data: map[string]interface{}{
		// 		"ip_address": user.IpAddress,
		// 	},
		// }, log); err != nil {
		// 	log.Errorw("FRN password reset requested notification failed", "user", user.Username, "err", err)
		// }

	case constants.EMAIL_CHANGED:
		log.Debugw("Processing email change", "username", user.Username, "new_email", user.Email)
		// Update email in FRN via PUT /users/{external_id} so future email notifications use the new address
		log.Debugw("Updating email in FRN", "external_id", user.Username, "new_email", user.Email)
		if err := frn.UpdateUserEmail(ctx, user.Username, user.Email); err != nil {
			log.Errorw("FRN email update failed", "username", user.Username, "new_email", user.Email, "err", err)
		} else {
			log.Debugw("FRN user email updated successfully", "username", user.Username, "new_email", user.Email)
		}
		// Notify user about the email change (in-app only — SMTP emails already sent by authz service)
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.Username,
			InAppTpl: constants.FRNTplEmailChangedInApp,
			Priority: "high",
			Category: constants.FRNCategorySecurity,
			Data: map[string]interface{}{
				"new_email": user.Email,
			},
		}, log); err != nil {
			log.Errorw("FRN email changed notification failed", "username", user.Username, "err", err)
		}

	case constants.USERNAME_CHANGED:
		log.Debugw("Processing username change", "old_username", user.Username, "new_username", user.NewUsername)

		// Send notification under the OLD username — FRN still knows the user by this external_id
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.Username,
			InAppTpl: constants.FRNTplUsernameChangedInApp,
			Priority: "normal",
			Category: constants.FRNCategorySecurity,
			Data: map[string]interface{}{
				"new_username": user.NewUsername,
			},
		}, log); err != nil {
			log.Errorw("FRN username changed notification failed", "old_username", user.Username, "new_username", user.NewUsername, "err", err)
		}

		// Now update FRN external_id: old → new
		log.Debugw("Updating FRN external_id", "old_external_id", user.Username, "new_external_id", user.NewUsername)
		if err := frn.UpdateUserExternalID(ctx, user.Username, user.NewUsername); err != nil {
			log.Errorw("FRN external_id update failed", "old", user.Username, "new", user.NewUsername, "err", err)
		} else {
			log.Debugw("FRN external_id updated", "old", user.Username, "new", user.NewUsername)
		}

	case constants.USER_ACCOUNT_DELETE:
		log.Debugw("=== NOTIFICATION: USER_ACCOUNT_DELETE received ===",
			"username", user.Username,
			"account_id", user.AccountId,
			"email", user.Email,
			"blog_ids_count", len(user.BlogIds),
		)

		// Remove user from FRN via DELETE /users/{external_id}
		log.Debugw("Deleting user from FRN", "external_id", user.Username)
		if err := frn.DeleteUser(ctx, user.Username); err != nil {
			log.Errorw("FRN user deletion failed", "user", user.Username, "err", err)
		} else {
			log.Debugw("FRN user deleted successfully", "username", user.Username)
		}
		log.Debugw("=== NOTIFICATION: USER_ACCOUNT_DELETE complete ===", "username", user.Username)

	case constants.USER_DEACTIVATED:
		log.Debugw("Processing user deactivation", "username", user.Username)
		// Enable DND in FRN to suppress all notifications while deactivated
		log.Debugw("Setting DND=true in FRN", "external_id", user.Username)
		if err := frn.DeactivateUser(ctx, user.Username); err != nil {
			log.Errorw("FRN user deactivation (DND on) failed", "user", user.Username, "err", err)
		} else {
			log.Debugw("FRN user DND enabled successfully", "username", user.Username)
		}
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.Username,
			InAppTpl: constants.FRNTplAccountDeactivatedInApp,
			Priority: "normal",
			Category: constants.FRNCategoryAccount,
			Data:     map[string]interface{}{},
		}, log); err != nil {
			log.Warnw("FRN deactivation notification failed", "user", user.Username, "err", err)
		}

	case constants.USER_REACTIVATED:
		log.Debugw("Processing user reactivation", "username", user.Username)
		// Clear DND in FRN to resume notifications
		log.Debugw("Setting DND=false in FRN", "external_id", user.Username)
		if err := frn.ReactivateUser(ctx, user.Username); err != nil {
			log.Errorw("FRN user reactivation (DND off) failed", "user", user.Username, "err", err)
		} else {
			log.Debugw("FRN user DND disabled successfully", "username", user.Username)
		}
		if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
			UserID:   user.Username,
			InAppTpl: constants.FRNTplAccountReactivatedInApp,
			Priority: "normal",
			Category: constants.FRNCategoryAccount,
			Data:     map[string]interface{}{},
		}, log); err != nil {
			log.Warnw("FRN reactivation notification failed", "user", user.Username, "err", err)
		}

	case constants.PREFERENCES_CHANGED:
		log.Debugw("Processing preferences change", "username", user.Username, "raw_payload", user.Notification)
		// Sync notification preferences to FRN
		// The message carries preference flags in the Notification field as JSON
		emailEnabled := true // default on
		pushEnabled := true  // default on
		if user.Notification != "" {
			var prefs struct {
				EmailEnabled *bool `json:"email_enabled"`
				PushEnabled  *bool `json:"push_enabled"`
			}
			if err := json.Unmarshal([]byte(user.Notification), &prefs); err != nil {
				log.Warnw("Failed to parse preferences payload, using defaults", "user", user.Username, "err", err)
			} else {
				if prefs.EmailEnabled != nil {
					emailEnabled = *prefs.EmailEnabled
				}
				if prefs.PushEnabled != nil {
					pushEnabled = *prefs.PushEnabled
				}
			}
		}
		log.Debugw("Syncing preferences to FRN", "external_id", user.Username, "email_enabled", emailEnabled, "push_enabled", pushEnabled)
		if err := frn.UpdateUserPreferences(ctx, user.Username, emailEnabled, pushEnabled); err != nil {
			log.Errorw("FRN preferences sync failed", "user", user.Username, "err", err)
		} else {
			log.Debugw("FRN preferences synced successfully", "username", user.Username, "email_enabled", emailEnabled, "push_enabled", pushEnabled)
		}

	default:
		if handleEventAction(ctx, user, log, frn) {
			return
		}
		log.Warnf("Unknown notification action: %s", user.Action)
	}
}

// eventTemplates maps each event action to the FRN templates it renders with.
// Ticketing and cancellation also go out by email because they carry money or
// a change of plans; the rest stay in-app.
var eventTemplates = map[string]struct {
	inApp    string
	email    string
	priority string
}{
	constants.EVENT_RSVP_CONFIRMED:    {constants.FRNTplEventRSVPConfirmedInApp, constants.FRNTplEventRSVPConfirmedEmail, "normal"},
	constants.EVENT_RSVP_WAITLISTED:   {constants.FRNTplEventRSVPWaitlistedInApp, "", "normal"},
	constants.EVENT_WAITLIST_PROMOTED: {constants.FRNTplEventWaitlistPromoInApp, constants.FRNTplEventWaitlistPromoEmail, "high"},
	constants.EVENT_REMINDER:          {constants.FRNTplEventReminderInApp, constants.FRNTplEventReminderEmail, "high"},
	constants.EVENT_CANCELLED:         {constants.FRNTplEventCancelledInApp, constants.FRNTplEventCancelledEmail, "high"},
	constants.EVENT_NEW_BY_FOLLOWED:   {constants.FRNTplEventNewByFollowedInApp, "", "low"},
	constants.EVENT_COMMENT_NEW:       {constants.FRNTplEventCommentInApp, "", "low"},
	constants.EVENT_PAYMENT_REFUND:    {constants.FRNTplEventRefundInApp, constants.FRNTplEventRefundEmail, "high"},
}

// handleEventAction dispatches the events service notifications. Every event
// action shares one payload shape, so a table keeps this to a single branch.
// It reports whether the action was recognised.
func handleEventAction(ctx context.Context, user models.TheMonkeysMessage, log *zap.SugaredLogger, frn *freerangenotify.Client) bool {
	tpl, ok := eventTemplates[user.Action]
	if !ok {
		return false
	}

	log.Debugw("Processing event notification",
		"action", user.Action, "recipient", user.NewUsername, "event", user.EventSlug)

	if err := freerangenotify.Notify(ctx, frn, freerangenotify.NotifyRequest{
		UserID:   user.NewUsername,
		InAppTpl: tpl.inApp,
		EmailTpl: tpl.email,
		Priority: tpl.priority,
		Category: constants.FRNCategoryEvents,
		Data: map[string]interface{}{
			"event_slug":  user.EventSlug,
			"event_title": user.EventTitle,
			"actor_name":  user.Username,
			"message":     user.Notification,
		},
	}, log); err != nil {
		log.Errorw("FRN event notification failed",
			"action", user.Action, "recipient", user.NewUsername, "err", err)
	}
	return true
}
