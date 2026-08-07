package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key is already bound to different intent")
	ErrActionNotFound      = errors.New("external action not found")
	ErrManualReview        = errors.New("external action requires manual review")
)

type ExternalAction struct {
	ID                domain.ActionID
	RunID             *domain.RunID
	IdempotencyKey    string
	ActionType        string
	Status            string
	RequestPayload    json.RawMessage
	ResponsePayload   json.RawMessage
	ProviderReference string
	AttemptCount      int
	LastError         string
}

type ActionLedger struct {
	pool *pgxpool.Pool
}

func NewActionLedger(pool *pgxpool.Pool) *ActionLedger {
	return &ActionLedger{pool: pool}
}

func (l *ActionLedger) Prepare(ctx context.Context, runID *domain.RunID, idempotencyKey, actionType string, request any) (ExternalAction, error) {
	if idempotencyKey == "" || actionType == "" {
		return ExternalAction{}, errors.New("idempotency key and action type are required")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return ExternalAction{}, fmt.Errorf("encode external action request: %w", err)
	}
	actionID := domain.NewActionID()
	var runIDValue any
	if runID != nil {
		runIDValue = runID.String()
	}
	_, err = l.pool.Exec(ctx, `
		INSERT INTO external_actions (id, run_id, idempotency_key, action_type, request_payload)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (idempotency_key) DO NOTHING
	`, actionID.String(), runIDValue, idempotencyKey, actionType, payload)
	if err != nil {
		return ExternalAction{}, fmt.Errorf("prepare external action: %w", err)
	}
	action, err := l.byKey(ctx, idempotencyKey)
	if err != nil {
		return ExternalAction{}, err
	}
	var sameIntent bool
	if err := l.pool.QueryRow(ctx, `SELECT action_type = $2 AND request_payload = $3::jsonb FROM external_actions WHERE idempotency_key = $1`,
		idempotencyKey, actionType, payload).Scan(&sameIntent); err != nil {
		return ExternalAction{}, err
	}
	if !sameIntent {
		return ExternalAction{}, ErrIdempotencyConflict
	}
	return action, nil
}

func (l *ActionLedger) BeginExecution(ctx context.Context, id domain.ActionID, allowFailedRetry bool) (ExternalAction, bool, error) {
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return ExternalAction{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	action, err := scanAction(tx.QueryRow(ctx, `
		SELECT id::text, run_id::text, idempotency_key, action_type, status, request_payload,
		       response_payload, COALESCE(provider_reference, ''), attempt_count, COALESCE(last_error, '')
		FROM external_actions WHERE id = $1 FOR UPDATE
	`, id.String()))
	if err != nil {
		return ExternalAction{}, false, err
	}
	switch action.Status {
	case "succeeded", "executing":
		return action, false, tx.Commit(ctx)
	case "unknown", "manual_review", "awaiting_approval":
		return action, false, ErrManualReview
	case "failed":
		if !allowFailedRetry {
			return action, false, ErrManualReview
		}
	case "prepared":
	default:
		return action, false, fmt.Errorf("unsupported external action state %q", action.Status)
	}
	if _, err := tx.Exec(ctx, `UPDATE external_actions SET status = 'executing', attempt_count = attempt_count + 1, last_error = NULL, updated_at = now() WHERE id = $1`, id.String()); err != nil {
		return ExternalAction{}, false, err
	}
	action.Status = "executing"
	action.AttemptCount++
	return action, true, tx.Commit(ctx)
}

func (l *ActionLedger) MarkSucceeded(ctx context.Context, id domain.ActionID, response any, providerReference string) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return l.finish(ctx, id, "succeeded", payload, providerReference, "")
}

func (l *ActionLedger) MarkFailed(ctx context.Context, id domain.ActionID, actionErr error) error {
	if actionErr == nil {
		actionErr = errors.New("external action failed")
	}
	return l.finish(ctx, id, "failed", nil, "", actionErr.Error())
}

func (l *ActionLedger) MarkUnknown(ctx context.Context, id domain.ActionID, actionErr error) error {
	if actionErr == nil {
		actionErr = errors.New("external action outcome is unknown")
	}
	return l.finish(ctx, id, "unknown", nil, "", actionErr.Error())
}

func (l *ActionLedger) finish(ctx context.Context, id domain.ActionID, status string, response json.RawMessage, providerReference, lastError string) error {
	command, err := l.pool.Exec(ctx, `
		UPDATE external_actions SET status = $2, response_payload = $3, provider_reference = NULLIF($4, ''),
		       last_error = NULLIF($5, ''), updated_at = now()
		WHERE id = $1 AND status = 'executing'
	`, id.String(), status, response, providerReference, lastError)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrActionNotFound
	}
	return nil
}

func (l *ActionLedger) byKey(ctx context.Context, key string) (ExternalAction, error) {
	return scanAction(l.pool.QueryRow(ctx, `
		SELECT id::text, run_id::text, idempotency_key, action_type, status, request_payload,
		       response_payload, COALESCE(provider_reference, ''), attempt_count, COALESCE(last_error, '')
		FROM external_actions WHERE idempotency_key = $1
	`, key))
}

func (l *ActionLedger) ByID(ctx context.Context, id domain.ActionID) (ExternalAction, error) {
	return scanAction(l.pool.QueryRow(ctx, `
		SELECT id::text, run_id::text, idempotency_key, action_type, status, request_payload,
		       response_payload, COALESCE(provider_reference, ''), attempt_count, COALESCE(last_error, '')
		FROM external_actions WHERE id=$1
	`, id.String()))
}

type rowScanner interface {
	Scan(...any) error
}

func scanAction(row rowScanner) (ExternalAction, error) {
	var action ExternalAction
	var id string
	var runID *string
	if err := row.Scan(&id, &runID, &action.IdempotencyKey, &action.ActionType, &action.Status, &action.RequestPayload,
		&action.ResponsePayload, &action.ProviderReference, &action.AttemptCount, &action.LastError); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExternalAction{}, ErrActionNotFound
		}
		return ExternalAction{}, err
	}
	parsedID, err := domain.ParseActionID(id)
	if err != nil {
		return ExternalAction{}, err
	}
	action.ID = parsedID
	if runID != nil {
		parsedRunID, err := domain.ParseRunID(*runID)
		if err != nil {
			return ExternalAction{}, err
		}
		action.RunID = &parsedRunID
	}
	return action, nil
}
