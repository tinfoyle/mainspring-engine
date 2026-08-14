package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	WorkItemID     string
	WorkItemNumber int64
	WorkItemTitle  string
}

type ApprovalService struct {
	pool   *pgxpool.Pool
	ledger *ActionLedger
}

const (
	TicketCreateAction = "tickets.create"
	WorkReviewAction   = "work.review"
)

type TicketCreatePayload struct {
	Title            string `json:"title"`
	Description      string `json:"description"`
	Priority         string `json:"priority"`
	Origin           string `json:"origin"`
	SearchQuery      string `json:"search_query"`
	ParentWorkItemID string `json:"parent_work_item_id,omitempty"`
}

type WorkReviewPayload struct {
	WorkItemID      string   `json:"work_item_id"`
	WorkItemNumber  int64    `json:"work_item_number"`
	Title           string   `json:"title"`
	Summary         string   `json:"summary"`
	Findings        []string `json:"findings,omitempty"`
	Recommendations []string `json:"recommendations,omitempty"`
}

func DecodeWorkReviewPayload(payload json.RawMessage) (WorkReviewPayload, error) {
	var input WorkReviewPayload
	if err := json.Unmarshal(payload, &input); err != nil {
		return WorkReviewPayload{}, errors.New("work review payload is invalid")
	}
	input.WorkItemID = strings.TrimSpace(input.WorkItemID)
	input.Title = strings.TrimSpace(input.Title)
	input.Summary = strings.TrimSpace(input.Summary)
	if _, err := uuid.Parse(input.WorkItemID); err != nil {
		return WorkReviewPayload{}, errors.New("work review item is invalid")
	}
	if input.WorkItemNumber <= 0 || input.Title == "" || input.Summary == "" {
		return WorkReviewPayload{}, errors.New("work review is incomplete")
	}
	return input, nil
}

