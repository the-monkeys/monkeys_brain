package database

import (
	"context"
	"database/sql"
	"fmt"
)

type Account struct {
	ID, Platform, DisplayName, Handle, Status string
}

func GetAccountPlatform(ctx context.Context, db *sql.DB, userID int64, accountID string) (string, error) {
	var platform string
	err := db.QueryRowContext(ctx, `
		SELECT platform FROM social_accounts
		WHERE id = $1::uuid AND owner_user_id = $2 AND status = 'active'`,
		accountID, userID).Scan(&platform)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get social account platform: %w", err)
	}
	return platform, nil
}

// ListAccounts returns the caller's mock destination accounts, auto-provisioned
// one-per-platform at signup time by the `provision_social_mock_accounts` trigger.
func ListAccounts(ctx context.Context, db *sql.DB, userID int64) ([]*Account, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id::text, platform, display_name, handle, status
		FROM social_accounts WHERE owner_user_id = $1 ORDER BY platform ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list social accounts: %w", err)
	}
	defer rows.Close()
	accounts := []*Account{}
	for rows.Next() {
		a := &Account{}
		if err := rows.Scan(&a.ID, &a.Platform, &a.DisplayName, &a.Handle, &a.Status); err != nil {
			return nil, fmt.Errorf("scan social account: %w", err)
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}
