package moderation

import (
	"database/sql"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"testing"
	"time"
)

func TestBirthdayEligibility(t *testing.T) {
	now := time.Date(2026, 11, 25, 23, 59, 0, 0, time.UTC)
	for _, tc := range []struct{ dob, want string }{
		{"", "dob_required"}, {"2008-11-25", "eligible"}, {"2008-11-26", "underage"},
		{"2000-02-29", "eligible"}, {"2027-01-01", "dob_required"}, {"2026-11-25", "underage"},
	} {
		var birth sql.NullTime
		if tc.dob != "" {
			birth.Time, _ = time.Parse("2006-01-02", tc.dob)
			birth.Valid = true
		}
		if got := Eligibility(true, birth, now); got != tc.want {
			t.Errorf("%s: %s want %s", tc.dob, got, tc.want)
		}
	}
	if got := Eligibility(false, sql.NullTime{}, now); got != "login_required" {
		t.Fatal(got)
	}
}
func TestLeapDayBirthdayAndTimezone(t *testing.T) {
	birth := sql.NullTime{Time: time.Date(2008, 2, 29, 0, 0, 0, 0, time.UTC), Valid: true}
	for _, tc := range []struct{ day, want string }{{"2026-02-28", "underage"}, {"2026-03-01", "eligible"}} {
		now, _ := time.Parse("2006-01-02", tc.day)
		if got := Eligibility(true, birth, now); got != tc.want {
			t.Fatalf("%s got %s", tc.day, got)
		}
	}
}
func TestStaffNSFWPermissionIsNarrow(t *testing.T) {
	for _, role := range []string{"Admin", "Community", "Support"} {
		if !staffaccess.Has(role, "moderation.nsfw") {
			t.Errorf("%s lacks nsfw permission", role)
		}
	}
	if staffaccess.Has("Support", staffaccess.ModerationManage) || staffaccess.Has("Support", staffaccess.UsersDelete) {
		t.Fatal("support gained unrelated powers")
	}
	if staffaccess.Has("Viewer", "moderation.nsfw") {
		t.Fatal("ordinary user may moderate")
	}
}
