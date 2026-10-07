package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_discussions/internal/discussions"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DiscussionDB is the persistence contract for discussions. It uses the same
// Postgres as groups and users. Account ids on the wire are resolved to
// user_account.id inside this package.
type DiscussionDB interface {
	CreateDiscussion(ctx context.Context, in discussions.CreateInput) (*discussions.Post, error)
	ListDiscussions(ctx context.Context, in discussions.ListInput) ([]*discussions.Post, error)
	GetDiscussion(ctx context.Context, accountID, publicID string) (*discussions.Post, error)
	ReplyToDiscussion(ctx context.Context, in discussions.ReplyInput) (*discussions.Reply, error)
	LikeDiscussion(ctx context.Context, accountID, publicID string) (bool, error)
	EditDiscussion(ctx context.Context, accountID, publicID, body string) (*discussions.Post, error)
	DeleteDiscussion(ctx context.Context, accountID, publicID string) error
	HideDiscussion(ctx context.Context, accountID, publicID string) error
	Close() error
}

type discussionDB struct {
	db  *sql.DB
	log *zap.SugaredLogger
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func NewDiscussionDB(cfg *config.Config, log *zap.SugaredLogger) (DiscussionDB, error) {
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
	db.SetMaxOpenConns(15)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres ping failed: %w", err)
	}
	return &discussionDB{db: db, log: log}, nil
}

func (db *discussionDB) Close() error { return db.db.Close() }

func (db *discussionDB) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
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

func resolveAccount(ctx context.Context, q querier, accountID string) (int64, error) {
	if accountID == "" {
		return 0, status.Error(codes.Unauthenticated, "Log in to post a discussion.")
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
