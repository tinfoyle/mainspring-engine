package mcpapi

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	baselinedomain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type baselineStartInput struct {
	AccountID   ids.AccountID `json:"account_id"`
	OperationID string        `json:"operation_id"`
}

type baselineGetInput struct {
	AccountID    ids.AccountID            `json:"account_id"`
	AssessmentID ids.BaselineAssessmentID `json:"assessment_id"`
}

type baselineFactInput struct {
	FactID   ids.KnowledgeFactID `json:"fact_id"`
	Revision uint64              `json:"revision"`
}

type baselineResponsibilityInput struct {
	Kind baselinedomain.ResponsibilityKind `json:"kind"`
	ID   string                            `json:"id,omitempty"`
}

type baselineMutationInput struct {
	AccountID         ids.AccountID                         `json:"account_id"`
	OperationID       string                                `json:"operation_id"`
	AssessmentID      ids.BaselineAssessmentID              `json:"assessment_id"`
	ExpectedVersion   uint64                                `json:"expected_version"`
	Action            string                                `json:"action"`
	QuestionKey       string                                `json:"question_key,omitempty"`
	AnswerKind        baselinedomain.AnswerKind             `json:"answer_kind,omitempty"`
	Fact              *baselineFactInput                    `json:"fact,omitempty"`
	Reason            string                                `json:"reason,omitempty"`
	RequirementID     ids.BaselineRequirementID             `json:"requirement_id,omitempty"`
	EvidenceID        ids.KnowledgeEvidenceID               `json:"evidence_id,omitempty"`
	EvidenceDecision  baselinedomain.EvidenceDecisionKind   `json:"evidence_decision,omitempty"`
	Disposition       baselinedomain.RequirementDisposition `json:"disposition,omitempty"`
	ContentSHA256     string                                `json:"content_sha256,omitempty"`
	PlanID            ids.BaselinePlanID                    `json:"plan_id,omitempty"`
	AssessmentVersion uint64                                `json:"assessment_version,omitempty"`
}

type baselineAnswerOutput struct {
	QuestionKey string                    `json:"question_key"`
	Kind        baselinedomain.AnswerKind `json:"kind"`
	Fact        *baselineFactInput        `json:"fact,omitempty"`
	Reason      string                    `json:"reason,omitempty"`
	AnsweredBy  ids.UserID                `json:"answered_by_user_id"`
	AnsweredAt  time.Time                 `json:"answered_at"`
}

type baselineEvidenceOutput struct {
	EvidenceID ids.KnowledgeEvidenceID             `json:"evidence_id"`
	Decision   baselinedomain.EvidenceDecisionKind `json:"decision"`
	Reason     string                              `json:"reason"`
	DecidedBy  ids.UserID                          `json:"decided_by_user_id"`
	DecidedAt  time.Time                           `json:"decided_at"`
}

type baselineRequirementOutput struct {
	ID                 ids.BaselineRequirementID             `json:"id"`
	Code               string                                `json:"code"`
	Title              string                                `json:"title"`
	Responsibility     baselineResponsibilityInput           `json:"responsibility"`
	RenewAfterDays     uint16                                `json:"renew_after_days"`
	CatalogVersion     string                                `json:"catalog_version"`
	ScopePolicyVersion string                                `json:"scope_policy_version"`
	Disposition        baselinedomain.RequirementDisposition `json:"disposition"`
	Reason             string                                `json:"reason,omitempty"`
	Evidence           []baselineEvidenceOutput              `json:"evidence"`
	RenewAt            *time.Time                            `json:"renew_at,omitempty"`
}

type baselinePlanOutput struct {
	ID                ids.BaselinePlanID `json:"id"`
	AssessmentVersion uint64             `json:"assessment_version"`
	ContentSHA256     string             `json:"content_sha256"`
	ProposedWorkCount uint16             `json:"proposed_work_count"`
	ApprovedBy        ids.UserID         `json:"approved_by_user_id,omitempty"`
	ApprovedAt        *time.Time         `json:"approved_at,omitempty"`
}

type baselineAssessmentOutput struct {
	ID                 ids.BaselineAssessmentID       `json:"id"`
	AccountID          ids.AccountID                  `json:"account_id"`
	CatalogVersion     string                         `json:"catalog_version"`
	ScopePolicyVersion string                         `json:"scope_policy_version"`
	State              baselinedomain.AssessmentState `json:"state"`
	Answers            []baselineAnswerOutput         `json:"answers"`
	Requirements       []baselineRequirementOutput    `json:"requirements"`
	Plan               *baselinePlanOutput            `json:"plan,omitempty"`
	SupersededBy       ids.BaselineAssessmentID       `json:"superseded_by_assessment_id,omitempty"`
	CreatedBy          ids.UserID                     `json:"created_by_user_id"`
	Version            uint64                         `json:"version"`
	CreatedAt          time.Time                      `json:"created_at"`
	UpdatedAt          time.Time                      `json:"updated_at"`
}

