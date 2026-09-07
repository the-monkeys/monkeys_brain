package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestIsOrphanChecksum(t *testing.T) {
	if isOrphanChecksum(1, 0) {
		t.Fatal("two-blog share: deleting one ref must not GC")
	}
	if !isOrphanChecksum(0, 0) {
		t.Fatal("last live ref gone with no verification pointer must GC")
	}
	if isOrphanChecksum(0, 1) {
		t.Fatal("verification checksum still referenced must not GC")
	}
}

func TestCollectOrphanedAssetsSQL(t *testing.T) {
	q := collectOrphanedAssetsSQL
	if !strings.Contains(q, "storage_asset_refs") || !strings.Contains(q, "deleted_at IS NULL") {
		t.Fatal("must require zero live refs")
	}
	if !strings.Contains(q, "verification_requests") || !strings.Contains(q, "selfie_checksum") {
		t.Fatal("must not GC blobs still on a verification request")
	}
	if !strings.Contains(q, "storage_assets") {
		t.Fatal("must return object_key from storage_assets")
	}
}

func TestDeleteHistoricalAssetRefsSQL(t *testing.T) {
	q := deleteHistoricalAssetRefsSQL
	if !strings.Contains(q, "DELETE FROM storage_asset_refs") {
		t.Fatal("must delete historical storage_asset_refs")
	}
	if !strings.Contains(q, "checksum") || !strings.Contains(q, "deleted_at IS NOT NULL") {
		t.Fatal("must target only soft-deleted refs for a checksum")
	}
	if strings.Contains(q, "deleted_at IS NULL") {
		t.Fatal("must never delete live refs")
	}
	if !strings.Contains(deleteOrphanedStorageAssetSQL, "DELETE FROM storage_assets") {
		t.Fatal("must then delete the CAS row")
	}
}

type fakeResult struct{ n int64 }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.n, nil }

type recordingExecer struct {
	calls  []string
	failOn string
	fail   error
}

func (e *recordingExecer) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	e.calls = append(e.calls, strings.TrimSpace(query))
	if e.failOn != "" && strings.Contains(query, e.failOn) {
		return nil, e.fail
	}
	n := int64(0)
	if strings.Contains(query, "DELETE FROM storage_assets") {
		n = 1
	}
	return fakeResult{n: n}, nil
}

func TestPurgeOrphanedStorageAssetSavepointOnFailedDelete(t *testing.T) {
	exec := &recordingExecer{failOn: "DELETE FROM storage_assets", fail: errors.New("fk restrict")}
	deleted, skip, err := purgeOrphanedStorageAsset(context.Background(), exec, "abc")
	if deleted || !skip || err == nil {
		t.Fatalf("want skip with error, got deleted=%v skip=%v err=%v", deleted, skip, err)
	}
	want := []string{
		gcAssetSavepointSQL,
		strings.TrimSpace(deleteHistoricalAssetRefsSQL),
		deleteOrphanedStorageAssetSQL,
		rollbackGCAssetSavepointSQL,
	}
	if len(exec.calls) != len(want) {
		t.Fatalf("calls=%v want=%v", exec.calls, want)
	}
	for i := range want {
		if exec.calls[i] != want[i] {
			t.Fatalf("call %d = %q want %q", i, exec.calls[i], want[i])
		}
	}
}

func TestPurgeOrphanedStorageAssetSuccess(t *testing.T) {
	exec := &recordingExecer{}
	deleted, skip, err := purgeOrphanedStorageAsset(context.Background(), exec, "abc")
	if !deleted || skip || err != nil {
		t.Fatalf("want deleted, got deleted=%v skip=%v err=%v", deleted, skip, err)
	}
	if exec.calls[0] != gcAssetSavepointSQL || exec.calls[len(exec.calls)-1] != releaseGCAssetSavepointSQL {
		t.Fatalf("expected savepoint then release, got %v", exec.calls)
	}
	if !strings.Contains(exec.calls[1], "deleted_at IS NOT NULL") {
		t.Fatal("historical refs must be deleted before storage_assets")
	}
	if exec.calls[2] != deleteOrphanedStorageAssetSQL {
		t.Fatal("storage_assets delete must follow historical refs")
	}
}

func TestReplaceSoftDeleteAssetRefsSQLReturnsChecksums(t *testing.T) {
	q := replaceSoftDeleteAssetRefsSQL
	if !strings.Contains(q, "UPDATE storage_asset_refs") {
		t.Fatal("replace must soft-delete the old live ref")
	}
	if !strings.Contains(q, "RETURNING checksum") {
		t.Fatal("replace must return old checksums for last-ref GC")
	}
	if !strings.Contains(q, "deleted_at IS NULL") {
		t.Fatal("replace must only touch live refs")
	}
}

func TestReplaceAssetRefUsesSharedOrphanGC(t *testing.T) {
	src, err := os.ReadFile("database.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "func (s *storageDB) ReplaceAssetRef")
	if start < 0 {
		t.Fatal("ReplaceAssetRef missing")
	}
	rest := text[start+1:]
	next := strings.Index(rest, "\nfunc ")
	if next < 0 {
		t.Fatal("could not bound ReplaceAssetRef")
	}
	body := rest[:next]
	if !strings.Contains(body, "replaceSoftDeleteAssetRefsSQL") {
		t.Fatal("replace must RETURNING checksums of replaced refs")
	}
	if !strings.Contains(body, "collectAndPurgeOrphans") {
		t.Fatal("replace must reuse DeleteAssetRef orphan SQL + SAVEPOINT purge")
	}
	insertAt := strings.Index(body, "INSERT INTO storage_asset_refs")
	gcAt := strings.Index(body, "collectAndPurgeOrphans")
	if insertAt < 0 || gcAt < insertAt {
		t.Fatal("GC must run after the new live ref is inserted")
	}
	delStart := strings.Index(text, "func (s *storageDB) DeleteAssetRef")
	delRest := text[delStart+1:]
	delNext := strings.Index(delRest, "\nfunc ")
	delBody := delRest[:delNext]
	if !strings.Contains(delBody, "collectAndPurgeOrphans") {
		t.Fatal("DeleteAssetRef must share collectAndPurgeOrphans")
	}
}
