package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/aitokenledger"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AITokenLedgerRepository struct{ pool *pgxpool.Pool }

func NewAITokenLedgerRepository(pool *pgxpool.Pool) *AITokenLedgerRepository {
	return &AITokenLedgerRepository{pool: pool}
}

func (r *AITokenLedgerRepository) Balance(ctx context.Context, accountID ids.AccountID, now time.Time) (aitokens.Balance, error) {
	grants, err := loadAITokenGrants(ctx, r.pool, accountID, false)
	if err != nil {
		return aitokens.Balance{}, err
	}
	return aitokens.Summarize(grants, now.UTC())
}

func (r *AITokenLedgerRepository) Reserve(ctx context.Context, requested aitokens.Reservation, now time.Time) (aitokens.Reservation, aitokens.Balance, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAITokenAccount(ctx, tx, requested.AccountID); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if existing, found, err := loadAITokenReservation(ctx, tx, requested.AccountID, requested.RequestID, true); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	} else if found {
		// Request identity freezes the original rate snapshot. A later Catalog
		// publication must not make an exact retry conflict or reprice it.
		if existing.Rate.Complexity != requested.Rate.Complexity {
			return aitokens.Reservation{}, aitokens.Balance{}, aitokens.ErrInvalidReservation
		}
		balance, err := transactionAITokenBalance(ctx, tx, requested.AccountID, now)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return existing, balance, err
	}
	grants, err := loadAITokenGrants(ctx, tx, requested.AccountID, true)
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	grants, reservation, err := aitokens.Reserve(grants, requested, now.UTC())
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if err := updateAITokenGrants(ctx, tx, grants); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	rate, err := json.Marshal(reservation.Rate)
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ai_token_reservations
		(id,account_id,request_id,rate_code,rate_version,complexity,rate_snapshot,maximum,settled,state,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0,'active',$9)`, reservation.ID, reservation.AccountID, reservation.RequestID, reservation.Rate.Code, reservation.Rate.Version, reservation.Rate.Complexity, rate, reservation.Maximum, reservation.CreatedAt); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	for index, allocation := range reservation.Allocations {
		if _, err := tx.Exec(ctx, `INSERT INTO ai_token_reservation_allocations (reservation_id,grant_id,ordinal,amount) VALUES ($1,$2,$3,$4)`, reservation.ID, allocation.GrantID, index+1, allocation.Amount); err != nil {
			return aitokens.Reservation{}, aitokens.Balance{}, err
		}
	}
	if err := insertAITokenEntry(ctx, tx, string(reservation.ID), reservation.AccountID, nil, &reservation.ID, "reserve:"+reservation.RequestID, "reserved", reservation.Maximum, now); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	balance, err := aitokens.Summarize(grants, now.UTC())
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	return reservation, balance, nil
}

func (r *AITokenLedgerRepository) Close(ctx context.Context, accountID ids.AccountID, requestID string, usage aitokenledger.Usage, now time.Time) (aitokens.Reservation, aitokens.Balance, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAITokenAccount(ctx, tx, accountID); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	reservation, found, err := loadAITokenReservation(ctx, tx, accountID, requestID, true)
	if err != nil || !found {
		if err == nil {
			err = aitokens.ErrInvalidReservation
		}
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if reservation.State != aitokens.ReservationActive {
		balance, err := transactionAITokenBalance(ctx, tx, accountID, now)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return reservation, balance, err
	}
	var settled int64
	if usage.ProviderStarted {
		settled, err = aitokens.Charge(reservation.Rate, usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.ToolInvocations)
		if err != nil {
			return aitokens.Reservation{}, aitokens.Balance{}, err
		}
	}
	grants, err := loadAITokenGrants(ctx, tx, accountID, true)
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	grants, reservation, err = aitokens.Close(grants, reservation, settled, now.UTC())
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if err := updateAITokenGrants(ctx, tx, grants); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_token_reservations SET settled=$2,state=$3,closed_at=$4 WHERE id=$1`, reservation.ID, reservation.Settled, reservation.State, reservation.ClosedAt); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if settled > 0 {
		if err := insertAITokenEntry(ctx, tx, string(reservation.ID), accountID, nil, &reservation.ID, "settle:"+requestID, "settled", settled, now); err != nil {
			return aitokens.Reservation{}, aitokens.Balance{}, err
		}
	}
	if released := reservation.Maximum - settled; released > 0 {
		if err := insertAITokenEntry(ctx, tx, string(reservation.ID), accountID, nil, &reservation.ID, "release:"+requestID, "released", released, now); err != nil {
			return aitokens.Reservation{}, aitokens.Balance{}, err
		}
	}
	balance, err := aitokens.Summarize(grants, now.UTC())
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	return reservation, balance, nil
}

