package database

import "testing"

func TestBuildPaymentRowSplitsGross(t *testing.T) {
	row := BuildPaymentRow(1, 2, 3, "order_1", "pay_1", 100_000)
	if row.Split.PlatformFeePaise != 5000 || row.Split.GstPaise != 900 || row.Split.HostPayablePaise != 94_100 {
		t.Fatalf("got %+v", row.Split)
	}
	if row.Split.PlatformFeePaise+row.Split.GstPaise+row.Split.HostPayablePaise != row.Split.GrossPaise {
		t.Fatalf("split identity failed: %+v", row.Split)
	}
	if row.Status != "captured" {
		t.Fatalf("status = %q", row.Status)
	}
	if row.EventID != 1 || row.AttendeeID != 2 || row.OrganizerUserID != 3 {
		t.Fatalf("ids: %+v", row)
	}
}

func TestNormalizeTicketCurrencyRejectsNonINRPaid(t *testing.T) {
	_, err := normalizeTicketCurrency(100, "USD")
	if err == nil {
		t.Fatal("paid USD must be rejected")
	}
}

func TestNormalizeTicketCurrencyDefaultsINR(t *testing.T) {
	got, err := normalizeTicketCurrency(100, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "INR" {
		t.Fatalf("got %q", got)
	}
}
