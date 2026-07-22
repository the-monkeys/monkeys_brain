package database

import (
	"database/sql"
	"errors"

	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
)

// userAddressColumns is the canonical projection used by every read so the scan
// order stays in lockstep with scanUserAddress.
const userAddressColumns = `id, account_id, label, line1, line2, city, state,
	postal_code, country, is_default, created_at, updated_at`

func scanUserAddress(row scanner) (*models.UserAddress, error) {
	var a models.UserAddress
	if err := row.Scan(
		&a.Id,
		&a.AccountId,
		&a.Label,
		&a.Line1,
		&a.Line2,
		&a.City,
		&a.State,
		&a.PostalCode,
		&a.Country,
		&a.IsDefault,
		&a.CreatedAt,
		&a.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &a, nil
}

// CreateUserAddress inserts a new private address for the owner. When marked
// default, existing defaults are cleared in the same transaction to satisfy the
// uq_user_addresses_default partial unique index.
func (uh *uDBHandler) CreateUserAddress(addr *models.UserAddress) (*models.UserAddress, error) {
	tx, err := uh.db.Begin()
	if err != nil {
		uh.log.Errorf("create user address: begin tx: %v", err)
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if addr.IsDefault {
		if _, err = tx.Exec(`
			UPDATE user_addresses
			SET is_default = FALSE, updated_at = CURRENT_TIMESTAMP
			WHERE account_id = $1 AND is_default AND deleted_at IS NULL;`,
			addr.AccountId); err != nil {
			uh.log.Errorf("create user address: clear defaults: %v", err)
			return nil, err
		}
	}

	row := tx.QueryRow(`
		INSERT INTO user_addresses
			(account_id, label, line1, line2, city, state, postal_code, country, is_default)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+userAddressColumns+`;`,
		addr.AccountId, addr.Label, addr.Line1, addr.Line2, addr.City, addr.State,
		addr.PostalCode, addr.Country, addr.IsDefault)

	created, err := scanUserAddress(row)
	if err != nil {
		uh.log.Errorf("create user address: insert: %v", err)
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		uh.log.Errorf("create user address: commit: %v", err)
		return nil, err
	}
	return created, nil
}

// GetUserAddress returns a single address scoped to its owner. Returns
// sql.ErrNoRows when missing, soft-deleted, or owned by someone else (prevents
// IDOR — a caller can never read another user's address).
func (uh *uDBHandler) GetUserAddress(accountId, id string) (*models.UserAddress, error) {
	row := uh.db.QueryRow(`
		SELECT `+userAddressColumns+`
		FROM user_addresses
		WHERE id = $1 AND account_id = $2 AND deleted_at IS NULL;`,
		id, accountId)

	addr, err := scanUserAddress(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		uh.log.Errorf("get user address: scan: %v", err)
		return nil, err
	}
	return addr, nil
}

// ListUserAddresses returns the owner's active addresses, default first then
// newest. The address book is small, so no pagination is applied.
func (uh *uDBHandler) ListUserAddresses(accountId string) ([]models.UserAddress, error) {
	rows, err := uh.db.Query(`
		SELECT `+userAddressColumns+`
		FROM user_addresses
		WHERE account_id = $1 AND deleted_at IS NULL
		ORDER BY is_default DESC, updated_at DESC;`,
		accountId)
	if err != nil {
		uh.log.Errorf("list user addresses: query: %v", err)
		return nil, err
	}
	defer func() {
		if cErr := rows.Close(); cErr != nil {
			uh.log.Errorf("list user addresses: close rows: %v", cErr)
		}
	}()

	addresses := make([]models.UserAddress, 0, 8)
	for rows.Next() {
		addr, err := scanUserAddress(rows)
		if err != nil {
			uh.log.Errorf("list user addresses: scan: %v", err)
			return nil, err
		}
		addresses = append(addresses, *addr)
	}
	if err = rows.Err(); err != nil {
		uh.log.Errorf("list user addresses: rows err: %v", err)
		return nil, err
	}
	return addresses, nil
}

// UpdateUserAddress performs an owner-scoped full update. Returns sql.ErrNoRows
// when the address does not exist / is not owned by the caller.
func (uh *uDBHandler) UpdateUserAddress(addr *models.UserAddress) (*models.UserAddress, error) {
	tx, err := uh.db.Begin()
	if err != nil {
		uh.log.Errorf("update user address: begin tx: %v", err)
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if addr.IsDefault {
		if _, err = tx.Exec(`
			UPDATE user_addresses
			SET is_default = FALSE, updated_at = CURRENT_TIMESTAMP
			WHERE account_id = $1 AND id <> $2 AND is_default AND deleted_at IS NULL;`,
			addr.AccountId, addr.Id); err != nil {
			uh.log.Errorf("update user address: clear defaults: %v", err)
			return nil, err
		}
	}

	row := tx.QueryRow(`
		UPDATE user_addresses SET
			label = $3,
			line1 = $4,
			line2 = $5,
			city = $6,
			state = $7,
			postal_code = $8,
			country = $9,
			is_default = $10,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND account_id = $1 AND deleted_at IS NULL
		RETURNING `+userAddressColumns+`;`,
		addr.AccountId, addr.Id, addr.Label, addr.Line1, addr.Line2, addr.City,
		addr.State, addr.PostalCode, addr.Country, addr.IsDefault)

	updated, err := scanUserAddress(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		uh.log.Errorf("update user address: exec: %v", err)
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		uh.log.Errorf("update user address: commit: %v", err)
		return nil, err
	}
	return updated, nil
}

// DeleteUserAddress soft-deletes an owner's address. Returns sql.ErrNoRows when
// nothing was deleted (missing / already deleted / not owned).
func (uh *uDBHandler) DeleteUserAddress(accountId, id string) error {
	res, err := uh.db.Exec(`
		UPDATE user_addresses
		SET deleted_at = CURRENT_TIMESTAMP, is_default = FALSE, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND account_id = $2 AND deleted_at IS NULL;`,
		id, accountId)
	if err != nil {
		uh.log.Errorf("delete user address: exec: %v", err)
		return err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		uh.log.Errorf("delete user address: rows affected: %v", err)
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
