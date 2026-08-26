package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// AffiliateAdminRepository has execute-only access to audited security-definer
// functions; its operator role needs no direct enrollment-table access.
type AffiliateAdminRepository struct{ pool *pgxpool.Pool }

func NewAffiliateAdminRepository(pool *pgxpool.Pool) *AffiliateAdminRepository {
	return &AffiliateAdminRepository{pool: pool}
}

func (r *AffiliateAdminRepository) Inspect(ctx context.Context, affiliateID ids.AffiliateID, change affiliateadmin.Change) (affiliates.Enrollment, error) {
	return scanAffiliateAdminEnrollment(r.pool.QueryRow(ctx, `
		SELECT affiliate_id,user_id,settlement_account_id,public_code,terms_version,rule_version,state,version,created_at
		FROM public.spyglass_inspect_affiliate_enrollment($1,$2,$3,$4,$5)`,
		change.EventID, affiliateID, change.Actor, change.Reason, change.Environment))
}

func (r *AffiliateAdminRepository) InspectRisk(ctx context.Context, affiliateID ids.AffiliateID, change affiliateadmin.Change) (affiliateadmin.RiskSummary, error) {
	var value affiliateadmin.RiskSummary
	err := r.pool.QueryRow(ctx, `
		SELECT affiliate_id,enrollment_state,enrollment_version,observed_at,reservation_window_started_at,
		       valid_reservations,distinct_referred_accounts,repeated_referred_accounts,
		       maximum_reservations_per_account,cross_affiliate_code_cycle_accounts,locked_attributions,
		       largest_account_share_basis_points,code_replacement_window_started_at,code_replacements
		FROM public.spyglass_inspect_affiliate_risk($1,$2,$3,$4,$5)`,
		change.EventID, affiliateID, change.Actor, change.Reason, change.Environment).Scan(
		&value.AffiliateID, &value.EnrollmentState, &value.EnrollmentVersion, &value.ObservedAt,
		&value.ReservationWindowStartedAt, &value.ValidReservations, &value.DistinctReferredAccounts,
		&value.RepeatedReferredAccounts, &value.MaximumReservationsPerAccount,
		&value.CrossAffiliateCodeCycleAccounts, &value.LockedAttributions,
		&value.LargestAccountShareBasisPoints, &value.CodeReplacementWindowStartedAt, &value.CodeReplacements)
	if err := classifyAffiliateAdminError(err); err != nil {
		return affiliateadmin.RiskSummary{}, err
	}
	if err := value.Validate(); err != nil {
		return affiliateadmin.RiskSummary{}, fmt.Errorf("Affiliate risk inspection returned invalid state: %w", err)
	}
	return value, nil
}

func (r *AffiliateAdminRepository) Transition(ctx context.Context, affiliateID ids.AffiliateID, expectedVersion uint64, state affiliates.EnrollmentState, change affiliateadmin.Change) (affiliates.Enrollment, error) {
	return scanAffiliateAdminEnrollment(r.pool.QueryRow(ctx, `
		SELECT affiliate_id,user_id,settlement_account_id,public_code,terms_version,rule_version,state,version,created_at
		FROM public.spyglass_transition_affiliate_enrollment($1,$2,$3,$4,$5,$6,$7)`,
		change.EventID, affiliateID, expectedVersion, state, change.Actor, change.Reason, change.Environment))
}

func scanAffiliateAdminEnrollment(row pgx.Row) (affiliates.Enrollment, error) {
	value, err := scanAffiliateEnrollment(row)
	if err := classifyAffiliateAdminError(err); err != nil {
		return affiliates.Enrollment{}, err
	}
	if err := value.Validate(); err != nil {
		return affiliates.Enrollment{}, fmt.Errorf("Affiliate administration returned invalid state: %w", err)
	}
	return value, nil
}

func classifyAffiliateAdminError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "22023":
			return affiliateadmin.ErrInvalidChange
		case "P0002":
			return affiliateadmin.ErrNotFound
		case "P0001":
			return affiliateadmin.ErrStateConflict
		}
	}
	return fmt.Errorf("Affiliate administration unavailable: %w", err)
}

var _ affiliateadmin.Store = (*AffiliateAdminRepository)(nil)
