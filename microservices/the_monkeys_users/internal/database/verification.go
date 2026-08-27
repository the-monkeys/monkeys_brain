package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
)

// ErrActiveVerificationExists signals a hit on the partial unique index
// uq_verification_requests_active (one live request per user). The service
// layer maps it to codes.AlreadyExists.
var ErrActiveVerificationExists = errors.New("an active verification request already exists")

// verificationColumns is the canonical projection shared by every read so
// the scan order stays in lockstep with scanVerificationRequest.
const verificationColumns = `id, username, verification_type, country, id_document_type,
	proof_urls, status, selfie_checksum, id_front_checksum, id_back_checksum,
	additional_info, reviewer_username, rejection_reason, created_at, updated_at, reviewed_at`

func scanVerificationRequest(row scanner) (*models.VerificationRequest, error) {
	var v models.VerificationRequest
	if err := row.Scan(
		&v.Id,
		&v.Username,
		&v.VerificationType,
		&v.Country,
		&v.IDDocumentType,
		&v.ProofUrls,
		&v.Status,
		&v.SelfieChecksum,
		&v.IDFrontChecksum,
		&v.IDBackChecksum,
		&v.AdditionalInfo,
		&v.ReviewerUsername,
		&v.RejectionReason,
		&v.CreatedAt,
		&v.UpdatedAt,
		&v.ReviewedAt,
	); err != nil {
		return nil, err
	}
	return &v, nil
}

