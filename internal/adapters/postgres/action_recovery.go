package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tinfoyle/spyglass-engine/internal/application/actionrecovery"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ActionRecoveryRepository struct{ cell *database.CellPool }

func NewActionRecoveryRepository(cell *database.CellPool) (*ActionRecoveryRepository, error) {
	if cell == nil {
		return nil, errors.New("action recovery cell pool is required")
	}
	return &ActionRecoveryRepository{cell: cell}, nil
}

const actionSummarySelect = `SELECT l.operation_id,a.approval_id,l.invocation_id,l.capability,l.executor_id,l.executor_version,
	l.executor_policy_version,l.state,l.attempt_count,COALESCE(l.last_error_code,''),l.next_attempt_at,l.completed_at,l.started_at,l.updated_at
	FROM spyglass.runner_action_ledger l JOIN spyglass.runner_action_authorizations a
	ON a.account_id=l.account_id AND a.operation_id=l.operation_id`

func (r *ActionRecoveryRepository) List(ctx context.Context, accountID ids.AccountID, query actionrecovery.ListQuery) (actionrecovery.Page, error) {
	page := actionrecovery.Page{Items: make([]actionrecovery.Summary, 0, query.Limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, actionSummarySelect+` WHERE l.account_id=$1 AND ($2='' OR l.state=$2)
			AND ($3::timestamptz IS NULL OR (l.updated_at,l.operation_id)<($3,$4::uuid))
			ORDER BY l.updated_at DESC,l.operation_id DESC LIMIT $5`, accountID, query.State, query.AfterUpdatedAt, nullableActionID(query.AfterOperationID), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanActionSummary(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return actionrecovery.Page{}, classifyActionRecovery(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &actionrecovery.Cursor{UpdatedAt: last.UpdatedAt, OperationID: last.OperationID}
	}
	return page, nil
}

func (r *ActionRecoveryRepository) Get(ctx context.Context, accountID ids.AccountID, operationID string) (actionrecovery.Detail, error) {
	var detail actionrecovery.Detail
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		item, err := scanActionSummary(tx.QueryRow(ctx, actionSummarySelect+` WHERE l.account_id=$1 AND l.operation_id=$2`, accountID, operationID))
		if errors.Is(err, pgx.ErrNoRows) {
			return actionrecovery.ErrNotFound
		}
		if err != nil {
			return err
		}
		detail.Summary = item
		var resolution actionrecovery.Resolution
		var digest []byte
		var confirmedID *ids.UserID
		err = tx.QueryRow(ctx, `SELECT resolution_id,operation_id,requested_outcome,reason_sha256,requested_by_user_id,requested_at,state,confirmed_by_user_id,confirmed_at
			FROM spyglass.runner_action_manual_resolutions WHERE account_id=$1 AND operation_id=$2 ORDER BY requested_at DESC,resolution_id DESC LIMIT 1`, accountID, operationID).
			Scan(&resolution.ID, &resolution.OperationID, &resolution.RequestedOutcome, &digest, &resolution.RequestedByUserID, &resolution.RequestedAt, &resolution.State, &confirmedID, &resolution.ConfirmedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(digest) != sha256.Size {
			return actionrecovery.ErrRepository
		}
		copy(resolution.ReasonSHA256[:], digest)
		if confirmedID != nil {
			resolution.ConfirmedByUserID = *confirmedID
		}
		detail.Resolution = &resolution
		return nil
	})
	if err != nil {
		return actionrecovery.Detail{}, classifyActionRecovery(err)
	}
	return detail, nil
}

func (r *ActionRecoveryRepository) RequestResolution(ctx context.Context, accountID ids.AccountID, operationID, resolutionID string, outcome actionrecovery.State, reason [sha256.Size]byte, userID ids.UserID, at time.Time) error {
	return r.exec(ctx, accountID, `SELECT public.spyglass_request_runner_action_resolution_v2($1,$2,$3,$4,$5,$6,$7)`, operationID, resolutionID, outcome, reason[:], userID, at)
}
func (r *ActionRecoveryRepository) ConfirmResolution(ctx context.Context, accountID ids.AccountID, operationID, resolutionID string, userID ids.UserID, at time.Time) error {
	return r.exec(ctx, accountID, `SELECT public.spyglass_confirm_runner_action_resolution_v2($1,$2,$3,$4,$5)`, operationID, resolutionID, userID, at)
}
func (r *ActionRecoveryRepository) exec(ctx context.Context, accountID ids.AccountID, statement string, args ...any) error {
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		values := append([]any{accountID}, args...)
		var changed bool
		return tx.QueryRow(ctx, statement, values...).Scan(&changed)
	})
	return classifyActionRecovery(err)
}

type actionScanner interface{ Scan(...any) error }

func scanActionSummary(row actionScanner) (actionrecovery.Summary, error) {
	var item actionrecovery.Summary
	var attempts int64
	err := row.Scan(&item.OperationID, &item.ApprovalID, &item.InvocationID, &item.Capability, &item.ExecutorID, &item.ExecutorVersion, &item.PolicyVersion, &item.State, &attempts, &item.LastErrorCode, &item.NextAttemptAt, &item.CompletedAt, &item.StartedAt, &item.UpdatedAt)
	if err == nil {
		if attempts < 1 || attempts > 1000 || !item.State.Valid() {
			return item, actionrecovery.ErrRepository
		}
		item.AttemptCount = uint32(attempts)
	}
	return item, err
}
func nullableActionID(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func classifyActionRecovery(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, actionrecovery.ErrNotFound) || errors.Is(err, actionrecovery.ErrRepository) {
		return err
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return fmt.Errorf("%w: %v", actionrecovery.ErrRepository, err)
	}
	switch pgErr.Code {
	case "P2001":
		return actionrecovery.ErrNotFound
	case "P2004":
		return actionrecovery.ErrConstraint
	case "P2005", "23505":
		return actionrecovery.ErrConflict
	case "22023", "23514":
		return actionrecovery.ErrInvalid
	default:
		return fmt.Errorf("%w: database code %s", actionrecovery.ErrRepository, pgErr.Code)
	}
}

var _ actionrecovery.Repository = (*ActionRecoveryRepository)(nil)
