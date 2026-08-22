package mcpapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/actionrecovery"
	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	attentiondomain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AttentionService interface {
	CreateInformation(context.Context, attentionapp.CreateInformationCommand) (attentiondomain.InformationRequest, error)
	AnswerInformation(context.Context, attentionapp.AnswerInformationCommand) (attentionapp.InformationCompletion, error)
	CancelInformation(context.Context, attentionapp.CancelInformationCommand) (attentiondomain.InformationRequest, error)
	GetInformation(context.Context, access.Actor, ids.AccountID, ids.InformationRequestID) (attentiondomain.InformationRequest, error)
	ListInformation(context.Context, access.Actor, ids.AccountID, attentionapp.InformationListQuery) (attentionapp.InformationSummaryPage, error)
	CreateWorkReview(context.Context, attentionapp.CreateWorkReviewCommand) (attentiondomain.WorkReview, error)
	DecideWorkReview(context.Context, attentionapp.DecideWorkReviewCommand) (attentiondomain.WorkReview, error)
	CancelWorkReview(context.Context, attentionapp.CancelWorkReviewCommand) (attentiondomain.WorkReview, error)
	GetWorkReview(context.Context, access.Actor, ids.AccountID, ids.WorkReviewID) (attentiondomain.WorkReview, error)
	ListWorkReviews(context.Context, access.Actor, ids.AccountID, attentionapp.WorkReviewListQuery) (attentionapp.WorkReviewSummaryPage, error)
	CreateApproval(context.Context, attentionapp.CreateApprovalCommand) (attentiondomain.ConsequentialApproval, error)
	DecideApproval(context.Context, attentionapp.DecideApprovalCommand) (attentiondomain.ConsequentialApproval, error)
	CancelApproval(context.Context, attentionapp.CancelApprovalCommand) (attentiondomain.ConsequentialApproval, error)
	GetApproval(context.Context, access.Actor, ids.AccountID, ids.ConsequentialApprovalID) (attentiondomain.ConsequentialApproval, error)
	ListApprovals(context.Context, access.Actor, ids.AccountID, attentionapp.ApprovalListQuery) (attentionapp.ApprovalSummaryPage, error)
}

type ActionRecoveryService interface {
	List(context.Context, access.Actor, ids.AccountID, actionrecovery.ListQuery) (actionrecovery.Page, error)
	Get(context.Context, access.Actor, ids.AccountID, string) (actionrecovery.Detail, error)
	Request(context.Context, actionrecovery.RequestCommand) (actionrecovery.Detail, error)
	Confirm(context.Context, actionrecovery.ConfirmCommand) (actionrecovery.Detail, error)
}

type KnowledgeService interface {
	RegisterEvidence(context.Context, knowledgeapp.RegisterEvidenceCommand) (knowledgedomain.Evidence, error)
	ProposeClaim(context.Context, knowledgeapp.ProposeClaimCommand) (knowledgedomain.Claim, error)
	DecideClaim(context.Context, knowledgeapp.DecideClaimCommand) (knowledgedomain.Claim, *knowledgedomain.Fact, error)
	GetClaim(context.Context, access.Actor, ids.AccountID, ids.KnowledgeClaimID) (knowledgedomain.Claim, error)
	ListFacts(context.Context, access.Actor, ids.AccountID, knowledgeapp.FactListQuery) (knowledgeapp.FactPage, error)
}

type requirementInput struct {
	Key     string                           `json:"key"`
	Scope   attentiondomain.InformationScope `json:"scope"`
	ScopeID string                           `json:"scope_id,omitempty"`
}

func (input requirementInput) domain() attentiondomain.FactRequirement {
	return attentiondomain.FactRequirement{Key: input.Key, Scope: input.Scope, ScopeID: input.ScopeID}
}

type actorOutput struct {
	Kind attentiondomain.ActorKind `json:"kind"`
	ID   string                    `json:"id"`
}

type requirementOutput struct {
	Key     string                           `json:"key"`
	Scope   attentiondomain.InformationScope `json:"scope"`
	ScopeID string                           `json:"scope_id,omitempty"`
}

type informationAnswerOutput struct {
	FactID      string      `json:"fact_id"`
	FactVersion uint64      `json:"fact_version"`
	AnsweredBy  actorOutput `json:"answered_by"`
	AnsweredAt  time.Time   `json:"answered_at"`
}

