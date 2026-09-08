package database

import (
	"strings"
	"testing"
)

func TestAccountOrganizedBlockingGroupsSQL(t *testing.T) {
	q := accountOrganizedBlockingGroupsSQL
	if !strings.Contains(q, "organizer_id") || !strings.Contains(q, "account_id") {
		t.Fatal("must scope to groups this account created")
	}
	if !strings.Contains(q, "event_payments") || !strings.Contains(q, "pending_payment") {
		t.Fatal("must block on child captured or pending payments")
	}
	if strings.Contains(q, "t.price > 0") {
		t.Fatal("unsold paid tiers must not block")
	}
}
