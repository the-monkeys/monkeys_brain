package database

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUniqueViolation(t *testing.T) {
	dup := &pgconn.PgError{Code: "23505", ConstraintName: "event_coupons_event_id_code_key"}
	if !isUniqueViolation(dup, "event_coupons_event_id_code_key") {
		t.Fatal("expected unique coupon constraint to match")
	}
	if isUniqueViolation(dup, "some_other_key") {
		t.Fatal("wrong constraint must not match")
	}
	if isUniqueViolation(fmt.Errorf("SQLSTATE 23505"), "event_coupons_event_id_code_key") {
		t.Fatal("plain error string must not match")
	}
}
