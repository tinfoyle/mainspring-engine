package mcpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var knowledgeClaimProposeInputSchema = json.RawMessage(`{"type":"object","required":["account_id","operation_id","scope","key","value","confidence","sensitivity","citations"],"properties":{"account_id":{"type":"string","format":"uuid"},"operation_id":{"type":"string","format":"uuid"},"scope":{"type":"object","required":["kind"],"properties":{"kind":{"type":"string","enum":["account","work_item","conversation"]},"id":{"type":"string","format":"uuid"}},"additionalProperties":false},"key":{"type":"string","pattern":"^[a-z][a-z0-9._:/-]{0,127}$"},"value":{},"confidence":{"type":"integer","minimum":0,"maximum":1000},"sensitivity":{"type":"string","enum":["public","internal","confidential","restricted"]},"citations":{"type":"array","minItems":1,"maxItems":64,"items":{"type":"object","required":["evidence_id","evidence_kind","relation","locator"],"properties":{"evidence_id":{"type":"string","format":"uuid"},"evidence_kind":{"type":"string"},"relation":{"type":"string","enum":["supports","refutes"]},"locator":{"type":"string"}},"additionalProperties":false}}},"additionalProperties":false}`)

type knowledgeScopeInput struct {
	Kind knowledgedomain.ScopeKind `json:"kind"`
	ID   string                    `json:"id,omitempty"`
}
type knowledgeCitationInput struct {
	EvidenceID   ids.KnowledgeEvidenceID          `json:"evidence_id"`
	EvidenceKind knowledgedomain.SourceKind       `json:"evidence_kind"`
	Relation     knowledgedomain.EvidenceRelation `json:"relation"`
	Locator      string                           `json:"locator"`
}
type knowledgeEvidenceRegisterInput struct {
	AccountID       ids.AccountID              `json:"account_id"`
	OperationID     string                     `json:"operation_id"`
	SourceKind      knowledgedomain.SourceKind `json:"source_kind"`
	SourceReference string                     `json:"source_reference"`
	SourceRevision  string                     `json:"source_revision"`
	ContentSHA256   string                     `json:"content_sha256"`
	CapturedAt      time.Time                  `json:"captured_at"`
}
type knowledgeClaimProposeInput struct {
	AccountID   ids.AccountID               `json:"account_id"`
	OperationID string                      `json:"operation_id"`
	Scope       knowledgeScopeInput         `json:"scope"`
	Key         string                      `json:"key"`
	Value       json.RawMessage             `json:"value"`
	Confidence  uint16                      `json:"confidence"`
	Sensitivity knowledgedomain.Sensitivity `json:"sensitivity"`
	Citations   []knowledgeCitationInput    `json:"citations"`
}
type knowledgeClaimGetInput struct {
	AccountID ids.AccountID        `json:"account_id"`
	ClaimID   ids.KnowledgeClaimID `json:"claim_id"`
}
type knowledgeClaimDecideInput struct {
	AccountID       ids.AccountID        `json:"account_id"`
	OperationID     string               `json:"operation_id"`
	ClaimID         ids.KnowledgeClaimID `json:"claim_id"`
	ExpectedVersion uint64               `json:"expected_version"`
	Accept          bool                 `json:"accept"`
	Reason          string               `json:"reason"`
}
type knowledgeFactListInput struct {
	AccountID ids.AccountID        `json:"account_id"`
	Scope     *knowledgeScopeInput `json:"scope,omitempty"`
	KeyPrefix string               `json:"key_prefix,omitempty"`
	Cursor    string               `json:"cursor,omitempty"`
	Limit     int                  `json:"limit,omitempty"`
}

