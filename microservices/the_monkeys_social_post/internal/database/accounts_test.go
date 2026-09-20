package database

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func getTestDB(t *testing.T) *sql.DB {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://root:Secret@localhost:5432/the_monkeys_user_dev?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("skipping test: database ping failed: %v", err)
	}
	return db
}

func setupTestUser(t *testing.T, db *sql.DB) int64 {
	unique := uuid.New().String()[:8]
	accountID := "db_acc_" + unique
	username := "db_user_" + unique
	email := "db_" + unique + "@example.com"
	var userID int64
	err := db.QueryRow(`
		INSERT INTO user_account (account_id, username, email, user_status)
		VALUES ($1, $2, $3, 1)
		RETURNING id`, accountID, username, email).Scan(&userID)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM social_publish_jobs WHERE post_id IN (SELECT id FROM social_posts WHERE owner_user_id = $1)", userID)
		_, _ = db.Exec("DELETE FROM social_rendition_media WHERE rendition_id IN (SELECT id FROM social_post_renditions WHERE post_id IN (SELECT id FROM social_posts WHERE owner_user_id = $1))", userID)
		_, _ = db.Exec("DELETE FROM social_post_renditions WHERE post_id IN (SELECT id FROM social_posts WHERE owner_user_id = $1)", userID)
		_, _ = db.Exec("DELETE FROM social_post_events WHERE actor_user_id = $1", userID)
		_, _ = db.Exec("DELETE FROM social_posts WHERE owner_user_id = $1", userID)
		_, _ = db.Exec("DELETE FROM social_accounts WHERE owner_user_id = $1", userID)
		_, _ = db.Exec("DELETE FROM user_account WHERE id = $1", userID)
	})

	return userID
}

func TestDatabaseLinkAccount(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()

	userID := setupTestUser(t, db)
	ctx := context.Background()

	// 1. Initial Link
	acc, err := LinkAccount(
		ctx, db, userID, "x", "@db_test", "DB Test",
		"ext_ref_123", "https://img.example/1.png", false,
		[]byte("access_1"), []byte("refresh_1"),
	)
	if err != nil {
		t.Fatalf("LinkAccount failed: %v", err)
	}
	if acc.ID == "" || acc.Handle != "@db_test" || acc.Status != "active" || acc.IsMock {
		t.Fatalf("unexpected account values: %+v", acc)
	}
	if acc.AvatarURL != "https://img.example/1.png" {
		t.Fatalf("expected avatar url, got %s", acc.AvatarURL)
	}

	// 2. Conflict update (same external_account_ref)
	accUpdated, err := LinkAccount(
		ctx, db, userID, "x", "@db_test_updated", "DB Test Updated",
		"ext_ref_123", "https://img.example/2.png", false,
		[]byte("access_2"), []byte("refresh_2"),
	)
	if err != nil {
		t.Fatalf("LinkAccount conflict update failed: %v", err)
	}
	if accUpdated.ID != acc.ID {
		t.Fatalf("expected same account ID %s, got %s", acc.ID, accUpdated.ID)
	}
	if accUpdated.Handle != "@db_test_updated" || accUpdated.DisplayName != "DB Test Updated" {
		t.Fatalf("expected updated handle and display name, got %+v", accUpdated)
	}
	if accUpdated.AvatarURL != "https://img.example/2.png" {
		t.Fatalf("expected updated avatar, got %s", accUpdated.AvatarURL)
	}

	// 3. ListAccounts includes this account
	accounts, err := ListAccounts(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListAccounts failed: %v", err)
	}
	var found bool
	for _, a := range accounts {
		if a.ID == acc.ID {
			found = true
			if a.Handle != "@db_test_updated" || a.AvatarURL != "https://img.example/2.png" {
				t.Fatalf("listed account data mismatch: %+v", a)
			}
		}
	}
	if !found {
		t.Fatalf("linked account %s not found in ListAccounts", acc.ID)
	}

	// 4. DisconnectAccount
	cancelledJobs, draftsReverted, err := DisconnectAccount(ctx, db, userID, acc.ID)
	if err != nil {
		t.Fatalf("DisconnectAccount failed: %v", err)
	}
	if cancelledJobs != 0 || draftsReverted != 0 {
		t.Fatalf("expected 0 cancelled jobs/drafts, got %d / %d", cancelledJobs, draftsReverted)
	}

	// 5. Verify status is 'disconnected' and tokens are cleared
	var status string
	var encAccess, encRefresh []byte
	err = db.QueryRow(`
		SELECT status, encrypted_access_token, encrypted_refresh_token
		FROM social_accounts WHERE id = $1::uuid`, acc.ID).Scan(&status, &encAccess, &encRefresh)
	if err != nil {
		t.Fatalf("query disconnected account: %v", err)
	}
	if status != "disconnected" {
		t.Fatalf("expected status 'disconnected', got %q", status)
	}
	if encAccess != nil || encRefresh != nil {
		t.Fatalf("expected cleared tokens, got access=%v refresh=%v", encAccess, encRefresh)
	}

	// 6. ListAccounts excludes disconnected account
	accountsAfterDisc, err := ListAccounts(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListAccounts after disconnect failed: %v", err)
	}
	for _, a := range accountsAfterDisc {
		if a.ID == acc.ID {
			t.Fatalf("disconnected account %s was returned in ListAccounts", acc.ID)
		}
	}

	// 7. Re-linking reactivates account
	accReactivated, err := LinkAccount(
		ctx, db, userID, "x", "@db_test_reactivated", "DB Reactivated",
		"ext_ref_123", "", false,
		[]byte("access_3"), nil,
	)
	if err != nil {
		t.Fatalf("re-linking account failed: %v", err)
	}
	if accReactivated.Status != "active" {
		t.Fatalf("expected reactivated status 'active', got %q", accReactivated.Status)
	}
}
