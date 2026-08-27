package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesettlement"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AffiliateSettlementRepository struct{ pool *pgxpool.Pool }

func NewAffiliateSettlementRepository(pool *pgxpool.Pool) *AffiliateSettlementRepository {
	return &AffiliateSettlementRepository{pool: pool}
}

func (r *AffiliateSettlementRepository) PrepareInvoiceCredits(ctx context.Context, reservationSeed string, accountID ids.AccountID, invoiceID string, amountDueMinor int64, currency string, now time.Time) ([]affiliatesettlement.Reservation, error) {
	if ids.Validate(reservationSeed) != nil || ids.Validate(string(accountID)) != nil || amountDueMinor <= 0 || now.IsZero() {
		return nil, affiliatesettlement.ErrInvalidInvoice
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "spyglass:affiliate-invoice-credit:"+string(accountID)); err != nil {
		return nil, err
	}
	var policyVersion uint64
	var policyCurrency string
	if err := tx.QueryRow(ctx, `
		SELECT policy.version,policy.currency
		FROM affiliate_settlement_policy_current current_policy
		JOIN affiliate_settlement_policies policy ON policy.version=current_policy.policy_version
		WHERE current_policy.singleton=true AND policy.mode='account_credit_with_support_check' AND policy.effective_from<=$1`, now.UTC()).Scan(&policyVersion, &policyCurrency); err != nil {
		return nil, err
	}
	if policyCurrency != currency {
		return nil, affiliatesettlement.ErrInvalidInvoice
	}
	rows, err := tx.Query(ctx, `
		SELECT enrollment.affiliate_id,COALESCE(profile.stripe_customer_id,'')
		FROM affiliate_enrollments enrollment
		LEFT JOIN billing_profiles profile ON profile.account_id=enrollment.settlement_account_id
		WHERE enrollment.settlement_account_id=$1
		ORDER BY enrollment.created_at,enrollment.affiliate_id`, accountID)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		affiliateID ids.AffiliateID
		customerID  string
	}
	candidates := make([]candidate, 0)
	for rows.Next() {
		var value candidate
		if err := rows.Scan(&value.affiliateID, &value.customerID); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	remainingInvoice := amountDueMinor
	reservations := make([]affiliatesettlement.Reservation, 0, len(candidates))
	seedAvailable := true
	for _, candidate := range candidates {
		if remainingInvoice <= 0 {
			break
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "spyglass:affiliate-credit:"+string(candidate.affiliateID)); err != nil {
			return nil, err
		}
		existing, err := scanAffiliateCreditReservation(tx.QueryRow(ctx, affiliateCreditReservationSelect+`
			WHERE affiliate_id=$1 AND provider_invoice_id=$2`, candidate.affiliateID, invoiceID))
		if err == nil {
			if existing.SettlementAccountID != accountID || existing.Currency != currency || existing.ProviderCustomerID != candidate.customerID || existing.PolicyVersion != policyVersion {
				return nil, affiliatesettlement.ErrSettlementConflict
			}
			if existing.State == "released" {
				continue
			}
			reservations = append(reservations, existing)
			remainingInvoice -= existing.AmountMinor
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var recoveryRemaining int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(sum(amount_minor),0)::bigint
			FROM affiliate_credit_reversal_adjustments
			WHERE affiliate_id=$1 AND kind='support_check_recovery' AND state='applied' AND currency=$2`,
			candidate.affiliateID, currency).Scan(&recoveryRemaining); err != nil {
			return nil, err
		}
		availableRows, err := tx.Query(ctx, `
			SELECT earned.entry_id,earned.amount_minor-COALESCE(sum(allocation.amount_minor) FILTER (WHERE reservation.state<>'released'),0)::bigint AS remaining_minor
			FROM affiliate_commission_entries earned
			JOIN affiliate_commission_entries maturity ON maturity.source_entry_id=earned.entry_id AND maturity.kind='maturity'
			LEFT JOIN affiliate_credit_allocations allocation ON allocation.earning_entry_id=earned.entry_id
			LEFT JOIN affiliate_credit_reservations reservation ON reservation.reservation_id=allocation.reservation_id
			WHERE earned.affiliate_id=$1 AND earned.kind='earned' AND earned.currency=$2
			  AND NOT EXISTS (SELECT 1 FROM affiliate_commission_entries reversal WHERE reversal.reverses_entry_id=earned.entry_id AND reversal.kind='reversal')
			GROUP BY earned.entry_id,earned.amount_minor,maturity.created_at
			HAVING earned.amount_minor-COALESCE(sum(allocation.amount_minor) FILTER (WHERE reservation.state<>'released'),0)::bigint>0
			ORDER BY maturity.created_at,earned.entry_id`, candidate.affiliateID, currency)
		if err != nil {
			return nil, err
		}
		type allocation struct {
			entryID ids.CommissionEntryID
			amount  int64
		}
		allocations := make([]allocation, 0)
		remaining := remainingInvoice
		for availableRows.Next() && remaining > 0 {
			var value allocation
			if err := availableRows.Scan(&value.entryID, &value.amount); err != nil {
				availableRows.Close()
				return nil, err
			}
			if recoveryRemaining >= value.amount {
				recoveryRemaining -= value.amount
				continue
			}
			if recoveryRemaining > 0 {
				value.amount -= recoveryRemaining
				recoveryRemaining = 0
			}
			if value.amount > remaining {
				value.amount = remaining
			}
			allocations = append(allocations, value)
			remaining -= value.amount
		}
		if err := availableRows.Err(); err != nil {
			availableRows.Close()
			return nil, err
		}
		availableRows.Close()
		amount := remainingInvoice - remaining
		if amount <= 0 {
			continue
		}
		if candidate.customerID == "" {
			return nil, affiliatesettlement.ErrCustomerUnavailable
		}
		reservationID := reservationSeed
		if !seedAvailable {
			if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&reservationID); err != nil {
				return nil, err
			}
		}
		seedAvailable = false
		if _, err := tx.Exec(ctx, `
			INSERT INTO affiliate_credit_reservations
				(reservation_id,affiliate_id,settlement_account_id,provider_invoice_id,policy_version,kind,state,amount_minor,currency,provider_customer_id,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,'invoice_credit','reserved',$6,$7,$8,1,$9,$9)`, reservationID, candidate.affiliateID,
			accountID, invoiceID, policyVersion, amount, currency, candidate.customerID, now.UTC()); err != nil {
			return nil, err
		}
		for _, allocation := range allocations {
			if _, err := tx.Exec(ctx, `INSERT INTO affiliate_credit_allocations (reservation_id,earning_entry_id,amount_minor,created_at) VALUES ($1,$2,$3,$4)`, reservationID, allocation.entryID, allocation.amount, now.UTC()); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO affiliate_credit_reservation_events (event_id,reservation_id,version,action,state,occurred_at) VALUES (gen_random_uuid(),$1,1,'reserved','reserved',$2)`, reservationID, now.UTC()); err != nil {
			return nil, err
		}
		reservations = append(reservations, affiliatesettlement.Reservation{ID: reservationID, AffiliateID: candidate.affiliateID,
			SettlementAccountID: accountID, ProviderInvoiceID: invoiceID, PolicyVersion: policyVersion, State: "reserved", AmountMinor: amount,
			Currency: currency, ProviderCustomerID: candidate.customerID})
		remainingInvoice -= amount
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return reservations, nil
}

func (r *AffiliateSettlementRepository) CompleteInvoiceCredit(ctx context.Context, reservationID, customerID, transactionID string, now time.Time) (affiliatesettlement.Reservation, error) {
	if ids.Validate(reservationID) != nil || now.IsZero() {
		return affiliatesettlement.Reservation{}, affiliatesettlement.ErrSettlementConflict
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return affiliatesettlement.Reservation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanAffiliateCreditReservation(tx.QueryRow(ctx, affiliateCreditReservationSelect+` WHERE reservation_id=$1 FOR UPDATE`, reservationID))
	if err != nil {
		return affiliatesettlement.Reservation{}, err
	}
	if current.State == "credited" || current.State == "settled" {
		if current.ProviderCustomerID != customerID || current.ProviderTransactionID != transactionID {
			return affiliatesettlement.Reservation{}, affiliatesettlement.ErrSettlementConflict
		}
		return current, tx.Commit(ctx)
	}
	if current.State != "reserved" || current.ProviderCustomerID != customerID {
		return affiliatesettlement.Reservation{}, affiliatesettlement.ErrSettlementConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE affiliate_credit_reservations SET state='credited',provider_balance_transaction_id=$2,version=version+1,updated_at=$3 WHERE reservation_id=$1`, reservationID, transactionID, now.UTC()); err != nil {
		return affiliatesettlement.Reservation{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO affiliate_credit_reservation_events (event_id,reservation_id,version,action,state,provider_reference,occurred_at) VALUES (gen_random_uuid(),$1,2,'credited','credited',$2,$3)`, reservationID, transactionID, now.UTC()); err != nil {
		return affiliatesettlement.Reservation{}, err
	}
	current.State, current.ProviderTransactionID = "credited", transactionID
	if err := tx.Commit(ctx); err != nil {
		return affiliatesettlement.Reservation{}, err
	}
	return current, nil
}

func (r *AffiliateSettlementRepository) SettleInvoiceCredits(ctx context.Context, invoiceID string, now time.Time) (int64, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT reservation_id,version,amount_minor FROM affiliate_credit_reservations WHERE provider_invoice_id=$1 AND state='credited' ORDER BY reservation_id FOR UPDATE`, invoiceID)
	if err != nil {
		return 0, err
	}
	type item struct {
		id      string
		version uint64
		amount  int64
	}
	items := make([]item, 0)
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.id, &value.version, &value.amount); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	var settled int64
	for _, item := range items {
		version := item.version + 1
		if _, err := tx.Exec(ctx, `UPDATE affiliate_credit_reservations SET state='settled',version=$2,updated_at=$3 WHERE reservation_id=$1`, item.id, version, now.UTC()); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO affiliate_credit_reservation_events (event_id,reservation_id,version,action,state,occurred_at) VALUES (gen_random_uuid(),$1,$2,'settled','settled',$3)`, item.id, version, now.UTC()); err != nil {
			return 0, err
		}
		settled += item.amount
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return settled, nil
}

func (r *AffiliateSettlementRepository) PrepareReversalAdjustments(ctx context.Context, adjustmentSeed, providerObjectID string, now time.Time) ([]affiliatesettlement.ReversalAdjustment, error) {
	if ids.Validate(adjustmentSeed) != nil || now.IsZero() ||
		(!strings.HasPrefix(providerObjectID, "re_") && !strings.HasPrefix(providerObjectID, "dp_") && !strings.HasPrefix(providerObjectID, "cn_")) {
		return nil, affiliatesettlement.ErrSettlementConflict
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var reversalID ids.CommissionEntryID
	var affiliateID ids.AffiliateID
	err = tx.QueryRow(ctx, `
		SELECT reversal.entry_id,original.affiliate_id
		FROM affiliate_provider_adverse_events adverse
		JOIN affiliate_commission_entries original ON original.kind='earned' AND (
			EXISTS (
				SELECT 1 FROM affiliate_commission_invoice_payments payment
				WHERE payment.earning_entry_id=original.entry_id AND payment.provider_payment_intent_id=adverse.provider_payment_intent_id
			) OR (
				adverse.kind='credit_note' AND original.provider_invoice_id=adverse.provider_invoice_id AND EXISTS (
					SELECT 1 FROM affiliate_commission_invoice_lines invoice_line
					JOIN affiliate_provider_adverse_invoice_lines adverse_line ON adverse_line.provider_invoice_line_id=invoice_line.provider_invoice_line_id
					WHERE invoice_line.earning_entry_id=original.entry_id AND adverse_line.provider_event_id=adverse.provider_event_id
				)
			)
		)
		JOIN affiliate_commission_entries reversal ON reversal.reverses_entry_id=original.entry_id AND reversal.kind='reversal'
		WHERE adverse.provider_object_id=$1
		LIMIT 1`, providerObjectID).Scan(&reversalID, &affiliateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "spyglass:affiliate-credit:"+string(affiliateID)); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT reservation.reservation_id,reservation.kind,reservation.state,
		       reservation.settlement_account_id::text,COALESCE(reservation.provider_customer_id,''),
		       allocation.amount_minor,reservation.currency,reservation.version
		FROM affiliate_commission_entries reversal
		JOIN affiliate_credit_allocations allocation ON allocation.earning_entry_id=reversal.reverses_entry_id
		JOIN affiliate_credit_reservations reservation ON reservation.reservation_id=allocation.reservation_id
		WHERE reversal.entry_id=$1
		ORDER BY reservation.created_at,reservation.reservation_id
		FOR UPDATE OF reservation`, reversalID)
	if err != nil {
		return nil, err
	}
	type allocatedReservation struct {
		id, kind, state, customerID, currency string
		accountID                             *string
		amount                                int64
		version                               uint64
	}
	allocated := make([]allocatedReservation, 0)
	for rows.Next() {
		var value allocatedReservation
		if err := rows.Scan(&value.id, &value.kind, &value.state, &value.accountID, &value.customerID,
			&value.amount, &value.currency, &value.version); err != nil {
			rows.Close()
			return nil, err
		}
		allocated = append(allocated, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	result := make([]affiliatesettlement.ReversalAdjustment, 0, len(allocated))
	seedAvailable := true
	for _, reservation := range allocated {
		if reservation.state == "released" {
			continue
		}
		if reservation.state == "reserved" {
			version := reservation.version + 1
			if _, err := tx.Exec(ctx, `UPDATE affiliate_credit_reservations SET state='released',version=$2,updated_at=$3 WHERE reservation_id=$1`, reservation.id, version, now.UTC()); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO affiliate_credit_reservation_events (event_id,reservation_id,version,action,state,occurred_at) VALUES (gen_random_uuid(),$1,$2,'released','released',$3)`, reservation.id, version, now.UTC()); err != nil {
				return nil, err
			}
			continue
		}
		kind, state := "", ""
		var accountID ids.AccountID
		if reservation.kind == "invoice_credit" && (reservation.state == "credited" || reservation.state == "settled") {
			if reservation.accountID == nil || ids.Validate(*reservation.accountID) != nil || !strings.HasPrefix(reservation.customerID, "cus_") {
				return nil, affiliatesettlement.ErrSettlementConflict
			}
			kind, state, accountID = "customer_balance_debit", "pending", ids.AccountID(*reservation.accountID)
		} else if reservation.kind == "support_check" && reservation.state == "settled" {
			kind, state = "support_check_recovery", "applied"
		} else {
			return nil, affiliatesettlement.ErrSettlementConflict
		}
		existing, err := scanAffiliateReversalAdjustment(tx.QueryRow(ctx, affiliateReversalAdjustmentSelect+`
			WHERE reversal_entry_id=$1 AND reservation_id=$2`, reversalID, reservation.id))
		if err == nil {
			if existing.AffiliateID != affiliateID || existing.Kind != kind || existing.AmountMinor != reservation.amount ||
				existing.Currency != reservation.currency || existing.SettlementAccountID != accountID ||
				existing.ProviderCustomerID != reservation.customerID {
				return nil, affiliatesettlement.ErrSettlementConflict
			}
			result = append(result, existing)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		adjustmentID := adjustmentSeed
		if !seedAvailable {
			if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&adjustmentID); err != nil {
				return nil, err
			}
		}
		seedAvailable = false
		var storedAccount, storedCustomer any
		if accountID != "" {
			storedAccount = accountID
		}
		if reservation.customerID != "" {
			storedCustomer = reservation.customerID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO affiliate_credit_reversal_adjustments
				(adjustment_id,reversal_entry_id,reservation_id,affiliate_id,settlement_account_id,kind,state,
				 amount_minor,currency,provider_customer_id,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$11)`, adjustmentID, reversalID, reservation.id,
			affiliateID, storedAccount, kind, state, reservation.amount, reservation.currency, storedCustomer, now.UTC()); err != nil {
			return nil, err
		}
		action := "prepared"
		if state == "applied" {
			action = "applied"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO affiliate_credit_reversal_adjustment_events (event_id,adjustment_id,version,action,state,occurred_at) VALUES (gen_random_uuid(),$1,1,$2,$3,$4)`, adjustmentID, action, state, now.UTC()); err != nil {
			return nil, err
		}
		result = append(result, affiliatesettlement.ReversalAdjustment{ID: adjustmentID, ReversalEntryID: reversalID,
			ReservationID: reservation.id, AffiliateID: affiliateID, SettlementAccountID: accountID, Kind: kind,
			State: state, AmountMinor: reservation.amount, Currency: reservation.currency, ProviderCustomerID: reservation.customerID})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *AffiliateSettlementRepository) CompleteReversalAdjustment(ctx context.Context, adjustmentID, customerID, transactionID string, now time.Time) (affiliatesettlement.ReversalAdjustment, error) {
	if ids.Validate(adjustmentID) != nil || !strings.HasPrefix(customerID, "cus_") || !strings.HasPrefix(transactionID, "cbtxn_") || now.IsZero() {
		return affiliatesettlement.ReversalAdjustment{}, affiliatesettlement.ErrSettlementConflict
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return affiliatesettlement.ReversalAdjustment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanAffiliateReversalAdjustment(tx.QueryRow(ctx, affiliateReversalAdjustmentSelect+` WHERE adjustment_id=$1 FOR UPDATE`, adjustmentID))
	if err != nil {
		return affiliatesettlement.ReversalAdjustment{}, err
	}
	if current.State == "applied" {
		if current.ProviderCustomerID != customerID || current.ProviderTransactionID != transactionID {
			return affiliatesettlement.ReversalAdjustment{}, affiliatesettlement.ErrSettlementConflict
		}
		return current, tx.Commit(ctx)
	}
	if current.Kind != "customer_balance_debit" || current.State != "pending" || current.ProviderCustomerID != customerID {
		return affiliatesettlement.ReversalAdjustment{}, affiliatesettlement.ErrSettlementConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE affiliate_credit_reversal_adjustments SET state='applied',provider_balance_transaction_id=$2,version=version+1,updated_at=$3 WHERE adjustment_id=$1`, adjustmentID, transactionID, now.UTC()); err != nil {
		return affiliatesettlement.ReversalAdjustment{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO affiliate_credit_reversal_adjustment_events (event_id,adjustment_id,version,action,state,provider_reference,occurred_at) VALUES (gen_random_uuid(),$1,2,'applied','applied',$2,$3)`, adjustmentID, transactionID, now.UTC()); err != nil {
		return affiliatesettlement.ReversalAdjustment{}, err
	}
	current.State, current.ProviderTransactionID = "applied", transactionID
	if err := tx.Commit(ctx); err != nil {
		return affiliatesettlement.ReversalAdjustment{}, err
	}
	return current, nil
}

func (r *AffiliateSettlementRepository) ReversalProviderObjects(ctx context.Context, providerInvoiceID string) ([]string, error) {
	if !strings.HasPrefix(providerInvoiceID, "in_") {
		return nil, affiliatesettlement.ErrInvalidInvoice
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT adverse.provider_object_id
		FROM affiliate_commission_entries original
		JOIN affiliate_commission_entries reversal ON reversal.reverses_entry_id=original.entry_id AND reversal.kind='reversal'
		JOIN affiliate_provider_adverse_events adverse ON (
			EXISTS (
				SELECT 1 FROM affiliate_commission_invoice_payments payment
				WHERE payment.earning_entry_id=original.entry_id AND payment.provider_payment_intent_id=adverse.provider_payment_intent_id
			) OR (
				adverse.kind='credit_note' AND adverse.provider_invoice_id=original.provider_invoice_id AND EXISTS (
					SELECT 1 FROM affiliate_commission_invoice_lines invoice_line
					JOIN affiliate_provider_adverse_invoice_lines adverse_line ON adverse_line.provider_invoice_line_id=invoice_line.provider_invoice_line_id
					WHERE invoice_line.earning_entry_id=original.entry_id AND adverse_line.provider_event_id=adverse.provider_event_id
				)
			)
		)
		WHERE original.provider_invoice_id=$1
		ORDER BY adverse.provider_object_id`, providerInvoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

const affiliateCreditReservationSelect = `
	SELECT reservation_id,affiliate_id,settlement_account_id,COALESCE(provider_invoice_id,''),policy_version,state,amount_minor,currency,
	       COALESCE(provider_customer_id,''),COALESCE(provider_balance_transaction_id,'')
	FROM affiliate_credit_reservations`

const affiliateReversalAdjustmentSelect = `
	SELECT adjustment_id,reversal_entry_id,reservation_id,affiliate_id,settlement_account_id::text,kind,state,
	       amount_minor,currency,COALESCE(provider_customer_id,''),COALESCE(provider_balance_transaction_id,'')
	FROM affiliate_credit_reversal_adjustments`

func scanAffiliateCreditReservation(row pgx.Row) (affiliatesettlement.Reservation, error) {
	var value affiliatesettlement.Reservation
	err := row.Scan(&value.ID, &value.AffiliateID, &value.SettlementAccountID, &value.ProviderInvoiceID,
		&value.PolicyVersion, &value.State, &value.AmountMinor, &value.Currency, &value.ProviderCustomerID, &value.ProviderTransactionID)
	return value, err
}

func scanAffiliateReversalAdjustment(row pgx.Row) (affiliatesettlement.ReversalAdjustment, error) {
	var value affiliatesettlement.ReversalAdjustment
	var accountID *string
	err := row.Scan(&value.ID, &value.ReversalEntryID, &value.ReservationID, &value.AffiliateID, &accountID,
		&value.Kind, &value.State, &value.AmountMinor, &value.Currency, &value.ProviderCustomerID, &value.ProviderTransactionID)
	if accountID != nil {
		value.SettlementAccountID = ids.AccountID(*accountID)
	}
	return value, err
}

var _ affiliatesettlement.Repository = (*AffiliateSettlementRepository)(nil)