type informationOutput struct {
	ID               ids.InformationRequestID                `json:"id"`
	ParentWorkItemID ids.WorkItemID                          `json:"parent_work_item_id"`
	Requirement      requirementOutput                       `json:"requirement"`
	Question         string                                  `json:"question"`
	RequestedBy      actorOutput                             `json:"requested_by"`
	State            attentiondomain.InformationRequestState `json:"state"`
	Answer           *informationAnswerOutput                `json:"answer,omitempty"`
	CanceledBy       *actorOutput                            `json:"canceled_by,omitempty"`
	Reason           string                                  `json:"reason,omitempty"`
	Version          uint64                                  `json:"version"`
	CreatedAt        time.Time                               `json:"created_at"`
	UpdatedAt        time.Time                               `json:"updated_at"`
}

type informationSummaryOutput struct {
	ID               ids.InformationRequestID                `json:"id"`
	ParentWorkItemID ids.WorkItemID                          `json:"parent_work_item_id"`
	Requirement      requirementOutput                       `json:"requirement"`
	Question         string                                  `json:"question"`
	RequestedBy      actorOutput                             `json:"requested_by"`
	State            attentiondomain.InformationRequestState `json:"state"`
	AnsweredAt       *time.Time                              `json:"answered_at,omitempty"`
	Version          uint64                                  `json:"version"`
	CreatedAt        time.Time                               `json:"created_at"`
	UpdatedAt        time.Time                               `json:"updated_at"`
}

