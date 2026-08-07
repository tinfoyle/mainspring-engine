package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var (
	ErrApprovalNotFound = errors.New("approval request not found")
	ErrApprovalDecided  = errors.New("approval request was already decided")
	ErrApprovalChanged  = errors.New("the proposed action changed after it was presented for approval")
)

type Approval struct {
	ID             string
	ActionID       domain.ActionID
	RunID          domain.RunID
	InvocationID   domain.InvocationID
	PersonaName    string
	PersonaRole    string
	ActionType     string
	Reason         string
	Evidence       []string
	RequestPayload json.RawMessage
	ActionStatus   string
	Status         string
	RequestedAt    time.Time
	ExpiresAt      *time.Time
	DecidedAt      *time.Time
	DecidedBy      string
}

type ApprovalService struct {
	pool   *pgxpool.Pool
	ledger *ActionLedger
}

func NewApprovalService(pool *pgxpool.Pool, ledger *ActionLedger) *ApprovalService {
	return &ApprovalService{pool: pool, ledger: ledger}
}

func (s *ApprovalService) EnsureInvocationActions(ctx context.Context, invocationID domain.InvocationID) error {
	var runIDText, personaVersionID string
	var resultPayload []byte
	var grantsPayload []byte
	if err := s.pool.QueryRow(ctx, `
		SELECT ai.run_id::text, ai.persona_version_id::text, ai.result_payload, pv.tool_grants
		FROM agent_invocations ai JOIN persona_versions pv ON pv.id=ai.persona_version_id
		WHERE ai.id=$1 AND ai.status='succeeded'
	`, invocationID.String()).Scan(&runIDText, &personaVersionID, &resultPayload, &grantsPayload); err != nil {
		return fmt.Errorf("load invocation proposals: %w", err)
	}
	runID, err := domain.ParseRunID(runIDText)
	if err != nil {
		return err
	}
	var result agent.Result
	if err := json.Unmarshal(resultPayload, &result); err != nil {
		return fmt.Errorf("decode invocation proposals: %w", err)
	}
	var grants []domain.ToolGrant
	if err := json.Unmarshal(grantsPayload, &grants); err != nil {
		return fmt.Errorf("decode invocation grants: %w", err)
	}
	for index, proposal := range result.Structured.ProposedActions {
		if proposal.ActionType == "email.send" && !approvalHasGrant(grants, domain.CapabilityEmailSend) {
			return errors.New("persona proposed email.send without the required grant")
		}
		if err := s.ensureProposal(ctx, runID, invocationID, personaVersionID, index, proposal); err != nil {
			return err
		}
	}
	return nil
}

func approvalHasGrant(grants []domain.ToolGrant, capability domain.Capability) bool {
	for _, grant := range grants {
		if grant.Capability == capability {
			return true
		}
	}
	return false
}

