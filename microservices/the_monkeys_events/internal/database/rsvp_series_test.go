package database

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestParseRSVPScope(t *testing.T) {
	series, err := parseRSVPScope("")
	if err != nil || series {
		t.Fatalf("empty should be this, got series=%v err=%v", series, err)
	}
	series, err = parseRSVPScope("this")
	if err != nil || series {
		t.Fatalf("this should be this, got series=%v err=%v", series, err)
	}
	series, err = parseRSVPScope("series")
	if err != nil || !series {
		t.Fatalf("series should be series, got series=%v err=%v", series, err)
	}
	if _, err := parseRSVPScope("all"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown scope should be InvalidArgument, got %v", err)
	}
}

func TestSeriesRSVPMessage(t *testing.T) {
	if got := SeriesRSVPMessage(1, 0); got != "" {
		t.Fatalf("single date should keep default copy, got %q", got)
	}
	if got := SeriesRSVPMessage(8, 0); got != "You're in for 8 upcoming dates." {
		t.Fatalf("got %q", got)
	}
	if got := SeriesRSVPMessage(8, 2); got != "You're in for 6 dates; 2 are waitlisted." {
		t.Fatalf("got %q", got)
	}
	if got := SeriesRSVPMessage(3, 3); got != "Those dates are full. You're on the waitlist for 3 dates." {
		t.Fatalf("got %q", got)
	}
}