// CreateVerificationRequest inserts a new submission with status 'pending'.
// Concurrent duplicate submissions lose at the DB level (partial unique
// index), which surfaces as ErrActiveVerificationExists.
func (uh *uDBHandler) CreateVerificationRequest(vr *models.VerificationRequest) (*models.VerificationRequest, error) {
	if strings.TrimSpace(vr.Id) == "" {
		vr.Id = uuid.NewString()
	}

	row := uh.db.QueryRow(`
		INSERT INTO verification_requests
			(id, username, verification_type, country, id_document_type,
			 status, selfie_checksum, id_front_checksum, id_back_checksum, additional_info)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+verificationColumns+`;`,
		vr.Id, vr.Username, vr.VerificationType, vr.Country, vr.IDDocumentType,
		constants.VerificationStatusPending, vr.SelfieChecksum, vr.IDFrontChecksum, vr.IDBackChecksum, vr.AdditionalInfo)

	created, err := scanVerificationRequest(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "uq_verification_requests_active" {
			return nil, ErrActiveVerificationExists
		}
		uh.log.Errorf("create verification request for %s: insert: %v", vr.Username, err)
		return nil, err
	}
	return created, nil
}

// GetLatestVerificationRequest returns the user's most recent submission
// (any state). sql.ErrNoRows when the user never applied.
func (uh *uDBHandler) GetLatestVerificationRequest(username string) (*models.VerificationRequest, error) {
	row := uh.db.QueryRow(`
		SELECT `+verificationColumns+`
		FROM verification_requests
		WHERE username = $1
		ORDER BY created_at DESC
		LIMIT 1;`, username)

	v, err := scanVerificationRequest(row)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			uh.log.Errorf("get latest verification request for %s: scan: %v", username, err)
		}
		return nil, err
	}
	return v, nil
}

// GetVerificationRequestByID returns any request by id (admin path).
func (uh *uDBHandler) GetVerificationRequestByID(id string) (*models.VerificationRequest, error) {
	row := uh.db.QueryRow(`
		SELECT `+verificationColumns+`
		FROM verification_requests
		WHERE id = $1;`, id)

	v, err := scanVerificationRequest(row)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			uh.log.Errorf("get verification request %s: scan: %v", id, err)
		}
		return nil, err
	}
	return v, nil
}

// CancelVerificationRequest deletes the user's own PENDING request —
// cancelling is only meaningful before review starts. Rows in later states
// are history and must stay queryable, so nothing else is cancellable.
// Returns sql.ErrNoRows when there is no pending request to cancel.
func (uh *uDBHandler) CancelVerificationRequest(id, username string) error {
	res, err := uh.db.Exec(`
		DELETE FROM verification_requests
		WHERE id = $1 AND username = $2 AND status = $3;`,
		id, username, constants.VerificationStatusPending)
	if err != nil {
		uh.log.Errorf("cancel verification request %s for %s: exec: %v", id, username, err)
		return err
	}
	if affected, aErr := res.RowsAffected(); aErr == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListVerificationRequests returns a page of the review queue, newest
// first, plus the total count for the same filter. Empty status = all.
func (uh *uDBHandler) ListVerificationRequests(statusFilter string, limit, offset int) ([]models.VerificationRequest, int, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	where := ""
	args := []interface{}{}
	if strings.TrimSpace(statusFilter) != "" {
		where = " WHERE status = $1"
		args = append(args, statusFilter)
	}

	var total int
	if err := uh.db.QueryRow(`SELECT COUNT(*) FROM verification_requests`+where+`;`, args...).Scan(&total); err != nil {
		uh.log.Errorf("list verification requests: count: %v", err)
		return nil, 0, err
	}

	args = append(args, limit, offset)
	query := "SELECT " + verificationColumns + `
		FROM verification_requests` + where + `
		ORDER BY created_at DESC
		LIMIT $` + fmt.Sprint(len(args)-1) + ` OFFSET $` + fmt.Sprint(len(args)) + `;`
	rows, err := uh.db.Query(query, args...)
	if err != nil {
		uh.log.Errorf("list verification requests: query: %v", err)
		return nil, 0, err
	}
	defer func() {
		if cErr := rows.Close(); cErr != nil {
			uh.log.Errorf("list verification requests: close rows: %v", cErr)
		}
	}()

	out := make([]models.VerificationRequest, 0, limit)
	for rows.Next() {
		v, sErr := scanVerificationRequest(rows)
		if sErr != nil {
			uh.log.Errorf("list verification requests: scan: %v", sErr)
			return nil, 0, sErr
		}
		out = append(out, *v)
	}
	if err = rows.Err(); err != nil {
		uh.log.Errorf("list verification requests: rows err: %v", err)
		return nil, 0, err
	}
	return out, total, nil
}

// ReviewVerificationRequest transitions a pending/under_review request to
// approved or rejected inside one transaction; approval also flips the
// account's verified badge. Returns sql.ErrNoRows when the request does
// not exist or is not in a reviewable state.
func (uh *uDBHandler) ReviewVerificationRequest(id, reviewer string, approve bool, reason string) (*models.VerificationRequest, error) {
	tx, err := uh.db.Begin()
	if err != nil {
		uh.log.Errorf("review verification request %s: begin tx: %v", id, err)
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	nextStatus := constants.VerificationStatusRejected
	if approve {
		nextStatus = constants.VerificationStatusApproved
	}

	row := tx.QueryRow(`
		UPDATE verification_requests SET
			status = $2,
			reviewer_username = $3,
			rejection_reason = NULLIF($4, ''),
			reviewed_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status IN ($5, $6)
		RETURNING `+verificationColumns+`;`,
		id, nextStatus, reviewer, reason, constants.VerificationStatusPending, constants.VerificationStatusUnderReview)

	updated, err := scanVerificationRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		uh.log.Errorf("review verification request %s: update: %v", id, err)
		return nil, err
	}

	if approve {
		res, err := tx.Exec(`UPDATE user_account SET is_verified = TRUE WHERE username = $1;`,
			updated.Username)
		if err != nil {
			uh.log.Errorf("review verification request %s: set badge for %s: %v", id, updated.Username, err)
			return nil, err
		}
		if affected, aErr := res.RowsAffected(); aErr == nil && affected == 0 {
			uh.log.Warnf("review verification request %s: approved for unknown user %s", id, updated.Username)
		}
	}

	if err = tx.Commit(); err != nil {
		uh.log.Errorf("review verification request %s: commit: %v", id, err)
		return nil, err
	}
	return updated, nil
}

// VerificationAssetsExist reports whether every checksum was previously
// registered by an upload (storage_assets row present). Prevents orphan
// pointers from clients skipping the upload step.
func (uh *uDBHandler) VerificationAssetsExist(checksums []string) (bool, error) {
	if len(checksums) == 0 {
		return true, nil
	}
	var found int
	if err := uh.db.QueryRow(`
		SELECT COUNT(*) FROM storage_assets WHERE checksum = ANY($1);`,
		checksums).Scan(&found); err != nil {
		uh.log.Errorf("verify assets exist: query: %v", err)
		return false, err
	}
	return found == len(checksums), nil
}