func (s *ApprovalService) ensureProposal(ctx context.Context, runID domain.RunID, invocationID domain.InvocationID, personaVersionID string, index int, proposal agent.ProposedAction) error {
	if proposal.ActionType != "email.send" {
		return fmt.Errorf("proposed action type %q is not supported", proposal.ActionType)
	}
	if len(proposal.Payload) == 0 || !json.Valid(proposal.Payload) {
		return errors.New("proposed action payload is invalid")
	}
	key := fmt.Sprintf("run:%s:invocation:%s:action:%d", runID.String(), invocationID.String(), index+1)
	action, err := s.ledger.Prepare(ctx, &runID, key, proposal.ActionType, proposal.Payload)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(proposal.Evidence)
	if err != nil {
		return err
	}
	requestHash := sha256.Sum256(action.RequestPayload)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE external_actions SET invocation_id=$2, persona_version_id=$3, reason=$4, evidence=$5,
			payload_hash=$6, status=CASE WHEN status='prepared' THEN 'awaiting_approval' ELSE status END, updated_at=now()
		WHERE id=$1
	`, action.ID.String(), invocationID.String(), personaVersionID, proposal.Reason, evidence, requestHash[:]); err != nil {
		return fmt.Errorf("bind proposed action: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO approval_requests (id, run_id, action_id, payload_hash, status)
		VALUES ($1,$2,$3,$4,'pending') ON CONFLICT (action_id) DO NOTHING
	`, uuid.NewString(), runID.String(), action.ID.String(), requestHash[:]); err != nil {
		return fmt.Errorf("create approval request: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *ApprovalService) List(ctx context.Context, includeDecided bool) ([]Approval, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ar.id::text, ar.action_id::text, ar.run_id::text, ea.invocation_id::text,
		       COALESCE(pv.name,''), COALESCE(pv.role,''), ea.action_type, ea.reason, ea.evidence,
		       ea.request_payload, ea.status, ar.status, ar.requested_at, ar.expires_at, ar.decided_at,
		       COALESCE(ar.decided_by::text,'')
		FROM approval_requests ar
		JOIN external_actions ea ON ea.id=ar.action_id
		LEFT JOIN persona_versions pv ON pv.id=ea.persona_version_id
		WHERE ($1::boolean OR ar.status='pending')
		ORDER BY CASE WHEN ar.status='pending' THEN 0 ELSE 1 END, ar.requested_at DESC
		LIMIT 200
	`, includeDecided)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	defer rows.Close()
	var approvals []Approval
	for rows.Next() {
		var item Approval
		var actionID, runID, invocationID string
		var evidence []byte
		if err := rows.Scan(&item.ID, &actionID, &runID, &invocationID, &item.PersonaName, &item.PersonaRole,
			&item.ActionType, &item.Reason, &evidence, &item.RequestPayload, &item.ActionStatus, &item.Status,
			&item.RequestedAt, &item.ExpiresAt, &item.DecidedAt, &item.DecidedBy); err != nil {
			return nil, fmt.Errorf("scan approval: %w", err)
		}
		var parseErr error
		item.ActionID, parseErr = domain.ParseActionID(actionID)
		if parseErr != nil {
			return nil, parseErr
		}
		item.RunID, parseErr = domain.ParseRunID(runID)
		if parseErr != nil {
			return nil, parseErr
		}
		item.InvocationID, parseErr = domain.ParseInvocationID(invocationID)
		if parseErr != nil {
			return nil, parseErr
		}
		if err := json.Unmarshal(evidence, &item.Evidence); err != nil {
			return nil, err
		}
		approvals = append(approvals, item)
	}
	return approvals, rows.Err()
}

func (s *ApprovalService) Decide(ctx context.Context, approvalID, userID string, approve bool) (ExternalAction, error) {
	if _, err := uuid.Parse(approvalID); err != nil {
		return ExternalAction{}, ErrApprovalNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExternalAction{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var actionID string
	var storedHash, requestPayload []byte
	var status string
	if err := tx.QueryRow(ctx, `
		SELECT ar.action_id::text, ar.status, ar.payload_hash, ea.request_payload
		FROM approval_requests ar JOIN external_actions ea ON ea.id=ar.action_id
		WHERE ar.id=$1 FOR UPDATE OF ar,ea
	`, approvalID).Scan(&actionID, &status, &storedHash, &requestPayload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExternalAction{}, ErrApprovalNotFound
		}
		return ExternalAction{}, err
	}
	if status != "pending" {
		return ExternalAction{}, ErrApprovalDecided
	}
	actualHash := sha256.Sum256(requestPayload)
	if !equalBytes(storedHash, actualHash[:]) {
		return ExternalAction{}, ErrApprovalChanged
	}
	decision, actionStatus := "rejected", "rejected"
	if approve {
		decision, actionStatus = "approved", "prepared"
	}
	if _, err := tx.Exec(ctx, `UPDATE approval_requests SET status=$2, decided_at=now(), decided_by=$3 WHERE id=$1`, approvalID, decision, userID); err != nil {
		return ExternalAction{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE external_actions SET status=$2, updated_at=now(), last_error=CASE WHEN $2='rejected' THEN 'Rejected by owner' ELSE NULL END WHERE id=$1`, actionID, actionStatus); err != nil {
		return ExternalAction{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExternalAction{}, err
	}
	parsed, err := domain.ParseActionID(actionID)
	if err != nil {
		return ExternalAction{}, err
	}
	return s.ledger.ByID(ctx, parsed)
}

func (s *ApprovalService) PendingForRun(ctx context.Context, runID domain.RunID) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM approval_requests WHERE run_id=$1 AND status='pending'`, runID.String()).Scan(&count)
	return count, err
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
