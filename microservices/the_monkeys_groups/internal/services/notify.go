package services

import (
	"context"
	"encoding/json"

	"github.com/the-monkeys/the_monkeys/common/interservice"
	"github.com/the-monkeys/the_monkeys/constants"
)

const notificationRoutingKey = 4

type groupNotification = interservice.Message

func groupJoinAction(status string) string {
	switch status {
	case "pending":
		return constants.GROUP_JOIN_REQUESTED
	case "active":
		return constants.GROUP_MEMBER_JOINED
	default:
		return ""
	}
}

func (s *GroupService) notify(n groupNotification) {
	if s.qConn == nil || s.cfg == nil || n.NewUsername == "" {
		return
	}
	if len(s.cfg.RabbitMQ.RoutingKeys) <= notificationRoutingKey {
		return
	}
	body, err := json.Marshal(n)
	if err != nil {
		s.log.Errorw("failed to marshal group notification", "action", n.Action, "err", err)
		return
	}
	go func() {
		if err := s.qConn.PublishMessage(
			s.cfg.RabbitMQ.Exchange,
			s.cfg.RabbitMQ.RoutingKeys[notificationRoutingKey],
			body,
		); err != nil {
			s.log.Errorw("failed to publish group notification", "action", n.Action, "err", err)
		}
	}()
}

func (s *GroupService) notifyAll(recipients []string, n groupNotification) {
	for _, recipient := range recipients {
		if recipient == n.Username {
			continue
		}
		n.NewUsername = recipient
		s.notify(n)
	}
}

func (s *GroupService) groupName(ctx context.Context, slug string) string {
	name, err := s.db.GroupName(ctx, slug)
	if err != nil {
		s.log.Warnw("failed to load group name for notification", "slug", slug, "err", err)
		return slug
	}
	return name
}

func (s *GroupService) actorUsername(ctx context.Context, accountID string) string {
	username, err := s.db.UsernameByAccountID(ctx, accountID)
	if err != nil {
		s.log.Warnw("failed to resolve actor username", "account_id", accountID, "err", err)
		return ""
	}
	return username
}

func (s *GroupService) notifyStaff(ctx context.Context, slug, actor, action string) {
	if action == "" {
		return
	}
	staff, err := s.db.StaffUsernames(ctx, slug)
	if err != nil {
		s.log.Warnw("failed to load group staff for notification", "slug", slug, "err", err)
		return
	}
	s.notifyAll(staff, groupNotification{
		Username:  actor,
		Action:    action,
		GroupSlug: slug,
		GroupName: s.groupName(ctx, slug),
	})
}
