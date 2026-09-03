package database

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNormalizeRsvpCloseHours(t *testing.T) {
	for _, h := range []int32{0, 12, 24, 72, 168} {
		got, err := normalizeRsvpCloseHours(h)
		if err != nil || got != h {
			t.Fatalf("hours=%d: got %d err %v", h, got, err)
		}
	}
	if _, err := normalizeRsvpCloseHours(6); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unexpected hours should be InvalidArgument, got %v", err)
	}
}

func TestRsvpClosesValue(t *testing.T) {
	start := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	if rsvpClosesValue(start, 0) != nil {
		t.Fatal("off must store a NULL close")
	}
	got, ok := rsvpClosesValue(start, 24).(time.Time)
	if !ok {
		t.Fatal("expected a timestamp")
	}
	want := start.Add(-24 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestValidateRsvpClosesAt(t *testing.T) {
	start := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	if err := validateRsvpClosesAt(start, nil); err != nil {
		t.Fatal(err)
	}
	if err := validateRsvpClosesAt(start, timestamppb.New(start.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := validateRsvpClosesAt(start, timestamppb.New(start)); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("close at start should be InvalidArgument, got %v", err)
	}
}
