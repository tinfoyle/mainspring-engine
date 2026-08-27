package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
)

func (r *CommercialAccessRepository) ProjectOneTimePurchase(ctx context.Context, evidence commercialaccess.PurchaseEvidence) (commercialaccess.PurchaseProjection, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commercialaccess.PurchaseProjection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var snapshot commercialaccess.PurchaseSnapshot
	var state, sessionID, currency string
	err = tx.QueryRow(ctx, `
		SELECT account_id,purchase_kind,offer_code,item_version,catalog_version,currency,amount_minor,COALESCE(quantity,0),state,COALESCE(provider_session_id,'')
		FROM billing_checkout_attempts
		WHERE request_id=$1 AND account_id=$2 AND provider='stripe'
		FOR UPDATE`, evidence.RequestID, evidence.AccountID).Scan(&snapshot.AccountID, &snapshot.Kind, &snapshot.ItemCode, &snapshot.ItemVersion, &snapshot.CatalogVersion, &currency, &snapshot.AmountMinor, &snapshot.Quantity, &state, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return commercialaccess.PurchaseProjection{}, commercialaccess.ErrInvalidPurchaseEvidence
	}
	if err != nil {
		return commercialaccess.PurchaseProjection{}, err
	}
	snapshot.Currency = currency
	if (snapshot.Kind != billing.PurchaseAITokenTopUp && snapshot.Kind != billing.PurchaseCommissioning) || snapshot.Kind != evidence.Kind || snapshot.ItemCode != evidence.ItemCode || snapshot.ItemVersion != evidence.ItemVersion || snapshot.CatalogVersion != evidence.CatalogVersion || snapshot.Currency != evidence.Currency || snapshot.AmountMinor != evidence.AmountSubtotal || sessionID != evidence.ProviderSessionID || (state != "active" && state != "completed") {
		return commercialaccess.PurchaseProjection{}, commercialaccess.ErrInvalidPurchaseEvidence
	}
	command, err := tx.Exec(ctx, `UPDATE billing_checkout_attempts SET state='completed',provider_payment_intent_id=$2,updated_at=statement_timestamp() WHERE request_id=$1 AND (provider_payment_intent_id IS NULL OR provider_payment_intent_id=$2)`, evidence.RequestID, evidence.ProviderPaymentIntentID)
	if err != nil {
		return commercialaccess.PurchaseProjection{}, err
	}
	if command.RowsAffected() != 1 {
		return commercialaccess.PurchaseProjection{}, commercialaccess.ErrInvalidPurchaseEvidence
	}
	if snapshot.Kind == billing.PurchaseCommissioning {
		command, err = tx.Exec(ctx, `
			INSERT INTO account_commissioning_purchases
			(account_id,checkout_request_id,item_code,item_version,catalog_version,amount_minor,currency,provider_reference,purchased_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,statement_timestamp())
			ON CONFLICT (account_id) DO NOTHING`, snapshot.AccountID, evidence.RequestID, snapshot.ItemCode, snapshot.ItemVersion, snapshot.CatalogVersion, snapshot.AmountMinor, snapshot.Currency, evidence.ProviderPaymentIntentID)
		if err != nil {
			return commercialaccess.PurchaseProjection{}, err
		}
		if command.RowsAffected() != 1 {
			var existingRequest string
			if scanErr := tx.QueryRow(ctx, `SELECT checkout_request_id::text FROM account_commissioning_purchases WHERE account_id=$1`, snapshot.AccountID).Scan(&existingRequest); scanErr != nil {
				return commercialaccess.PurchaseProjection{}, scanErr
			}
			if existingRequest != evidence.RequestID {
				return commercialaccess.PurchaseProjection{}, commercialaccess.ErrCommissioningOwned
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return commercialaccess.PurchaseProjection{}, err
	}
	return commercialaccess.PurchaseProjection{Snapshot: snapshot, PaymentIntentID: evidence.ProviderPaymentIntentID}, nil
}

func (r *CommercialAccessRepository) ProjectPurchaseRefund(ctx context.Context, evidence commercialaccess.PurchaseRefundEvidence) (commercialaccess.PurchaseProjection, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commercialaccess.PurchaseProjection{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var snapshot commercialaccess.PurchaseSnapshot
	var state string
	err = tx.QueryRow(ctx, `
		SELECT account_id,purchase_kind,offer_code,item_version,catalog_version,currency,amount_minor,COALESCE(quantity,0),state
		FROM billing_checkout_attempts
		WHERE provider='stripe' AND provider_payment_intent_id=$1 AND purchase_kind='ai_token_top_up'
		FOR UPDATE`, evidence.PaymentIntentID).Scan(&snapshot.AccountID, &snapshot.Kind, &snapshot.ItemCode, &snapshot.ItemVersion, &snapshot.CatalogVersion, &snapshot.Currency, &snapshot.AmountMinor, &snapshot.Quantity, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return commercialaccess.PurchaseProjection{}, false, nil
	}
	if err != nil {
		return commercialaccess.PurchaseProjection{}, false, err
	}
	if state != "completed" && state != "refunded" {
		return commercialaccess.PurchaseProjection{}, false, commercialaccess.ErrInvalidPurchaseEvidence
	}
	if !evidence.Refunded || evidence.AmountRefunded != evidence.Amount {
		return commercialaccess.PurchaseProjection{}, false, commercialaccess.ErrInvalidPurchaseEvidence
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_checkout_attempts SET state='refunded',updated_at=statement_timestamp() WHERE provider='stripe' AND provider_payment_intent_id=$1`, evidence.PaymentIntentID); err != nil {
		return commercialaccess.PurchaseProjection{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return commercialaccess.PurchaseProjection{}, false, err
	}
	return commercialaccess.PurchaseProjection{Snapshot: snapshot, PaymentIntentID: evidence.PaymentIntentID}, true, nil
}

func (r *CommercialAccessRepository) ProjectSubscriptionCommissioning(ctx context.Context, evidence commercialaccess.SubscriptionCommissioningEvidence) (commercialaccess.PurchaseProjection, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commercialaccess.PurchaseProjection{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var snapshot commercialaccess.PurchaseSnapshot
	var providerPrice string
	err = tx.QueryRow(ctx, `
		SELECT b.account_id,'commissioning',b.commissioning_code,b.commissioning_version,b.commissioning_catalog_version,
		       cp.content->'commissioning_offer'->>'currency',
		       (cp.content->'commissioning_offer'->>'amount_minor')::bigint,
		       opp.provider_price_id
		FROM billing_checkout_attempts b
		JOIN catalog_publications cp ON cp.version=b.commissioning_catalog_version
		JOIN offer_provider_prices opp ON opp.catalog_version=b.commissioning_catalog_version
		 AND opp.offer_code=b.commissioning_code AND opp.provider='stripe' AND opp.mode=b.mode
		WHERE b.request_id=$1 AND b.account_id=$2 AND b.purchase_kind='subscription'
		FOR UPDATE OF b`, evidence.RequestID, evidence.AccountID).Scan(&snapshot.AccountID, &snapshot.Kind, &snapshot.ItemCode, &snapshot.ItemVersion, &snapshot.CatalogVersion, &snapshot.Currency, &snapshot.AmountMinor, &providerPrice)
	if errors.Is(err, pgx.ErrNoRows) {
		return commercialaccess.PurchaseProjection{}, false, nil
	}
	if err != nil {
		return commercialaccess.PurchaseProjection{}, false, err
	}
	priceFound := false
	for _, price := range evidence.ProviderPriceIDs {
		if price == providerPrice {
			priceFound = true
			break
		}
	}
	if snapshot.ItemCode != evidence.ItemCode || snapshot.ItemVersion != evidence.ItemVersion || snapshot.Currency != "USD" || snapshot.AmountMinor <= 0 || !priceFound {
		return commercialaccess.PurchaseProjection{}, false, commercialaccess.ErrInvalidPurchaseEvidence
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO account_commissioning_purchases
		(account_id,checkout_request_id,item_code,item_version,catalog_version,amount_minor,currency,provider_reference,purchased_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,statement_timestamp())
		ON CONFLICT (account_id) DO NOTHING`, snapshot.AccountID, evidence.RequestID, snapshot.ItemCode, snapshot.ItemVersion, snapshot.CatalogVersion, snapshot.AmountMinor, snapshot.Currency, evidence.InvoiceID)
	if err != nil {
		return commercialaccess.PurchaseProjection{}, false, err
	}
	if command.RowsAffected() != 1 {
		var existingRequest string
		if scanErr := tx.QueryRow(ctx, `SELECT checkout_request_id::text FROM account_commissioning_purchases WHERE account_id=$1`, snapshot.AccountID).Scan(&existingRequest); scanErr != nil {
			return commercialaccess.PurchaseProjection{}, false, scanErr
		}
		if existingRequest != evidence.RequestID {
			return commercialaccess.PurchaseProjection{}, false, commercialaccess.ErrCommissioningOwned
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return commercialaccess.PurchaseProjection{}, false, err
	}
	return commercialaccess.PurchaseProjection{Snapshot: snapshot}, true, nil
}

var _ commercialaccess.PurchaseProjectionStore = (*CommercialAccessRepository)(nil)
