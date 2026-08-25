package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesupportadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// AffiliateSupportAdminRepository uses only audited security-definer
// functions. The operator role receives no direct support-table privileges.
type AffiliateSupportAdminRepository struct{ pool *pgxpool.Pool }

func NewAffiliateSupportAdminRepository(pool *pgxpool.Pool) *AffiliateSupportAdminRepository {
	return &AffiliateSupportAdminRepository{pool: pool}
}

func (r *AffiliateSupportAdminRepository) Inspect(ctx context.Context, requestID ids.AffiliateSupportRequestID, change affiliatesupportadmin.Change) (affiliates.SupportRequest, error) {
	return scanAffiliateSupportAdmin(r.pool.QueryRow(ctx, `
		SELECT request_id,affiliate_id,user_id,kind,commission_entry_id,state,outcome,version,created_at,updated_at
		FROM public.spyglass_inspect_affiliate_support_request($1,$2,$3,$4,$5)`,
		change.EventID, requestID, change.Actor, change.Reason, change.Environment))
}

func (r *AffiliateSupportAdminRepository) Transition(ctx context.Context, requestID ids.AffiliateSupportRequestID, expectedVersion uint64, state affiliates.SupportState, outcome affiliates.SupportOutcome, change affiliatesupportadmin.Change) (affiliates.SupportRequest, error) {
	action := "resolved"
	var databaseOutcome any = outcome
	if state == affiliates.SupportInReview {
		action, databaseOutcome = "review_started", nil
	}
	return scanAffiliateSupportAdmin(r.pool.QueryRow(ctx, `
		SELECT request_id,affiliate_id,user_id,kind,commission_entry_id,state,outcome,version,created_at,updated_at
		FROM public.spyglass_transition_affiliate_support_request($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		change.EventID, requestID, expectedVersion, action, state, databaseOutcome, change.Actor, change.Reason, change.Environment))
}

func scanAffiliateSupportAdmin(row pgx.Row) (affiliates.SupportRequest, error) {
	value, err := scanAffiliateSupportRequest(row)
	if err := classifyAffiliateSupportAdminError(err); err != nil {
		return affiliates.SupportRequest{}, err
	}
	if err := value.Validate(); err != nil {
		return affiliates.SupportRequest{}, fmt.Errorf("Affiliate support administration returned invalid state: %w", err)
	}
	return value, nil
}

func classifyAffiliateSupportAdminError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "22023":
			return affiliatesupportadmin.ErrInvalidChange
		case "P0002":
			return affiliatesupportadmin.ErrNotFound
		case "P0001":
			return affiliatesupportadmin.ErrStateConflict
		}
	}
	return fmt.Errorf("Affiliate support administration unavailable: %w", err)
}

var _ affiliatesupportadmin.Store = (*AffiliateSupportAdminRepository)(nil)
