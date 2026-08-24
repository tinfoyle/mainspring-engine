package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AffiliateProgramRepository struct{ pool *pgxpool.Pool }

func NewAffiliateProgramRepository(pool *pgxpool.Pool) *AffiliateProgramRepository {
	return &AffiliateProgramRepository{pool: pool}
}

func (r *AffiliateProgramRepository) CanSettleToAccount(ctx context.Context, userID ids.UserID, accountID ids.AccountID) (bool, error) {
	var allowed bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM memberships
			WHERE user_id=$1 AND account_id=$2 AND role='owner' AND state='active'
		)`, userID, accountID).Scan(&allowed)
	return allowed, err
}

func (r *AffiliateProgramRepository) CreateEnrollment(ctx context.Context, enrollment affiliates.Enrollment) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO affiliate_enrollments
			(affiliate_id,user_id,settlement_account_id,public_code,terms_version,rule_version,state,version,created_at,updated_at)
		VALUES ($1,$2,NULLIF($3::text,'')::uuid,$4,$5,$6,$7,$8,$9,$9)`, enrollment.ID, enrollment.UserID,
		enrollment.SettlementAccountID, enrollment.PublicCode, enrollment.TermsVersion, enrollment.RuleVersion,
		enrollment.State, enrollment.Version, enrollment.CreatedAt)
	return err
}

func (r *AffiliateProgramRepository) EnrollmentByUser(ctx context.Context, userID ids.UserID) (affiliates.Enrollment, error) {
	return scanAffiliateEnrollment(r.pool.QueryRow(ctx, `
		SELECT affiliate_id,user_id,settlement_account_id::text,public_code,terms_version,rule_version,state,version,created_at
		FROM affiliate_enrollments WHERE user_id=$1`, userID))
}

func (r *AffiliateProgramRepository) EnrollmentByCode(ctx context.Context, code string) (affiliates.Enrollment, error) {
	return scanAffiliateEnrollment(r.pool.QueryRow(ctx, `
		SELECT affiliate_id,user_id,settlement_account_id::text,public_code,terms_version,rule_version,state,version,created_at
		FROM affiliate_enrollments WHERE public_code=$1 AND state='active'`, affiliates.NormalizeCode(code)))
}

func scanAffiliateEnrollment(row pgx.Row) (affiliates.Enrollment, error) {
	var value affiliates.Enrollment
	var settlement *string
	err := row.Scan(&value.ID, &value.UserID, &settlement, &value.PublicCode, &value.TermsVersion,
		&value.RuleVersion, &value.State, &value.Version, &value.CreatedAt)
	if settlement != nil {
		value.SettlementAccountID = ids.AccountID(*settlement)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return affiliates.Enrollment{}, affiliateprogram.ErrCodeUnavailable
	}
	return value, err
}