func (r *AITokenLedgerRepository) Issue(ctx context.Context, requested aitokens.Grant, expireIncluded bool) (aitokens.Grant, aitokens.Balance, error) {
	if requested.Origin == aitokens.OriginPromotion {
		return aitokens.Grant{}, aitokens.Balance{}, aitokens.ErrInvalidGrant
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAITokenAccount(ctx, tx, requested.AccountID); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	var existingID ids.AITokenGrantID
	err = tx.QueryRow(ctx, `SELECT id FROM ai_token_grants WHERE account_id=$1 AND origin=$2 AND definition_code=$3 AND source_reference=$4`, requested.AccountID, requested.Origin, requested.DefinitionCode, requested.SourceReference).Scan(&existingID)
	if err == nil {
		grant, err := loadAITokenGrant(ctx, tx, existingID)
		if err != nil || grant.Quantity != requested.Quantity || grant.CatalogVersion != requested.CatalogVersion {
			if err == nil {
				err = aitokens.ErrInvalidGrant
			}
			return aitokens.Grant{}, aitokens.Balance{}, err
		}
		balance, err := transactionAITokenBalance(ctx, tx, requested.AccountID, requested.CreatedAt)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return grant, balance, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if expireIncluded {
		rows, err := tx.Query(ctx, `
			WITH expiring AS (
				SELECT id,available FROM ai_token_grants
				WHERE account_id=$1 AND origin='included' AND state IN ('active','frozen')
				FOR UPDATE
			)
			UPDATE ai_token_grants AS grants SET available=0,state='expired'
			FROM expiring WHERE grants.id=expiring.id
			RETURNING grants.id,expiring.available`, requested.AccountID)
		if err != nil {
			return aitokens.Grant{}, aitokens.Balance{}, err
		}
		for rows.Next() {
			var grantID ids.AITokenGrantID
			var unused int64
			if err := rows.Scan(&grantID, &unused); err != nil {
				rows.Close()
				return aitokens.Grant{}, aitokens.Balance{}, err
			}
			if unused > 0 {
				if err := insertAITokenEntry(ctx, tx, string(requested.ID), requested.AccountID, &grantID, nil, "expire:"+string(grantID)+":"+requested.SourceReference, "expired", unused, requested.CreatedAt); err != nil {
					rows.Close()
					return aitokens.Grant{}, aitokens.Balance{}, err
				}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return aitokens.Grant{}, aitokens.Balance{}, err
		}
		rows.Close()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ai_token_grants
		(id,account_id,origin,definition_code,catalog_version,source_reference,quantity,available,reserved,consumed,state,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, requested.ID, requested.AccountID, requested.Origin, requested.DefinitionCode, requested.CatalogVersion, requested.SourceReference, requested.Quantity, requested.Available, requested.Reserved, requested.Consumed, requested.State, requested.ExpiresAt, requested.CreatedAt); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if err := insertAITokenEntry(ctx, tx, string(requested.ID), requested.AccountID, &requested.ID, nil, "issue:"+string(requested.Origin)+":"+requested.DefinitionCode+":"+requested.SourceReference, "issued", requested.Quantity, requested.CreatedAt); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	grants, err := loadAITokenGrants(ctx, tx, requested.AccountID, false)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	balance, err := aitokens.Summarize(grants, requested.CreatedAt)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	return requested, balance, nil
}

func (r *AITokenLedgerRepository) Promotion(ctx context.Context, accountID ids.AccountID, sourceReference string, now time.Time) (aitokens.Grant, aitokens.Balance, bool, error) {
	var grantID ids.AITokenGrantID
	err := r.pool.QueryRow(ctx, `SELECT id FROM ai_token_grants WHERE account_id=$1 AND origin='promotion' AND source_reference=$2`, accountID, sourceReference).Scan(&grantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return aitokens.Grant{}, aitokens.Balance{}, false, nil
	}
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, false, err
	}
	grant, err := loadAITokenGrant(ctx, r.pool, grantID)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, false, err
	}
	balance, err := r.Balance(ctx, accountID, now.UTC())
	return grant, balance, true, err
}

func (r *AITokenLedgerRepository) RedeemPromotion(ctx context.Context, requested aitokens.Grant, campaignVersion uint64, perAccountLimit, issuanceCap int64) (aitokens.Grant, aitokens.Balance, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("ai-token-promotion:%s:%d", requested.DefinitionCode, campaignVersion)); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if err := lockAITokenAccount(ctx, tx, requested.AccountID); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	var existingID ids.AITokenGrantID
	err = tx.QueryRow(ctx, `SELECT id FROM ai_token_grants WHERE account_id=$1 AND origin='promotion' AND source_reference=$2 FOR UPDATE`, requested.AccountID, requested.SourceReference).Scan(&existingID)
	if err == nil {
		grant, loadErr := loadAITokenGrant(ctx, tx, existingID)
		if loadErr != nil {
			return aitokens.Grant{}, aitokens.Balance{}, loadErr
		}
		if grant.DefinitionCode != requested.DefinitionCode {
			return aitokens.Grant{}, aitokens.Balance{}, aitokenledger.ErrPromotionConflict
		}
		balance, loadErr := transactionAITokenBalance(ctx, tx, requested.AccountID, requested.CreatedAt)
		if loadErr == nil {
			loadErr = tx.Commit(ctx)
		}
		return grant, balance, loadErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	var accountRedemptions, totalRedemptions int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai_token_promotion_issuances WHERE account_id=$1 AND campaign_code=$2 AND campaign_version=$3`, requested.AccountID, requested.DefinitionCode, campaignVersion).Scan(&accountRedemptions); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if accountRedemptions >= perAccountLimit {
		return aitokens.Grant{}, aitokens.Balance{}, aitokenledger.ErrPromotionAccountLimit
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai_token_promotion_issuances WHERE campaign_code=$1 AND campaign_version=$2`, requested.DefinitionCode, campaignVersion).Scan(&totalRedemptions); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if totalRedemptions >= issuanceCap {
		return aitokens.Grant{}, aitokens.Balance{}, aitokenledger.ErrPromotionIssuanceLimit
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ai_token_grants
		(id,account_id,origin,definition_code,catalog_version,source_reference,quantity,available,reserved,consumed,state,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, requested.ID, requested.AccountID, requested.Origin, requested.DefinitionCode, requested.CatalogVersion, requested.SourceReference, requested.Quantity, requested.Available, requested.Reserved, requested.Consumed, requested.State, requested.ExpiresAt, requested.CreatedAt); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ai_token_promotion_issuances
		(grant_id,account_id,campaign_code,campaign_version,catalog_version,source_reference,issued_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, requested.ID, requested.AccountID, requested.DefinitionCode, campaignVersion, requested.CatalogVersion, requested.SourceReference, requested.CreatedAt); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if err := insertAITokenEntry(ctx, tx, string(requested.ID), requested.AccountID, &requested.ID, nil, "issue:promotion:"+requested.DefinitionCode+":"+requested.SourceReference, "issued", requested.Quantity, requested.CreatedAt); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	balance, err := transactionAITokenBalance(ctx, tx, requested.AccountID, requested.CreatedAt)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	return requested, balance, nil
}

func (r *AITokenLedgerRepository) ReverseUnused(ctx context.Context, accountID ids.AccountID, origin aitokens.GrantOrigin, sourceReference, adversityReference string, now time.Time) (aitokens.Grant, aitokens.Balance, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAITokenAccount(ctx, tx, accountID); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	var grantID ids.AITokenGrantID
	err = tx.QueryRow(ctx, `SELECT id FROM ai_token_grants WHERE account_id=$1 AND origin=$2 AND source_reference=$3 FOR UPDATE`, accountID, origin, sourceReference).Scan(&grantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return aitokens.Grant{}, aitokens.Balance{}, aitokens.ErrInvalidGrant
	}
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	grant, err := loadAITokenGrant(ctx, tx, grantID)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if grant.State != aitokens.GrantReversed {
		unused := grant.Available
		grant, err = aitokens.ReverseUnused(grant, aitokens.GrantReversed)
		if err != nil {
			return aitokens.Grant{}, aitokens.Balance{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE ai_token_grants SET available=0,state='reversed' WHERE id=$1`, grant.ID); err != nil {
			return aitokens.Grant{}, aitokens.Balance{}, err
		}
		if unused > 0 {
			if err := insertAITokenEntry(ctx, tx, string(grant.ID), accountID, &grant.ID, nil, "reverse:"+adversityReference, "reversed", unused, now.UTC()); err != nil {
				return aitokens.Grant{}, aitokens.Balance{}, err
			}
		}
	}
	balance, err := transactionAITokenBalance(ctx, tx, accountID, now.UTC())
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	return grant, balance, nil
}

type aiTokenQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func lockAITokenAccount(ctx context.Context, tx pgx.Tx, accountID ids.AccountID) error {
	var entitlementVersion uint64
	return tx.QueryRow(ctx, `SELECT spyglass_lock_account_entitlement_version($1)`, accountID).Scan(&entitlementVersion)
}

func loadAITokenGrants(ctx context.Context, queryer aiTokenQueryer, accountID ids.AccountID, lock bool) ([]aitokens.Grant, error) {
	query := `SELECT id,account_id,origin,definition_code,catalog_version,source_reference,quantity,available,reserved,consumed,state,expires_at,created_at FROM ai_token_grants WHERE account_id=$1 ORDER BY created_at,id`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := queryer.Query(ctx, query, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []aitokens.Grant{}
	for rows.Next() {
		grant, err := scanAITokenGrant(rows)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

type aiTokenScanner interface{ Scan(...any) error }

func scanAITokenGrant(row aiTokenScanner) (aitokens.Grant, error) {
	var grant aitokens.Grant
	if err := row.Scan(&grant.ID, &grant.AccountID, &grant.Origin, &grant.DefinitionCode, &grant.CatalogVersion, &grant.SourceReference, &grant.Quantity, &grant.Available, &grant.Reserved, &grant.Consumed, &grant.State, &grant.ExpiresAt, &grant.CreatedAt); err != nil {
		return aitokens.Grant{}, err
	}
	return aitokens.RestoreGrant(grant)
}

func loadAITokenGrant(ctx context.Context, queryer aiTokenQueryer, grantID ids.AITokenGrantID) (aitokens.Grant, error) {
	return scanAITokenGrant(queryer.QueryRow(ctx, `SELECT id,account_id,origin,definition_code,catalog_version,source_reference,quantity,available,reserved,consumed,state,expires_at,created_at FROM ai_token_grants WHERE id=$1`, grantID))
}

func loadAITokenReservation(ctx context.Context, queryer aiTokenQueryer, accountID ids.AccountID, requestID string, lock bool) (aitokens.Reservation, bool, error) {
	query := `SELECT id,account_id,request_id,rate_snapshot,maximum,settled,state,created_at,closed_at FROM ai_token_reservations WHERE account_id=$1 AND request_id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var reservation aitokens.Reservation
	var raw []byte
	err := queryer.QueryRow(ctx, query, accountID, requestID).Scan(&reservation.ID, &reservation.AccountID, &reservation.RequestID, &raw, &reservation.Maximum, &reservation.Settled, &reservation.State, &reservation.CreatedAt, &reservation.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return aitokens.Reservation{}, false, nil
	}
	if err != nil {
		return aitokens.Reservation{}, false, err
	}
	if err := json.Unmarshal(raw, &reservation.Rate); err != nil {
		return aitokens.Reservation{}, false, err
	}
	rows, err := queryer.Query(ctx, `SELECT grant_id,amount FROM ai_token_reservation_allocations WHERE reservation_id=$1 ORDER BY ordinal`, reservation.ID)
	if err != nil {
		return aitokens.Reservation{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var allocation aitokens.Allocation
		if err := rows.Scan(&allocation.GrantID, &allocation.Amount); err != nil {
			return aitokens.Reservation{}, false, err
		}
		reservation.Allocations = append(reservation.Allocations, allocation)
	}
	return reservation, true, rows.Err()
}

func updateAITokenGrants(ctx context.Context, tx pgx.Tx, grants []aitokens.Grant) error {
	for _, grant := range grants {
		if _, err := tx.Exec(ctx, `UPDATE ai_token_grants SET available=$2,reserved=$3,consumed=$4,state=$5 WHERE id=$1`, grant.ID, grant.Available, grant.Reserved, grant.Consumed, grant.State); err != nil {
			return err
		}
	}
	return nil
}

func transactionAITokenBalance(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, now time.Time) (aitokens.Balance, error) {
	grants, err := loadAITokenGrants(ctx, tx, accountID, false)
	if err != nil {
		return aitokens.Balance{}, err
	}
	return aitokens.Summarize(grants, now.UTC())
}

func insertAITokenEntry(ctx context.Context, tx pgx.Tx, namespace string, accountID ids.AccountID, grantID *ids.AITokenGrantID, reservationID *ids.AITokenReservationID, eventKey, kind string, amount int64, now time.Time) error {
	entryID, err := ids.Derive(namespace, eventKey)
	if err != nil {
		return fmt.Errorf("derive AI Token ledger entry: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_token_ledger_entries (id,account_id,grant_id,reservation_id,event_key,kind,amount,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (account_id,event_key) DO NOTHING`, entryID, accountID, grantID, reservationID, eventKey, kind, amount, now.UTC())
	return err
}

var _ aitokenledger.Store = (*AITokenLedgerRepository)(nil)
