package db

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestDatabaseIdentityLogsResolvedRelationsWithoutCredentials(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	core, logs := observer.New(zapcore.InfoLevel)
	handler := &authDBHandler{db: database, log: zap.New(core).Sugar()}
	mock.ExpectQuery(`SELECT current_database\(\), current_user,`).WillReturnRows(sqlmock.NewRows([]string{"database", "db_user", "schema", "search_path", "server_addr", "server_port", "account_table", "role_table"}).AddRow("fixture_db", "fixture_app", "fixture_schema", `"fixture_app", public`, "127.0.0.1", 5432, "fixture_schema.user_account", "public.user_role"))
	handler.logConnectionIdentity()
	entries := logs.FilterLevelExact(zapcore.InfoLevel).All()
	if len(entries) != 1 {
		t.Fatalf("expected one database identity record, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	for key, want := range map[string]any{"db_name": "fixture_db", "db_user": "fixture_app", "db_schema": "fixture_schema", "db_server": "127.0.0.1", "db_port": int64(5432), "user_account_table": "fixture_schema.user_account", "user_role_table": "public.user_role"} {
		if fields[key] != want {
			t.Errorf("%s=%v, want %v", key, fields[key], want)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseIdentityFailureIsNonfatalAndDoesNotLogDriverError(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	core, logs := observer.New(zapcore.InfoLevel)
	handler := &authDBHandler{db: database, log: zap.New(core).Sugar()}
	mock.ExpectQuery(`SELECT current_database\(\), current_user,`).WillReturnError(errors.New("driver error containing password=synthetic-db-secret"))
	handler.logConnectionIdentity()
	entries := logs.FilterLevelExact(zapcore.WarnLevel).All()
	if len(entries) != 1 || entries[0].ContextMap()["reason"] != "database_identity_unavailable" {
		t.Fatal("missing safe nonfatal diagnostic warning")
	}
	encoded, _ := json.Marshal(logs.All())
	if strings.Contains(string(encoded), "synthetic-db-secret") || strings.Contains(string(encoded), "password=") {
		t.Fatal("raw database error leaked")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountLookupFailureDoesNotExposeDriverErrorOrEmail(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	core, logs := observer.New(zapcore.InfoLevel)
	handler := &authDBHandler{db: database, log: zap.New(core).Sugar()}
	email := "private-fixture@example.invalid"
	mock.ExpectQuery(`SELECT ua.id, ua.account_id`).WithArgs(email).WillReturnError(errors.New("driver error password=synthetic-db-secret"))
	if _, err := handler.CheckIfEmailExist(email); err == nil {
		t.Fatal("lookup failure was swallowed")
	}
	encoded, _ := json.Marshal(logs.All())
	for _, forbidden := range []string{email, "synthetic-db-secret", "password="} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("account lookup log exposes sensitive driver/identity data")
		}
	}
	if logs.FilterLevelExact(zapcore.WarnLevel).Len() != 1 {
		t.Fatal("lookup failure needs one safe operational warning")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
