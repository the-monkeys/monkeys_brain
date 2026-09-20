package services

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_social_post/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_social_post/internal/database"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func setupTestUser(t *testing.T, db *sql.DB) (int64, string) {
	unique := uuid.New().String()[:8]
	accountID := "test_acc_" + unique
	username := "test_user_" + unique
	email := "test_" + unique + "@example.com"
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

	return userID, accountID
}

func TestLinkAccount(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()

	_, accountID := setupTestUser(t, db)
	svc := New(&database.Store{DB: db})
	ctx := context.Background()

	reqCtx := &pb.RequestContext{AccountId: accountID}

	// 1. Link mock account
	resp, err := svc.LinkAccount(ctx, &pb.LinkAccountRequest{
		Context:            reqCtx,
		Platform:           "x",
		Handle:             "@mock_tester",
		DisplayName:        "Mock Tester",
		ExternalAccountRef: "mock:x:tester1",
		AvatarUrl:          "https://example.com/avatar.png",
		IsMock:             true,
	})
	if err != nil {
		t.Fatalf("LinkAccount failed: %v", err)
	}

	if resp.GetId() == "" {
		t.Fatal("expected non-empty account ID")
	}
	if resp.GetPlatform() != pb.Platform_PLATFORM_X {
		t.Fatalf("expected platform X, got %v", resp.GetPlatform())
	}
	if resp.GetHandle() != "@mock_tester" {
		t.Fatalf("expected handle @mock_tester, got %s", resp.GetHandle())
	}
	if resp.GetDisplayName() != "Mock Tester" {
		t.Fatalf("expected display name Mock Tester, got %s", resp.GetDisplayName())
	}
	if resp.GetStatus() != "active" {
		t.Fatalf("expected status active, got %s", resp.GetStatus())
	}
	if !resp.GetIsMock() {
		t.Fatalf("expected is_mock true, got false")
	}
	if resp.GetAvatarUrl() != "https://example.com/avatar.png" {
		t.Fatalf("expected avatar url https://example.com/avatar.png, got %s", resp.GetAvatarUrl())
	}
	if resp.GetValidation() == nil {
		t.Fatal("expected non-nil validation metadata")
	}

	// 2. Verify ListAccounts returns the linked account with IsMock and AvatarUrl
	listResp, err := svc.ListAccounts(ctx, &pb.ListAccountsRequest{Context: reqCtx})
	if err != nil {
		t.Fatalf("ListAccounts failed: %v", err)
	}

	var found bool
	for _, acc := range listResp.GetAccounts() {
		if acc.GetId() == resp.GetId() {
			found = true
			if !acc.GetIsMock() {
				t.Fatalf("expected list account is_mock true, got false")
			}
			if acc.GetAvatarUrl() != "https://example.com/avatar.png" {
				t.Fatalf("expected list account avatar url, got %s", acc.GetAvatarUrl())
			}
		}
	}
	if !found {
		t.Fatalf("linked account %s not found in ListAccounts", resp.GetId())
	}
}

