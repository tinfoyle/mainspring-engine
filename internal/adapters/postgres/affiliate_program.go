package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func (r *AffiliateProgramRepository) ReplaceEnrollmentCode(ctx context.Context, userID ids.UserID, expectedVersion uint64, code string, now time.Time) (affiliates.Enrollment, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return affiliates.Enrollment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanAffiliateEnrollment(tx.QueryRow(ctx, `
		SELECT affiliate_id,user_id,settlement_account_id::text,public_code,terms_version,rule_version,state,version,created_at
		FROM affiliate_enrollments WHERE user_id=$1 FOR UPDATE`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return affiliates.Enrollment{}, affiliateprogram.ErrEnrollmentNotFound
	}
	if err != nil {
		return affiliates.Enrollment{}, err
	}
	if current.State != affiliates.EnrollmentActive {
		return affiliates.Enrollment{}, affiliateprogram.ErrEnrollmentState
	}
	if current.Version != expectedVersion {
		return affiliates.Enrollment{}, affiliateprogram.ErrEnrollmentConflict
	}
	replaced, err := current.ReplacePublicCode(code)
	if err != nil {
		return affiliates.Enrollment{}, err
	}
	history, err := tx.Exec(ctx, `UPDATE affiliate_public_code_history SET replaced_at=$2 WHERE public_code=$1 AND replaced_at IS NULL`, current.PublicCode, now.UTC())
	if err != nil {
		return affiliates.Enrollment{}, err
	}
	if history.RowsAffected() != 1 {
		return affiliates.Enrollment{}, affiliateprogram.ErrEnrollmentConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO affiliate_public_code_history (public_code,affiliate_id,enrollment_version,activated_at) VALUES ($1,$2,$3,$4)`, replaced.PublicCode, replaced.ID, replaced.Version, now.UTC()); err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return affiliates.Enrollment{}, affiliateprogram.ErrCodeUnavailable
		}
		return affiliates.Enrollment{}, err
	}
	command, err := tx.Exec(ctx, `UPDATE affiliate_enrollments SET public_code=$3,version=$4,updated_at=$5 WHERE user_id=$1 AND version=$2`, userID, expectedVersion, replaced.PublicCode, replaced.Version, now.UTC())
	if err != nil {
		return affiliates.Enrollment{}, err
	}
	if command.RowsAffected() != 1 {
		return affiliates.Enrollment{}, affiliateprogram.ErrEnrollmentConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return affiliates.Enrollment{}, err
	}
	return replaced, nil
}

func (r *AffiliateProgramRepository) EnrollmentByUser(ctx context.Context, userID ids.UserID) (affiliates.Enrollment, error) {
	value, err := scanAffiliateEnrollment(r.pool.QueryRow(ctx, `
		SELECT affiliate_id,user_id,settlement_account_id::text,public_code,terms_version,rule_version,state,version,created_at
		FROM affiliate_enrollments WHERE user_id=$1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return affiliates.Enrollment{}, affiliateprogram.ErrEnrollmentNotFound
	}
	return value, err
}

func (r *AffiliateProgramRepository) EnrollmentByCode(ctx context.Context, code string) (affiliates.Enrollment, error) {
	value, err := scanAffiliateEnrollment(r.pool.QueryRow(ctx, `
		SELECT affiliate_id,user_id,settlement_account_id::text,public_code,terms_version,rule_version,state,version,created_at
		FROM affiliate_enrollments WHERE public_code=$1 AND state='active'`, affiliates.NormalizeCode(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return affiliates.Enrollment{}, affiliateprogram.ErrCodeUnavailable
	}
	return value, err
}

func scanAffiliateEnrollment(row pgx.Row) (affiliates.Enrollment, error) {
	var value affiliates.Enrollment
	var settlement *string
	err := row.Scan(&value.ID, &value.UserID, &settlement, &value.PublicCode, &value.TermsVersion,
		&value.RuleVersion, &value.State, &value.Version, &value.CreatedAt)
	if settlement != nil {
		value.SettlementAccountID = ids.AccountID(*settlement)
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
			 provider_payment_intent_id,cycle,kind,state,amount_minor,currency,reverses_entry_id,available_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (provider_subscription_id,provider_invoice_id,rule_version,kind) DO NOTHING`, entry.ID,
		entry.AffiliateID, entry.AttributionID, entry.RuleVersion, entry.SubscriptionID, entry.InvoiceID, entry.PaymentIntentID,
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

func (r *AffiliateProgramRepository) RecordPaidCommission(ctx context.Context, id, reversalID ids.CommissionEntryID, attribution affiliates.Attribution, rule affiliates.CommissionRule, invoiceID, paymentIntentID string, initial bool, now time.Time) (affiliates.CommissionEntry, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "spyglass:affiliate-commission:"+attribution.SubscriptionID); err != nil {
		return affiliates.CommissionEntry{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "spyglass:affiliate-payment:"+paymentIntentID); err != nil {
		return affiliates.CommissionEntry{}, err
	}
	existing, err := scanAffiliateCommission(tx.QueryRow(ctx, affiliateCommissionSelect+`
		WHERE provider_subscription_id=$1 AND provider_invoice_id=$2 AND rule_version=$3 AND kind='earned'`,
		attribution.SubscriptionID, invoiceID, rule.Version))
	if err == nil {
		if existing.AffiliateID != attribution.AffiliateID || existing.AttributionID != attribution.ID ||
			existing.AmountMinor != rule.CommissionMinor || existing.Currency != rule.Currency ||
			(existing.PaymentIntentID != "" && existing.PaymentIntentID != paymentIntentID) {
			return affiliates.CommissionEntry{}, fmt.Errorf("affiliate commission idempotency conflict")
		}
		if existing.PaymentIntentID != "" {
			if _, _, err := maybeAppendCommissionReversal(ctx, tx, reversalID, existing, rule.EligibleInvoiceMinor, now); err != nil {
				return affiliates.CommissionEntry{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return affiliates.CommissionEntry{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return affiliates.CommissionEntry{}, err
	}
	var maximumCycle int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(cycle),0) FROM affiliate_commission_entries
		WHERE provider_subscription_id=$1 AND rule_version=$2 AND kind='earned'`,
		attribution.SubscriptionID, rule.Version).Scan(&maximumCycle); err != nil {
		return affiliates.CommissionEntry{}, err
	}
	if maximumCycle < 0 || maximumCycle >= int64(^uint32(0)) {
		return affiliates.CommissionEntry{}, affiliates.ErrInvalidCommission
	}
	cycle := uint32(1)
	if initial {
		if maximumCycle != 0 {
			return affiliates.CommissionEntry{}, affiliates.ErrInvalidCommission
		}
	} else {
		cycle = uint32(maximumCycle) + 1
		if cycle < 2 {
			cycle = 2
		}
	}
	entry, err := affiliates.NewEarnedEntry(id, attribution, rule, invoiceID, paymentIntentID, cycle, now)
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO affiliate_commission_entries
			(entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
			 provider_payment_intent_id,cycle,kind,state,amount_minor,currency,reverses_entry_id,available_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULL,$13,$14)`, entry.ID, entry.AffiliateID,
		entry.AttributionID, entry.RuleVersion, entry.SubscriptionID, entry.InvoiceID, entry.PaymentIntentID, entry.Cycle,
		entry.Kind, entry.State, entry.AmountMinor, entry.Currency, entry.AvailableAt, entry.CreatedAt)
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	if _, _, err := maybeAppendCommissionReversal(ctx, tx, reversalID, entry, rule.EligibleInvoiceMinor, now); err != nil {
		return affiliates.CommissionEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return affiliates.CommissionEntry{}, err
	}
	return entry, nil
}

func (r *AffiliateProgramRepository) RecordAdverseCommission(ctx context.Context, reversalID ids.CommissionEntryID, evidence affiliates.AdverseBillingEvidence, now time.Time) (affiliates.CommissionEntry, bool, error) {
	if evidence.Validate() != nil || ids.Validate(string(reversalID)) != nil || now.IsZero() || now.Before(evidence.OccurredAt) {
		return affiliates.CommissionEntry{}, false, affiliates.ErrInvalidAdverse
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "spyglass:affiliate-payment:"+evidence.PaymentIntentID); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO affiliate_provider_adverse_events
			(provider_event_id,kind,provider_object_id,provider_payment_intent_id,amount_minor,currency,occurred_at,recorded_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT DO NOTHING`, evidence.EventID, evidence.Kind, evidence.ProviderObjectID, evidence.PaymentIntentID,
		evidence.AmountMinor, evidence.Currency, evidence.OccurredAt.UTC(), now.UTC())
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	if command.RowsAffected() == 0 {
		stored, err := scanAffiliateAdverseEvidence(tx.QueryRow(ctx, affiliateAdverseSelect+`
			WHERE kind=$1 AND provider_object_id=$2`, evidence.Kind, evidence.ProviderObjectID))
		if errors.Is(err, pgx.ErrNoRows) {
			stored, err = scanAffiliateAdverseEvidence(tx.QueryRow(ctx, affiliateAdverseSelect+`
				WHERE provider_event_id=$1`, evidence.EventID))
		}
		if err != nil {
			return affiliates.CommissionEntry{}, false, err
		}
		if !sameAdverseEvidence(stored, evidence) {
			return affiliates.CommissionEntry{}, false, fmt.Errorf("affiliate adverse-event idempotency conflict")
		}
	}
	original, err := scanAffiliateCommission(tx.QueryRow(ctx, affiliateCommissionSelect+`
		WHERE provider_payment_intent_id=$1 AND kind='earned'`, evidence.PaymentIntentID))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return affiliates.CommissionEntry{}, false, err
		}
		return affiliates.CommissionEntry{}, false, nil
	}
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	var eligibleMinor int64
	if err := tx.QueryRow(ctx, `SELECT eligible_invoice_minor FROM affiliate_commission_rules WHERE version=$1`, original.RuleVersion).Scan(&eligibleMinor); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	reversal, reversed, err := maybeAppendCommissionReversal(ctx, tx, reversalID, original, eligibleMinor, now)
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	return reversal, reversed, nil
}

