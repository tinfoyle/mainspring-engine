package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
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
		SELECT rule_id,version,offer_code,currency,eligible_invoice_minor,commission_minor,commission_rate_basis_points,
		       initial_invoice_qualifies,maximum_cycles,hold_days,effective_from
		FROM affiliate_commission_rules WHERE version=$1`, version).Scan(&value.ID, &value.Version,
		&value.OfferCode, &value.Currency, &value.EligibleInvoiceMinor, &value.CommissionMinor, &value.CommissionRateBasisPoints,
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

func (r *AffiliateProgramRepository) EligibleInvoiceLines(ctx context.Context, attribution affiliates.Attribution, mode string, lines []affiliateprogram.InvoiceLine) ([]affiliateprogram.InvoiceLine, int64, error) {
	if attribution.State != affiliates.AttributionLocked || ids.Validate(string(attribution.ID)) != nil || attribution.OfferVersion == 0 || attribution.OfferCode == "" || (mode != "test" && mode != "live") || len(lines) == 0 {
		return nil, 0, affiliateprogram.ErrInvoiceIneligible
	}
	rows, err := r.pool.Query(ctx, `
		SELECT provider_price_id FROM offer_provider_prices
		WHERE catalog_version=$1 AND offer_code=$2 AND provider='stripe' AND mode=$3 AND active=true`,
		attribution.OfferVersion, attribution.OfferCode, mode)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	prices := make(map[string]struct{})
	for rows.Next() {
		var price string
		if err := rows.Scan(&price); err != nil {
			return nil, 0, err
		}
		prices[price] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	eligible := make([]affiliateprogram.InvoiceLine, 0, len(lines))
	var total int64
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		if _, duplicate := seen[line.ID]; duplicate {
			return nil, 0, affiliateprogram.ErrInvoiceIneligible
		}
		seen[line.ID] = struct{}{}
		if _, matched := prices[line.ProviderPriceID]; !matched || line.AmountMinor <= 0 {
			continue
		}
		if len(line.Currency) != 3 || line.AmountMinor > 99_999_999 || total > 99_999_999-line.AmountMinor {
			return nil, 0, affiliateprogram.ErrInvoiceIneligible
		}
		total += line.AmountMinor
		eligible = append(eligible, line)
	}
	if len(eligible) == 0 || total <= 0 {
		return nil, 0, affiliateprogram.ErrInvoiceIneligible
	}
	return eligible, total, nil
}

func (r *AffiliateProgramRepository) AppendCommission(ctx context.Context, entry affiliates.CommissionEntry) (affiliates.CommissionEntry, error) {
	var reverses any
	var source any
	if entry.ReversesID != nil {
		reverses = *entry.ReversesID
	}
	if entry.SourceID != nil {
		source = *entry.SourceID
	}
	command, err := r.pool.Exec(ctx, `
		INSERT INTO affiliate_commission_entries
			(entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
			 provider_payment_intent_id,cycle,kind,state,amount_minor,currency,reverses_entry_id,source_entry_id,available_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (provider_subscription_id,provider_invoice_id,rule_version,kind) DO NOTHING`, entry.ID,
		entry.AffiliateID, entry.AttributionID, entry.RuleVersion, entry.SubscriptionID, entry.InvoiceID, entry.PaymentIntentID,
		entry.Cycle, entry.Kind, entry.State, entry.AmountMinor, entry.Currency, reverses, source, entry.AvailableAt, entry.CreatedAt)
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

func (r *AffiliateProgramRepository) RecordPaidCommission(ctx context.Context, id, maturityID, reversalID ids.CommissionEntryID, attribution affiliates.Attribution, rule affiliates.CommissionRule, paid affiliateprogram.PaidInvoice, eligibleLines []affiliateprogram.InvoiceLine, commissionMinor int64, now time.Time) (affiliates.CommissionEntry, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "spyglass:affiliate-commission:"+attribution.SubscriptionID); err != nil {
		return affiliates.CommissionEntry{}, err
	}
	var enrollmentState string
	var enrollmentUpdatedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT state,updated_at FROM affiliate_enrollments WHERE affiliate_id=$1`, attribution.AffiliateID).Scan(&enrollmentState, &enrollmentUpdatedAt); err != nil {
		return affiliates.CommissionEntry{}, err
	}
	if enrollmentState == string(affiliates.EnrollmentClosed) && (paid.OccurredAt.IsZero() || !paid.OccurredAt.Before(enrollmentUpdatedAt)) {
		return affiliates.CommissionEntry{}, affiliateprogram.ErrInvoiceIneligible
	}
	paymentIntentIDs := normalizedPaymentIntentIDs(paid)
	for _, paymentIntentID := range paymentIntentIDs {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "spyglass:affiliate-payment:"+paymentIntentID); err != nil {
			return affiliates.CommissionEntry{}, err
		}
	}
	existing, err := scanAffiliateCommission(tx.QueryRow(ctx, affiliateCommissionSelect+`
		WHERE provider_subscription_id=$1 AND provider_invoice_id=$2 AND rule_version=$3 AND kind='earned'`,
		attribution.SubscriptionID, paid.InvoiceID, rule.Version))
	if err == nil {
		if existing.AffiliateID != attribution.AffiliateID || existing.AttributionID != attribution.ID ||
			existing.AmountMinor != commissionMinor || existing.Currency != rule.Currency {
			return affiliates.CommissionEntry{}, fmt.Errorf("affiliate commission idempotency conflict")
		}
		if err := verifyCommissionInvoiceEvidence(ctx, tx, existing.ID, paymentIntentIDs, eligibleLines); err != nil {
			return affiliates.CommissionEntry{}, err
		}
		if _, _, err := maybeAppendCommissionReversal(ctx, tx, reversalID, existing, now); err != nil {
			return affiliates.CommissionEntry{}, err
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
	if paid.Initial {
		if maximumCycle != 0 {
			return affiliates.CommissionEntry{}, affiliates.ErrInvalidCommission
		}
	} else {
		cycle = uint32(maximumCycle) + 1
		if cycle < 2 {
			cycle = 2
		}
	}
	primaryPaymentIntent := ""
	if len(paymentIntentIDs) > 0 {
		primaryPaymentIntent = paymentIntentIDs[0]
	}
	entry, err := affiliates.NewEarnedEntryAmount(id, attribution, rule, paid.InvoiceID, primaryPaymentIntent, cycle, commissionMinor, now)
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO affiliate_commission_entries
			(entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
			 provider_payment_intent_id,cycle,kind,state,amount_minor,currency,reverses_entry_id,source_entry_id,available_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULL,NULL,$13,$14)`, entry.ID, entry.AffiliateID,
		entry.AttributionID, entry.RuleVersion, entry.SubscriptionID, entry.InvoiceID, entry.PaymentIntentID, entry.Cycle,
		entry.Kind, entry.State, entry.AmountMinor, entry.Currency, entry.AvailableAt, entry.CreatedAt)
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	if err := insertCommissionInvoiceEvidence(ctx, tx, entry, paymentIntentIDs, eligibleLines); err != nil {
		return affiliates.CommissionEntry{}, err
	}
	previous, previousErr := scanAffiliateCommission(tx.QueryRow(ctx, affiliateCommissionSelect+`
		WHERE provider_subscription_id=$1 AND rule_version=$2 AND kind='earned' AND entry_id<>$3
		  AND NOT EXISTS (SELECT 1 FROM affiliate_commission_entries lifecycle WHERE lifecycle.source_entry_id=affiliate_commission_entries.entry_id AND lifecycle.kind IN ('maturity','void'))
		  AND NOT EXISTS (SELECT 1 FROM affiliate_commission_entries reversal WHERE reversal.reverses_entry_id=affiliate_commission_entries.entry_id AND reversal.kind='reversal')
		ORDER BY cycle DESC LIMIT 1`, attribution.SubscriptionID, rule.Version, entry.ID))
	if previousErr == nil {
		maturity, maturityErr := affiliates.NewMaturityEntry(maturityID, previous, paid.InvoiceID, now)
		if maturityErr != nil {
			return affiliates.CommissionEntry{}, maturityErr
		}
		if _, maturityErr = tx.Exec(ctx, `
			INSERT INTO affiliate_commission_entries
				(entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
				 provider_payment_intent_id,cycle,kind,state,amount_minor,currency,reverses_entry_id,source_entry_id,available_at,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,NULL,$7,$8,$9,$10,$11,NULL,$12,$13,$14)`, maturity.ID, maturity.AffiliateID,
			maturity.AttributionID, maturity.RuleVersion, maturity.SubscriptionID, maturity.InvoiceID, maturity.Cycle,
			maturity.Kind, maturity.State, maturity.AmountMinor, maturity.Currency, *maturity.SourceID, maturity.AvailableAt, maturity.CreatedAt); maturityErr != nil {
			return affiliates.CommissionEntry{}, maturityErr
		}
	} else if !errors.Is(previousErr, pgx.ErrNoRows) {
		return affiliates.CommissionEntry{}, previousErr
	}
	if _, _, err := maybeAppendCommissionReversal(ctx, tx, reversalID, entry, now); err != nil {
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
	adverseLock := "spyglass:affiliate-payment:" + evidence.PaymentIntentID
	if evidence.Kind == affiliates.AdverseCreditNote {
		adverseLock = "spyglass:affiliate-invoice:" + evidence.InvoiceID
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, adverseLock); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO affiliate_provider_adverse_events
			(provider_event_id,kind,provider_object_id,provider_payment_intent_id,provider_invoice_id,amount_minor,currency,occurred_at,recorded_at)
		VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9)
		ON CONFLICT DO NOTHING`, evidence.EventID, evidence.Kind, evidence.ProviderObjectID, evidence.PaymentIntentID,
		evidence.InvoiceID, evidence.AmountMinor, evidence.Currency, evidence.OccurredAt.UTC(), now.UTC())
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	if command.RowsAffected() == 1 {
		for _, lineID := range evidence.InvoiceLineIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO affiliate_provider_adverse_invoice_lines (provider_event_id,provider_invoice_line_id,created_at) VALUES ($1,$2,$3)`, evidence.EventID, lineID, now.UTC()); err != nil {
				return affiliates.CommissionEntry{}, false, err
			}
		}
	} else {
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
		if err := verifyAffiliateAdverseInvoiceLines(ctx, tx, stored.EventID, evidence.InvoiceLineIDs); err != nil {
			return affiliates.CommissionEntry{}, false, err
		}
	}
	originalQuery := affiliateCommissionSelect + `
		JOIN affiliate_commission_invoice_payments payment ON payment.earning_entry_id=affiliate_commission_entries.entry_id
		WHERE payment.provider_payment_intent_id=$1 AND kind='earned'`
	originalArgument := evidence.PaymentIntentID
	if evidence.Kind == affiliates.AdverseCreditNote {
		originalQuery = affiliateCommissionSelect + `
			JOIN affiliate_commission_invoice_lines line ON line.earning_entry_id=affiliate_commission_entries.entry_id
			WHERE affiliate_commission_entries.provider_invoice_id=$1 AND line.provider_invoice_line_id=ANY($2) AND kind='earned'
			LIMIT 1`
		originalArgument = evidence.InvoiceID
	}
	var original affiliates.CommissionEntry
	if evidence.Kind == affiliates.AdverseCreditNote {
		original, err = scanAffiliateCommission(tx.QueryRow(ctx, originalQuery, originalArgument, evidence.InvoiceLineIDs))
	} else {
		original, err = scanAffiliateCommission(tx.QueryRow(ctx, originalQuery, originalArgument))
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return affiliates.CommissionEntry{}, false, err
		}
		return affiliates.CommissionEntry{}, false, nil
	}
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	reversal, reversed, err := maybeAppendCommissionReversal(ctx, tx, reversalID, original, now)
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	return reversal, reversed, nil
}

const affiliateAdverseSelect = `
	SELECT provider_event_id,kind,provider_object_id,COALESCE(provider_payment_intent_id,''),COALESCE(provider_invoice_id,''),amount_minor,currency,occurred_at
	FROM affiliate_provider_adverse_events`

func scanAffiliateAdverseEvidence(row pgx.Row) (affiliates.AdverseBillingEvidence, error) {
	var value affiliates.AdverseBillingEvidence
	err := row.Scan(&value.EventID, &value.Kind, &value.ProviderObjectID, &value.PaymentIntentID, &value.InvoiceID,
		&value.AmountMinor, &value.Currency, &value.OccurredAt)
	return value, err
}

func sameAdverseEvidence(left, right affiliates.AdverseBillingEvidence) bool {
	// Stripe can emit refund.created and refund.updated for the same successful
	// Refund object. Provider object identity plus financial evidence is the
	// semantic idempotency boundary; delivery event IDs and times may differ.
	return left.Kind == right.Kind && left.ProviderObjectID == right.ProviderObjectID &&
		left.PaymentIntentID == right.PaymentIntentID && left.InvoiceID == right.InvoiceID &&
		left.AmountMinor == right.AmountMinor && left.Currency == right.Currency
}

func verifyAffiliateAdverseInvoiceLines(ctx context.Context, tx pgx.Tx, eventID ids.AffiliateProviderEventID, expected []string) error {
	rows, err := tx.Query(ctx, `SELECT provider_invoice_line_id FROM affiliate_provider_adverse_invoice_lines WHERE provider_event_id=$1 ORDER BY provider_invoice_line_id`, eventID)
	if err != nil {
		return err
	}
	defer rows.Close()
	actual := make([]string, 0, len(expected))
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return err
		}
		actual = append(actual, value)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	want := append([]string{}, expected...)
	slices.Sort(actual)
	slices.Sort(want)
	if !slices.Equal(actual, want) {
		return fmt.Errorf("affiliate adverse-event line idempotency conflict")
	}
	return nil
}

func maybeAppendCommissionReversal(ctx context.Context, tx pgx.Tx, reversalID ids.CommissionEntryID, original affiliates.CommissionEntry, now time.Time) (affiliates.CommissionEntry, bool, error) {
	existing, err := scanAffiliateCommission(tx.QueryRow(ctx, affiliateCommissionSelect+`
		WHERE reverses_entry_id=$1 AND kind='reversal'`, original.ID))
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return affiliates.CommissionEntry{}, false, err
	}
	var adverse bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM affiliate_commission_invoice_payments payment
			JOIN affiliate_provider_adverse_events adverse ON adverse.provider_payment_intent_id=payment.provider_payment_intent_id
			WHERE payment.earning_entry_id=$1 AND adverse.amount_minor>0
			UNION ALL
			SELECT 1 FROM affiliate_commission_invoice_lines invoice_line
			JOIN affiliate_provider_adverse_invoice_lines adverse_line ON adverse_line.provider_invoice_line_id=invoice_line.provider_invoice_line_id
			JOIN affiliate_provider_adverse_events adverse ON adverse.provider_event_id=adverse_line.provider_event_id
			WHERE invoice_line.earning_entry_id=$1 AND adverse.amount_minor>0
		)`, original.ID).Scan(&adverse); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	if !adverse {
		return affiliates.CommissionEntry{}, false, nil
	}
	reversal, err := affiliates.NewReversalEntry(reversalID, original, original.InvoiceID, now)
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO affiliate_commission_entries
			(entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
			 provider_payment_intent_id,cycle,kind,state,amount_minor,currency,reverses_entry_id,source_entry_id,available_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NULL,$14,$15)`, reversal.ID, reversal.AffiliateID,
		reversal.AttributionID, reversal.RuleVersion, reversal.SubscriptionID, reversal.InvoiceID, reversal.PaymentIntentID,
		reversal.Cycle, reversal.Kind, reversal.State, reversal.AmountMinor, reversal.Currency, *reversal.ReversesID,
		reversal.AvailableAt, reversal.CreatedAt)
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	return reversal, true, nil
}

func (r *AffiliateProgramRepository) RecordSubscriptionTermination(ctx context.Context, subscriptionID string, voidID ids.CommissionEntryID, occurredAt time.Time) (affiliates.CommissionEntry, bool, error) {
	if !strings.HasPrefix(subscriptionID, "sub_") || ids.Validate(string(voidID)) != nil || occurredAt.IsZero() {
		return affiliates.CommissionEntry{}, false, affiliates.ErrInvalidCommission
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "spyglass:affiliate-commission:"+subscriptionID); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	original, err := scanAffiliateCommission(tx.QueryRow(ctx, affiliateCommissionSelect+`
		WHERE provider_subscription_id=$1 AND kind='earned'
		  AND NOT EXISTS (SELECT 1 FROM affiliate_commission_entries lifecycle WHERE lifecycle.source_entry_id=affiliate_commission_entries.entry_id AND lifecycle.kind IN ('maturity','void'))
		  AND NOT EXISTS (SELECT 1 FROM affiliate_commission_entries reversal WHERE reversal.reverses_entry_id=affiliate_commission_entries.entry_id AND reversal.kind='reversal')
		ORDER BY cycle DESC LIMIT 1`, subscriptionID))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return affiliates.CommissionEntry{}, false, err
		}
		return affiliates.CommissionEntry{}, false, nil
	}
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	voided, err := affiliates.NewVoidEntry(voidID, original, occurredAt.UTC())
	if err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO affiliate_commission_entries
			(entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
			 provider_payment_intent_id,cycle,kind,state,amount_minor,currency,reverses_entry_id,source_entry_id,available_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULL,$7,$8,$9,$10,$11,NULL,$12,$13,$14)`, voided.ID, voided.AffiliateID,
		voided.AttributionID, voided.RuleVersion, voided.SubscriptionID, voided.InvoiceID, voided.Cycle,
		voided.Kind, voided.State, voided.AmountMinor, voided.Currency, *voided.SourceID, voided.AvailableAt, voided.CreatedAt); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return affiliates.CommissionEntry{}, false, err
	}
	return voided, true, nil
}

