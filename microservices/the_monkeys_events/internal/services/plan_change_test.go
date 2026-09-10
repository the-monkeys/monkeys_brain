package services

import (
	"testing"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_events/internal/database"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPlanChangeSummaryJoinsPlanBreakingFields(t *testing.T) {
	start := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	before := &pb.Event{
		StartTime:   timestamppb.New(start),
		EndTime:     timestamppb.New(start.Add(time.Hour)),
		Timezone:    "UTC",
		Location:    "Pune",
		MeetingLink: "https://meet.example/old",
	}
	after := &pb.Event{
		StartTime:   timestamppb.New(start.Add(2 * time.Hour)),
		EndTime:     timestamppb.New(start.Add(3 * time.Hour)),
		Timezone:    "Asia/Kolkata",
		Location:    "Mumbai",
		MeetingLink: "https://meet.example/new",
	}
	got := planChangeSummary(before, after)
	want := "The time changed. The timezone changed. The location changed. The meeting link changed."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPlanChangeSummaryEmptyWhenUnchanged(t *testing.T) {
	start := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	ev := &pb.Event{
		StartTime: timestamppb.New(start),
		EndTime:   timestamppb.New(start.Add(time.Hour)),
		Timezone:  "UTC",
		Location:  "Pune",
		Title:     "new title only",
	}
	if got := planChangeSummary(ev, ev); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestTicketPriceChanged(t *testing.T) {
	before := &pb.Event{TicketTiers: []*pb.TicketTier{{Id: 7, Price: 500}}}
	if !ticketPriceChanged(before, 7, 700) {
		t.Fatal("expected price change")
	}
	if ticketPriceChanged(before, 7, 500) {
		t.Fatal("same price is not plan-breaking")
	}
}

func TestHostNoticeAction(t *testing.T) {
	if got := hostNoticeAction(database.RSVPPendingHostReview); got != constants.EVENT_APPLICATION_RECEIVED {
		t.Fatalf("review=%q", got)
	}
	if got := hostNoticeAction(database.RSVPConfirmed); got != constants.EVENT_RSVP_HOST_NOTICE {
		t.Fatalf("confirmed=%q", got)
	}
	if got := hostNoticeAction(database.RSVPPendingPayment); got != constants.EVENT_RSVP_HOST_NOTICE {
		t.Fatalf("pending_payment=%q", got)
	}
	if got := hostNoticeAction(database.RSVPWaitlisted); got != "" {
		t.Fatalf("waitlist should stay guest-only, got %q", got)
	}
}

func TestApproveNextStep(t *testing.T) {
	if got := approveNextStep(database.RSVPConfirmed, 0); got != "You are in." {
		t.Fatalf("free=%q", got)
	}
	if got := approveNextStep(database.RSVPPendingPayment, 250); got != "Pay to confirm your seat." {
		t.Fatalf("paid=%q", got)
	}
}
