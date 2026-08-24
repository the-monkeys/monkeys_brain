package database

import "database/sql"

// nullifyStr maps an empty string to SQL NULL so optional text columns store
// NULL instead of ”, keeping partial indexes and IS NULL filters meaningful.
func nullifyStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullifyCoord maps a zero coordinate to SQL NULL. Exact 0.0 latitude AND
// longitude is Null Island; treating an unset pair as NULL avoids polluting the
// geo index with a phantom point off the African coast.
func nullifyCoord(c float64) any {
	if c == 0 {
		return nil
	}
	return c
}

// nullTimeToPtr and related conversions live beside the columns that use them;
// this file only holds the null-input helpers shared by the Meetup-parity
// additions (venues, questions, recurring, attendance, saved).

// nullStringVal unwraps a sql.NullString to its value or "".
func nullStringVal(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
