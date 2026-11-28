// Package moderation implements content ratings independently of publication and audience.
package moderation

import (
	"database/sql"
	"time"
)

// Eligibility uses calendar birthdays in UTC; Feb 29 birthdays turn eighteen on
// March 1 in a non-leap year. Missing, invalid and future dates never grant access.
func Eligibility(signedIn bool, birth sql.NullTime, now time.Time) string {
	if !signedIn {
		return "login_required"
	}
	if !birth.Valid {
		return "dob_required"
	}
	today := now.UTC()
	y, m, d := birth.Time.Date()
	if y < 1900 || y > today.Year() {
		return "dob_required"
	}
	birthday := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	date := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	if birthday.After(date) {
		return "dob_required"
	}
	eighteenth := time.Date(y+18, m, d, 0, 0, 0, 0, time.UTC)
	if date.Before(eighteenth) {
		return "underage"
	}
	return "eligible"
}
