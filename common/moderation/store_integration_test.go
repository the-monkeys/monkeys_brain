package moderation

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests accept only a deliberately named, loopback test database. Each
// test builds the real prerequisite migrations in its own disposable schema.
// Example: MODERATION_TEST_DSN=postgres://postgres:...@127.0.0.1:.../moderation_store_test?sslmode=disable
func storeFixture(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("MODERATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set MODERATION_TEST_DSN to an isolated moderation_store_test PostgreSQL database")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || !strings.HasPrefix(u.Path, "/moderation_store_test") {
		t.Fatal("MODERATION_TEST_DSN must name a loopback moderation_store_test database")
	}
	root, err := sql.Open("postgres", dsn)
	mustStore(t, err)
	defer root.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	mustStore(t, root.PingContext(ctx))
	_, err = root.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm`)
	mustStore(t, err)
	schema := "moderation_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = root.ExecContext(ctx, `CREATE SCHEMA `+schema)
	mustStore(t, err)
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	mustStore(t, err)
	db.SetMaxOpenConns(12)
	t.Cleanup(func() {
		db.Close()
		clean, e := sql.Open("postgres", dsn)
		if e != nil {
			t.Error(e)
			return
		}
		defer clean.Close()
		_, e = clean.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if e != nil {
			t.Error(e)
		}
	})
	for _, name := range []string{"000001_intital_relation_setup", "000005_add_storage_assets_table", "000010_add_events", "000011_meetup_communities", "000019_admin_moderation", "000022_blog_group_audience", "000023_discussions", "000024_discussion_group_audience", "000025_content_moderation"} {
		data, e := os.ReadFile(filepath.Join("..", "..", "schema", name+".up.sql"))
		mustStore(t, e)
		if _, e = db.ExecContext(ctx, string(data)); e != nil {
			t.Fatalf("migration %s: %v", name, e)
		}
	}
	execStore(t, db, `INSERT INTO user_status(status) VALUES ('active') ON CONFLICT DO NOTHING;
 INSERT INTO user_role(role_desc) VALUES ('Viewer'),('Support'),('Community'),('Admin') ON CONFLICT DO NOTHING;`)
	for _, a := range []struct{ id, role, dob string }{{"owner", "Viewer", "1990-01-01"}, {"adult", "Viewer", "1990-01-01"}, {"young", "Viewer", "2020-01-01"}, {"nodob", "Viewer", ""}, {"support", "Support", "1990-01-01"}, {"community", "Community", "1990-01-01"}, {"admin", "Admin", "1990-01-01"}} {
		execStore(t, db, `INSERT INTO user_account(account_id,username,email,date_of_birth,role_id,user_status) VALUES($1::text,$1::text,$1::text||'@store.test',NULLIF($3,'')::date,(SELECT id FROM user_role WHERE role_desc=$2),(SELECT id FROM user_status WHERE status='active'))`, a.id, a.role, a.dob)
	}
	execStore(t, db, `INSERT INTO blog(blog_id,user_id,status) SELECT 'post',id,'Published' FROM user_account WHERE account_id='owner';
 INSERT INTO groups(slug,name,organizer_id,status) SELECT 'group','Group',id,'published' FROM user_account WHERE account_id='owner';
 INSERT INTO events(slug,title,organizer_id,start_time,end_time,event_type,status) SELECT 'event','Event',id,NOW(),NOW()+interval '1 hour','virtual','published' FROM user_account WHERE account_id='owner';
 INSERT INTO discussion_posts(public_id,author_id,body,edited_until) SELECT 'discussion',id,'private raw content must never leave Check',NOW() FROM user_account WHERE account_id='owner';`)
	return NewStore(db), db
}
func mustStore(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func execStore(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	_, e := db.Exec(q, args...)
	mustStore(t, e)
}
func targetStore(kind string) *pb.Target {
	id := kind
	if kind == "blog" {
		id = "post"
	}
	return &pb.Target{Kind: kind, Id: id}
}
func actionStore(actor, kind, reason string) *pb.ActionRequest {
	return &pb.ActionRequest{Actor: &pb.Actor{AccountId: actor, Ip: "127.0.0.9"}, Target: targetStore(kind), Reason: reason}
}
func checkStore(t *testing.T, s *Store, actor, kind string) *pb.Access {
	t.Helper()
	r, e := s.Check(context.Background(), &pb.CheckRequest{AccountId: actor, Targets: []*pb.Target{targetStore(kind)}})
	mustStore(t, e)
	if len(r.Items) != 1 {
		t.Fatalf("items: %v", r)
	}
	return r.Items[0]
}
func scalarStore(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	mustStore(t, db.QueryRow(q, args...).Scan(&n))
	return n
}

func TestStoreOrdinaryReportPersistsAndDeduplicates(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	req := actionStore("adult", "blog", "nsfw")
	req.Details = "review this"
	first, e := s.ReportContent(ctx, req)
	mustStore(t, e)
	if first.Report.Status != "pending" || first.Access.IsNsfw {
		t.Fatalf("ordinary report must await review: %v", first)
	}
	again, e := NewStore(db).ReportContent(ctx, req)
	mustStore(t, e)
	if again.Report.Id != first.Report.Id || scalarStore(t, db, `SELECT count(*) FROM content_reports`) != 1 {
		t.Fatal("duplicate was not durable")
	}
	if _, e = s.ListReports(ctx, &pb.ListRequest{Actor: &pb.Actor{AccountId: "adult"}}); status.Code(e) != codes.PermissionDenied {
		t.Fatalf("ordinary user read queue: %v", e)
	}
	queue, e := s.ListReports(ctx, &pb.ListRequest{Actor: &pb.Actor{AccountId: "support"}, Status: "pending", Limit: 1})
	mustStore(t, e)
	if queue.Total != 1 || len(queue.Reports) != 1 || queue.Reports[0].ReporterUsername != "adult" {
		t.Fatalf("queue %v", queue)
	}
}

func TestStoreStaffReportAtomicAuditedAndRetrySafe(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	for _, actor := range []string{"support", "community", "admin"} {
		req := actionStore(actor, "blog", "nsfw")
		r, e := s.ReportContent(ctx, req)
		mustStore(t, e)
		if !r.Access.IsNsfw || r.Report.Status != "action_taken" {
			t.Fatalf("staff report %v", r)
		}
		rev := r.Access.Revision
		again, e := s.ReportContent(ctx, req)
		mustStore(t, e)
		if again.Report.Id != r.Report.Id || again.Access.Revision != rev {
			t.Fatalf("retry changed state: %v", again)
		}
	}
	if scalarStore(t, db, `SELECT count(*) FROM admin_audit_log WHERE action='moderation.report_nsfw'`) != 3 {
		t.Fatal("staff reporting audit missing or duplicated")
	}
	// A database audit failure must roll the report and rating back together.
	execStore(t, db, `CREATE FUNCTION reject_moderation_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$;
 CREATE TRIGGER reject_moderation_audit BEFORE INSERT ON admin_audit_log FOR EACH ROW EXECUTE FUNCTION reject_moderation_audit();`)
	_, e := s.ReportContent(ctx, actionStore("support", "event", "nsfw"))
	if e == nil {
		t.Fatal("audit failure succeeded")
	}
	if scalarStore(t, db, `SELECT count(*) FROM content_reports WHERE target_kind='event'`) != 0 || checkStore(t, s, "adult", "event").IsNsfw {
		t.Fatal("atomic report rollback failed")
	}
}

func TestStoreAdditiveLabelsAndRevisionScopedReveal(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	req := actionStore("owner", "blog", "")
	req.IsNsfw = true
	r, e := s.SetAuthorLabel(ctx, req)
	mustStore(t, e)
	original := r.Access.Revision
	for _, tc := range []struct{ who, why string }{{"", "login_required"}, {"young", "underage"}, {"nodob", "dob_required"}, {"adult", "reveal_required"}} {
		a := checkStore(t, s, tc.who, "blog")
		if !a.IsNsfw || a.Revealed || a.Reason != tc.why || a.CanReveal != (tc.who == "adult") {
			t.Fatalf("%s: %v", tc.who, a)
		}
	}
	_, e = s.Reveal(ctx, actionStore("young", "blog", ""))
	if status.Code(e) != codes.PermissionDenied {
		t.Fatalf("underage reveal: %v", e)
	}
	_, e = s.Reveal(ctx, actionStore("adult", "blog", ""))
	mustStore(t, e)
	if !checkStore(t, NewStore(db), "adult", "blog").Revealed || checkStore(t, s, "owner", "blog").Revealed {
		t.Fatal("reveal not durable or not per user")
	}
	_, e = s.ReportContent(ctx, actionStore("support", "blog", "nsfw"))
	mustStore(t, e)
	if a := checkStore(t, s, "adult", "blog"); a.Revision <= original || a.Revealed {
		t.Fatalf("old reveal survived a revision: %v", a)
	}
	req.IsNsfw = false
	r, e = s.SetAuthorLabel(ctx, req)
	mustStore(t, e)
	if !r.Access.IsNsfw {
		t.Fatal("author removed staff restriction")
	}
	other := actionStore("support", "blog", "")
	other.IsNsfw = false
	if _, e = s.SetAuthorLabel(ctx, other); status.Code(e) != codes.PermissionDenied {
		t.Fatalf("nonowner label: %v", e)
	}
	execStore(t, db, `UPDATE content_moderation SET scanner_nsfw=true,scan_status='complete' WHERE target_kind='blog' AND target_id='post'`)
	if !checkStore(t, s, "adult", "blog").IsNsfw {
		t.Fatal("scanner rating ignored")
	}
	// Eligibility and staff standing must be read afresh from PostgreSQL.
	execStore(t, db, `UPDATE user_account SET date_of_birth='2020-01-01' WHERE account_id='adult'`)
	if checkStore(t, s, "adult", "blog").CanReveal {
		t.Fatal("stale DOB allowed reveal")
	}
	execStore(t, db, `UPDATE user_account SET role_id=(SELECT id FROM user_role WHERE role_desc='Viewer') WHERE account_id='support'`)
	if _, e = s.ListReports(ctx, &pb.ListRequest{Actor: &pb.Actor{AccountId: "support"}}); status.Code(e) != codes.PermissionDenied {
		t.Fatalf("stale staff role: %v", e)
	}
}

func TestStoreReviewTransitionsAreAuditedAndIdempotent(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	r, e := s.ReportContent(ctx, actionStore("adult", "blog", "nsfw"))
	mustStore(t, e)
	review := &pb.ReviewRequest{Actor: &pb.Actor{AccountId: "admin", Ip: "127.0.0.8"}, ReportId: r.Report.Id, Action: "confirm_nsfw", Note: "confirmed"}
	r, e = s.ReviewReport(ctx, review)
	mustStore(t, e)
	if !r.Access.IsNsfw || r.Report.Status != "action_taken" || r.Report.ReviewedBy != "admin" {
		t.Fatalf("confirm %v", r)
	}
	rev := r.Access.Revision
	_, e = s.ReviewReport(ctx, review)
	mustStore(t, e)
	if checkStore(t, s, "adult", "blog").Revision != rev {
		t.Fatal("confirm retry bumped revision")
	}
	review.Action = "reverse"
	r, e = s.ReviewReport(ctx, review)
	mustStore(t, e)
	if r.Access.IsNsfw || r.Report.Status != "reversed" {
		t.Fatalf("reverse %v", r)
	}
	rev = r.Access.Revision
	_, e = s.ReviewReport(ctx, review)
	mustStore(t, e)
	review.Action = "confirm_nsfw"
	r, e = s.ReviewReport(ctx, review)
	mustStore(t, e)
	if r.Access.IsNsfw || r.Report.Status != "reversed" || r.Access.Revision != rev {
		t.Fatal("late retry re-applied restriction")
	}
	if scalarStore(t, db, `SELECT count(*) FROM admin_audit_log WHERE action LIKE 'moderation.review_%'`) != 2 {
		t.Fatal("review retries duplicated audit")
	}
	r, e = s.ReportContent(ctx, actionStore("owner", "event", "spam"))
	mustStore(t, e)
	review.ReportId = r.Report.Id
	review.Action = "dismiss"
	r, e = s.ReviewReport(ctx, review)
	mustStore(t, e)
	if r.Report.Status != "dismissed" || r.Access.IsNsfw {
		t.Fatalf("dismiss %v", r)
	}
	_, e = s.ReviewReport(ctx, review)
	mustStore(t, e)
	review.Actor.AccountId = "adult"
	if _, e = s.ReviewReport(ctx, review); status.Code(e) != codes.PermissionDenied {
		t.Fatalf("nonstaff review %v", e)
	}
}

func TestStoreLegacyFlagsAndAllKinds(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	for _, kind := range []string{"blog", "event", "group", "discussion"} {
		t.Run(kind, func(t *testing.T) {
			if a := checkStore(t, s, "", ""+kind); a.IsNsfw || a.ScanStatus != "unknown" {
				t.Fatalf("legacy unknown scan blocked: %v", a)
			}
			if kind != "discussion" {
				table, id, src, pub := "blog_admin_flags", "blog_id", "blog", "blog_id"
				if kind == "event" {
					table, id, src, pub = "event_admin_flags", "event_id", "events", "slug"
				}
				if kind == "group" {
					table, id, src, pub = "group_admin_flags", "group_id", "groups", "slug"
				}
				execStore(t, db, fmt.Sprintf(`INSERT INTO %s(%s,flag_type,reason,created_by) SELECT id,'nsfw','legacy','admin' FROM %s WHERE %s=$1`, table, id, src, pub), targetStore(kind).Id)
				if !checkStore(t, s, "", kind).IsNsfw {
					t.Fatal("legacy NSFW flag ignored")
				}
			}
			r, e := s.ReportContent(ctx, actionStore("support", kind, "nsfw"))
			mustStore(t, e)
			_, e = s.ReviewReport(ctx, &pb.ReviewRequest{Actor: &pb.Actor{AccountId: "admin"}, ReportId: r.Report.Id, Action: "reverse"})
			mustStore(t, e)
			if checkStore(t, s, "", kind).IsNsfw {
				t.Fatal("reverse left legacy restriction")
			}
		})
	}
}

func TestStoreVisibilityCannotBeBypassedByReportsRevealOrMedia(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	execStore(t, db, `UPDATE groups SET visibility='private'; UPDATE blog SET group_id=(SELECT id FROM groups),audience='public'; UPDATE events SET group_id=(SELECT id FROM groups),visibility='group_members'; UPDATE discussion_posts SET group_id=(SELECT id FROM groups),audience='public';`)
	for _, kind := range []string{"blog", "event", "group", "discussion"} {
		label := actionStore("owner", kind, "")
		label.IsNsfw = true
		_, e := s.SetAuthorLabel(ctx, label)
		mustStore(t, e)
		a := checkStore(t, s, "adult", kind)
		if a.Reason != "not_found" || a.CanReveal || a.Revealed || a.IsNsfw || a.Revision != 0 || a.ScanStatus != "" {
			t.Fatalf("private metadata leaked for %s: %v", kind, a)
		}
		if _, e = s.ReportContent(ctx, actionStore("adult", kind, "nsfw")); status.Code(e) != codes.NotFound {
			t.Fatalf("private report %s: %v", kind, e)
		}
		if _, e = s.Reveal(ctx, actionStore("adult", kind, "")); status.Code(e) != codes.NotFound {
			t.Fatalf("private reveal %s: %v", kind, e)
		}
	}
	execStore(t, db, `INSERT INTO group_members(group_id,user_id,status) SELECT g.id,u.id,'active' FROM groups g,user_account u WHERE u.account_id='adult'`)
	for _, kind := range []string{"blog", "event", "group", "discussion"} {
		if !checkStore(t, s, "adult", kind).CanReveal {
			t.Fatalf("active member denied %s", kind)
		}
	}
	execStore(t, db, `UPDATE group_members SET status='banned'`)
	if checkStore(t, s, "adult", "blog").Reason != "not_found" {
		t.Fatal("banned member admitted")
	}
	execStore(t, db, `UPDATE blog SET status='Draft'; UPDATE events SET status='draft'; UPDATE groups SET status='draft'; UPDATE discussion_posts SET status='hidden'`)
	for _, kind := range []string{"blog", "event", "group", "discussion"} {
		if checkStore(t, s, "adult", kind).Reason != "not_found" {
			t.Fatalf("draft/hidden visible: %s", kind)
		}
	}
	for _, path := range []string{"/api/v2/storage/posts/post/a.jpg", "/api/storage/events/event/cover", "/api/v1/files/post/post/a.jpg", "/api/storage/groups/group/logo"} {
		r, e := s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: path})
		mustStore(t, e)
		if r.Allowed {
			t.Fatalf("private media allowed %s", path)
		}
	}
}

func TestStoreMediaAliasesCASAndDiscussionKeys(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	checksum := strings.Repeat("a", 64)
	execStore(t, db, `INSERT INTO storage_assets(checksum,object_key) VALUES($1,$2)`, checksum, "assets/sha256/aa/aa/"+checksum+".jpg")
	execStore(t, db, `INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,'blog','post','editor_image','a.jpg')`, uuid.NewString(), checksum)
	for _, path := range []string{"/api/v2/storage/posts/post/a.jpg", "/api/storage/posts/post/a.jpg", "/api/v1/files/post/post/a.jpg", "/api/v2/storage/assets/sha256/aa/aa/" + checksum + ".jpg"} {
		r, e := s.CheckMedia(ctx, &pb.MediaRequest{Path: path})
		mustStore(t, e)
		if !r.Allowed || r.Restricted {
			t.Fatalf("unscanned legacy media blocked %s: %v", path, r)
		}
	}
	_, e := s.ReportContent(ctx, actionStore("support", "blog", "nsfw"))
	mustStore(t, e)
	for _, path := range []string{"/api/v2/storage/posts/post/a.jpg", "/api/storage/posts/post/a.jpg", "/api/v1/files/post/post/a.jpg", "/api/storage/assets/sha256/aa/aa/" + checksum + ".jpg"} {
		r, e := s.CheckMedia(ctx, &pb.MediaRequest{Path: path})
		mustStore(t, e)
		if r.Allowed || !r.Restricted {
			t.Fatalf("flagged media allowed %s: %v", path, r)
		}
	}
	_, e = s.Reveal(ctx, actionStore("adult", "blog", ""))
	mustStore(t, e)
	r, e := s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: "/api/storage/posts/post/a.jpg"})
	mustStore(t, e)
	if !r.Allowed {
		t.Fatal("explicit post reveal ignored")
	}
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: "/api/storage/assets/sha256/aa/aa/" + checksum + ".jpg"})
	mustStore(t, e)
	if !r.Allowed || !r.Restricted {
		t.Fatalf("CAS ignored the current reveal of its sole owning content: %v", r)
	}
	execStore(t, db, `INSERT INTO blog(blog_id,user_id,status) SELECT 'unrevealed-owner',id,'Published' FROM user_account WHERE account_id='owner'`)
	execStore(t, db, `INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,'blog','unrevealed-owner','image','a.jpg')`, uuid.NewString(), checksum)
	_, e = s.ReportContent(ctx, &pb.ActionRequest{Actor: &pb.Actor{AccountId: "support"}, Target: &pb.Target{Kind: "blog", Id: "unrevealed-owner"}, Reason: "nsfw"})
	mustStore(t, e)
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: "/api/storage/assets/sha256/aa/aa/" + checksum + ".jpg"})
	mustStore(t, e)
	if r.Allowed || !r.Restricted {
		t.Fatal("shared CAS bypassed another flagged owner without a reveal")
	}

	execStore(t, db, `UPDATE storage_asset_refs SET deleted_at=NOW(); UPDATE storage_assets SET is_nsfw=true`)
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{Path: "/api/storage/assets/sha256/aa/aa/" + checksum + ".jpg"})
	mustStore(t, e)
	if r.Allowed || !r.Restricted {
		t.Fatal("asset's own scanner rating ignored")
	}
	execStore(t, db, `INSERT INTO discussion_files(post_id,storage_key,content_type) SELECT id,'https://cdn.example.test/arbitrary/file.jpg','image/jpeg' FROM discussion_posts`)
	_, e = s.ReportContent(ctx, actionStore("support", "discussion", "nsfw"))
	mustStore(t, e)
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{Path: "https://cdn.example.test/arbitrary/file.jpg"})
	mustStore(t, e)
	if r.Allowed || !r.Restricted {
		t.Fatal("arbitrary discussion storage key bypassed moderation")
	}
}

func TestStoreGroupRatingDoesNotFlagMemberContent(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	execStore(t, db, `UPDATE blog SET group_id=(SELECT id FROM groups); UPDATE events SET group_id=(SELECT id FROM groups); UPDATE discussion_posts SET group_id=(SELECT id FROM groups);`)
	_, e := s.ReportContent(ctx, actionStore("support", "group", "nsfw"))
	mustStore(t, e)
	for _, kind := range []string{"blog", "event", "discussion"} {
		if checkStore(t, s, "adult", kind).IsNsfw {
			t.Fatalf("group rating infected member %s", kind)
		}
	}
	for _, path := range []string{"/api/storage/groups/group/logo", "/api/v2/storage/groups/group/cover"} {
		r, e := s.CheckMedia(ctx, &pb.MediaRequest{Path: path})
		mustStore(t, e)
		if r.Allowed || !r.Restricted {
			t.Fatalf("flagged group presentation allowed: %v", r)
		}
	}
}

func TestStoreConcurrentDuplicateReportAndReview(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	ids := make(chan int64, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.ReportContent(ctx, actionStore("support", "blog", "nsfw"))
			if e != nil {
				errs <- e
				return
			}
			ids <- r.Report.Id
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		t.Error(e)
	}
	var id int64
	for n := range ids {
		if id != 0 && id != n {
			t.Error("concurrent duplicates")
		}
		id = n
	}
	if scalarStore(t, db, `SELECT count(*) FROM content_reports`) != 1 || scalarStore(t, db, `SELECT count(*) FROM admin_audit_log WHERE action='moderation.report_nsfw'`) != 1 {
		t.Fatal("duplicate report/audit")
	}
	errs = make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.ReviewReport(ctx, &pb.ReviewRequest{Actor: &pb.Actor{AccountId: "admin"}, ReportId: id, Action: "reverse"})
			if e != nil {
				errs <- e
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if checkStore(t, s, "adult", "blog").IsNsfw || scalarStore(t, db, `SELECT count(*) FROM admin_audit_log WHERE action='moderation.review_reverse'`) != 1 {
		t.Fatal("duplicate reverse/audit")
	}
}

func TestStoreMigrationDownAndUp(t *testing.T) {
	_, db := storeFixture(t)
	down, e := os.ReadFile(filepath.Join("..", "..", "schema", "000025_content_moderation.down.sql"))
	mustStore(t, e)
	execStore(t, db, string(down))
	up, e := os.ReadFile(filepath.Join("..", "..", "schema", "000025_content_moderation.up.sql"))
	mustStore(t, e)
	execStore(t, db, string(up))
	if scalarStore(t, db, `SELECT count(*) FROM content_reports`) != 0 {
		t.Fatal("migration did not reset owned tables")
	}
}

func TestStorePublicStatusAndPromotion(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	r, e := s.ReportContent(ctx, actionStore("adult", "blog", "nsfw"))
	mustStore(t, e)
	execStore(t, db, `UPDATE user_account SET role_id=(SELECT id FROM user_role WHERE role_desc='Support') WHERE account_id='adult'`)
	promoted, e := s.ReportContent(ctx, actionStore("adult", "blog", "nsfw"))
	mustStore(t, e)
	if promoted.Report.Id != r.Report.Id || promoted.Report.Status != "action_taken" || !promoted.Access.IsNsfw {
		t.Fatalf("current staff report not applied: %v", promoted)
	}
	queue, e := s.ListReports(ctx, &pb.ListRequest{Actor: &pb.Actor{AccountId: "support"}, Status: "action_taken"})
	mustStore(t, e)
	if queue.Total != 1 || len(queue.Reports) != 1 || queue.Reports[0].Status != "action_taken" {
		t.Fatalf("public status contract: %v", queue)
	}
}

func TestStoreCheckOrderMissingDraftAndInactiveActor(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	execStore(t, db, `UPDATE blog SET status='Draft'`)
	targets := []*pb.Target{{Kind: "blog", Id: "stale-es-id"}, targetStore("blog"), targetStore("event"), targetStore("blog")}
	r, e := s.Check(ctx, &pb.CheckRequest{AccountId: "owner", Targets: targets})
	mustStore(t, e)
	if len(r.Items) != len(targets) {
		t.Fatal("missing placeholders")
	}
	for i, a := range r.Items {
		if a.Target.Id != targets[i].Id || a.Target.Kind != targets[i].Kind {
			t.Fatal("order changed")
		}
	}
	if r.Items[0].Reason != "not_found" || r.Items[1].Reason == "not_found" || r.Items[2].Reason == "not_found" {
		t.Fatalf("missing/owner draft handling: %v", r)
	}
	batch := make([]*pb.Target, 101)
	for i := range batch {
		batch[i] = targetStore("event")
	}
	if _, e = s.Check(ctx, &pb.CheckRequest{Targets: batch}); status.Code(e) != codes.InvalidArgument {
		t.Fatalf("oversized batch %v", e)
	}
	execStore(t, db, `UPDATE user_account SET user_status=(SELECT id FROM user_status WHERE status='suspended') WHERE account_id='support'`)
	if _, e = s.ListReports(ctx, &pb.ListRequest{Actor: &pb.Actor{AccountId: "support"}}); status.Code(e) != codes.PermissionDenied {
		t.Fatalf("suspended staff authorized: %v", e)
	}
}

func TestStoreCASRevealSharedRefsAndDOBRevalidation(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	sum := strings.Repeat("b", 64)
	media := "/api/storage/assets/sha256/bb/bb/" + sum + ".jpg"
	execStore(t, db, `INSERT INTO storage_assets(checksum,object_key,is_nsfw) VALUES($1,$2,true)`, sum, "assets/sha256/bb/bb/"+sum+".jpg")
	r, e := s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: media})
	mustStore(t, e)
	if r.Allowed {
		t.Fatal("orphan NSFW asset allowed")
	}
	execStore(t, db, `INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,'blog','post','image','b.jpg')`, uuid.NewString(), sum)
	_, e = s.Reveal(ctx, actionStore("adult", "blog", ""))
	mustStore(t, e)
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: media})
	mustStore(t, e)
	if !r.Allowed || !r.Restricted {
		t.Fatalf("adult reveal denied on scanner-rated asset: %v", r)
	}
	execStore(t, db, `UPDATE user_account SET date_of_birth='2020-01-01' WHERE account_id='adult'`)
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: media})
	mustStore(t, e)
	if r.Allowed {
		t.Fatal("DOB change bypassed CAS gate")
	}
	execStore(t, db, `UPDATE user_account SET date_of_birth='1990-01-01' WHERE account_id='adult'; INSERT INTO blog(blog_id,user_id,status) SELECT 'second',id,'Published' FROM user_account WHERE account_id='owner'`)
	execStore(t, db, `INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,'blog','second','image','b.jpg')`, uuid.NewString(), sum)
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: media})
	mustStore(t, e)
	if r.Allowed {
		t.Fatal("one reveal exposed a second rated owner")
	}
	_, e = s.Reveal(ctx, &pb.ActionRequest{Actor: &pb.Actor{AccountId: "adult"}, Target: &pb.Target{Kind: "blog", Id: "second"}})
	mustStore(t, e)
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: media})
	mustStore(t, e)
	if !r.Allowed {
		t.Fatalf("all owning-content reveals denied %v", r)
	}
}

func TestStoreMediaSourceChangesInvalidateReveal(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	sum := strings.Repeat("c", 64)
	label := actionStore("owner", "blog", "")
	label.IsNsfw = true
	_, e := s.SetAuthorLabel(ctx, label)
	mustStore(t, e)
	_, e = s.Reveal(ctx, actionStore("adult", "blog", ""))
	mustStore(t, e)
	before := checkStore(t, s, "adult", "blog")
	execStore(t, db, `INSERT INTO storage_assets(checksum,object_key) VALUES($1,$2)`, sum, "assets/sha256/cc/cc/"+sum+".jpg")
	execStore(t, db, `INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,'blog','post','image','c.jpg')`, uuid.NewString(), sum)
	execStore(t, db, `UPDATE storage_assets SET is_nsfw=true WHERE checksum=$1`, sum)
	after := checkStore(t, s, "adult", "blog")
	if after.Revealed || after.Revision <= before.Revision {
		t.Fatalf("scanner changed without invalidating reveal: before %v after %v", before, after)
	}
	_, e = s.Reveal(ctx, actionStore("adult", "blog", ""))
	mustStore(t, e)
	execStore(t, db, `INSERT INTO blog_admin_flags(blog_id,flag_type,reason,created_by) SELECT id,'nsfw','legacy','admin' FROM blog WHERE blog_id='post'`)
	if checkStore(t, s, "adult", "blog").Revealed {
		t.Fatal("legacy flag did not invalidate reveal")
	}
}

func TestStoreReversePreservesOtherRatingSources(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	first, e := s.ReportContent(ctx, actionStore("support", "blog", "nsfw"))
	mustStore(t, e)
	second, e := s.ReportContent(ctx, actionStore("community", "blog", "nsfw"))
	mustStore(t, e)
	rev := func(id int64) {
		_, e := s.ReviewReport(ctx, &pb.ReviewRequest{Actor: &pb.Actor{AccountId: "admin"}, ReportId: id, Action: "reverse"})
		mustStore(t, e)
	}
	rev(first.Report.Id)
	if !checkStore(t, s, "adult", "blog").IsNsfw {
		t.Fatal("reversal erased another staff decision")
	}
	label := actionStore("owner", "blog", "")
	label.IsNsfw = true
	_, e = s.SetAuthorLabel(ctx, label)
	mustStore(t, e)
	execStore(t, db, `UPDATE content_moderation SET scanner_nsfw=true WHERE target_kind='blog' AND target_id='post'`)
	rev(second.Report.Id)
	if !checkStore(t, s, "adult", "blog").IsNsfw {
		t.Fatal("reversal erased author/scanner labels")
	}
}

func TestStoreSubstantiveEditsInvalidateReveal(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	for _, tc := range []struct{ kind, edit string }{
		{"discussion", `UPDATE discussion_posts SET body='body-only edit' WHERE public_id='discussion'`},
		{"event", `UPDATE events SET description='description-only edit' WHERE slug='event'`},
		{"event", `UPDATE events SET title='new title',cover_image='/api/storage/events/event/cover?revision=2' WHERE slug='event'`},
		{"group", `UPDATE groups SET description='description-only edit' WHERE slug='group'`},
		{"group", `UPDATE groups SET name='New name',logo_image='/api/storage/groups/group/logo?v=2',cover_image='/api/storage/groups/group/cover?v=2' WHERE slug='group'`},
		{"blog", `UPDATE blog SET updated_at=updated_at+interval '1 second' WHERE blog_id='post'`},
		{"blog", `UPDATE blog SET status='Draft' WHERE blog_id='post'; UPDATE blog SET status='Published' WHERE blog_id='post'`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			label := actionStore("owner", tc.kind, "")
			label.IsNsfw = true
			_, e := s.SetAuthorLabel(ctx, label)
			mustStore(t, e)
			_, e = s.Reveal(ctx, actionStore("adult", tc.kind, ""))
			mustStore(t, e)
			before := checkStore(t, s, "adult", tc.kind)
			if !before.Revealed {
				t.Fatal("fixture did not reveal content")
			}
			execStore(t, db, tc.edit)
			after := checkStore(t, s, "adult", tc.kind)
			if after.Revealed || after.Revision <= before.Revision {
				t.Fatalf("substantive edit retained consent: before %v after %v", before, after)
			}
		})
	}
}

func TestStoreScannerOnlyChangeInvalidatesExistingOwnerReveals(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	sum := strings.Repeat("e", 64)
	execStore(t, db, `INSERT INTO storage_assets(checksum,object_key) VALUES($1,$2)`, sum, "assets/sha256/ee/ee/"+sum+".jpg")
	for _, kind := range []string{"blog", "event", "group", "discussion"} {
		execStore(t, db, `INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,$3,$4,'image','existing.jpg')`, uuid.NewString(), sum, kind, targetStore(kind).Id)
		label := actionStore("owner", kind, "")
		label.IsNsfw = true
		_, e := s.SetAuthorLabel(ctx, label)
		mustStore(t, e)
	}
	// Every reference and author label exists BEFORE consent. Only changing the
	// scanner bit below may invalidate these reveals; ref insertion cannot mask it.
	before := map[string]int64{}
	for _, kind := range []string{"blog", "event", "group", "discussion"} {
		_, e := s.Reveal(ctx, actionStore("adult", kind, ""))
		mustStore(t, e)
		a := checkStore(t, s, "adult", kind)
		if !a.Revealed {
			t.Fatalf("%s not revealed", kind)
		}
		before[kind] = a.Revision
	}
	execStore(t, db, `UPDATE storage_assets SET is_nsfw=true WHERE checksum=$1`, sum)
	for _, kind := range []string{"blog", "event", "group", "discussion"} {
		a := checkStore(t, s, "adult", kind)
		if a.Revealed || a.Revision <= before[kind] {
			t.Errorf("%s scanner-only change retained consent: %v", kind, a)
		}
	}
}

func TestStoreMediaResolutionIsExplicit(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		path     string
		resolved bool
	}{
		{"/api/storage/posts/post/a.jpg", true},
		{"/api/v2/storage/groups/group/logo", true},
		{"/api/storage/events/event/cover", true},
		{"/api/v1/files/profile/legacy/profile.jpg", false},
		{"/api/storage/verifications/document.jpg", false},
		{"/unrelated/legacy.jpg", false},
	} {
		r, e := s.CheckMedia(ctx, &pb.MediaRequest{Path: tc.path})
		mustStore(t, e)
		if !r.Allowed || r.Resolved != tc.resolved {
			t.Errorf("%s: %v want resolved %t", tc.path, r, tc.resolved)
		}
	}
	_, e := s.ReportContent(ctx, actionStore("support", "blog", "nsfw"))
	mustStore(t, e)
	r, e := s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: "/api/storage/posts/post/a.jpg"})
	mustStore(t, e)
	if r.Allowed || r.Resolved {
		t.Fatal("unrevealed media was resolved as authorized")
	}
	_, e = s.Reveal(ctx, actionStore("adult", "blog", ""))
	mustStore(t, e)
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: "/api/storage/posts/post/a.jpg"})
	mustStore(t, e)
	if !r.Allowed || !r.Resolved {
		t.Fatalf("revealed owning media not resolved %v", r)
	}
	execStore(t, db, `UPDATE events SET visibility='private'`)
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: "/api/storage/events/event/cover"})
	mustStore(t, e)
	if r.Allowed || r.Resolved {
		t.Fatal("private event media got trusted admission")
	}
	sum := strings.Repeat("f", 64)
	execStore(t, db, `INSERT INTO storage_assets(checksum,object_key,is_nsfw) VALUES($1,$2,true)`, sum, "assets/sha256/ff/ff/"+sum+".jpg")
	r, e = s.CheckMedia(ctx, &pb.MediaRequest{AccountId: "adult", Path: "/api/storage/assets/sha256/ff/ff/" + sum + ".jpg"})
	mustStore(t, e)
	if r.Allowed || r.Resolved {
		t.Fatal("orphan NSFW asset got trusted admission")
	}
}

func TestStorePrivateFlaggedReferenceCannotLaunderSharedAsset(t *testing.T) {
	s, db := storeFixture(t)
	ctx := context.Background()
	sum := strings.Repeat("a", 64)
	execStore(t, db, `INSERT INTO storage_assets(checksum,object_key) VALUES($1,$2)`, sum, "assets/sha256/aa/aa/"+sum+".jpg")
	execStore(t, db, `INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,'blog','post','editor_image','shared.jpg')`, uuid.NewString(), sum)
	execStore(t, db, `INSERT INTO blog(blog_id,user_id,status,audience) SELECT 'private-owner',id,'Published','group_only' FROM user_account WHERE account_id='owner'`)
	execStore(t, db, `INSERT INTO storage_asset_refs(id,checksum,owner_type,owner_id,purpose,file_name) VALUES($1,$2,'blog','private-owner','editor_image','shared.jpg')`, uuid.NewString(), sum)
	_, err := s.SetAuthorLabel(ctx, &pb.ActionRequest{Actor: &pb.Actor{AccountId: "owner"}, Target: &pb.Target{Kind: "blog", Id: "private-owner"}, IsNsfw: true})
	mustStore(t, err)
	media, err := s.CheckMedia(ctx, &pb.MediaRequest{Path: "/api/v2/storage/assets/sha256/aa/aa/" + sum + ".jpg"})
	mustStore(t, err)
	if media.Allowed {
		t.Fatal("public reference laundered NSFW asset owned by private content")
	}
}