func DecodeTicketCreatePayload(payload json.RawMessage) (TicketCreatePayload, error) {
	var input TicketCreatePayload
	if err := json.Unmarshal(payload, &input); err != nil {
		return TicketCreatePayload{}, errors.New("proposed ticket payload is invalid")
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Priority = strings.ToLower(strings.TrimSpace(input.Priority))
	input.Origin = strings.ToLower(strings.TrimSpace(input.Origin))
	input.SearchQuery = strings.TrimSpace(input.SearchQuery)
	input.ParentWorkItemID = strings.TrimSpace(input.ParentWorkItemID)
	if input.Title == "" || len(input.Title) > 240 {
		return TicketCreatePayload{}, errors.New("proposed ticket title must contain between 1 and 240 characters")
	}
	if input.Description == "" || len(input.Description) > 6000 {
		return TicketCreatePayload{}, errors.New("proposed ticket description must contain between 1 and 6000 characters")
	}
	switch input.Priority {
	case "low", "normal", "high", "urgent":
	default:
		return TicketCreatePayload{}, errors.New("proposed ticket priority is invalid")
	}
	if input.Origin != "unknown_answer" && input.Origin != "direct_request" {
		return TicketCreatePayload{}, errors.New("proposed ticket origin is invalid")
	}
	if input.Origin == "unknown_answer" {
		if input.SearchQuery == "" {
			return TicketCreatePayload{}, errors.New("unknown-answer ticket must record its document search query")
		}
		if !strings.Contains(strings.ToLower(input.Description), "work plan:") || !strings.Contains(strings.ToLower(input.Description), "definition of done:") {
			return TicketCreatePayload{}, errors.New("unknown-answer ticket must include a work plan and definition of done")
		}
	}
	if input.ParentWorkItemID != "" {
		if _, err := uuid.Parse(input.ParentWorkItemID); err != nil {
			return TicketCreatePayload{}, errors.New("proposed ticket parent work item is invalid")
		}
	}
	return input, nil
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
		FROM agent_invocations ai
		JOIN persona_versions pv ON pv.id=ai.persona_version_id
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
	proposals := result.Structured.ProposedActions
	for index, proposal := range proposals {
		switch proposal.ActionType {
		case "email.send":
			if !approvalHasGrant(grants, domain.CapabilityEmailSend) {
				return errors.New("persona proposed email.send without the required grant")
			}
		case TicketCreateAction:
			if !approvalHasGrant(grants, domain.CapabilityTicketCreate) {
				return errors.New("persona proposed tickets.create without the required grant")
			}
		case WorkInputAction:
			if !approvalHasGrant(grants, domain.CapabilityTicketCreate) {
				return errors.New("persona requested owner input without the required ticket grant")
			}
			if err := s.ensureHumanInput(ctx, runID, invocationID, proposal.Payload); err != nil {
				return err
			}
			continue
		case WorkReviewAction:
			if _, err := DecodeWorkReviewPayload(proposal.Payload); err != nil {
				return err
			}
			payload, err := DecodeTicketCreatePayload(proposal.Payload)
			if err != nil {
				return err
			}
			if payload.Origin == "unknown_answer" {
				if result.Structured.Confidence != "low" {
					return errors.New("unknown-answer ticket requires a low-confidence answer")
				}
				searched, _, err := s.runDocumentSearch(ctx, runID)
				if err != nil {
					return err
				}
				if !searched {
					return errors.New("unknown-answer ticket requires a completed document search")
				}
			}
		default:
			return fmt.Errorf("proposed action type %q is not supported", proposal.ActionType)
		}
		if err := s.ensureProposal(ctx, runID, invocationID, personaVersionID, index, proposal); err != nil {
			return err
		}
	}
	return nil
}

func (s *ApprovalService) invocationDocumentSearch(ctx context.Context, invocationID domain.InvocationID) (bool, string, error) {
	var searched bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM agent_invocation_events
			WHERE invocation_id=$1 AND event_type='tool.completed' AND payload->>'name'='documents.search'
		)
	`, invocationID.String()).Scan(&searched)
	if err != nil {
		return false, "", fmt.Errorf("verify unknown-answer document search: %w", err)
	}
	var query string
	if searched {
		_ = s.pool.QueryRow(ctx, `
			SELECT COALESCE(payload->'arguments'->>'query','')
			FROM agent_invocation_events
			WHERE invocation_id=$1 AND event_type='tool.requested' AND payload->>'name'='documents.search'
			ORDER BY event_sequence DESC LIMIT 1
		`, invocationID.String()).Scan(&query)
	}
	return searched, strings.TrimSpace(query), nil
}

func (s *ApprovalService) runDocumentSearch(ctx context.Context, runID domain.RunID) (bool, string, error) {
	var invocationIDText string
	err := s.pool.QueryRow(ctx, `
		SELECT ai.id::text
		FROM agent_invocations ai
		WHERE ai.run_id=$1
		  AND EXISTS (
			SELECT 1
			FROM agent_invocation_events event
			WHERE event.invocation_id=ai.id
			  AND event.event_type='tool.completed'
			  AND event.payload->>'name'='documents.search'
		  )
		ORDER BY ai.turn_number
		LIMIT 1
	`, runID.String()).Scan(&invocationIDText)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("find run document search: %w", err)
	}
	invocationID, err := domain.ParseInvocationID(invocationIDText)
	if err != nil {
		return false, "", err
	}
	return s.invocationDocumentSearch(ctx, invocationID)
}

func (s *ApprovalService) EnsureRunActions(ctx context.Context, runID domain.RunID) error {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM agent_invocations WHERE run_id=$1 AND status='succeeded' ORDER BY turn_number`, runID.String())
	if err != nil {
		return fmt.Errorf("list run invocations for actions: %w", err)
	}
	defer rows.Close()
	var invocationIDs []domain.InvocationID
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return err
		}
		invocationID, err := domain.ParseInvocationID(value)
		if err != nil {
			return err
		}
		invocationIDs = append(invocationIDs, invocationID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, invocationID := range invocationIDs {
		if err := s.EnsureInvocationActions(ctx, invocationID); err != nil {
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
	if proposal.ActionType != "email.send" && proposal.ActionType != TicketCreateAction && proposal.ActionType != WorkReviewAction {
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

// EnsureWorkReview turns completed ticket work into an explicit owner review.
// It returns blocked=true when active subtasks still require human input, in
// which case the parent ticket is left waiting instead of being presented as
// complete.
func (s *ApprovalService) EnsureWorkReview(ctx context.Context, runID domain.RunID) (created, blocked bool, err error) {
	var workItemID, invocationIDText, personaVersionID, title string
	var number int64
	var resultPayload []byte
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(r.configuration_snapshot->>'work_item_id',''),
		       ai.id::text, ai.persona_version_id::text, wi.number, wi.title, ai.result_payload
		FROM boardroom_runs r
		JOIN work_items wi ON wi.id=NULLIF(r.configuration_snapshot->>'work_item_id','')::uuid
		JOIN LATERAL (
			SELECT id, persona_version_id, result_payload FROM agent_invocations
			WHERE run_id=r.id AND status='succeeded' ORDER BY turn_number DESC LIMIT 1
		) ai ON true
		WHERE r.id=$1
	`, runID.String()).Scan(&workItemID, &invocationIDText, &personaVersionID, &number, &title, &resultPayload)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("load work review result: %w", err)
	}
	var ownerInputRequiresResume bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM human_input_requests
			WHERE parent_work_item_id=$1 AND run_id=$2 AND status='answered'
		)
	`, workItemID, runID.String()).Scan(&ownerInputRequiresResume); err != nil {
		return false, false, err
	}
	if ownerInputRequiresResume {
		_, err := s.pool.Exec(ctx, `UPDATE work_items SET status='waiting', updated_at=now() WHERE id=$1`, workItemID)
		return false, true, err
	}
	var activeChildren int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM work_items
		WHERE parent_id=$1 AND status IN ('open','in_progress','waiting')
	`, workItemID).Scan(&activeChildren); err != nil {
		return false, false, err
	}
	if activeChildren > 0 {
		_, err := s.pool.Exec(ctx, `UPDATE work_items SET status='waiting', updated_at=now() WHERE id=$1`, workItemID)
		return false, true, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM external_actions WHERE run_id=$1 AND action_type=$2)`, runID.String(), WorkReviewAction).Scan(&exists); err != nil {
		return false, false, err
	}
	if exists {
		return false, false, nil
	}
	var result agent.Result
	if err := json.Unmarshal(resultPayload, &result); err != nil {
		return false, false, fmt.Errorf("decode work review result: %w", err)
	}
	payload, err := json.Marshal(WorkReviewPayload{
		WorkItemID: workItemID, WorkItemNumber: number, Title: title,
		Summary: strings.TrimSpace(result.Structured.Contribution), Findings: result.Structured.Findings,
		Recommendations: result.Structured.Recommendations,
	})
	if err != nil {
		return false, false, err
	}
	if strings.TrimSpace(result.Structured.Contribution) == "" {
		return false, false, errors.New("agent work completed without a reviewable summary")
	}
	invocationID, err := domain.ParseInvocationID(invocationIDText)
	if err != nil {
		return false, false, err
	}
	proposal := agent.ProposedAction{
		ActionType: WorkReviewAction,
		Reason:     fmt.Sprintf("Review agent work for ticket #%04d: %s", number, title),
		Payload:    payload,
		Evidence:   append([]string{"The assigned agent completed its ticket run."}, result.Structured.Findings...),
	}
	if err := s.ensureProposal(ctx, runID, invocationID, personaVersionID, 9999, proposal); err != nil {
		return false, false, err
	}
	_, err = s.pool.Exec(ctx, `UPDATE work_items SET status='waiting', updated_at=now() WHERE id=$1`, workItemID)
	return true, false, err
}

func (s *ApprovalService) BeginExecution(ctx context.Context, actionID domain.ActionID) (ExternalAction, bool, error) {
	return s.ledger.BeginExecution(ctx, actionID, false)
}

func (s *ApprovalService) MarkSucceeded(ctx context.Context, actionID domain.ActionID, response any, providerReference string) error {
	return s.ledger.MarkSucceeded(ctx, actionID, response, providerReference)
}

func (s *ApprovalService) MarkFailed(ctx context.Context, actionID domain.ActionID, actionErr error) error {
	return s.ledger.MarkFailed(ctx, actionID, actionErr)
}

func (s *ApprovalService) List(ctx context.Context, includeDecided bool) ([]Approval, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ar.id::text, ar.action_id::text, ar.run_id::text, ea.invocation_id::text,
		       COALESCE(pv.name,''), COALESCE(pv.role,''), ea.action_type, ea.reason, ea.evidence,
		       ea.request_payload, ea.status, ar.status, ar.requested_at, ar.expires_at, ar.decided_at,
		       COALESCE(ar.decided_by::text,''), COALESCE(wi.id::text,''), COALESCE(wi.number,0), COALESCE(wi.title,'')
		FROM approval_requests ar
		JOIN external_actions ea ON ea.id=ar.action_id
		LEFT JOIN persona_versions pv ON pv.id=ea.persona_version_id
		LEFT JOIN boardroom_runs br ON br.id=ar.run_id
		LEFT JOIN work_items wi ON wi.id=NULLIF(br.configuration_snapshot->>'work_item_id','')::uuid
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
			&item.RequestedAt, &item.ExpiresAt, &item.DecidedAt, &item.DecidedBy,
			&item.WorkItemID, &item.WorkItemNumber, &item.WorkItemTitle); err != nil {
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

// WaitForWorkItemApproval reflects that an agent-owned ticket has done all the
// work it can until the owner decides one of the run's pending approvals.
// Runs that are not attached to a work item are intentionally unaffected.
func (s *ApprovalService) WaitForWorkItemApproval(ctx context.Context, runID domain.RunID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE work_items AS work
		SET status='waiting', updated_at=now()
		FROM boardroom_runs AS run
		WHERE run.id=$1
		  AND work.id=NULLIF(run.configuration_snapshot->>'work_item_id','')::uuid
		  AND work.status='in_progress'
	`, runID.String())
	return err
}

func (s *ApprovalService) PendingApprovalsForRun(ctx context.Context, runID domain.RunID) ([]Approval, error) {
	items, err := s.List(ctx, false)
	if err != nil {
		return nil, err
	}
	result := make([]Approval, 0, len(items))
	for _, item := range items {
		if item.RunID == runID {
			result = append(result, item)
		}
	}
	return result, nil
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