type knowledgeActorOutput struct {
	Kind knowledgedomain.ActorKind `json:"kind"`
	ID   string                    `json:"id"`
}
type knowledgeScopeOutput struct {
	Kind knowledgedomain.ScopeKind `json:"kind"`
	ID   string                    `json:"id,omitempty"`
}
type knowledgeCitationOutput struct {
	EvidenceID   ids.KnowledgeEvidenceID          `json:"evidence_id"`
	EvidenceKind knowledgedomain.SourceKind       `json:"evidence_kind"`
	Relation     knowledgedomain.EvidenceRelation `json:"relation"`
	Locator      string                           `json:"locator"`
}
type knowledgeEvidenceOutput struct {
	ID              ids.KnowledgeEvidenceID    `json:"id"`
	AccountID       ids.AccountID              `json:"account_id"`
	SourceKind      knowledgedomain.SourceKind `json:"source_kind"`
	SourceReference string                     `json:"source_reference"`
	SourceRevision  string                     `json:"source_revision"`
	ContentSHA256   string                     `json:"content_sha256"`
	CapturedAt      time.Time                  `json:"captured_at"`
	CreatedBy       knowledgeActorOutput       `json:"created_by"`
	CreatedAt       time.Time                  `json:"created_at"`
}
type knowledgeDecisionOutput struct {
	Reason    string     `json:"reason"`
	DecidedBy ids.UserID `json:"decided_by_user_id"`
	DecidedAt time.Time  `json:"decided_at"`
}
type knowledgeClaimOutput struct {
	ID          ids.KnowledgeClaimID        `json:"id"`
	AccountID   ids.AccountID               `json:"account_id"`
	Scope       knowledgeScopeOutput        `json:"scope"`
	Key         string                      `json:"key"`
	Value       any                         `json:"value"`
	ValueSHA256 string                      `json:"value_sha256"`
	HashVersion uint16                      `json:"hash_version"`
	Confidence  uint16                      `json:"confidence"`
	Sensitivity knowledgedomain.Sensitivity `json:"sensitivity"`
	Citations   []knowledgeCitationOutput   `json:"citations"`
	ProposedBy  knowledgeActorOutput        `json:"proposed_by"`
	State       knowledgedomain.ClaimState  `json:"state"`
	Decision    *knowledgeDecisionOutput    `json:"decision,omitempty"`
	Version     uint64                      `json:"version"`
	CreatedAt   time.Time                   `json:"created_at"`
	UpdatedAt   time.Time                   `json:"updated_at"`
}
type knowledgeFactOutput struct {
	ID             ids.KnowledgeFactID         `json:"id"`
	AccountID      ids.AccountID               `json:"account_id,omitempty"`
	CurrentClaimID ids.KnowledgeClaimID        `json:"current_claim_id"`
	Scope          knowledgeScopeOutput        `json:"scope"`
	Key            string                      `json:"key"`
	Sensitivity    knowledgedomain.Sensitivity `json:"sensitivity"`
	State          knowledgedomain.FactState   `json:"state"`
	Revision       uint64                      `json:"revision"`
	AcceptedBy     ids.UserID                  `json:"accepted_by_user_id,omitempty"`
	AcceptedAt     time.Time                   `json:"accepted_at"`
	CreatedAt      time.Time                   `json:"created_at,omitempty"`
	UpdatedAt      time.Time                   `json:"updated_at"`
}
type knowledgeDecisionResultOutput struct {
	Claim knowledgeClaimOutput `json:"claim"`
	Fact  *knowledgeFactOutput `json:"fact,omitempty"`
}
type knowledgeFactPageOutput struct {
	Items      []knowledgeFactOutput `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

func (s *Server) registerKnowledge(server *mcp.Server, actor access.Actor) {
	read := access.Requirement{Package: catalog.PackageKnowledge}
	mutation := access.Requirement{Package: catalog.PackageKnowledge, Mutation: true}
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_knowledge_evidence_register", Title: "Register Knowledge evidence", Description: "Register one immutable exact source revision and digest.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input knowledgeEvidenceRegisterInput) (*mcp.CallToolResult, knowledgeEvidenceOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, mutation)
		if err != nil {
			return nil, knowledgeEvidenceOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, knowledgeEvidenceOutput{}, err
		}
		digest, err := digest(input.ContentSHA256)
		if err != nil {
			return nil, knowledgeEvidenceOutput{}, err
		}
		item, err := s.knowledge.RegisterEvidence(ctx, knowledgeapp.RegisterEvidenceCommand{Actor: actor, AccountID: input.AccountID, EvidenceID: ids.KnowledgeEvidenceID(op), Kind: input.SourceKind, SourceReference: input.SourceReference, SourceRevision: input.SourceRevision, ContentSHA256: digest, CapturedAt: input.CapturedAt, CorrelationID: op})
		if err != nil {
			return nil, knowledgeEvidenceOutput{}, knowledgeError(err)
		}
		return nil, knowledgeEvidenceView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_knowledge_claim_propose", Title: "Propose Knowledge claim", Description: "Propose a canonical source-attributed value for human review. Workloads cannot make it authoritative.", InputSchema: knowledgeClaimProposeInputSchema, Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input knowledgeClaimProposeInput) (*mcp.CallToolResult, knowledgeClaimOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, mutation)
		if err != nil {
			return nil, knowledgeClaimOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, knowledgeClaimOutput{}, err
		}
		citations := make([]knowledgedomain.Citation, 0, len(input.Citations))
		for _, value := range input.Citations {
			citations = append(citations, knowledgedomain.Citation{EvidenceID: value.EvidenceID, EvidenceKind: value.EvidenceKind, Relation: value.Relation, Locator: value.Locator})
		}
		item, err := s.knowledge.ProposeClaim(ctx, knowledgeapp.ProposeClaimCommand{Actor: actor, AccountID: input.AccountID, ClaimID: ids.KnowledgeClaimID(op), Scope: knowledgedomain.Scope{Kind: input.Scope.Kind, ID: input.Scope.ID}, Key: input.Key, CanonicalValue: input.Value, Confidence: input.Confidence, Sensitivity: input.Sensitivity, Citations: citations, CorrelationID: op})
		if err != nil {
			return nil, knowledgeClaimOutput{}, knowledgeError(err)
		}
		return nil, knowledgeClaimView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_knowledge_claim_get", Title: "Get Knowledge claim", Description: "Get an authorized claim value, citations, state, and decision.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input knowledgeClaimGetInput) (*mcp.CallToolResult, knowledgeClaimOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, knowledgeClaimOutput{}, err
		}
		item, err := s.knowledge.GetClaim(ctx, actor, input.AccountID, input.ClaimID)
		if err != nil {
			return nil, knowledgeClaimOutput{}, knowledgeError(err)
		}
		return nil, knowledgeClaimView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_knowledge_claim_decide", Title: "Decide Knowledge claim", Description: "Accept or reject a claim at its expected version. Acceptance requires independent supporting evidence.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input knowledgeClaimDecideInput) (*mcp.CallToolResult, knowledgeDecisionResultOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, mutation)
		if err != nil {
			return nil, knowledgeDecisionResultOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, knowledgeDecisionResultOutput{}, err
		}
		if input.ExpectedVersion == 0 {
			return nil, knowledgeDecisionResultOutput{}, safeError("knowledge_version_required")
		}
		claim, fact, err := s.knowledge.DecideClaim(ctx, knowledgeapp.DecideClaimCommand{Actor: actor, AccountID: input.AccountID, ClaimID: input.ClaimID, Accept: input.Accept, Reason: input.Reason, ExpectedVersion: input.ExpectedVersion, CorrelationID: op})
		if err != nil {
			return nil, knowledgeDecisionResultOutput{}, knowledgeError(err)
		}
		output := knowledgeDecisionResultOutput{Claim: knowledgeClaimView(claim)}
		if fact != nil {
			value := knowledgeFactView(*fact, claim.Sensitivity)
			output.Fact = &value
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_knowledge_fact_list", Title: "List Knowledge facts", Description: "List a bounded, sensitivity-filtered Account fact projection without claim values.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input knowledgeFactListInput) (*mcp.CallToolResult, knowledgeFactPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, knowledgeFactPageOutput{}, err
		}
		bounded, err := limit(input.Limit)
		if err != nil {
			return nil, knowledgeFactPageOutput{}, err
		}
		query := knowledgeapp.FactListQuery{KeyPrefix: input.KeyPrefix, Limit: bounded}
		if input.Scope != nil {
			scope := knowledgedomain.Scope{Kind: input.Scope.Kind, ID: input.Scope.ID}
			query.Scope = &scope
		}
		if input.Cursor != "" {
			cursor, err := decodeCursor(input.Cursor, "knowledge_fact")
			if err != nil {
				return nil, knowledgeFactPageOutput{}, err
			}
			query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.KnowledgeFactID(cursor.ID)
		}
		page, err := s.knowledge.ListFacts(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, knowledgeFactPageOutput{}, knowledgeError(err)
		}
		output := knowledgeFactPageOutput{Items: make([]knowledgeFactOutput, 0, len(page.Items))}
		for _, item := range page.Items {
			output.Items = append(output.Items, knowledgeFactSummaryView(item))
		}
		if page.NextCursor != nil {
			output.NextCursor = encodeCursor("knowledge_fact", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
		}
		return nil, output, nil
	})
}

func knowledgeEvidenceView(value knowledgedomain.Evidence) knowledgeEvidenceOutput {
	return knowledgeEvidenceOutput{ID: value.ID, AccountID: value.AccountID, SourceKind: value.Kind, SourceReference: value.SourceReference, SourceRevision: value.SourceRevision, ContentSHA256: hex.EncodeToString(value.ContentSHA256[:]), CapturedAt: value.CapturedAt, CreatedBy: knowledgeActorOutput{Kind: value.CreatedBy.Kind, ID: value.CreatedBy.ID}, CreatedAt: value.CreatedAt}
}
func knowledgeClaimView(value knowledgedomain.Claim) knowledgeClaimOutput {
	var decoded any
	_ = json.Unmarshal(value.CanonicalValue, &decoded)
	output := knowledgeClaimOutput{ID: value.ID, AccountID: value.AccountID, Scope: knowledgeScopeOutput{Kind: value.Scope.Kind, ID: value.Scope.ID}, Key: value.Key, Value: decoded, ValueSHA256: hex.EncodeToString(value.ValueSHA256[:]), HashVersion: value.HashVersion, Confidence: value.Confidence, Sensitivity: value.Sensitivity, Citations: make([]knowledgeCitationOutput, 0, len(value.Citations)), ProposedBy: knowledgeActorOutput{Kind: value.ProposedBy.Kind, ID: value.ProposedBy.ID}, State: value.State, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
	for _, item := range value.Citations {
		output.Citations = append(output.Citations, knowledgeCitationOutput{EvidenceID: item.EvidenceID, EvidenceKind: item.EvidenceKind, Relation: item.Relation, Locator: item.Locator})
	}
	if value.Decision != nil {
		output.Decision = &knowledgeDecisionOutput{Reason: value.Decision.Reason, DecidedBy: value.Decision.DecidedBy, DecidedAt: value.Decision.DecidedAt}
	}
	return output
}
func knowledgeFactView(value knowledgedomain.Fact, sensitivity knowledgedomain.Sensitivity) knowledgeFactOutput {
	return knowledgeFactOutput{ID: value.ID, AccountID: value.AccountID, CurrentClaimID: value.CurrentClaimID, Scope: knowledgeScopeOutput{Kind: value.Scope.Kind, ID: value.Scope.ID}, Key: value.Key, Sensitivity: sensitivity, State: value.State, Revision: value.Revision, AcceptedBy: value.AcceptedBy, AcceptedAt: value.AcceptedAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}
func knowledgeFactSummaryView(value knowledgeapp.FactSummary) knowledgeFactOutput {
	return knowledgeFactOutput{ID: value.ID, CurrentClaimID: value.CurrentClaimID, Scope: knowledgeScopeOutput{Kind: value.Scope.Kind, ID: value.Scope.ID}, Key: value.Key, Sensitivity: value.Sensitivity, State: value.State, Revision: value.Revision, AcceptedAt: value.AcceptedAt, UpdatedAt: value.UpdatedAt}
}
