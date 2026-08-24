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

type eventDB struct {
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

// NewEventDB initializes the database with connection pooling.
func NewEventDB(cfg *config.Config, log *zap.SugaredLogger) (EventDB, error) {
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

	return &eventDB{db: db, log: log}, nil
}

func (db *eventDB) Close() error { return db.db.Close() }

// inTx runs fn inside a transaction, rolling back on any error.
func (db *eventDB) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
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
// Identity & event lookups
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

// resolveEvent returns the numeric event id and its organizer's numeric id.
func resolveEvent(ctx context.Context, q querier, slug string) (eventID, organizerID int64, err error) {
	err = q.QueryRowContext(ctx,
		"SELECT id, organizer_id FROM events WHERE slug = $1", slug).Scan(&eventID, &organizerID)
	if err == sql.ErrNoRows {
		return 0, 0, status.Error(codes.NotFound, "event not found")
	}
	if err != nil {
		return 0, 0, status.Error(codes.Internal, "failed to load event")
	}
	return eventID, organizerID, nil
}

// resolveGroupForOrganizer returns the numeric id of the group identified by
// slug, but only when userID is an active organizer or co-organizer of it. An
// empty slug yields 0 (a standalone event). This is the authorization boundary
// for attaching an event to a group: a plain member must never be able to post
// events under a community they do not run.
func resolveGroupForOrganizer(ctx context.Context, q querier, slug string, userID int64) (int64, error) {
	if strings.TrimSpace(slug) == "" {
		return 0, nil
	}
	var groupID int64
	err := q.QueryRowContext(ctx, `
		SELECT g.id
		FROM groups g
		JOIN group_members gm ON gm.group_id = g.id AND gm.user_id = $2
		WHERE g.slug = $1
		  AND gm.status = 'active'
		  AND gm.role IN ('organizer', 'co_organizer')`,
		slug, userID).Scan(&groupID)
	if err == sql.ErrNoRows {
		return 0, status.Error(codes.PermissionDenied,
			"only a group organizer can attach an event to the group")
	}
	if err != nil {
		return 0, status.Error(codes.Internal, "failed to resolve group")
	}
	return groupID, nil
}

// -----------------------------------------------------------------------------
// Slugs
// -----------------------------------------------------------------------------

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugify builds a URL-safe slug from a title and appends a short random
// suffix, which keeps slugs unique without a retry loop on conflict.
func slugify(title string) string {
	base := strings.Trim(nonSlugChars.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(base) > 80 {
		base = strings.Trim(base[:80], "-")
	}
	if base == "" {
		base = "event"
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
	}
	return base + "-" + hex.EncodeToString(buf)
}
