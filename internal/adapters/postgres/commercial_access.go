package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type CommercialAccessRepository struct{ pool *pgxpool.Pool }

func NewCommercialAccessRepository(pool *pgxpool.Pool) *CommercialAccessRepository {
	return &CommercialAccessRepository{pool: pool}
}

func (r *CommercialAccessRepository) AccountProfile(ctx context.Context, accountID ids.AccountID) (commercialaccess.AccountProfile, error) {
	var value commercialaccess.AccountProfile
	err := r.pool.QueryRow(ctx, `
		SELECT a.id,a.display_name,u.primary_email,COALESCE(bp.stripe_customer_id,'')
		FROM accounts a JOIN users u ON u.id=a.created_by_user_id
		LEFT JOIN billing_profiles bp ON bp.account_id=a.id
		WHERE a.id=$1`, accountID).Scan(&value.AccountID, &value.AccountName, &value.BillingEmail, &value.CustomerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return commercialaccess.AccountProfile{}, errors.New("billing account does not exist")
	}
	return value, err
}

func (r *CommercialAccessRepository) AttachCustomer(ctx context.Context, accountID ids.AccountID, customerID, email string, now time.Time) (string, error) {
	var stored string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO billing_profiles (account_id,stripe_customer_id,billing_email,version,created_at,updated_at)
		VALUES ($1,$2,$3,1,$4,$4)
		ON CONFLICT (account_id) DO UPDATE
		SET stripe_customer_id=COALESCE(billing_profiles.stripe_customer_id,EXCLUDED.stripe_customer_id),
		    billing_email=COALESCE(billing_profiles.billing_email,EXCLUDED.billing_email),
		    version=billing_profiles.version+1,updated_at=EXCLUDED.updated_at
		RETURNING stripe_customer_id`, accountID, customerID, email, now.UTC()).Scan(&stored)
	return stored, err
}

func (r *CommercialAccessRepository) ProviderPrice(ctx context.Context, catalogVersion uint64, offerCode, provider, mode string) (string, error) {
	var priceID string
	err := r.pool.QueryRow(ctx, `
		SELECT provider_price_id FROM offer_provider_prices
		WHERE catalog_version=$1 AND offer_code=$2 AND provider=$3 AND mode=$4 AND active=true`, catalogVersion, offerCode, provider, mode).Scan(&priceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", commercialaccess.ErrBillingUnavailable
	}
	return priceID, err
}

func (r *CommercialAccessRepository) BillingStatus(ctx context.Context, accountID ids.AccountID, provider, mode string) (commercialaccess.Status, error) {
	var customerID string
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(stripe_customer_id,'') FROM billing_profiles WHERE account_id=$1`, accountID).Scan(&customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return commercialaccess.Status{}, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT state,offer_code,offer_version,current_period_start,current_period_end,cancel_at,last_synced_at
		FROM subscriptions WHERE account_id=$1 AND provider=$2 AND provider_mode=$3
		ORDER BY updated_at DESC,provider_subscription_id`, accountID, provider, mode)
	if err != nil {
		return commercialaccess.Status{}, err
	}
	defer rows.Close()
	status := commercialaccess.Status{HasCustomer: customerID != "", Subscriptions: make([]commercialaccess.Subscription, 0)}
	for rows.Next() {
		var value commercialaccess.Subscription
		if err := rows.Scan(&value.State, &value.OfferCode, &value.CatalogVersion, &value.CurrentPeriodStart, &value.CurrentPeriodEnd, &value.CancelAt, &value.LastSyncedAt); err != nil {
			return commercialaccess.Status{}, err
		}
		status.Subscriptions = append(status.Subscriptions, value)
	}
	return status, rows.Err()
}

func (r *CommercialAccessRepository) BeginCheckout(ctx context.Context, accountID ids.AccountID, offerCode, mode, requestID string, now time.Time) (commercialaccess.CheckoutReservation, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commercialaccess.CheckoutReservation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := string(accountID) + "/stripe/" + mode
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return commercialaccess.CheckoutReservation{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_checkout_attempts SET state='expired',updated_at=$2 WHERE account_id=$1 AND provider='stripe' AND mode=$3 AND state='active' AND expires_at<=$2`, accountID, now.UTC(), mode); err != nil {
		return commercialaccess.CheckoutReservation{}, err
	}
	var existingRequest, existingOffer, sessionID, hostedURL string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT request_id,offer_code,COALESCE(provider_session_id,''),COALESCE(hosted_url,''),expires_at FROM billing_checkout_attempts WHERE account_id=$1 AND provider='stripe' AND mode=$2 AND state='active'`, accountID, mode).Scan(&existingRequest, &existingOffer, &sessionID, &hostedURL, &expiresAt)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return commercialaccess.CheckoutReservation{}, err
		}
		if existingOffer == offerCode && sessionID != "" && hostedURL != "" {
			session := billing.HostedSession{ID: sessionID, URL: hostedURL, ExpiresAt: expiresAt.UTC()}
			return commercialaccess.CheckoutReservation{Resume: &session}, nil
		}
		return commercialaccess.CheckoutReservation{Proceed: existingRequest == requestID && existingOffer == offerCode}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return commercialaccess.CheckoutReservation{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO billing_checkout_attempts (request_id,account_id,provider,mode,offer_code,state,expires_at,created_at,updated_at) VALUES ($1,$2,'stripe',$3,$4,'active',$5,$6,$6)`, requestID, accountID, mode, offerCode, now.UTC().Add(30*time.Minute), now.UTC())
	if err != nil {
		return commercialaccess.CheckoutReservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return commercialaccess.CheckoutReservation{}, err
	}
	return commercialaccess.CheckoutReservation{Proceed: true}, nil
}

func (r *CommercialAccessRepository) CompleteCheckout(ctx context.Context, accountID ids.AccountID, requestID string, session billing.HostedSession, now time.Time) error {
	expires := session.ExpiresAt.UTC()
	if expires.IsZero() || !expires.After(now) {
		expires = now.UTC().Add(24 * time.Hour)
	}
	command, err := r.pool.Exec(ctx, `UPDATE billing_checkout_attempts SET provider_session_id=$3,hosted_url=$4,expires_at=$5,updated_at=$6 WHERE request_id=$1 AND account_id=$2`, requestID, accountID, session.ID, session.URL, expires, now.UTC())
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("billing checkout reservation was lost")
	}
	return err
}

var _ commercialaccess.Repository = (*CommercialAccessRepository)(nil)
