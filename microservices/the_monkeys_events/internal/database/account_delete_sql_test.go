package database

import (
	"strings"
	"testing"
)

func TestAccountOrganizedBlockingEventsSQL(t *testing.T) {
	q := accountOrganizedBlockingEventsSQL
	if !strings.Contains(q, "organizer_id") || !strings.Contains(q, "account_id") {
		t.Fatal("must scope to the account's organized events")
	}
	if !strings.Contains(q, "event_payments") || !strings.Contains(q, "pending_payment") {
		t.Fatal("must use the same paid gate as single-event delete")
	}
	if !strings.Contains(q, "attendee_id") || !strings.Contains(q, "UNION") {
		t.Fatal("must also block payment-linked RSVPs on other events")
	}
	if strings.Contains(strings.ToUpper(q), "DELETE FROM EVENT_PAYMENTS") {
		t.Fatal("paid gate must not mutate the ledger")
	}
}

func TestUnlinkUnpaidAttendeesLeavesLedger(t *testing.T) {
	q := unlinkUnpaidAttendeesSQL
	if !strings.Contains(q, "event_attendees") || !strings.Contains(q, "NOT EXISTS") {
		t.Fatal("must drop only RSVPs without a ledger row")
	}
	if !strings.Contains(q, "event_payments") {
		t.Fatal("must leave payment-linked attendees untouched")
	}
}
