package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrightsadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// PrivacyRightsAdminRepository has execute-only access to audited
// security-definer functions; its operator role needs no direct table access.
type PrivacyRightsAdminRepository struct{ pool *pgxpool.Pool }

func NewPrivacyRightsAdminRepository(pool *pgxpool.Pool) *PrivacyRightsAdminRepository {
	return &PrivacyRightsAdminRepository{pool: pool}
}

func (r *PrivacyRightsAdminRepository) ListOpen(ctx context.Context, dueBefore time.Time, limit int, change privacyrightsadmin.Change) ([]privacyrightsadmin.QueueItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT request_id,version,kind,scope,state,requested_at,response_due_at,updated_at
		FROM public.spyglass_list_open_privacy_rights_requests($1,$2,$3,$4,$5,$6)`,
		change.EventID, dueBefore, limit, change.Actor, change.Reason, change.Environment)
	if err != nil {
		return nil, classifyPrivacyRightsAdminError(err)
	}
	defer rows.Close()
	items := make([]privacyrightsadmin.QueueItem, 0)
	for rows.Next() {
		var item privacyrightsadmin.QueueItem
		if err := rows.Scan(&item.RequestID, &item.Version, &item.Kind, &item.Scope, &item.State,
			&item.RequestedAt, &item.ResponseDueAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan privacy rights queue: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read privacy rights queue: %w", err)
	}
	return items, nil
}

func (r *PrivacyRightsAdminRepository) Inspect(ctx context.Context, requestID ids.PrivacyRightsRequestID, change privacyrightsadmin.Change) (privacy.RightsRequest, error) {
	return scanPrivacyRightsAdminRequest(r.pool.QueryRow(ctx, `
		SELECT request_id,user_id,version,kind,scope,state,verified_at,requested_at,response_due_at,updated_at
		FROM public.spyglass_inspect_privacy_rights_request($1,$2,$3,$4,$5)`,
		change.EventID, requestID, change.Actor, change.Reason, change.Environment))
}

func (r *PrivacyRightsAdminRepository) StartReview(ctx context.Context, requestID ids.PrivacyRightsRequestID, expectedVersion uint64, change privacyrightsadmin.Change) (privacy.RightsRequest, error) {
	return r.transition(ctx, requestID, expectedVersion, "review_started", privacy.RightsInReview, privacyrightsadmin.ResolutionEvidence{}, change)
}

func (r *PrivacyRightsAdminRepository) Resolve(ctx context.Context, requestID ids.PrivacyRightsRequestID, expectedVersion uint64, state privacy.RightsState, evidence privacyrightsadmin.ResolutionEvidence, change privacyrightsadmin.Change) (privacy.RightsRequest, error) {
	return r.transition(ctx, requestID, expectedVersion, "resolved", state, evidence, change)
}

func (r *PrivacyRightsAdminRepository) transition(ctx context.Context, requestID ids.PrivacyRightsRequestID, expectedVersion uint64, action string, state privacy.RightsState, evidence privacyrightsadmin.ResolutionEvidence, change privacyrightsadmin.Change) (privacy.RightsRequest, error) {
	var evidenceID any
	var evidenceSHA any
	if evidence.ID != "" {
		evidenceID = evidence.ID
		evidenceSHA = evidence.SHA256[:]
	}
	return scanPrivacyRightsAdminRequest(r.pool.QueryRow(ctx, `
		SELECT request_id,user_id,version,kind,scope,state,verified_at,requested_at,response_due_at,updated_at
		FROM public.spyglass_transition_privacy_rights_request($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		change.EventID, requestID, expectedVersion, action, state, evidenceID, evidenceSHA,
		change.Actor, change.Reason, change.Environment))
}

func scanPrivacyRightsAdminRequest(row pgx.Row) (privacy.RightsRequest, error) {
	var request privacy.RightsRequest
	err := row.Scan(&request.ID, &request.UserID, &request.Version, &request.Kind, &request.Scope, &request.State,
		&request.VerifiedAt, &request.RequestedAt, &request.ResponseDueAt, &request.UpdatedAt)
	if err := classifyPrivacyRightsAdminError(err); err != nil {
		return privacy.RightsRequest{}, err
	}
	if err := request.Validate(); err != nil {
		return privacy.RightsRequest{}, fmt.Errorf("privacy rights administration returned invalid state: %w", err)
	}
	return request, nil
}

func classifyPrivacyRightsAdminError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "22023":
			return privacyrightsadmin.ErrInvalidChange
		case "P0002":
			return privacyrightsadmin.ErrNotFound
		case "P0001":
			return privacyrightsadmin.ErrStateConflict
		}
	}
	return fmt.Errorf("privacy rights administration unavailable: %w", err)
}

var _ privacyrightsadmin.Store = (*PrivacyRightsAdminRepository)(nil)
