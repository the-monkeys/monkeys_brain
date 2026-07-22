package database

import (
	"database/sql"
	"errors"

	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
)

// businessCardColumns is the canonical projection used by every read so the
// scan order stays in lockstep with scanBusinessCard. card_state is cast to
// text because the model carries the JSON document as a raw string.
const businessCardColumns = `id, account_id, name, template_id, theme_id, card_state::text,
	is_default, avatar_asset_checksum, logo_asset_checksum, created_at, updated_at`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanBusinessCard(row scanner) (*models.BusinessCard, error) {
	var c models.BusinessCard
	if err := row.Scan(
		&c.Id,
		&c.AccountId,
		&c.Name,
		&c.TemplateId,
		&c.ThemeId,
		&c.CardState,
		&c.IsDefault,
		&c.AvatarAssetChecksum,
		&c.LogoAssetChecksum,
		&c.CreatedAt,
		&c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

// CreateBusinessCard inserts a new card for the owner. When the card is marked
// default, existing defaults are cleared in the same transaction to satisfy the
// uq_business_cards_default partial unique index.
func (uh *uDBHandler) CreateBusinessCard(card *models.BusinessCard) (*models.BusinessCard, error) {
	tx, err := uh.db.Begin()
	if err != nil {
		uh.log.Errorf("create business card: begin tx: %v", err)
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if card.IsDefault {
		if _, err = tx.Exec(`
			UPDATE business_cards
			SET is_default = FALSE, updated_at = CURRENT_TIMESTAMP
			WHERE account_id = $1 AND is_default AND deleted_at IS NULL;`,
			card.AccountId); err != nil {
			uh.log.Errorf("create business card: clear defaults: %v", err)
			return nil, err
		}
	}

	row := tx.QueryRow(`
		INSERT INTO business_cards
			(account_id, name, template_id, theme_id, card_state,
			 is_default, avatar_asset_checksum, logo_asset_checksum)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
		RETURNING `+businessCardColumns+`;`,
		card.AccountId, card.Name, card.TemplateId, card.ThemeId, card.CardState,
		card.IsDefault, card.AvatarAssetChecksum, card.LogoAssetChecksum)

	created, err := scanBusinessCard(row)
	if err != nil {
		uh.log.Errorf("create business card: insert: %v", err)
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		uh.log.Errorf("create business card: commit: %v", err)
		return nil, err
	}
	return created, nil
}

// GetBusinessCard returns a single card scoped to its owner. Returns
// sql.ErrNoRows when the card is missing, soft-deleted, or owned by someone
// else (prevents IDOR — a caller can never read another user's card).
func (uh *uDBHandler) GetBusinessCard(accountId, id string) (*models.BusinessCard, error) {
	row := uh.db.QueryRow(`
		SELECT `+businessCardColumns+`
		FROM business_cards
		WHERE id = $1 AND account_id = $2 AND deleted_at IS NULL;`,
		id, accountId)

	card, err := scanBusinessCard(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		uh.log.Errorf("get business card: scan: %v", err)
		return nil, err
	}
	return card, nil
}

// ListBusinessCards returns the owner's active cards, newest first.
func (uh *uDBHandler) ListBusinessCards(accountId string, limit, offset int) ([]models.BusinessCard, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := uh.db.Query(`
		SELECT `+businessCardColumns+`
		FROM business_cards
		WHERE account_id = $1 AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3;`,
		accountId, limit, offset)
	if err != nil {
		uh.log.Errorf("list business cards: query: %v", err)
		return nil, err
	}
	defer func() {
		if cErr := rows.Close(); cErr != nil {
			uh.log.Errorf("list business cards: close rows: %v", cErr)
		}
	}()

	cards := make([]models.BusinessCard, 0, limit)
	for rows.Next() {
		card, err := scanBusinessCard(rows)
		if err != nil {
			uh.log.Errorf("list business cards: scan: %v", err)
			return nil, err
		}
		cards = append(cards, *card)
	}
	if err = rows.Err(); err != nil {
		uh.log.Errorf("list business cards: rows err: %v", err)
		return nil, err
	}
	return cards, nil
}

// UpdateBusinessCard performs an owner-scoped full update. Returns
// sql.ErrNoRows when the card does not exist / is not owned by the caller.
func (uh *uDBHandler) UpdateBusinessCard(card *models.BusinessCard) (*models.BusinessCard, error) {
	tx, err := uh.db.Begin()
	if err != nil {
		uh.log.Errorf("update business card: begin tx: %v", err)
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if card.IsDefault {
		if _, err = tx.Exec(`
			UPDATE business_cards
			SET is_default = FALSE, updated_at = CURRENT_TIMESTAMP
			WHERE account_id = $1 AND id <> $2 AND is_default AND deleted_at IS NULL;`,
			card.AccountId, card.Id); err != nil {
			uh.log.Errorf("update business card: clear defaults: %v", err)
			return nil, err
		}
	}

	row := tx.QueryRow(`
		UPDATE business_cards SET
			name = $3,
			template_id = $4,
			theme_id = $5,
			card_state = $6::jsonb,
			is_default = $7,
			avatar_asset_checksum = $8,
			logo_asset_checksum = $9,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND account_id = $1 AND deleted_at IS NULL
		RETURNING `+businessCardColumns+`;`,
		card.AccountId, card.Id, card.Name, card.TemplateId, card.ThemeId, card.CardState,
		card.IsDefault, card.AvatarAssetChecksum, card.LogoAssetChecksum)

	updated, err := scanBusinessCard(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		uh.log.Errorf("update business card: exec: %v", err)
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		uh.log.Errorf("update business card: commit: %v", err)
		return nil, err
	}
	return updated, nil
}

// DeleteBusinessCard soft-deletes an owner's card. Returns sql.ErrNoRows when
// nothing was deleted (missing / already deleted / not owned).
func (uh *uDBHandler) DeleteBusinessCard(accountId, id string) error {
	res, err := uh.db.Exec(`
		UPDATE business_cards
		SET deleted_at = CURRENT_TIMESTAMP, is_default = FALSE, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND account_id = $2 AND deleted_at IS NULL;`,
		id, accountId)
	if err != nil {
		uh.log.Errorf("delete business card: exec: %v", err)
		return err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		uh.log.Errorf("delete business card: rows affected: %v", err)
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
