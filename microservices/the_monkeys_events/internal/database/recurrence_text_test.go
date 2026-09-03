package database

import "testing"

func TestRecurrenceText(t *testing.T) {
	cases := []struct {
		rule, want string
	}{
		{"", "Part of a recurring series"},
		{"FREQ=WEEKLY;INTERVAL=1;BYDAY=SA", "Every week on Sat"},
		{"FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,TH", "Every 2 weeks on Tue, Thu"},
		{"FREQ=DAILY;INTERVAL=1", "Every day"},
		{"FREQ=MONTHLY;INTERVAL=1", "Every month"},
		{"FREQ=YEARLY;INTERVAL=3", "Every 3 years"},
	}
	for _, c := range cases {
		if got := recurrenceText(c.rule); got != c.want {
			t.Errorf("recurrenceText(%q) = %q, want %q", c.rule, got, c.want)
		}
	}
}