func (r *AffiliateProgramRepository) CreateAttribution(ctx context.Context, attribution affiliates.Attribution) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO affiliate_attributions
			(attribution_id,affiliate_id,referred_account_id,checkout_request_id,offer_code,offer_version,
			 rule_version,state,provider_subscription_id,version,created_at,locked_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULL,$9,$10,NULL)`, attribution.ID, attribution.AffiliateID,
		attribution.ReferredAccountID, attribution.CheckoutRequestID, attribution.OfferCode, attribution.OfferVersion,
		attribution.RuleVersion, attribution.State, attribution.Version, attribution.CreatedAt)
	return err
}

func (r *AffiliateProgramRepository) AttributionByCheckoutRequest(ctx context.Context, requestID string) (affiliates.Attribution, error) {
	return scanAffiliateAttribution(r.pool.QueryRow(ctx, affiliateAttributionSelect+` WHERE checkout_request_id=$1`, requestID))
}

func (r *AffiliateProgramRepository) AttributionBySubscription(ctx context.Context, subscriptionID string) (affiliates.Attribution, error) {
	return scanAffiliateAttribution(r.pool.QueryRow(ctx, affiliateAttributionSelect+` WHERE provider_subscription_id=$1`, subscriptionID))
}

const affiliateAttributionSelect = `
	SELECT attribution_id,affiliate_id,referred_account_id,checkout_request_id,offer_code,offer_version,
	       rule_version,state,COALESCE(provider_subscription_id,''),version,created_at,locked_at
	FROM affiliate_attributions`

func scanAffiliateAttribution(row pgx.Row) (affiliates.Attribution, error) {
	var value affiliates.Attribution
	err := row.Scan(&value.ID, &value.AffiliateID, &value.ReferredAccountID, &value.CheckoutRequestID,
		&value.OfferCode, &value.OfferVersion, &value.RuleVersion, &value.State, &value.SubscriptionID,
		&value.Version, &value.CreatedAt, &value.LockedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return affiliates.Attribution{}, affiliateprogram.ErrAttributionNotFound
	}
	return value, err
}

func (r *AffiliateProgramRepository) LockAttribution(ctx context.Context, attributionID ids.ReferralAttributionID, subscriptionID string, now time.Time) (affiliates.Attribution, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return affiliates.Attribution{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanAffiliateAttribution(tx.QueryRow(ctx, affiliateAttributionSelect+` WHERE attribution_id=$1 FOR UPDATE`, attributionID))
	if err != nil {
		return affiliates.Attribution{}, err
	}
	locked, err := current.Lock(subscriptionID, now)
	if err != nil {
		return affiliates.Attribution{}, err
	}
	if locked.Version != current.Version {
		command, err := tx.Exec(ctx, `
			UPDATE affiliate_attributions
			SET state=$2,provider_subscription_id=$3,version=$4,locked_at=$5
			WHERE attribution_id=$1 AND version=$6`, locked.ID, locked.State, locked.SubscriptionID,
			locked.Version, locked.LockedAt, current.Version)
		if err != nil {
			return affiliates.Attribution{}, err
		}
		if command.RowsAffected() != 1 {
			return affiliates.Attribution{}, affiliates.ErrAttributionLocked
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return affiliates.Attribution{}, err
	}
	return locked, nil
}

func (r *AffiliateProgramRepository) CommissionRule(ctx context.Context, version uint64) (affiliates.CommissionRule, error) {
	var value affiliates.CommissionRule
	var maximumCycles int64
	var holdDays int64
	err := r.pool.QueryRow(ctx, `
		SELECT rule_id,version,offer_code,currency,eligible_invoice_minor,commission_minor,
		       initial_invoice_qualifies,maximum_cycles,hold_days,effective_from
		FROM affiliate_commission_rules WHERE version=$1`, version).Scan(&value.ID, &value.Version,
		&value.OfferCode, &value.Currency, &value.EligibleInvoiceMinor, &value.CommissionMinor,
		&value.InitialInvoiceQualifies, &maximumCycles, &holdDays, &value.EffectiveFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		return affiliates.CommissionRule{}, affiliates.ErrInvalidRule
	}
	if err != nil {
		return affiliates.CommissionRule{}, err
	}
	if maximumCycles < 0 || maximumCycles > int64(^uint32(0)) || holdDays < 0 || holdDays > int64(^uint16(0)) {
		return affiliates.CommissionRule{}, affiliates.ErrInvalidRule
	}
	value.MaximumCycles = uint32(maximumCycles)
	value.HoldDays = uint16(holdDays)
	return value, value.Validate()
}

func (r *AffiliateProgramRepository) AppendCommission(ctx context.Context, entry affiliates.CommissionEntry) (affiliates.CommissionEntry, error) {
	var reverses any
	if entry.ReversesID != nil {
		reverses = *entry.ReversesID
	}
	command, err := r.pool.Exec(ctx, `
		INSERT INTO affiliate_commission_entries
			(entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
			 cycle,kind,state,amount_minor,currency,reverses_entry_id,available_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (provider_subscription_id,provider_invoice_id,rule_version,kind) DO NOTHING`, entry.ID,
		entry.AffiliateID, entry.AttributionID, entry.RuleVersion, entry.SubscriptionID, entry.InvoiceID,
		entry.Cycle, entry.Kind, entry.State, entry.AmountMinor, entry.Currency, reverses, entry.AvailableAt, entry.CreatedAt)
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	if command.RowsAffected() == 1 {
		return entry, nil
	}
	stored, err := scanAffiliateCommission(r.pool.QueryRow(ctx, affiliateCommissionSelect+`
		WHERE provider_subscription_id=$1 AND provider_invoice_id=$2 AND rule_version=$3 AND kind=$4`,
		entry.SubscriptionID, entry.InvoiceID, entry.RuleVersion, entry.Kind))
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	if !sameCommissionEvidence(stored, entry) {
		return affiliates.CommissionEntry{}, fmt.Errorf("affiliate commission idempotency conflict")
	}
	return stored, nil
}

func (r *AffiliateProgramRepository) CommissionEntries(ctx context.Context, affiliateID ids.AffiliateID) ([]affiliates.CommissionEntry, error) {
	rows, err := r.pool.Query(ctx, affiliateCommissionSelect+` WHERE affiliate_id=$1 ORDER BY created_at DESC,entry_id DESC`, affiliateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]affiliates.CommissionEntry, 0)
	for rows.Next() {
		value, err := scanAffiliateCommission(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

const affiliateCommissionSelect = `
	SELECT entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
	       cycle,kind,state,amount_minor,currency,reverses_entry_id::text,available_at,created_at
	FROM affiliate_commission_entries`

func scanAffiliateCommission(row pgx.Row) (affiliates.CommissionEntry, error) {
	var value affiliates.CommissionEntry
	var reverses *string
	err := row.Scan(&value.ID, &value.AffiliateID, &value.AttributionID, &value.RuleVersion,
		&value.SubscriptionID, &value.InvoiceID, &value.Cycle, &value.Kind, &value.State,
		&value.AmountMinor, &value.Currency, &reverses, &value.AvailableAt, &value.CreatedAt)
	if reverses != nil {
		identifier := ids.CommissionEntryID(*reverses)
		value.ReversesID = &identifier
	}
	return value, err
}

func sameCommissionEvidence(left, right affiliates.CommissionEntry) bool {
	return left.AffiliateID == right.AffiliateID && left.AttributionID == right.AttributionID &&
		left.RuleVersion == right.RuleVersion && left.SubscriptionID == right.SubscriptionID &&
		left.InvoiceID == right.InvoiceID && left.Cycle == right.Cycle && left.Kind == right.Kind &&
		left.State == right.State && left.AmountMinor == right.AmountMinor && left.Currency == right.Currency &&
		left.AvailableAt.Equal(right.AvailableAt) && left.CreatedAt.Equal(right.CreatedAt)
}

var _ affiliateprogram.Repository = (*AffiliateProgramRepository)(nil)
