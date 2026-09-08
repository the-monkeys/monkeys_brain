package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"go.uber.org/zap"
)

type StorageDB interface {
	CheckAsset(ctx context.Context, checksum string) (*pb.CheckAssetRes, error)
	RegisterAsset(ctx context.Context, req *pb.RegisterAssetReq) (*pb.RegisterAssetRes, error)
	UpdateNSFW(ctx context.Context, checksum string, isNSFW bool, score float32) error
	CreateAssetRef(ctx context.Context, req *pb.CreateAssetRefReq) (*pb.CreateAssetRefRes, error)
	DeleteAssetRef(ctx context.Context, req *pb.DeleteAssetRefReq) (*pb.DeleteAssetRefRes, error)
	ReplaceAssetRef(ctx context.Context, req *pb.ReplaceAssetRefReq) (*pb.ReplaceAssetRefRes, error)
	ResolveAssetRead(ctx context.Context, req *pb.ResolveAssetReadReq) (*pb.ResolveAssetReadResp, error)
}

type storageDB struct {
	db  *sql.DB
	log *zap.SugaredLogger
}

func NewStorageDB(cfg *config.Config, log *zap.SugaredLogger) (StorageDB, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.Postgresql.PrimaryDB.DBUsername,
		cfg.Postgresql.PrimaryDB.DBPassword,
		cfg.Postgresql.PrimaryDB.DBHost,
		cfg.Postgresql.PrimaryDB.DBPort,
		cfg.Postgresql.PrimaryDB.DBName,
	)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %v", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(3)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping postgres: %v", err)
	}

	return &storageDB{db: db, log: log}, nil
}

func (s *storageDB) CheckAsset(ctx context.Context, checksum string) (*pb.CheckAssetRes, error) {
	var objectKey string
	var width, height sql.NullInt32
	var blurhash sql.NullString

	err := s.db.QueryRowContext(ctx,
		"SELECT object_key, width, height, blurhash FROM storage_assets WHERE checksum = $1",
		strings.TrimSpace(checksum)).Scan(&objectKey, &width, &height, &blurhash)

	if err != nil {
		if err == sql.ErrNoRows {
			return &pb.CheckAssetRes{Exists: false}, nil
		}
		return nil, err
	}

	res := &pb.CheckAssetRes{
		Exists:    true,
		ObjectKey: objectKey,
	}
	if width.Valid {
		res.Width = width.Int32
	}
	if height.Valid {
		res.Height = height.Int32
	}
	if blurhash.Valid {
		res.Blurhash = blurhash.String
	}
	return res, nil
}

func (s *storageDB) RegisterAsset(ctx context.Context, req *pb.RegisterAssetReq) (*pb.RegisterAssetRes, error) {
	checksum := strings.TrimSpace(req.Checksum)
	objectKey := strings.TrimSpace(req.ObjectKey)
	if checksum == "" {
		return nil, fmt.Errorf("checksum is required")
	}
	if objectKey == "" {
		return nil, fmt.Errorf("object_key is required")
	}

	var canonicalChecksum, canonicalObjectKey string
	err := s.db.QueryRowContext(ctx,
		`WITH inserted AS (
			INSERT INTO storage_assets (checksum, object_key, content_type, size, width, height, blurhash)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (checksum) DO NOTHING
			RETURNING checksum, object_key
		)
		SELECT checksum, object_key FROM inserted
		UNION ALL
		SELECT checksum, object_key FROM storage_assets WHERE checksum = $1
		LIMIT 1`,
		checksum, objectKey, req.ContentType, nullableInt64(req.Size), nullableInt32(req.Width), nullableInt32(req.Height), nullableString(req.Blurhash),
	).Scan(&canonicalChecksum, &canonicalObjectKey)
	if err != nil {
		return nil, err
	}

	return &pb.RegisterAssetRes{
		Success:   true,
		Checksum:  canonicalChecksum,
		ObjectKey: canonicalObjectKey,
	}, nil
}

func (s *storageDB) UpdateNSFW(ctx context.Context, checksum string, isNSFW bool, score float32) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE storage_assets SET is_nsfw = $1, nsfw_score = $2, updated_at = CURRENT_TIMESTAMP WHERE checksum = $3",
		isNSFW, score, strings.TrimSpace(checksum))
	return err
}