type baselineMutationOutput struct {
	Assessment  *baselineAssessmentOutput `json:"assessment,omitempty"`
	Archived    *baselineAssessmentOutput `json:"archived,omitempty"`
	Next        *baselineAssessmentOutput `json:"next,omitempty"`
	WorkItemIDs []ids.WorkItemID          `json:"work_item_ids,omitempty"`
}

func (s *Server) registerBaseline(server *mcp.Server, actor access.Actor) {
	read := access.Requirement{Package: catalog.PackageKnowledge}
	mutation := access.Requirement{Package: catalog.PackageKnowledge, Mutation: true}
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_baseline_start", Title: "Start Baseline assessment", Description: "Start a version-frozen Baseline interview using the operation ID as the assessment identity.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input baselineStartInput) (*mcp.CallToolResult, baselineAssessmentOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, mutation)
		if err != nil {
			return nil, baselineAssessmentOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, baselineAssessmentOutput{}, err
		}
		item, err := s.baseline.Start(ctx, baselineapp.StartCommand{Actor: actor, AccountID: input.AccountID, AssessmentID: ids.BaselineAssessmentID(op), CorrelationID: op})
		if err != nil {
			return nil, baselineAssessmentOutput{}, baselineError(err)
		}
		return nil, baselineAssessmentView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_baseline_get", Title: "Get Baseline assessment", Description: "Get the authorized versioned assessment, exact references, decisions, and plan binding.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input baselineGetInput) (*mcp.CallToolResult, baselineAssessmentOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, baselineAssessmentOutput{}, err
		}
		item, err := s.baseline.Get(ctx, actor, input.AccountID, input.AssessmentID)
		if err != nil {
			return nil, baselineAssessmentOutput{}, baselineError(err)
		}
		return nil, baselineAssessmentView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_baseline_mutate", Title: "Advance Baseline assessment", Description: "Apply one optimistic Baseline action: answer, begin_inventory, complete_inventory, decide_evidence, disposition, submit_plan, approve_plan, materialize_plan, mark_ready, or reassess.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input baselineMutationInput) (*mcp.CallToolResult, baselineMutationOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, mutation)
		if err != nil {
			return nil, baselineMutationOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil || input.ExpectedVersion == 0 || ids.Validate(string(input.AssessmentID)) != nil {
			return nil, baselineMutationOutput{}, safeError("invalid_baseline_request")
		}
		advance := baselineapp.AdvanceCommand{Actor: actor, AccountID: input.AccountID, AssessmentID: input.AssessmentID, ExpectedVersion: input.ExpectedVersion, CorrelationID: op}
		var item baselinedomain.Assessment
		switch input.Action {
		case "answer":
			var fact *baselinedomain.FactReference
			if input.Fact != nil {
				fact = &baselinedomain.FactReference{FactID: input.Fact.FactID, Revision: input.Fact.Revision}
			}
			item, err = s.baseline.Answer(ctx, baselineapp.AnswerCommand{Actor: actor, AccountID: input.AccountID, AssessmentID: input.AssessmentID, QuestionKey: input.QuestionKey, Kind: input.AnswerKind, Fact: fact, Reason: input.Reason, ExpectedVersion: input.ExpectedVersion, CorrelationID: op})
		case "begin_inventory":
			item, err = s.baseline.BeginInventory(ctx, advance)
		case "complete_inventory":
			item, err = s.baseline.CompleteInventory(ctx, baselineapp.CompleteInventoryCommand{AdvanceCommand: advance})
		case "decide_evidence":
			item, err = s.baseline.DecideEvidence(ctx, baselineapp.DecideEvidenceCommand{AdvanceCommand: advance, RequirementID: input.RequirementID, EvidenceID: input.EvidenceID, Decision: input.EvidenceDecision, Reason: input.Reason})
		case "disposition":
			item, err = s.baseline.Disposition(ctx, baselineapp.DispositionCommand{AdvanceCommand: advance, RequirementID: input.RequirementID, Disposition: input.Disposition, Reason: input.Reason})
		case "submit_plan":
			item, err = s.baseline.SubmitPlan(ctx, baselineapp.SubmitPlanCommand{AdvanceCommand: advance, PlanID: ids.BaselinePlanID(op)})
		case "approve_plan":
			value, digestErr := digest(input.ContentSHA256)
			if digestErr != nil {
				return nil, baselineMutationOutput{}, digestErr
			}
			item, err = s.baseline.ApprovePlan(ctx, baselineapp.ApprovePlanCommand{AdvanceCommand: advance, PlanID: input.PlanID, ContentSHA256: value, AssessmentVersion: input.AssessmentVersion})
		case "materialize_plan":
			value, digestErr := digest(input.ContentSHA256)
			if digestErr != nil {
				return nil, baselineMutationOutput{}, digestErr
			}
			items, materializeErr := s.baseline.MaterializePlan(ctx, baselineapp.MaterializePlanCommand{AdvanceCommand: advance, PlanID: input.PlanID, ContentSHA256: value, AssessmentVersion: input.AssessmentVersion})
			if materializeErr != nil {
				return nil, baselineMutationOutput{}, baselineError(materializeErr)
			}
			workItemIDs := make([]ids.WorkItemID, 0, len(items))
			for _, value := range items {
				workItemIDs = append(workItemIDs, value.ID)
			}
			return nil, baselineMutationOutput{WorkItemIDs: workItemIDs}, nil
		case "mark_ready":
			item, err = s.baseline.MarkReady(ctx, advance)
		case "reassess":
			archived, next, reassessErr := s.baseline.Reassess(ctx, baselineapp.ReassessCommand{AdvanceCommand: advance, NewAssessmentID: ids.BaselineAssessmentID(op)})
			if reassessErr != nil {
				return nil, baselineMutationOutput{}, baselineError(reassessErr)
			}
			archivedOutput, nextOutput := baselineAssessmentView(archived), baselineAssessmentView(next)
			return nil, baselineMutationOutput{Archived: &archivedOutput, Next: &nextOutput}, nil
		default:
			return nil, baselineMutationOutput{}, safeError("invalid_baseline_action")
		}
		if err != nil {
			return nil, baselineMutationOutput{}, baselineError(err)
		}
		output := baselineAssessmentView(item)
		return nil, baselineMutationOutput{Assessment: &output}, nil
	})
}

