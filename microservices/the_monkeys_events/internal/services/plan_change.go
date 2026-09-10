package services

import (
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/database"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func planChangeSummary(before, after *pb.Event) string {
	if before == nil || after == nil {
		return ""
	}
	var parts []string
	if !sameTimestamp(before.StartTime, after.StartTime) || !sameTimestamp(before.EndTime, after.EndTime) {
		parts = append(parts, "The time changed.")
	}
	if before.Timezone != after.Timezone {
		parts = append(parts, "The timezone changed.")
	}
	if before.Location != after.Location {
		parts = append(parts, "The location changed.")
	}
	if before.MeetingLink != after.MeetingLink {
		parts = append(parts, "The meeting link changed.")
	}
	return strings.Join(parts, " ")
}

func sameTimestamp(a, b *timestamppb.Timestamp) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.AsTime().Equal(b.AsTime())
}

func ticketPriceChanged(before *pb.Event, tierID int64, newPrice float64) bool {
	if before == nil {
		return false
	}
	for _, tier := range before.TicketTiers {
		if tier != nil && tier.Id == tierID {
			return tier.Price != newPrice
		}
	}
	return false
}

func hostNoticeAction(status string) string {
	switch status {
	case database.RSVPPendingHostReview:
		return constants.EVENT_APPLICATION_RECEIVED
	case database.RSVPConfirmed, database.RSVPPendingPayment:
		return constants.EVENT_RSVP_HOST_NOTICE
	default:
		return ""
	}
}

func approveNextStep(status string, amountDue float64) string {
	if status == database.RSVPPendingPayment || amountDue > 0 {
		return "Pay to confirm your seat."
	}
	return "You are in."
}

func eventIsOpen(status string) bool {
	return status == database.StatusPublished || status == database.StatusLive
}
