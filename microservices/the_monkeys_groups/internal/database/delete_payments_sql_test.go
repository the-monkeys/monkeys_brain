package database

import (
	"strings"
	"testing"
)

func TestGroupHasBlockingPaymentsSQL(t *testing.T) {
	q := groupHasBlockingPaymentsSQL
	if !strings.Contains(q, "event_payments") || !strings.Contains(q, "pending_payment") {
		t.Fatal("group delete must block on child captured or pending payments")
	}
	if strings.Contains(q, "t.price > 0") {
		t.Fatal("unsold paid tiers must not block group delete")
	}
}
