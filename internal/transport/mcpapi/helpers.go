package mcpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tinfoyle/spyglass-engine/internal/application/actionrecovery"
	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	attentiondomain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type safeToolError struct{ code string }

func (err safeToolError) Error() string { return err.code }
func safeError(code string) error       { return safeToolError{code: code} }

func attentionError(err error) error {
	var denied *access.DeniedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, attentionapp.ErrInvalidCommand), errors.Is(err, attentiondomain.ErrInvalid):
		return safeError("invalid_attention_command")
	case errors.Is(err, attentionapp.ErrNotFound):
		return safeError("attention_not_found")
	case errors.Is(err, attentionapp.ErrConflict), errors.Is(err, attentiondomain.ErrConflict), errors.Is(err, workapp.ErrConflict):
		return safeError("attention_version_conflict")
	case errors.Is(err, attentionapp.ErrCompletionTooLarge):
		return safeError("attention_completion_too_large")
	case errors.Is(err, attentionapp.ErrConstraint), errors.Is(err, attentiondomain.ErrState), errors.Is(err, attentiondomain.ErrReasonRequired), errors.Is(err, attentiondomain.ErrRequirementMismatch), errors.Is(err, attentiondomain.ErrReviewer), errors.Is(err, attentiondomain.ErrSelfApproval), errors.Is(err, attentiondomain.ErrExpired), errors.Is(err, workdomain.ErrTransition):
		return safeError("attention_command_rejected")
	case errors.Is(err, attentiondomain.ErrRole):
		return safeError(string(access.DenialRole))
	case errors.As(err, &denied):
		return safeError(string(denied.Code))
	default:
		return safeError("attention_unavailable")
	}
}

func actionRecoveryError(err error) error {
	var denied *access.DeniedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, actionrecovery.ErrInvalid):
		return safeError("invalid_action_recovery")
	case errors.Is(err, actionrecovery.ErrNotFound):
		return safeError("action_not_found")
	case errors.Is(err, actionrecovery.ErrConflict):
		return safeError("action_recovery_conflict")
	case errors.Is(err, actionrecovery.ErrConstraint):
		return safeError("action_recovery_rejected")
	case errors.As(err, &denied):
		return safeError(string(denied.Code))
	default:
		return safeError("action_recovery_unavailable")
	}
}

func knowledgeError(err error) error {
	var denied *access.DeniedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, knowledgeapp.ErrInvalid):
		return safeError("invalid_knowledge_request")
	case errors.Is(err, knowledgeapp.ErrNotFound):
		return safeError("knowledge_not_found")
	case errors.Is(err, knowledgeapp.ErrConflict):
		return safeError("knowledge_conflict")
	case errors.Is(err, knowledgeapp.ErrConstraint):
		return safeError("knowledge_rejected")
	case errors.As(err, &denied):
		return safeError(string(denied.Code))
	default:
		return safeError("knowledge_unavailable")
	}
}

func baselineError(err error) error {
	var denied *access.DeniedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, baselineapp.ErrInvalid):
		return safeError("invalid_baseline_request")
	case errors.Is(err, baselineapp.ErrNotFound):
		return safeError("baseline_not_found")
	case errors.Is(err, baselineapp.ErrConflict):
		return safeError("baseline_conflict")
	case errors.Is(err, baselineapp.ErrConstraint):
		return safeError("baseline_rejected")
	case errors.As(err, &denied):
		return safeError(string(denied.Code))
	default:
		return safeError("baseline_unavailable")
	}
}

func financeError(err error) error {
	var denied *access.DeniedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, financeapp.ErrInvalid):
		return safeError("invalid_finance_request")
	case errors.Is(err, financeapp.ErrNotFound):
		return safeError("finance_not_found")
	case errors.Is(err, financeapp.ErrConflict):
		return safeError("finance_version_conflict")
	case errors.Is(err, financeapp.ErrAggregateOverflow):
		return safeError("finance_aggregate_overflow")
	case errors.As(err, &denied):
		return safeError(string(denied.Code))
	default:
		return safeError("finance_unavailable")
	}
}

