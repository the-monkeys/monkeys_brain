package database

import (
	"strings"
	"testing"
)

func TestEventHasBlockingPaymentsSQL(t *testing.T) {
	q := eventHasBlockingPaymentsSQL
	if !strings.Contains(q, "event_payments") {
		t.Fatal("must inspect event_payments ledger")
	}
	if !strings.Contains(q, "pending_payment") {
		t.Fatal("must block in-flight checkout")
	}
	if !strings.Contains(q, "payment_id") {
		t.Fatal("must still see pre-ledger captured attendees")
	}
	if strings.Contains(strings.ToUpper(q), "DELETE FROM EVENT_PAYMENTS") {
		t.Fatal("paid gate must not mutate the ledger")
	}
}