func baselineAssessmentView(value baselinedomain.Assessment) baselineAssessmentOutput {
	output := baselineAssessmentOutput{ID: value.ID, AccountID: value.AccountID, CatalogVersion: value.CatalogVersion, ScopePolicyVersion: value.ScopePolicyVersion, State: value.State, Answers: make([]baselineAnswerOutput, 0, len(value.Answers)), Requirements: make([]baselineRequirementOutput, 0, len(value.Requirements)), SupersededBy: value.SupersededBy, CreatedBy: value.CreatedBy.UserID, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
	for _, answer := range value.Answers {
		var fact *baselineFactInput
		if answer.Fact != nil {
			fact = &baselineFactInput{FactID: answer.Fact.FactID, Revision: answer.Fact.Revision}
		}
		output.Answers = append(output.Answers, baselineAnswerOutput{QuestionKey: answer.QuestionKey, Kind: answer.Kind, Fact: fact, Reason: answer.Reason, AnsweredBy: answer.AnsweredBy.UserID, AnsweredAt: answer.AnsweredAt})
	}
	for _, requirement := range value.Requirements {
		item := baselineRequirementOutput{ID: requirement.ID, Code: requirement.Code, Title: requirement.Title, Responsibility: baselineResponsibilityInput{Kind: requirement.Responsibility.Kind, ID: requirement.Responsibility.ID}, RenewAfterDays: requirement.RenewAfterDays, CatalogVersion: requirement.CatalogVersion, ScopePolicyVersion: requirement.ScopePolicyVersion, Disposition: requirement.Disposition, Reason: requirement.Reason, Evidence: make([]baselineEvidenceOutput, 0, len(requirement.Evidence)), RenewAt: requirement.RenewAt}
		for _, decision := range requirement.Evidence {
			item.Evidence = append(item.Evidence, baselineEvidenceOutput{EvidenceID: decision.EvidenceID, Decision: decision.Decision, Reason: decision.Reason, DecidedBy: decision.DecidedBy.UserID, DecidedAt: decision.DecidedAt})
		}
		output.Requirements = append(output.Requirements, item)
	}
	if value.Plan != nil {
		plan := baselinePlanOutput{ID: value.Plan.ID, AssessmentVersion: value.Plan.AssessmentVersion, ContentSHA256: hex.EncodeToString(value.Plan.ContentSHA256[:]), ProposedWorkCount: value.Plan.ProposedWorkCount, ApprovedAt: value.Plan.ApprovedAt}
		if value.Plan.ApprovedBy != nil {
			plan.ApprovedBy = value.Plan.ApprovedBy.UserID
		}
		output.Plan = &plan
	}
	return output
}