func toolAnnotations(readOnly, destructive bool) *mcp.ToolAnnotations {
	closedWorld := false
	return &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &closedWorld}
}

type cursorEnvelope struct {
	Version   int       `json:"v"`
	Kind      string    `json:"kind"`
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"id"`
}

func encodeCursor(kind string, updatedAt time.Time, id string) string {
	raw, _ := json.Marshal(cursorEnvelope{Version: 1, Kind: kind, UpdatedAt: updatedAt.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(raw, kind string) (cursorEnvelope, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 1024 {
		return cursorEnvelope{}, safeError("invalid_attention_cursor")
	}
	var cursor cursorEnvelope
	if json.Unmarshal(decoded, &cursor) != nil || cursor.Version != 1 || cursor.Kind != kind || cursor.UpdatedAt.IsZero() || ids.Validate(cursor.ID) != nil {
		return cursorEnvelope{}, safeError("invalid_attention_cursor")
	}
	return cursor, nil
}

func digest(value string) ([32]byte, error) {
	var output [32]byte
	if len(value) != 64 {
		return output, safeError("invalid_sha256")
	}
	raw, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(raw) != value {
		return output, safeError("invalid_sha256")
	}
	copy(output[:], raw)
	if output == ([32]byte{}) {
		return output, safeError("invalid_sha256")
	}
	return output, nil
}

func actorView(actor attentiondomain.Actor) actorOutput {
	return actorOutput{Kind: actor.Kind, ID: actor.ID}
}

func requirementView(requirement attentiondomain.FactRequirement) requirementOutput {
	return requirementOutput{Key: requirement.Key, Scope: requirement.Scope, ScopeID: requirement.ScopeID}
}

func informationView(item attentiondomain.InformationRequest) informationOutput {
	output := informationOutput{ID: item.ID, ParentWorkItemID: item.ParentWorkItemID, Requirement: requirementView(item.Requirement), Question: item.Question, RequestedBy: actorView(item.RequestedBy), State: item.State, Reason: item.Reason, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if item.AnswerRecord != nil {
		output.Answer = &informationAnswerOutput{FactID: item.AnswerRecord.Fact.ID, FactVersion: item.AnswerRecord.Fact.Version, AnsweredBy: actorView(item.AnswerRecord.AnsweredBy), AnsweredAt: item.AnswerRecord.AnsweredAt}
	}
	if item.CanceledBy != nil {
		value := actorView(*item.CanceledBy)
		output.CanceledBy = &value
	}
	return output
}

func informationSummaryView(item attentionapp.InformationSummary) informationSummaryOutput {
	return informationSummaryOutput{ID: item.ID, ParentWorkItemID: item.ParentWorkItemID, Requirement: requirementView(item.Requirement), Question: item.Question, RequestedBy: actorView(item.RequestedBy), State: item.State, AnsweredAt: item.AnsweredAt, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func reviewView(item attentiondomain.WorkReview) reviewOutput {
	output := reviewOutput{ID: item.ID, WorkItemID: item.WorkItemID, WorkVersion: item.WorkVersion, ProposalSHA256: hex.EncodeToString(item.ProposalSHA256[:]), Question: item.Question, RequestedBy: actorView(item.RequestedBy), ReviewerID: item.ReviewerID, State: item.State, CancelReason: item.CancelReason, InvalidatedAt: item.InvalidatedAt, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if item.Decision != nil {
		output.Decision = &reviewDecisionOutput{Decision: item.Decision.Decision, Reason: item.Decision.Reason, DecidedBy: actorView(item.Decision.DecidedBy), DecidedAt: item.Decision.DecidedAt}
	}
	if item.CanceledBy != nil {
		value := actorView(*item.CanceledBy)
		output.CanceledBy = &value
	}
	return output
}

func reviewSummaryView(item attentionapp.WorkReviewSummary) reviewSummaryOutput {
	return reviewSummaryOutput{ID: item.ID, WorkItemID: item.WorkItemID, WorkVersion: item.WorkVersion, Question: item.Question, RequestedBy: actorView(item.RequestedBy), ReviewerID: item.ReviewerID, State: item.State, Decision: item.Decision, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func approvalView(item attentiondomain.ConsequentialApproval) approvalOutput {
	output := approvalOutput{ID: item.ID, WorkItemID: item.WorkItemID, OperationID: item.OperationID, InvocationID: item.InvocationID, Capability: item.Capability, Payload: item.CanonicalPayload, InputSHA256: hex.EncodeToString(item.InputSHA256[:]), HashVersion: item.HashVersion, EvidenceSHA256: hex.EncodeToString(item.EvidenceSHA256[:]), Proposer: actorView(item.Proposer), PolicyVersion: item.PolicyVersion, RequireIndependentReview: item.RequireIndependentReview, ExpiresAt: item.ExpiresAt, State: item.State, Reason: item.Reason, CanceledAt: item.CanceledAt, InvalidatedAt: item.InvalidatedAt, ExpiredAt: item.ExpiredAt, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if item.Decision != nil {
		output.Decision = &approvalDecisionOutput{Decision: item.Decision.Decision, Reason: item.Decision.Reason, DecidedBy: item.Decision.DecidedBy, DecidedAt: item.Decision.DecidedAt}
	}
	if item.CanceledBy != nil {
		value := actorView(*item.CanceledBy)
		output.CanceledBy = &value
	}
	return output
}

func approvalSummaryView(item attentionapp.ApprovalSummary) approvalSummaryOutput {
	return approvalSummaryOutput{ID: item.ID, WorkItemID: item.WorkItemID, OperationID: item.OperationID, InvocationID: item.InvocationID, Capability: item.Capability, Proposer: actorView(item.Proposer), PolicyVersion: item.PolicyVersion, ExpiresAt: item.ExpiresAt, State: item.State, Decision: item.Decision, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func actionSummaryView(item actionrecovery.Summary) actionSummaryOutput {
	return actionSummaryOutput{OperationID: item.OperationID, ApprovalID: item.ApprovalID, InvocationID: item.InvocationID, Capability: item.Capability, ExecutorID: item.ExecutorID, ExecutorVersion: item.ExecutorVersion, PolicyVersion: item.PolicyVersion, State: item.State, AttemptCount: item.AttemptCount, LastErrorCode: item.LastErrorCode, NextAttemptAt: item.NextAttemptAt, CompletedAt: item.CompletedAt, StartedAt: item.StartedAt, UpdatedAt: item.UpdatedAt}
}

func actionDetailView(item actionrecovery.Detail) actionDetailOutput {
	output := actionDetailOutput{actionSummaryOutput: actionSummaryView(item.Summary)}
	if item.Resolution != nil {
		resolution := item.Resolution
		output.Resolution = &actionResolutionOutput{ID: resolution.ID, OperationID: resolution.OperationID, RequestedOutcome: resolution.RequestedOutcome, ReasonSHA256: hex.EncodeToString(resolution.ReasonSHA256[:]), RequestedByUserID: resolution.RequestedByUserID, RequestedAt: resolution.RequestedAt, State: resolution.State, ConfirmedByUserID: resolution.ConfirmedByUserID, ConfirmedAt: resolution.ConfirmedAt}
	}
	return output
}

func operationID(value string) (string, error) {
	if ids.Validate(value) != nil {
		return "", safeError("invalid_operation_id")
	}
	return value, nil
}

func expectedVersion(value uint64) error {
	if value == 0 {
		return safeError("attention_version_required")
	}
	return nil
}

func limit(value int) (int, error) {
	if value == 0 {
		return attentionapp.DefaultLimit, nil
	}
	if value < 1 || value > 100 {
		return 0, safeError("invalid_attention_limit")
	}
	return value, nil
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, attentionError(err))
}

func decodeStrictToolInput(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return safeError("invalid_attention_command")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return safeError("invalid_attention_command")
	}
	return nil
}

func structuredToolResult(value any) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, safeError("attention_unavailable")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: json.RawMessage(raw)}, nil
}

func failedToolResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}, IsError: true}
}
