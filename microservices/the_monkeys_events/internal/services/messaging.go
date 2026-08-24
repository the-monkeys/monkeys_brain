package services

import (
	"encoding/json"
)

// eventNotification is the payload the notification service consumes. It
// mirrors models.TheMonkeysMessage and adds the two event fields, so the
// existing consumer keeps working while event notifications carry context.
type eventNotification struct {
	AccountId    string `json:"account_id,omitempty"`
	Username     string `json:"username"`     // actor
	NewUsername  string `json:"new_username"` // recipient
	Action       string `json:"action"`
	Notification string `json:"notification"`
	EventSlug    string `json:"event_slug"`
	EventTitle   string `json:"event_title"`
}

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
