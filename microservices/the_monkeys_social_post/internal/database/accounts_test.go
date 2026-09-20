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

func TestDisconnectAccountMultiPlatformPostReversion(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()

	userID := setupTestUser(t, db)
	ctx := context.Background()

	// 1. Link Account A and Account B
	accA, err := LinkAccount(ctx, db, userID, "x", "@acc_a", "Account A", "ext_a", "", false, nil, nil)
	if err != nil {
		t.Fatalf("link accA: %v", err)
	}
	accB, err := LinkAccount(ctx, db, userID, "linkedin", "@acc_b", "Account B", "ext_b", "", false, nil, nil)
	if err != nil {
		t.Fatalf("link accB: %v", err)
	}

	// 2. Create a scheduled post
	var postID string
	var initialVersion int64
	err = db.QueryRow(`
		INSERT INTO social_posts (owner_user_id, base_text, state, version, scheduled_at, schedule_timezone)
		VALUES ($1, 'Multi-platform post', 'scheduled', 1, NOW() + interval '2 days', 'UTC')
		RETURNING id::text, version`, userID).Scan(&postID, &initialVersion)
	if err != nil {
		t.Fatalf("create scheduled post: %v", err)
	}

	// 3. Create renditions for both accounts
	var rendAID, rendBID string
	err = db.QueryRow(`
		INSERT INTO social_post_renditions (post_id, social_account_id, platform, state, version)
		VALUES ($1::uuid, $2::uuid, 'x', 'scheduled', 1)
		RETURNING id::text`, postID, accA.ID).Scan(&rendAID)
	if err != nil {
		t.Fatalf("create rendA: %v", err)
	}
	err = db.QueryRow(`
		INSERT INTO social_post_renditions (post_id, social_account_id, platform, state, version)
		VALUES ($1::uuid, $2::uuid, 'linkedin', 'scheduled', 1)
		RETURNING id::text`, postID, accB.ID).Scan(&rendBID)
	if err != nil {
		t.Fatalf("create rendB: %v", err)
	}

	// 4. Create publish jobs for both renditions
	var jobAID, jobBID string
	err = db.QueryRow(`
		INSERT INTO social_publish_jobs (post_id, rendition_id, rendition_version, dedupe_key, status, run_at, provider_idempotency_key)
		VALUES ($1::uuid, $2::uuid, 1, $2::text || ':1', 'ready', NOW() + interval '2 days', 'job_a')
		RETURNING id::text`, postID, rendAID).Scan(&jobAID)
	if err != nil {
		t.Fatalf("create jobA: %v", err)
	}
	err = db.QueryRow(`
		INSERT INTO social_publish_jobs (post_id, rendition_id, rendition_version, dedupe_key, status, run_at, provider_idempotency_key)
		VALUES ($1::uuid, $2::uuid, 1, $2::text || ':1', 'ready', NOW() + interval '2 days', 'job_b')
		RETURNING id::text`, postID, rendBID).Scan(&jobBID)
	if err != nil {
		t.Fatalf("create jobB: %v", err)
	}

	// 5. Disconnect Account A
	cancelledJobs, draftsReverted, err := DisconnectAccount(ctx, db, userID, accA.ID)
	if err != nil {
		t.Fatalf("disconnect accA: %v", err)
	}
	if draftsReverted != 1 {
		t.Fatalf("expected 1 draft reverted, got %d", draftsReverted)
	}
	if cancelledJobs != 2 {
		t.Fatalf("expected 2 jobs cancelled (both accA and accB for this post), got %d", cancelledJobs)
	}

	// 6. Verify post state = 'draft', version incremented, schedule cleared
	var postState, schedAt, schedTz sql.NullString
	var postVersion int64
	err = db.QueryRow(`
		SELECT state, version, scheduled_at::text, schedule_timezone
		FROM social_posts WHERE id = $1::uuid`, postID).Scan(&postState, &postVersion, &schedAt, &schedTz)
	if err != nil {
		t.Fatalf("query post: %v", err)
	}
	if postState.String != "draft" {
		t.Fatalf("expected post state 'draft', got %q", postState.String)
	}
	if postVersion != initialVersion+1 {
		t.Fatalf("expected post version %d, got %d", initialVersion+1, postVersion)
	}
	if schedAt.Valid || schedTz.Valid {
		t.Fatalf("expected cleared schedule on post, got schedAt=%v, schedTz=%v", schedAt, schedTz)
	}

	// 7. Verify both renditions are reverted to draft
	var rendAState, rendBState string
	_ = db.QueryRow("SELECT state FROM social_post_renditions WHERE id = $1::uuid", rendAID).Scan(&rendAState)
	_ = db.QueryRow("SELECT state FROM social_post_renditions WHERE id = $1::uuid", rendBID).Scan(&rendBState)
	if rendAState != "draft" {
		t.Fatalf("expected rendA state 'draft', got %q", rendAState)
	}
	if rendBState != "draft" {
		t.Fatalf("expected rendB state 'draft', got %q", rendBState)
	}

	// 8. Verify both jobs are cancelled
	var jobAStatus, jobBStatus string
	_ = db.QueryRow("SELECT status FROM social_publish_jobs WHERE id = $1::uuid", jobAID).Scan(&jobAStatus)
	_ = db.QueryRow("SELECT status FROM social_publish_jobs WHERE id = $1::uuid", jobBID).Scan(&jobBStatus)
	if jobAStatus != "cancelled" {
		t.Fatalf("expected jobA status 'cancelled', got %q", jobAStatus)
	}
	if jobBStatus != "cancelled" {
		t.Fatalf("expected jobB status 'cancelled', got %q", jobBStatus)
	}
}
