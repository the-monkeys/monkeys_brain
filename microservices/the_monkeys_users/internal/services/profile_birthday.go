package services

import (
	"database/sql"
	"regexp"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var profileBirthdayOffset = regexp.MustCompile(`[+-]([0-9]{2}):?([0-9]{2})`)

// parseProfileBirthday accepts current date inputs and historical private-profile
// timestamps. Birthdays are calendar dates, so an offset must not shift the day.
// Empty and old zero-time values mean no replacement: the legacy string contract
// cannot distinguish an omitted birthday from a request to clear it.
func parseProfileBirthday(value string) (sql.NullTime, error) {
	if value == "" {
		return sql.NullTime{}, nil
	}
	// time.Parse permits out-of-range offsets such as +24:00 and +05:60.
	for _, offset := range profileBirthdayOffset.FindAllStringSubmatch(value, -1) {
		if offset[1] > "23" || offset[2] > "59" {
			return sql.NullTime{}, status.Error(codes.InvalidArgument, "date_of_birth must be a valid calendar date")
		}
	}
	for _, layout := range []string{time.DateOnly, "2006-01-02 15:04:05 -0700 MST", "2006-01-02 15:04:05 -0700 -0700", time.RFC3339Nano} {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		if parsed.IsZero() {
			return sql.NullTime{}, nil
		}
		if parsed.Year() <= 1 {
			break
		}
		date := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
		return sql.NullTime{Time: date, Valid: true}, nil
	}
	return sql.NullTime{}, status.Error(codes.InvalidArgument, "date_of_birth must be a valid calendar date")
}
