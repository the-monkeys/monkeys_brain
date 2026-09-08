package services

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestApplyEventRedactionHidesDraftFromStranger(t *testing.T) {
	ev := &pb.Event{Status: "draft", MeetingLink: "https://meet.example/secret"}
	err := applyEventRedaction(ev, false, "")
	if status.Code(err) != codes.NotFound {
		t.Fatalf("draft stranger: got %v", err)
	}
}

func TestApplyEventRedactionHostKeepsDraftAndLink(t *testing.T) {
	ev := &pb.Event{Status: "draft", MeetingLink: "https://meet.example/secret"}
	if err := applyEventRedaction(ev, true, ""); err != nil {
		t.Fatal(err)
	}
	if ev.MeetingLink == "" {
		t.Fatal("host must keep meeting link")
	}
}

func TestApplyEventRedactionStripsLinkUnlessConfirmed(t *testing.T) {
	ev := &pb.Event{Status: "published", MeetingLink: "https://meet.example/secret"}
	if err := applyEventRedaction(ev, false, ""); err != nil {
		t.Fatal(err)
	}
	if ev.MeetingLink != "" {
		t.Fatal("anonymous viewer must not see meeting link")
	}
	ev.MeetingLink = "https://meet.example/secret"
	if err := applyEventRedaction(ev, false, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if ev.MeetingLink == "" {
		t.Fatal("confirmed attendee must see meeting link")
	}
}
