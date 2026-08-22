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

type baselineSourceListInput struct {
	AccountID    ids.AccountID             `json:"account_id"`
	AssessmentID ids.BaselineAssessmentID  `json:"assessment_id"`
	After        ids.BaselineSourceGrantID `json:"after,omitempty"`
	Limit        uint16                    `json:"limit,omitempty"`
}

type baselineSourceMutationInput struct {
	AccountID       ids.AccountID             `json:"account_id"`
	OperationID     string                    `json:"operation_id"`
	AssessmentID    ids.BaselineAssessmentID  `json:"assessment_id"`
	Action          string                    `json:"action"`
	GrantID         ids.BaselineSourceGrantID `json:"grant_id,omitempty"`
	ExpectedVersion uint64                    `json:"expected_version,omitempty"`
	ConnectionID    string                    `json:"connection_id,omitempty"`
	SourceKind      baselinedomain.SourceKind `json:"source_kind,omitempty"`
	Folders         []string                  `json:"folders,omitempty"`
	Since           *time.Time                `json:"since_at,omitempty"`
	Until           *time.Time                `json:"until_at,omitempty"`
	Reason          string                    `json:"reason,omitempty"`
}

type baselineSourceGrantOutput struct {
	ID           ids.BaselineSourceGrantID       `json:"id"`
	AccountID    ids.AccountID                   `json:"account_id"`
	AssessmentID ids.BaselineAssessmentID        `json:"assessment_id"`
	ConnectionID string                          `json:"connection_id"`
	SourceKind   baselinedomain.SourceKind       `json:"source_kind"`
	Folders      []string                        `json:"folders"`
	Since        *time.Time                      `json:"since_at,omitempty"`
	Until        *time.Time                      `json:"until_at,omitempty"`
	State        baselinedomain.SourceGrantState `json:"state"`
	GrantedBy    ids.UserID                      `json:"granted_by_user_id"`
	RevokedBy    ids.UserID                      `json:"revoked_by_user_id,omitempty"`
	RevokeReason string                          `json:"revoke_reason,omitempty"`
	Version      uint64                          `json:"version"`
	CreatedAt    time.Time                       `json:"created_at"`
	UpdatedAt    time.Time                       `json:"updated_at"`
	RevokedAt    *time.Time                      `json:"revoked_at,omitempty"`
}

type baselineSourcePageOutput struct {
	Items      []baselineSourceGrantOutput `json:"items"`
	NextCursor ids.BaselineSourceGrantID   `json:"next_cursor,omitempty"`
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

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_baseline_source_list", Title: "List Baseline source grants", Description: "List narrow read-only connector scopes granted after Baseline plan approval.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input baselineSourceListInput) (*mcp.CallToolResult, baselineSourcePageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageIntegrations})
		if err != nil {
			return nil, baselineSourcePageOutput{}, err
		}
		if input.Limit == 0 {
			input.Limit = 50
		}
		page, err := s.baseline.ListSourceGrants(ctx, baselineapp.ListSourceGrantsQuery{Actor: actor, AccountID: input.AccountID, AssessmentID: input.AssessmentID, After: input.After, Limit: input.Limit})
		if err != nil {
			return nil, baselineSourcePageOutput{}, baselineError(err)
		}
		output := baselineSourcePageOutput{Items: make([]baselineSourceGrantOutput, 0, len(page.Items)), NextCursor: page.NextCursor}
		for _, grant := range page.Items {
			output.Items = append(output.Items, baselineSourceGrantView(grant))
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_baseline_source_mutate", Title: "Manage Baseline source grant", Description: "Grant or revoke one narrow read-only email or Google Drive scope.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input baselineSourceMutationInput) (*mcp.CallToolResult, baselineSourceGrantOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageIntegrations, Mutation: true})
		if err != nil {
			return nil, baselineSourceGrantOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, baselineSourceGrantOutput{}, err
		}
		var grant baselinedomain.SourceGrant
		switch input.Action {
		case "grant":
			grant, err = s.baseline.GrantSource(ctx, baselineapp.GrantSourceCommand{Actor: actor, AccountID: input.AccountID, AssessmentID: input.AssessmentID, GrantID: ids.BaselineSourceGrantID(op), ConnectionID: input.ConnectionID, Kind: input.SourceKind, Scope: baselinedomain.SourceScope{Folders: input.Folders, Since: input.Since, Until: input.Until}, CorrelationID: op})
		case "revoke":
			grant, err = s.baseline.RevokeSource(ctx, baselineapp.RevokeSourceCommand{Actor: actor, AccountID: input.AccountID, AssessmentID: input.AssessmentID, GrantID: input.GrantID, ExpectedVersion: input.ExpectedVersion, Reason: input.Reason, CorrelationID: op})
		default:
			return nil, baselineSourceGrantOutput{}, safeError("invalid_baseline_source_action")
		}
		if err != nil {
			return nil, baselineSourceGrantOutput{}, baselineError(err)
		}
		return nil, baselineSourceGrantView(grant), nil
	})
}

func baselineSourceGrantView(grant baselinedomain.SourceGrant) baselineSourceGrantOutput {
	output := baselineSourceGrantOutput{ID: grant.ID, AccountID: grant.AccountID, AssessmentID: grant.AssessmentID, ConnectionID: grant.ConnectionID, SourceKind: grant.Kind, Folders: append([]string(nil), grant.Scope.Folders...), Since: grant.Scope.Since, Until: grant.Scope.Until, State: grant.State, GrantedBy: grant.GrantedBy.UserID, RevokeReason: grant.Reason, Version: grant.Version, CreatedAt: grant.CreatedAt, UpdatedAt: grant.UpdatedAt, RevokedAt: grant.RevokedAt}
	if grant.RevokedBy != nil {
		output.RevokedBy = grant.RevokedBy.UserID
	}
	return output
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