func (s *storageDB) CreateAssetRef(ctx context.Context, req *pb.CreateAssetRefReq) (*pb.CreateAssetRefRes, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := createAssetRefTx(ctx, tx, req)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

const collectOrphanedAssetsSQL = `
SELECT a.checksum, a.object_key
FROM storage_assets a
WHERE a.checksum = ANY($1::text[])
  AND NOT EXISTS (
      SELECT 1 FROM storage_asset_refs r
      WHERE r.checksum = a.checksum AND r.deleted_at IS NULL
  )
  AND NOT EXISTS (
      SELECT 1 FROM verification_requests v
      WHERE v.selfie_checksum = a.checksum
         OR v.id_front_checksum = a.checksum
         OR v.id_back_checksum = a.checksum
  )`

// Historical (soft-deleted) refs still RESTRICT-delete storage_assets. Remove only
// those rows — never live refs (deleted_at IS NULL).
const deleteHistoricalAssetRefsSQL = `
DELETE FROM storage_asset_refs
WHERE checksum = $1 AND deleted_at IS NOT NULL`

const deleteOrphanedStorageAssetSQL = `DELETE FROM storage_assets WHERE checksum = $1`

// Soft-delete the live owner/purpose/file ref, then RETURNING checksum so
// ReplaceAssetRef can run the same last-ref GC as DeleteAssetRef.
const replaceSoftDeleteAssetRefsSQL = `UPDATE storage_asset_refs
		 SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		 WHERE owner_type = $1
		   AND owner_id = $2
		   AND purpose = $3
		   AND COALESCE(file_name, '') = $4
		   AND deleted_at IS NULL
		 RETURNING checksum`

const (
	gcAssetSavepointSQL         = `SAVEPOINT gc_storage_asset`
	rollbackGCAssetSavepointSQL = `ROLLBACK TO SAVEPOINT gc_storage_asset`
	releaseGCAssetSavepointSQL  = `RELEASE SAVEPOINT gc_storage_asset`
)

type txExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func isOrphanChecksum(liveRefs, verificationPointers int) bool {
	return liveRefs == 0 && verificationPointers == 0
}

// purgeOrphanedStorageAsset deletes historical refs then the CAS row inside a
// SAVEPOINT so a failed DELETE cannot abort the outer transaction (and undo
// the soft-delete UPDATE). skip is true when GC failed but the savepoint was
// rolled back; the caller should log and continue. A non-skip err is fatal.
func purgeOrphanedStorageAsset(ctx context.Context, tx txExecer, checksum string) (deleted, skip bool, err error) {
	if _, err := tx.ExecContext(ctx, gcAssetSavepointSQL); err != nil {
		return false, false, err
	}
	rollback := func() error {
		_, rbErr := tx.ExecContext(ctx, rollbackGCAssetSavepointSQL)
		return rbErr
	}
	if _, err := tx.ExecContext(ctx, deleteHistoricalAssetRefsSQL, checksum); err != nil {
		if rbErr := rollback(); rbErr != nil {
			return false, false, rbErr
		}
		return false, true, err
	}
	res, err := tx.ExecContext(ctx, deleteOrphanedStorageAssetSQL, checksum)
	if err != nil {
		if rbErr := rollback(); rbErr != nil {
			return false, false, rbErr
		}
		return false, true, err
	}
	if _, err := tx.ExecContext(ctx, releaseGCAssetSavepointSQL); err != nil {
		return false, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, false, nil
	}
	return true, false, nil
}

func (s *storageDB) DeleteAssetRef(ctx context.Context, req *pb.DeleteAssetRefReq) (*pb.DeleteAssetRefRes, error) {
	refID := strings.TrimSpace(req.RefId)
	ownerType := strings.TrimSpace(req.OwnerType)
	ownerID := strings.TrimSpace(req.OwnerId)
	purpose := strings.TrimSpace(req.Purpose)
	fileName := strings.TrimSpace(req.FileName)

	var query string
	var args []any
	switch {
	case refID != "":
		query = `UPDATE storage_asset_refs
			 SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
			 WHERE id = $1 AND deleted_at IS NULL
			 RETURNING checksum`
		args = []any{refID}
	case ownerType != "" && ownerID != "" && purpose != "":
		query = `UPDATE storage_asset_refs
			 SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
			 WHERE owner_type = $1
			   AND owner_id = $2
			   AND purpose = $3
			   AND COALESCE(file_name, '') = $4
			   AND deleted_at IS NULL
			 RETURNING checksum`
		args = []any{ownerType, ownerID, purpose, fileName}
	case ownerType != "" && ownerID != "":
		query = `UPDATE storage_asset_refs
			 SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
			 WHERE owner_type = $1
			   AND owner_id = $2
			   AND deleted_at IS NULL
			 RETURNING checksum`
		args = []any{ownerType, ownerID}
	default:
		return nil, fmt.Errorf("ref_id or owner_type and owner_id are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var checksums []string
	var count int32
	for rows.Next() {
		var checksum string
		if err := rows.Scan(&checksum); err != nil {
			rows.Close()
			return nil, err
		}
		count++
		if _, ok := seen[checksum]; ok {
			continue
		}
		seen[checksum] = struct{}{}
		checksums = append(checksums, checksum)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	orphans, err := collectAndPurgeOrphans(ctx, tx, s.log, checksums)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &pb.DeleteAssetRefRes{Success: true, DeletedCount: count, Orphans: orphans}, nil
}

func collectAndPurgeOrphans(ctx context.Context, tx *sql.Tx, log *zap.SugaredLogger, checksums []string) ([]*pb.OrphanedAsset, error) {
	orphans := make([]*pb.OrphanedAsset, 0)
	if len(checksums) == 0 {
		return orphans, nil
	}
	candRows, err := tx.QueryContext(ctx, collectOrphanedAssetsSQL, checksums)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		checksum  string
		objectKey string
	}
	var candidates []candidate
	for candRows.Next() {
		var c candidate
		if err := candRows.Scan(&c.checksum, &c.objectKey); err != nil {
			candRows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	if err := candRows.Close(); err != nil {
		return nil, err
	}
	if err := candRows.Err(); err != nil {
		return nil, err
	}

	for _, c := range candidates {
		deleted, skip, err := purgeOrphanedStorageAsset(ctx, tx, c.checksum)
		if err != nil {
			if skip {
				if log != nil {
					log.Warnf("skip GC storage_assets checksum=%s: %v", c.checksum, err)
				}
				continue
			}
			return nil, err
		}
		if deleted {
			orphans = append(orphans, &pb.OrphanedAsset{Checksum: c.checksum, ObjectKey: c.objectKey})
		}
	}
	return orphans, nil
}

func (s *storageDB) ReplaceAssetRef(ctx context.Context, req *pb.ReplaceAssetRefReq) (*pb.ReplaceAssetRefRes, error) {
	checksum := strings.TrimSpace(req.Checksum)
	ownerType := strings.TrimSpace(req.OwnerType)
	ownerID := strings.TrimSpace(req.OwnerId)
	purpose := strings.TrimSpace(req.Purpose)
	fileName := strings.TrimSpace(req.FileName)
	if checksum == "" || ownerType == "" || ownerID == "" || purpose == "" {
		return nil, fmt.Errorf("checksum, owner_type, owner_id, and purpose are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	objectKey, err := assetObjectKeyTx(ctx, tx, checksum)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, replaceSoftDeleteAssetRefsSQL, ownerType, ownerID, purpose, fileName)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var oldChecksums []string
	var count int32
	for rows.Next() {
		var old string
		if err := rows.Scan(&old); err != nil {
			rows.Close()
			return nil, err
		}
		count++
		if _, ok := seen[old]; ok {
			continue
		}
		seen[old] = struct{}{}
		oldChecksums = append(oldChecksums, old)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	refID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO storage_asset_refs (id, checksum, owner_type, owner_id, purpose, file_name)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		refID, checksum, ownerType, ownerID, purpose, fileName,
	); err != nil {
		return nil, err
	}

	// Last-ref GC after the new live ref exists so a same-checksum replace is not orphaned.
	orphans, err := collectAndPurgeOrphans(ctx, tx, s.log, oldChecksums)
	if err != nil {
		return nil, err
	}
	_ = orphans // ReplaceAssetRefRes.Orphans / GetOrphans() after WSL protoc; do not invent pb.go.

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &pb.ReplaceAssetRefRes{
		Success:      true,
		RefId:        refID,
		Checksum:     checksum,
		ObjectKey:    objectKey,
		DeletedCount: count,
	}, nil
}

func createAssetRefTx(ctx context.Context, tx *sql.Tx, req *pb.CreateAssetRefReq) (*pb.CreateAssetRefRes, error) {
	checksum := strings.TrimSpace(req.Checksum)
	ownerType := strings.TrimSpace(req.OwnerType)
	ownerID := strings.TrimSpace(req.OwnerId)
	purpose := strings.TrimSpace(req.Purpose)
	fileName := strings.TrimSpace(req.FileName)
	if checksum == "" || ownerType == "" || ownerID == "" || purpose == "" {
		return nil, fmt.Errorf("checksum, owner_type, owner_id, and purpose are required")
	}

	objectKey, err := assetObjectKeyTx(ctx, tx, checksum)
	if err != nil {
		return nil, err
	}

	var existingID, existingChecksum string
	err = tx.QueryRowContext(ctx,
		`SELECT id, checksum
		 FROM storage_asset_refs
		 WHERE owner_type = $1
		   AND owner_id = $2
		   AND purpose = $3
		   AND COALESCE(file_name, '') = $4
		   AND deleted_at IS NULL`,
		ownerType, ownerID, purpose, fileName,
	).Scan(&existingID, &existingChecksum)
	if err == nil {
		if existingChecksum != checksum {
			return nil, fmt.Errorf("active asset reference already exists for owner")
		}
		return &pb.CreateAssetRefRes{
			Success:   true,
			RefId:     existingID,
			Checksum:  checksum,
			ObjectKey: objectKey,
		}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	var restoredID string
	err = tx.QueryRowContext(ctx,
		`UPDATE storage_asset_refs
		 SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = (
			 SELECT id
			 FROM storage_asset_refs
			 WHERE checksum = $1
			   AND owner_type = $2
			   AND owner_id = $3
			   AND purpose = $4
			   AND COALESCE(file_name, '') = $5
			   AND deleted_at IS NOT NULL
			 ORDER BY updated_at DESC
			 LIMIT 1
		 )
		 RETURNING id`,
		checksum, ownerType, ownerID, purpose, fileName,
	).Scan(&restoredID)
	if err == nil {
		return &pb.CreateAssetRefRes{
			Success:   true,
			RefId:     restoredID,
			Checksum:  checksum,
			ObjectKey: objectKey,
		}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	refID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO storage_asset_refs (id, checksum, owner_type, owner_id, purpose, file_name)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		refID, checksum, ownerType, ownerID, purpose, fileName,
	); err != nil {
		return nil, err
	}

	return &pb.CreateAssetRefRes{
		Success:   true,
		RefId:     refID,
		Checksum:  checksum,
		ObjectKey: objectKey,
	}, nil
}

func assetObjectKeyTx(ctx context.Context, tx *sql.Tx, checksum string) (string, error) {
	var objectKey string
	err := tx.QueryRowContext(ctx,
		"SELECT object_key FROM storage_assets WHERE checksum = $1",
		checksum,
	).Scan(&objectKey)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("asset not found for checksum %s", checksum)
	}
	return objectKey, err
}

func nullableString(v string) sql.NullString {
	v = strings.TrimSpace(v)
	return sql.NullString{String: v, Valid: v != ""}
}

func nullableInt32(v int32) sql.NullInt32 {
	return sql.NullInt32{Int32: v, Valid: v > 0}
}

func nullableInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: v > 0}
}

const resolveAssetObjectKeySQL = `SELECT object_key FROM storage_assets WHERE checksum = $1`

const resolveBlogStatusSQL = `SELECT COALESCE(status, '') FROM blog WHERE blog_id = $1`

const resolveAssetReadSQL = `
SELECT COALESCE(blog.status, ''), r.owner_id, a.object_key
FROM storage_asset_refs r
LEFT JOIN blog ON blog.blog_id = r.owner_id
LEFT JOIN storage_assets a ON a.checksum = r.checksum
WHERE r.checksum = $1
  AND r.owner_type = 'blog'
  AND r.deleted_at IS NULL
`

func (s *storageDB) ResolveAssetRead(ctx context.Context, req *pb.ResolveAssetReadReq) (*pb.ResolveAssetReadResp, error) {
	resp := &pb.ResolveAssetReadResp{}
	if req == nil {
		resp.NotFound = true
		return resp, nil
	}

	blogID := strings.TrimSpace(req.GetBlogId())
	checksum := strings.TrimSpace(req.GetChecksum())

	if blogID != "" && checksum == "" {
		var status string
		err := s.db.QueryRowContext(ctx, resolveBlogStatusSQL, blogID).Scan(&status)
		if err == sql.ErrNoRows {
			return resp, nil
		}
		if err != nil {
			return nil, err
		}
		resp.AllowPublic = status == constants.BlogStatusPublished
		return resp, nil
	}

	if checksum == "" {
		resp.NotFound = true
		return resp, nil
	}

	var objectKey string
	err := s.db.QueryRowContext(ctx, resolveAssetObjectKeySQL, checksum).Scan(&objectKey)
	if err == sql.ErrNoRows {
		resp.NotFound = true
		return resp, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(objectKey, "verifications/") {
		resp.VerificationOnly = true
		return resp, nil
	}

	rows, err := s.db.QueryContext(ctx, resolveAssetReadSQL, checksum)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	unpublished := make([]string, 0)
	for rows.Next() {
		var status, ownerID string
		var refObjectKey sql.NullString
		if err := rows.Scan(&status, &ownerID, &refObjectKey); err != nil {
			return nil, err
		}
		if status == constants.BlogStatusPublished {
			resp.AllowPublic = true
			continue
		}
		if ownerID != "" {
			unpublished = append(unpublished, ownerID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if resp.AllowPublic {
		return resp, nil
	}
	resp.UnpublishedBlogIds = unpublished
	return resp, nil
}