const affiliateAdverseSelect = `
	SELECT provider_event_id,kind,provider_object_id,provider_payment_intent_id,amount_minor,currency,occurred_at
	FROM affiliate_provider_adverse_events`

func scanAffiliateAdverseEvidence(row pgx.Row) (affiliates.AdverseBillingEvidence, error) {
	var value affiliates.AdverseBillingEvidence
	err := row.Scan(&value.EventID, &value.Kind, &value.ProviderObjectID, &value.PaymentIntentID,
		&value.AmountMinor, &value.Currency, &value.OccurredAt)
	return value, err
}

func sameAdverseEvidence(left, right affiliates.AdverseBillingEvidence) bool {
	// Stripe can emit refund.created and refund.updated for the same successful
	// Refund object. Provider object identity plus financial evidence is the
	// semantic idempotency boundary; delivery event IDs and times may differ.
	return left.Kind == right.Kind && left.ProviderObjectID == right.ProviderObjectID &&
		left.PaymentIntentID == right.PaymentIntentID && left.AmountMinor == right.AmountMinor && left.Currency == right.Currency
}

func maybeAppendCommissionReversal(ctx context.Context, tx pgx.Tx, reversalID ids.CommissionEntryID, original affiliates.CommissionEntry, eligibleMinor int64, now time.Time) (affiliates.CommissionEntry, bool, error) {
	existing, err := scanAffiliateCommission(tx.QueryRow(ctx, affiliateCommissionSelect+`
		WHERE reverses_entry_id=$1 AND kind='reversal'`, original.ID))
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return affiliates.CommissionEntry{}, false, err
	}
	var refundedMinor, disputedMinor int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(amount_minor) FILTER (WHERE kind='refund'),0)::bigint,
		       COALESCE(max(amount_minor) FILTER (WHERE kind='dispute'),0)::bigint
		FROM affiliate_provider_adverse_events WHERE provider_payment_intent_id=$1`, original.PaymentIntentID).Scan(&refundedMinor, &disputedMinor); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	if refundedMinor < eligibleMinor && disputedMinor < eligibleMinor {
		return affiliates.CommissionEntry{}, false, nil
	}
	reversal, err := affiliates.NewReversalEntry(reversalID, original, original.InvoiceID, now)
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO affiliate_commission_entries
			(entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
			 provider_payment_intent_id,cycle,kind,state,amount_minor,currency,reverses_entry_id,available_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, reversal.ID, reversal.AffiliateID,
		reversal.AttributionID, reversal.RuleVersion, reversal.SubscriptionID, reversal.InvoiceID, reversal.PaymentIntentID,
		reversal.Cycle, reversal.Kind, reversal.State, reversal.AmountMinor, reversal.Currency, *reversal.ReversesID,
		reversal.AvailableAt, reversal.CreatedAt)
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	return reversal, true, nil
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
	       COALESCE(provider_payment_intent_id,''),cycle,kind,state,amount_minor,currency,reverses_entry_id::text,available_at,created_at
	FROM affiliate_commission_entries`

func scanAffiliateCommission(row pgx.Row) (affiliates.CommissionEntry, error) {
	var value affiliates.CommissionEntry
	var reverses *string
	err := row.Scan(&value.ID, &value.AffiliateID, &value.AttributionID, &value.RuleVersion,
		&value.SubscriptionID, &value.InvoiceID, &value.PaymentIntentID, &value.Cycle, &value.Kind, &value.State,
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
		left.InvoiceID == right.InvoiceID && left.PaymentIntentID == right.PaymentIntentID && left.Cycle == right.Cycle && left.Kind == right.Kind &&
		left.State == right.State && left.AmountMinor == right.AmountMinor && left.Currency == right.Currency
}

var _ affiliateprogram.Repository = (*AffiliateProgramRepository)(nil)
