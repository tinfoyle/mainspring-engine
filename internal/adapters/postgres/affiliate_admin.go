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
