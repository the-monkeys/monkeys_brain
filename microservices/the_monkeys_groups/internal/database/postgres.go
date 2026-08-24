package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/the-monkeys/the_monkeys/config"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type groupDB struct {
	db  *sql.DB
	log *zap.SugaredLogger
}

// querier is satisfied by both *sql.DB and *sql.Tx so the shared lookup
// helpers work inside and outside a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// NewGroupDB initializes the database with connection pooling. It shares the
// primary Postgres instance with the other services; the group tables live in
// the same schema so the cross-domain foreign keys resolve.
func NewGroupDB(cfg *config.Config, log *zap.SugaredLogger) (GroupDB, error) {
	url := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.Postgresql.PrimaryDB.DBUsername,
		cfg.Postgresql.PrimaryDB.DBPassword,
		cfg.Postgresql.PrimaryDB.DBHost,
		cfg.Postgresql.PrimaryDB.DBPort,
		cfg.Postgresql.PrimaryDB.DBName,
	)
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("cannot open postgres connection: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres ping failed: %w", err)
	}

	return &groupDB{db: db, log: log}, nil
}

func (db *groupDB) Close() error { return db.db.Close() }

// inTx runs fn inside a transaction, rolling back on any error.
func (db *groupDB) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return status.Error(codes.Internal, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return status.Error(codes.Internal, "failed to commit transaction")
	}
	return nil
}

// -----------------------------------------------------------------------------
// Identity & group lookups
//
// The wire protocol speaks account_id / username; the schema speaks
// user_account.id. Every write path funnels through these helpers so the
// translation lives in exactly one place.
// -----------------------------------------------------------------------------

func resolveAccount(ctx context.Context, q querier, accountID string) (int64, error) {
	if accountID == "" {
		return 0, status.Error(codes.Unauthenticated, "account id is required")
	}
	var id int64
	err := q.QueryRowContext(ctx, "SELECT id FROM user_account WHERE account_id = $1", accountID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, status.Error(codes.NotFound, "account not found")
	}
	if err != nil {
		return 0, status.Error(codes.Internal, "failed to resolve account")
	}
	return id, nil
}

func resolveUsername(ctx context.Context, q querier, username string) (int64, error) {
	if username == "" {
		return 0, status.Error(codes.InvalidArgument, "username is required")
	}
	var id int64
	err := q.QueryRowContext(ctx, "SELECT id FROM user_account WHERE username = $1", username).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, status.Error(codes.NotFound, "user not found")
	}
	if err != nil {
		return 0, status.Error(codes.Internal, "failed to resolve user")
	}
	return id, nil
}

// resolveGroup returns the numeric group id and its organizer's numeric id.
func resolveGroup(ctx context.Context, q querier, slug string) (groupID, organizerID int64, err error) {
	err = q.QueryRowContext(ctx,
		"SELECT id, organizer_id FROM groups WHERE slug = $1", slug).Scan(&groupID, &organizerID)
	if err == sql.ErrNoRows {
		return 0, 0, status.Error(codes.NotFound, "group not found")
	}
	if err != nil {
		return 0, 0, status.Error(codes.Internal, "failed to load group")
	}
	return groupID, organizerID, nil
}

// -----------------------------------------------------------------------------
// Slugs
// -----------------------------------------------------------------------------

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugify builds a URL-safe slug from a name and appends a short random
// suffix, which keeps slugs unique without a retry loop on conflict.
func slugify(name string) string {
	base := strings.Trim(nonSlugChars.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(base) > 80 {
		base = strings.Trim(base[:80], "-")
	}
	if base == "" {
		base = "group"
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
	}
	return base + "-" + hex.EncodeToString(buf)
}

// nullifyStr maps an empty string to a NULL bind so optional columns stay NULL
// rather than storing empty strings.
func nullifyStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// nullifyCoord maps a zero coordinate to NULL so "unset" is distinguishable
// from the equator/prime-meridian origin.
func nullifyCoord(c float64) any {
	if c == 0 {
		return nil
	}
	return c
}

// nullStringVal unwraps a sql.NullString to its value or "".
func nullStringVal(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
