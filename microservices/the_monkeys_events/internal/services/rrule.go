package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	seriesHorizon = 12
	seriesCap     = 52
)

func expandRecurrence(r *pb.Recurrence, start time.Time) ([]time.Time, string, error) {
	if r == nil {
		return nil, "", status.Error(codes.InvalidArgument, "recurrence is required")
	}
	freq := strings.ToLower(strings.TrimSpace(r.Freq))
	switch freq {
	case "daily", "weekly", "monthly", "yearly":
	default:
		return nil, "", status.Error(codes.InvalidArgument, "freq must be daily, weekly, monthly, or yearly")
	}
	interval := int(r.Interval)
	if interval < 1 {
		interval = 1
	}
	limit := seriesHorizon
	if r.Count > 0 && int(r.Count) < limit {
		limit = int(r.Count)
	}
	if r.Count > seriesCap {
		limit = seriesCap
	}
	if limit > seriesCap {
		limit = seriesCap
	}

	var until *time.Time
	if r.Until != nil {
		t := r.Until.AsTime()
		until = &t
	}

	times := walkRecurrence(freq, interval, start, r.ByDay, int(r.Count), until, limit)
	if len(times) == 0 {
		return nil, "", status.Error(codes.InvalidArgument, "recurrence produced no dates")
	}
	return times, buildRRULE(freq, interval, r.ByDay, r.Count, until), nil
}

func buildRRULE(freq string, interval int, byDay []string, count int32, until *time.Time) string {
	parts := []string{"FREQ=" + strings.ToUpper(freq), fmt.Sprintf("INTERVAL=%d", interval)}
	if days := rrDays(byDay); days != "" {
		parts = append(parts, "BYDAY="+days)
	}
	if count > 0 {
		parts = append(parts, fmt.Sprintf("COUNT=%d", count))
	}
	if until != nil {
		parts = append(parts, "UNTIL="+until.UTC().Format("20060102T150405Z"))
	}
	return strings.Join(parts, ";")
}

func walkRecurrence(freq string, interval int, start time.Time, byDay []string, count int, until *time.Time, limit int) []time.Time {
	out := make([]time.Time, 0, limit)
	wanted := weekdaySet(byDay)
	t := start
	for n := 0; n < 400 && len(out) < limit; n++ {
		if until != nil && t.After(*until) {
			break
		}
		if count > 0 && len(out) >= count {
			break
		}
		if freq == "weekly" && len(wanted) > 0 {
			if wanted[t.Weekday()] {
				weeks := int(t.Sub(start).Hours() / 24 / 7)
				if weeks%interval == 0 {
					out = append(out, t)
				}
			}
			t = t.AddDate(0, 0, 1)
			continue
		}
		out = append(out, t)
		switch freq {
		case "daily":
			t = t.AddDate(0, 0, interval)
		case "weekly":
			t = t.AddDate(0, 0, 7*interval)
		case "monthly":
			t = t.AddDate(0, interval, 0)
		case "yearly":
			t = t.AddDate(interval, 0, 0)
		default:
			return out
		}
	}
	return out
}

func weekdaySet(byDay []string) map[time.Weekday]bool {
	if len(byDay) == 0 {
		return nil
	}
	m := map[time.Weekday]bool{}
	for _, d := range byDay {
		switch strings.ToUpper(strings.TrimSpace(d)) {
		case "SU", "SUN", "SUNDAY":
			m[time.Sunday] = true
		case "MO", "MON", "MONDAY":
			m[time.Monday] = true
		case "TU", "TUE", "TUESDAY":
			m[time.Tuesday] = true
		case "WE", "WED", "WEDNESDAY":
			m[time.Wednesday] = true
		case "TH", "THU", "THURSDAY":
			m[time.Thursday] = true
		case "FR", "FRI", "FRIDAY":
			m[time.Friday] = true
		case "SA", "SAT", "SATURDAY":
			m[time.Saturday] = true
		}
	}
	return m
}

func rrDays(byDay []string) string {
	seen := weekdaySet(byDay)
	if len(seen) == 0 {
		return ""
	}
	order := []struct {
		d time.Weekday
		s string
	}{
		{time.Monday, "MO"}, {time.Tuesday, "TU"}, {time.Wednesday, "WE"},
		{time.Thursday, "TH"}, {time.Friday, "FR"}, {time.Saturday, "SA"}, {time.Sunday, "SU"},
	}
	var parts []string
	for _, o := range order {
		if seen[o.d] {
			parts = append(parts, o.s)
		}
	}
	return strings.Join(parts, ",")
}
