package services

import (
	"encoding/json"

	"github.com/the-monkeys/the_monkeys/common/interservice"
)

// eventNotification is the payload the notification service consumes.
type eventNotification = interservice.Message

// notify publishes one notification. Failures are logged rather than
// propagated: a missed notification must never fail the user's request.
func (s *EventService) notify(n eventNotification) {
	if n.NewUsername == "" {
		return
	}
	body, err := json.Marshal(n)
	if err != nil {
		s.log.Errorw("failed to marshal event notification", "action", n.Action, "err", err)
		return
	}
	go func() {
		if err := s.qConn.PublishMessage(
			s.cfg.RabbitMQ.Exchange,
			s.cfg.RabbitMQ.RoutingKeys[notificationRoutingKey],
			body,
		); err != nil {
			s.log.Errorw("failed to publish event notification", "action", n.Action, "err", err)
		}
	}()
}

// notifyAll fans one notification out to a list of recipients.
func (s *EventService) notifyAll(recipients []string, n eventNotification) {
	for _, recipient := range recipients {
		if recipient == n.Username {
			continue // never notify the actor about their own action
		}
		n.NewUsername = recipient
		s.notify(n)
	}
}

func (s *EventService) publishStorageDelete(action, eventSlug, groupSlug string) {
	if s.qConn == nil || s.cfg == nil || len(s.cfg.RabbitMQ.RoutingKeys) <= storageRoutingKey {
		return
	}
	body, err := json.Marshal(interservice.Message{
		Action:    action,
		EventSlug: eventSlug,
		GroupSlug: groupSlug,
	})
	if err != nil {
		s.log.Errorw("failed to marshal entity delete", "action", action, "err", err)
		return
	}
	rk := s.cfg.RabbitMQ.RoutingKeys[storageRoutingKey]
	go func() {
		if err := s.qConn.PublishReliable(s.cfg.RabbitMQ.Exchange, rk, body, s.cfg.RabbitMQ.MaxRetries); err != nil {
			s.log.Errorw("failed to publish entity delete to storage", "action", action, "routing_key", rk, "err", err)
		}
	}()
}