type informationPageOutput struct {
	Items      []informationSummaryOutput `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

type informationCompletionOutput struct {
	Answered           []informationOutput `json:"answered"`
	ResumableParentIDs []ids.WorkItemID    `json:"resumable_parent_ids"`
}

type reviewDecisionOutput struct {
	Decision  attentiondomain.WorkReviewDecision `json:"decision"`
	Reason    string                             `json:"reason"`
	DecidedBy actorOutput                        `json:"decided_by"`
	DecidedAt time.Time                          `json:"decided_at"`
}

type reviewOutput struct {
	ID             ids.WorkReviewID                `json:"id"`
	WorkItemID     ids.WorkItemID                  `json:"work_item_id"`
	WorkVersion    uint64                          `json:"work_version"`
	ProposalSHA256 string                          `json:"proposal_sha256"`
	Question       string                          `json:"question"`
	RequestedBy    actorOutput                     `json:"requested_by"`
	ReviewerID     ids.UserID                      `json:"reviewer_id"`
	State          attentiondomain.WorkReviewState `json:"state"`
	Decision       *reviewDecisionOutput           `json:"decision,omitempty"`
	CanceledBy     *actorOutput                    `json:"canceled_by,omitempty"`
	CancelReason   string                          `json:"cancel_reason,omitempty"`
	InvalidatedAt  *time.Time                      `json:"invalidated_at,omitempty"`
	Version        uint64                          `json:"version"`
	CreatedAt      time.Time                       `json:"created_at"`
	UpdatedAt      time.Time                       `json:"updated_at"`
}

type reviewSummaryOutput struct {
	ID          ids.WorkReviewID                   `json:"id"`
	WorkItemID  ids.WorkItemID                     `json:"work_item_id"`
	WorkVersion uint64                             `json:"work_version"`
	Question    string                             `json:"question"`
	RequestedBy actorOutput                        `json:"requested_by"`
	ReviewerID  ids.UserID                         `json:"reviewer_id"`
	State       attentiondomain.WorkReviewState    `json:"state"`
	Decision    attentiondomain.WorkReviewDecision `json:"decision,omitempty"`
	Version     uint64                             `json:"version"`
	CreatedAt   time.Time                          `json:"created_at"`
	UpdatedAt   time.Time                          `json:"updated_at"`
}

type reviewPageOutput struct {
	Items      []reviewSummaryOutput `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

type approvalDecisionOutput struct {
	Decision  attentiondomain.ApprovalDecision `json:"decision"`
	Reason    string                           `json:"reason"`
	DecidedBy ids.UserID                       `json:"decided_by"`
	DecidedAt time.Time                        `json:"decided_at"`
}

type approvalOutput struct {
	ID                       ids.ConsequentialApprovalID                `json:"id"`
	WorkItemID               ids.WorkItemID                             `json:"work_item_id,omitempty"`
	OperationID              string                                     `json:"operation_id"`
	InvocationID             ids.AgentInvocationID                      `json:"invocation_id"`
	Capability               string                                     `json:"capability"`
	Payload                  json.RawMessage                            `json:"payload"`
	InputSHA256              string                                     `json:"input_sha256"`
	HashVersion              uint16                                     `json:"hash_version"`
	EvidenceSHA256           string                                     `json:"evidence_sha256"`
	Proposer                 actorOutput                                `json:"proposer"`
	PolicyVersion            uint64                                     `json:"policy_version"`
	RequireIndependentReview bool                                       `json:"require_independent_review"`
	ExpiresAt                time.Time                                  `json:"expires_at"`
	State                    attentiondomain.ConsequentialApprovalState `json:"state"`
	Decision                 *approvalDecisionOutput                    `json:"decision,omitempty"`
	CanceledBy               *actorOutput                               `json:"canceled_by,omitempty"`
	Reason                   string                                     `json:"reason,omitempty"`
	CanceledAt               *time.Time                                 `json:"canceled_at,omitempty"`
	InvalidatedAt            *time.Time                                 `json:"invalidated_at,omitempty"`
	ExpiredAt                *time.Time                                 `json:"expired_at,omitempty"`
	Version                  uint64                                     `json:"version"`
	CreatedAt                time.Time                                  `json:"created_at"`
	UpdatedAt                time.Time                                  `json:"updated_at"`
}

type approvalSummaryOutput struct {
	ID            ids.ConsequentialApprovalID                `json:"id"`
	WorkItemID    ids.WorkItemID                             `json:"work_item_id,omitempty"`
	OperationID   string                                     `json:"operation_id"`
	InvocationID  ids.AgentInvocationID                      `json:"invocation_id"`
	Capability    string                                     `json:"capability"`
	Proposer      actorOutput                                `json:"proposer"`
	PolicyVersion uint64                                     `json:"policy_version"`
	ExpiresAt     time.Time                                  `json:"expires_at"`
	State         attentiondomain.ConsequentialApprovalState `json:"state"`
	Decision      attentiondomain.ApprovalDecision           `json:"decision,omitempty"`
	Version       uint64                                     `json:"version"`
	CreatedAt     time.Time                                  `json:"created_at"`
	UpdatedAt     time.Time                                  `json:"updated_at"`
}

type approvalPageOutput struct {
	Items      []approvalSummaryOutput `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

type actionSummaryOutput struct {
	OperationID     string               `json:"operation_id"`
	ApprovalID      string               `json:"approval_id"`
	InvocationID    string               `json:"invocation_id"`
	Capability      string               `json:"capability"`
	ExecutorID      string               `json:"executor_id"`
	ExecutorVersion uint64               `json:"executor_version"`
	PolicyVersion   uint64               `json:"policy_version"`
	State           actionrecovery.State `json:"state"`
	AttemptCount    uint32               `json:"attempt_count"`
	LastErrorCode   string               `json:"last_error_code,omitempty"`
	NextAttemptAt   *time.Time           `json:"next_attempt_at,omitempty"`
	CompletedAt     *time.Time           `json:"completed_at,omitempty"`
	StartedAt       time.Time            `json:"started_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

type actionResolutionOutput struct {
	ID                string               `json:"id"`
	OperationID       string               `json:"operation_id"`
	RequestedOutcome  actionrecovery.State `json:"requested_outcome"`
	ReasonSHA256      string               `json:"reason_sha256"`
	RequestedByUserID ids.UserID           `json:"requested_by_user_id"`
	RequestedAt       time.Time            `json:"requested_at"`
	State             string               `json:"state"`
	ConfirmedByUserID ids.UserID           `json:"confirmed_by_user_id,omitempty"`
	ConfirmedAt       *time.Time           `json:"confirmed_at,omitempty"`
}

type actionDetailOutput struct {
	actionSummaryOutput
	Resolution *actionResolutionOutput `json:"resolution,omitempty"`
}

type actionPageOutput struct {
	Items      []actionSummaryOutput `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}
