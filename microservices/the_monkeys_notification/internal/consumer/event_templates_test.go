package consumer

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_notification/internal/models"
)

func TestFanoutNotifyRequestWiresSSEAndExtraFields(t *testing.T) {
	req, ok := fanoutNotifyRequest(models.TheMonkeysMessage{
		Action:        constants.EVENT_APPLICATION_APPROVED,
		NewUsername:   "guest",
		Username:      "host",
		EventSlug:     "tea-talk",
		EventTitle:    "Tea Talk",
		GroupSlug:     "writers",
		GroupName:     "Writers",
		NextStep:      "You are in.",
		ChangeSummary: "The time changed.",
		Reason:        "full",
	})
	if !ok {
		t.Fatal("expected recognised action")
	}
	if req.UserID != "guest" {
		t.Fatalf("user=%q", req.UserID)
	}
	if req.InAppTpl != constants.FRNTplEventApplicationApprovedInApp {
		t.Fatalf("inapp=%q", req.InAppTpl)
	}
	if req.SSETpl != constants.FRNTplEventApplicationApprovedSSE {
		t.Fatalf("sse=%q", req.SSETpl)
	}
	if req.EmailTpl != constants.FRNTplEventApplicationApprovedEmail {
		t.Fatalf("email=%q", req.EmailTpl)
	}
	if req.Priority != "high" || req.Category != constants.FRNCategoryEvents {
		t.Fatalf("priority=%q category=%q", req.Priority, req.Category)
	}
	want := map[string]string{
		"actor_name":     "host",
		"event_slug":     "tea-talk",
		"event_title":    "Tea Talk",
		"group_slug":     "writers",
		"group_name":     "Writers",
		"next_step":      "You are in.",
		"change_summary": "The time changed.",
		"reason":         "full",
	}
	for k, v := range want {
		got, ok := req.Data[k]
		if !ok || got != v {
			t.Fatalf("data[%s]=%v want %q", k, req.Data[k], v)
		}
	}
}

func TestFanoutNotifyRequestHostNoticeSkipsEmail(t *testing.T) {
	req, ok := fanoutNotifyRequest(models.TheMonkeysMessage{
		Action:     constants.EVENT_RSVP_HOST_NOTICE,
		NewUsername: "host",
		Username:    "guest",
		EventSlug:   "tea-talk",
		EventTitle:  "Tea Talk",
	})
	if !ok {
		t.Fatal("expected recognised action")
	}
	if req.SSETpl != constants.FRNTplEventRSVPHostNoticeSSE {
		t.Fatalf("sse=%q", req.SSETpl)
	}
	if req.EmailTpl != "" {
		t.Fatalf("host notice must not email, got %q", req.EmailTpl)
	}
}

func TestFanoutNotifyRequestUnknownAction(t *testing.T) {
	if _, ok := fanoutNotifyRequest(models.TheMonkeysMessage{Action: "not_a_thing"}); ok {
		t.Fatal("unknown action must be rejected")
	}
}
