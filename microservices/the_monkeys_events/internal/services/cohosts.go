package services

import (
	"context"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/constants"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Resolve identity before changing permissions. Reads are deliberately free of
// GetEvent's geocoding writes, and notification dispatch happens only on success.
func (s *EventService) coHostContext(ctx context.Context, req *pb.CoHostReq) (*pb.Event, string, error) {
	if req == nil || req.GetEventSlug() == "" || req.GetAccountId() == "" {
		return nil, "", status.Error(codes.InvalidArgument, "event and account are required")
	}
	req.CohostUsername = strings.TrimPrefix(strings.TrimSpace(req.CohostUsername), "@")
	if req.CohostUsername == "" {
		return nil, "", status.Error(codes.InvalidArgument, "co-host username is required")
	}
	event, _, err := s.db.GetCalendarEvent(ctx, req.EventSlug, req.AccountId)
	if err != nil {
		return nil, "", err
	}
	if event == nil {
		return nil, "", status.Error(codes.NotFound, "event not found")
	}
	if req.CohostUsername == event.OrganizerUsername {
		return nil, "", status.Error(codes.InvalidArgument, "the organizer cannot be added or removed as a co-host")
	}
	actor, err := s.db.UsernameByAccountID(ctx, req.AccountId)
	if err != nil {
		return nil, "", err
	}
	return event, actor, nil
}

func (s *EventService) notifyCoHost(event *pb.Event, actor, recipient, action string) {
	if event == nil || recipient == "" || actor == recipient {
		return
	}
	s.notify(eventNotification{Username: actor, NewUsername: recipient, Action: action, EventSlug: event.Slug, EventTitle: event.Title})
}

func (s *EventService) notifyCreatedCoHosts(event *pb.Event) {
	if event == nil {
		return
	}
	seen := make(map[string]bool, len(event.CoHostUsernames))
	for _, username := range event.CoHostUsernames {
		if username == "" || seen[username] {
			continue
		}
		seen[username] = true
		s.notifyCoHost(event, event.OrganizerUsername, username, constants.EVENT_COHOST_ADDED)
	}
}
