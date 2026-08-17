package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
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

var _ commercialaccess.Repository = (*CommercialAccessRepository)(nil)