func normalizedPaymentIntentIDs(paid affiliateprogram.PaidInvoice) []string {
	values := append([]string{}, paid.PaymentIntentIDs...)
	if paid.PaymentIntentID != "" {
		values = append(values, paid.PaymentIntentID)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !strings.HasPrefix(value, "pi_") || strings.ContainsAny(value, "\r\n\t ") || len(value) > 200 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func insertCommissionInvoiceEvidence(ctx context.Context, tx pgx.Tx, entry affiliates.CommissionEntry, paymentIntentIDs []string, lines []affiliateprogram.InvoiceLine) error {
	for _, paymentIntentID := range paymentIntentIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO affiliate_commission_invoice_payments (earning_entry_id,provider_payment_intent_id,created_at) VALUES ($1,$2,$3)`, entry.ID, paymentIntentID, entry.CreatedAt); err != nil {
			return err
		}
	}
	for _, line := range lines {
		if _, err := tx.Exec(ctx, `INSERT INTO affiliate_commission_invoice_lines (earning_entry_id,provider_invoice_line_id,provider_price_id,eligible_amount_minor,currency,created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
			entry.ID, line.ID, line.ProviderPriceID, line.AmountMinor, line.Currency, entry.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func verifyCommissionInvoiceEvidence(ctx context.Context, tx pgx.Tx, entryID ids.CommissionEntryID, paymentIntentIDs []string, lines []affiliateprogram.InvoiceLine) error {
	var storedPayments, storedLines int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM affiliate_commission_invoice_payments WHERE earning_entry_id=$1`, entryID).Scan(&storedPayments); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM affiliate_commission_invoice_lines WHERE earning_entry_id=$1`, entryID).Scan(&storedLines); err != nil {
		return err
	}
	if storedPayments != len(paymentIntentIDs) || storedLines != len(lines) {
		return fmt.Errorf("affiliate commission invoice-evidence idempotency conflict")
	}
	for _, paymentIntentID := range paymentIntentIDs {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM affiliate_commission_invoice_payments WHERE earning_entry_id=$1 AND provider_payment_intent_id=$2)`, entryID, paymentIntentID).Scan(&exists); err != nil || !exists {
			return fmt.Errorf("affiliate commission invoice-evidence idempotency conflict")
		}
	}
	for _, line := range lines {
		var amount int64
		var price, currency string
		if err := tx.QueryRow(ctx, `SELECT provider_price_id,eligible_amount_minor,currency FROM affiliate_commission_invoice_lines WHERE earning_entry_id=$1 AND provider_invoice_line_id=$2`, entryID, line.ID).Scan(&price, &amount, &currency); err != nil || price != line.ProviderPriceID || amount != line.AmountMinor || currency != line.Currency {
			return fmt.Errorf("affiliate commission invoice-evidence idempotency conflict")
		}
	}
	return nil
}

func (r *AffiliateProgramRepository) StatementSnapshot(ctx context.Context, affiliateID ids.AffiliateID) (uint64, []affiliates.CommissionEntry, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var count uint64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM affiliate_attributions WHERE affiliate_id=$1 AND state='locked'`, affiliateID).Scan(&count); err != nil {
		return 0, nil, err
	}
	rows, err := tx.Query(ctx, affiliateCommissionSelect+` WHERE affiliate_id=$1 ORDER BY created_at DESC,entry_id DESC`, affiliateID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	values := make([]affiliates.CommissionEntry, 0)
	for rows.Next() {
		value, err := scanAffiliateCommission(rows)
		if err != nil {
			return 0, nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return count, values, nil
}

func (r *AffiliateProgramRepository) SettlementSnapshot(ctx context.Context, affiliateID ids.AffiliateID) (affiliateprogram.SettlementSnapshot, error) {
	var value affiliateprogram.SettlementSnapshot
	err := r.pool.QueryRow(ctx, `
		WITH earning_balance AS (
			SELECT earned.entry_id,earned.amount_minor,
			       COALESCE(sum(allocation.amount_minor) FILTER (WHERE reservation.state IN ('reserved','credited')),0)::bigint reserved_minor,
			       COALESCE(sum(allocation.amount_minor) FILTER (WHERE reservation.state='settled'),0)::bigint settled_minor
			FROM affiliate_commission_entries earned
			JOIN affiliate_commission_entries maturity ON maturity.source_entry_id=earned.entry_id AND maturity.kind='maturity'
			LEFT JOIN affiliate_credit_allocations allocation ON allocation.earning_entry_id=earned.entry_id
			LEFT JOIN affiliate_credit_reservations reservation ON reservation.reservation_id=allocation.reservation_id
			WHERE earned.affiliate_id=$1 AND earned.kind='earned'
			  AND NOT EXISTS (SELECT 1 FROM affiliate_commission_entries reversal WHERE reversal.reverses_entry_id=earned.entry_id AND reversal.kind='reversal')
			GROUP BY earned.entry_id,earned.amount_minor
		), policy AS (
			SELECT settlement.check_threshold_minor
			FROM affiliate_settlement_policy_current current_policy
			JOIN affiliate_settlement_policies settlement ON settlement.version=current_policy.policy_version
			WHERE current_policy.singleton=true
		), recovery AS (
			SELECT COALESCE(sum(amount_minor),0)::bigint amount_minor
			FROM affiliate_credit_reversal_adjustments
			WHERE affiliate_id=$1 AND kind='support_check_recovery' AND state='applied'
		)
		SELECT GREATEST(COALESCE(sum(earning_balance.amount_minor-reserved_minor-settled_minor),0)::bigint-(SELECT amount_minor FROM recovery),0),
		       COALESCE(sum(reserved_minor),0)::bigint,COALESCE(sum(settled_minor),0)::bigint,
		       (SELECT check_threshold_minor FROM policy)
		FROM earning_balance`, affiliateID).Scan(&value.AvailableMinor, &value.ReservedMinor, &value.SettledMinor, &value.CheckThresholdMinor)
	return value, err
}

func (r *AffiliateProgramRepository) DataExport(ctx context.Context, userID ids.UserID) (affiliateprogram.DataExport, error) {
	var raw []byte
	if err := r.pool.QueryRow(ctx, `SELECT spyglass_export_affiliate_data($1)`, userID).Scan(&raw); err != nil {
		return affiliateprogram.DataExport{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value affiliateprogram.DataExport
	if err := decoder.Decode(&value); err != nil {
		return affiliateprogram.DataExport{}, fmt.Errorf("decode Affiliate data export: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return affiliateprogram.DataExport{}, errors.New("Affiliate data export must contain one JSON document")
	}
	return value, nil
}

const affiliateCommissionSelect = `
	SELECT affiliate_commission_entries.entry_id,affiliate_commission_entries.affiliate_id,affiliate_commission_entries.attribution_id,
	       affiliate_commission_entries.rule_version,affiliate_commission_entries.provider_subscription_id,
	       affiliate_commission_entries.provider_invoice_id,COALESCE(affiliate_commission_entries.provider_payment_intent_id,''),
	       affiliate_commission_entries.cycle,affiliate_commission_entries.kind,affiliate_commission_entries.state,
	       affiliate_commission_entries.amount_minor,affiliate_commission_entries.currency,
	       affiliate_commission_entries.reverses_entry_id::text,affiliate_commission_entries.source_entry_id::text,
	       affiliate_commission_entries.available_at,affiliate_commission_entries.created_at
	FROM affiliate_commission_entries`

func scanAffiliateCommission(row pgx.Row) (affiliates.CommissionEntry, error) {
	var value affiliates.CommissionEntry
	var reverses, source *string
	err := row.Scan(&value.ID, &value.AffiliateID, &value.AttributionID, &value.RuleVersion,
		&value.SubscriptionID, &value.InvoiceID, &value.PaymentIntentID, &value.Cycle, &value.Kind, &value.State,
		&value.AmountMinor, &value.Currency, &reverses, &source, &value.AvailableAt, &value.CreatedAt)
	if reverses != nil {
		identifier := ids.CommissionEntryID(*reverses)
		value.ReversesID = &identifier
	}
	if source != nil {
		identifier := ids.CommissionEntryID(*source)
		value.SourceID = &identifier
	}
	return value, err
}

func sameCommissionEvidence(left, right affiliates.CommissionEntry) bool {
	return left.AffiliateID == right.AffiliateID && left.AttributionID == right.AttributionID &&
		left.RuleVersion == right.RuleVersion && left.SubscriptionID == right.SubscriptionID &&
		left.InvoiceID == right.InvoiceID && left.PaymentIntentID == right.PaymentIntentID && left.Cycle == right.Cycle && left.Kind == right.Kind &&
		left.State == right.State && left.AmountMinor == right.AmountMinor && left.Currency == right.Currency &&
		sameCommissionEntryReference(left.ReversesID, right.ReversesID) && sameCommissionEntryReference(left.SourceID, right.SourceID)
}

func sameCommissionEntryReference(left, right *ids.CommissionEntryID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

var _ affiliateprogram.Repository = (*AffiliateProgramRepository)(nil)
