package services

import (
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

func TestWeeklyRecurrenceUsesMondayCalendarWeeks(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		day      int
		interval int32
		byDay    []string
		want     []string
	}{
		{
			name: "Wednesday biweekly",
			day:  21, interval: 2, byDay: []string{"MO", "WE"},
			want: []string{"2026-10-21T17:00:00Z", "2026-11-02T18:00:00Z", "2026-11-04T18:00:00Z", "2026-11-16T18:00:00Z"},
		},
		{
			name: "Sunday biweekly across DST",
			day:  18, interval: 2, byDay: []string{"SU", "MO"},
			want: []string{"2026-10-18T17:00:00Z", "2026-10-26T18:00:00Z", "2026-11-01T18:00:00Z", "2026-11-09T18:00:00Z"},
		},
		{
			name: "Wednesday every week",
			day:  21, interval: 1, byDay: []string{"MO", "WE"},
			want: []string{"2026-10-21T17:00:00Z", "2026-10-26T18:00:00Z", "2026-10-28T18:00:00Z", "2026-11-02T18:00:00Z"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Date(2026, 10, tc.day, 19, 0, 0, 0, zone)
			occs, rule, err := expandRecurrence(&pb.Recurrence{Freq: "weekly", Interval: tc.interval, ByDay: tc.byDay, Count: 4}, start)
			if err != nil {
				t.Fatal(err)
			}
			if len(occs) != len(tc.want) {
				t.Fatalf("occurrences = %v; want %v (rule %s)", occs, tc.want, rule)
			}
			for i, occ := range occs {
				if got := occ.UTC().Format(time.RFC3339); got != tc.want[i] {
					t.Errorf("occurrence %d = %s, want %s (rule %s)", i, got, tc.want[i], rule)
				}
			}
		})
	}
}

func TestRecurrenceSkipsMissingWallTimeWithoutDriftingLaterDates(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 22, 2, 30, 0, 0, zone)
	occs, _, err := expandRecurrence(&pb.Recurrence{Freq: "weekly", Count: 3, ByDay: []string{"SU"}}, start)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-03-22T01:30:00Z", "2026-04-05T00:30:00Z", "2026-04-12T00:30:00Z"}
	if len(occs) != len(want) {
		t.Fatalf("occurrences: %v", occs)
	}
	for i, occurrence := range occs {
		if got := occurrence.UTC().Format(time.RFC3339); got != want[i] {
			t.Fatalf("occurrence %d = %s, want %s", i, got, want[i])
		}
	}
}

func TestRecurrencePreservesExplicitFoldStart(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC).In(zone) // The second 02:30, explicitly supplied as an instant.
	occs, _, err := expandRecurrence(&pb.Recurrence{Freq: "weekly", Count: 2}, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 2 || !occs[0].Equal(start) {
		t.Fatalf("explicit start changed: %v", occs)
	}
}
