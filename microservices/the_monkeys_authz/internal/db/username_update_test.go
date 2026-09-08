package db

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsFKViolation(t *testing.T) {
	fk := &pgconn.PgError{
		Code:           "23503",
		ConstraintName: "verification_requests_username_fkey",
	}
	if !isFKViolation(fk) {
		t.Fatal("expected verification_requests FK error to match")
	}
	if isFKViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("unique-violation must not match")
	}
	if isFKViolation(fmt.Errorf("SQLSTATE 23503")) {
		t.Fatal("plain error string must not match")
	}
}