func TestDisconnectAccount(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()

	userID, accountID := setupTestUser(t, db)
	svc := New(&database.Store{DB: db})
	ctx := context.Background()

	reqCtx := &pb.RequestContext{AccountId: accountID}

	// 1. Link a new account
	acc, err := svc.LinkAccount(ctx, &pb.LinkAccountRequest{
		Context:            reqCtx,
		Platform:           "linkedin",
		Handle:             "@disconnect_target",
		DisplayName:        "Disconnect Target",
		ExternalAccountRef: "mock:linkedin:target",
		IsMock:             true,
	})
	if err != nil {
		t.Fatalf("LinkAccount failed: %v", err)
	}

	// 2. Create a post with state = 'scheduled' and rendition pointing to this account
	var postID string
	err = db.QueryRow(`
		INSERT INTO social_posts (owner_user_id, base_text, state, scheduled_at, schedule_timezone)
		VALUES ($1, 'Disconnect test post', 'scheduled', NOW() + interval '1 day', 'UTC')
		RETURNING id::text`, userID).Scan(&postID)
	if err != nil {
		t.Fatalf("create scheduled post: %v", err)
	}

	var renditionID string
	err = db.QueryRow(`
		INSERT INTO social_post_renditions (post_id, social_account_id, platform, state)
		VALUES ($1::uuid, $2::uuid, 'linkedin', 'scheduled')
		RETURNING id::text`, postID, acc.GetId()).Scan(&renditionID)
	if err != nil {
		t.Fatalf("create rendition: %v", err)
	}

	var jobID string
	err = db.QueryRow(`
		INSERT INTO social_publish_jobs (post_id, rendition_id, rendition_version, dedupe_key, status, run_at, provider_idempotency_key)
		VALUES ($1::uuid, $2::uuid, 1, $2::text || ':1', 'ready', NOW() + interval '1 day', 'test_key')
		RETURNING id::text`, postID, renditionID).Scan(&jobID)
	if err != nil {
		t.Fatalf("create publish job: %v", err)
	}

	// 3. Disconnect the account
	discResp, err := svc.DisconnectAccount(ctx, &pb.DisconnectAccountRequest{
		Context:   reqCtx,
		AccountId: acc.GetId(),
	})
	if err != nil {
		t.Fatalf("DisconnectAccount failed: %v", err)
	}

	if !discResp.GetSuccess() {
		t.Fatalf("expected success true, got false")
	}
	if discResp.GetCancelledJobsCount() != 1 {
		t.Fatalf("expected 1 cancelled job, got %d", discResp.GetCancelledJobsCount())
	}
	if discResp.GetDraftsRevertedCount() != 1 {
		t.Fatalf("expected 1 draft reverted, got %d", discResp.GetDraftsRevertedCount())
	}

	// 4. Verify account status = 'disconnected' in DB
	var accStatus string
	err = db.QueryRow("SELECT status FROM social_accounts WHERE id = $1::uuid", acc.GetId()).Scan(&accStatus)
	if err != nil {
		t.Fatalf("query account status: %v", err)
	}
	if accStatus != "disconnected" {
		t.Fatalf("expected account status 'disconnected', got %q", accStatus)
	}

	// 5. Verify publish job status = 'cancelled'
	var jobStatus string
	err = db.QueryRow("SELECT status FROM social_publish_jobs WHERE id = $1::uuid", jobID).Scan(&jobStatus)
	if err != nil {
		t.Fatalf("query job status: %v", err)
	}
	if jobStatus != "cancelled" {
		t.Fatalf("expected job status 'cancelled', got %q", jobStatus)
	}

	// 6. Verify post state reverted to 'draft'
	var postState string
	err = db.QueryRow("SELECT state FROM social_posts WHERE id = $1::uuid", postID).Scan(&postState)
	if err != nil {
		t.Fatalf("query post state: %v", err)
	}
	if postState != "draft" {
		t.Fatalf("expected post state 'draft', got %q", postState)
	}

	// 7. Verify rendition state reverted to 'draft'
	var renditionState string
	err = db.QueryRow("SELECT state FROM social_post_renditions WHERE id = $1::uuid", renditionID).Scan(&renditionState)
	if err != nil {
		t.Fatalf("query rendition state: %v", err)
	}
	if renditionState != "draft" {
		t.Fatalf("expected rendition state 'draft', got %q", renditionState)
	}

	// 8. Verify ListAccounts excludes disconnected accounts
	listResp, err := svc.ListAccounts(ctx, &pb.ListAccountsRequest{Context: reqCtx})
	if err != nil {
		t.Fatalf("ListAccounts failed: %v", err)
	}
	for _, a := range listResp.GetAccounts() {
		if a.GetId() == acc.GetId() {
			t.Fatalf("disconnected account %s was returned by ListAccounts", acc.GetId())
		}
	}
}

func TestValidationErrors(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()

	_, accountID := setupTestUser(t, db)
	svc := New(&database.Store{DB: db})
	ctx := context.Background()

	reqCtx := &pb.RequestContext{AccountId: accountID}

	// Link with invalid platform
	_, err := svc.LinkAccount(ctx, &pb.LinkAccountRequest{
		Context:  reqCtx,
		Platform: "invalid_platform",
		Handle:   "@test",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for bad platform, got %v", err)
	}

	// Link with empty handle
	_, err = svc.LinkAccount(ctx, &pb.LinkAccountRequest{
		Context:  reqCtx,
		Platform: "x",
		Handle:   "",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty handle, got %v", err)
	}

	// Disconnect with empty account ID
	_, err = svc.DisconnectAccount(ctx, &pb.DisconnectAccountRequest{
		Context:   reqCtx,
		AccountId: "",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty account ID, got %v", err)
	}

	// Disconnect non-existent account
	_, err = svc.DisconnectAccount(ctx, &pb.DisconnectAccountRequest{
		Context:   reqCtx,
		AccountId: uuid.New().String(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for unknown account, got %v", err)
	}
}
