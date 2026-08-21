package mcpapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	attentiondomain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var approvalCreateInputSchema = json.RawMessage(`{
  "type":"object",
  "required":["account_id","idempotency_key","operation_id","invocation_id","capability","payload","evidence_sha256","policy_version","require_independent_review","expires_at"],
  "properties":{
    "account_id":{"type":"string","format":"uuid"},
    "idempotency_key":{"type":"string","format":"uuid"},
    "operation_id":{"type":"string","format":"uuid"},
    "invocation_id":{"type":"string","format":"uuid"},
    "work_item_id":{"type":"string","format":"uuid"},
    "capability":{"type":"string","pattern":"^[a-z][a-z0-9.:/-]{0,127}$"},
    "payload":{"type":"object"},
    "evidence_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"},
    "policy_version":{"type":"integer","minimum":1},
    "require_independent_review":{"type":"boolean"},
    "expires_at":{"type":"string","format":"date-time"}
  },
  "additionalProperties":false
}`)

var approvalOutputSchema = json.RawMessage(`{
  "type":"object",
  "required":["id","operation_id","invocation_id","capability","payload","input_sha256","hash_version","evidence_sha256","proposer","policy_version","require_independent_review","expires_at","state","version","created_at","updated_at"],
  "properties":{
    "id":{"type":"string","format":"uuid"},
    "work_item_id":{"type":"string","format":"uuid"},
    "operation_id":{"type":"string","format":"uuid"},
    "invocation_id":{"type":"string","format":"uuid"},
    "capability":{"type":"string"},
    "payload":{"type":"object"},
    "input_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"},
    "hash_version":{"type":"integer","minimum":1},
    "evidence_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"},
    "proposer":{"type":"object","required":["kind","id"],"properties":{"kind":{"type":"string"},"id":{"type":"string"}},"additionalProperties":false},
    "policy_version":{"type":"integer","minimum":1},
    "require_independent_review":{"type":"boolean"},
    "expires_at":{"type":"string","format":"date-time"},
    "state":{"type":"string"},
    "decision":{"type":"object","required":["decision","reason","decided_by","decided_at"],"properties":{"decision":{"type":"string"},"reason":{"type":"string"},"decided_by":{"type":"string","format":"uuid"},"decided_at":{"type":"string","format":"date-time"}},"additionalProperties":false},
    "canceled_by":{"type":"object","required":["kind","id"],"properties":{"kind":{"type":"string"},"id":{"type":"string"}},"additionalProperties":false},
    "reason":{"type":"string"},
    "canceled_at":{"type":"string","format":"date-time"},
    "invalidated_at":{"type":"string","format":"date-time"},
    "expired_at":{"type":"string","format":"date-time"},
    "version":{"type":"integer","minimum":1},
    "created_at":{"type":"string","format":"date-time"},
    "updated_at":{"type":"string","format":"date-time"}
  },
  "additionalProperties":false
}`)

type approvalListInput struct {
	AccountID  ids.AccountID                              `json:"account_id"`
	State      attentiondomain.ConsequentialApprovalState `json:"state,omitempty"`
	WorkItemID ids.WorkItemID                             `json:"work_item_id,omitempty"`
	Cursor     string                                     `json:"cursor,omitempty"`
	Limit      int                                        `json:"limit,omitempty"`
}

type approvalGetInput struct {
	AccountID  ids.AccountID               `json:"account_id"`
	ApprovalID ids.ConsequentialApprovalID `json:"approval_id"`
}

type approvalCreateInput struct {
	AccountID         ids.AccountID         `json:"account_id"`
	IdempotencyKey    string                `json:"idempotency_key"`
	OperationID       string                `json:"operation_id"`
	InvocationID      ids.AgentInvocationID `json:"invocation_id"`
	WorkItemID        ids.WorkItemID        `json:"work_item_id,omitempty"`
	Capability        string                `json:"capability"`
	Payload           json.RawMessage       `json:"payload"`
	EvidenceSHA256    string                `json:"evidence_sha256"`
	PolicyVersion     uint64                `json:"policy_version"`
	IndependentReview bool                  `json:"require_independent_review"`
	ExpiresAt         time.Time             `json:"expires_at"`
}

type approvalDecideInput struct {
	AccountID       ids.AccountID                    `json:"account_id"`
	OperationID     string                           `json:"operation_id"`
	ApprovalID      ids.ConsequentialApprovalID      `json:"approval_id"`
	ExpectedVersion uint64                           `json:"expected_version"`
	Decision        attentiondomain.ApprovalDecision `json:"decision"`
	Reason          string                           `json:"reason"`
}

type approvalCancelInput struct {
	AccountID       ids.AccountID               `json:"account_id"`
	OperationID     string                      `json:"operation_id"`
	ApprovalID      ids.ConsequentialApprovalID `json:"approval_id"`
	ExpectedVersion uint64                      `json:"expected_version"`
	Reason          string                      `json:"reason"`
}

func (s *Server) registerApprovals(server *mcp.Server, actor access.Actor) {
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_approval_list", Title: "List consequential approvals", Description: "List a bounded redacted Account queue without payloads, digests, or human reasons.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input approvalListInput) (*mcp.CallToolResult, approvalPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageAgents})
		if err != nil {
			return nil, approvalPageOutput{}, err
		}
		bounded, err := limit(input.Limit)
		if err != nil {
			return nil, approvalPageOutput{}, err
		}
		query := attentionapp.ApprovalListQuery{State: input.State, WorkItemID: input.WorkItemID, Limit: bounded}
		if input.Cursor != "" {
			cursor, err := decodeCursor(input.Cursor, "approval")
			if err != nil {
				return nil, approvalPageOutput{}, err
			}
			query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.ConsequentialApprovalID(cursor.ID)
		}
		page, err := s.attention.ListApprovals(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, approvalPageOutput{}, wrapError("list approvals", err)
		}
		output := approvalPageOutput{Items: make([]approvalSummaryOutput, 0, len(page.Items))}
		for _, item := range page.Items {
			output.Items = append(output.Items, approvalSummaryView(item))
		}
		if page.NextCursor != nil {
			output.NextCursor = encodeCursor("approval", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_approval_get", Title: "Get consequential approval", Description: "Get Owner or Administrator decision detail for one consequential approval.", OutputSchema: approvalOutputSchema, Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input approvalGetInput) (*mcp.CallToolResult, approvalOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageAgents})
		if err != nil {
			return nil, approvalOutput{}, err
		}
		item, err := s.attention.GetApproval(ctx, actor, input.AccountID, input.ApprovalID)
		if err != nil {
			return nil, approvalOutput{}, wrapError("get approval", err)
		}
		return nil, approvalView(item), nil
	})

	server.AddTool(&mcp.Tool{Name: "spyglass_attention_approval_create", Title: "Create consequential approval", Description: "Create a proposal bound to exact canonical input, evidence, invocation, policy, and expiry.", InputSchema: approvalCreateInputSchema, OutputSchema: approvalOutputSchema, Annotations: toolAnnotations(false, false)}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input approvalCreateInput
		if err := decodeStrictToolInput(request.Params.Arguments, &input); err != nil {
			return failedToolResult(err), nil
		}
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageAgents, Mutation: true})
		if err != nil {
			return failedToolResult(err), nil
		}
		idempotencyKey, err := operationID(input.IdempotencyKey)
		if err != nil {
			return failedToolResult(err), nil
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return failedToolResult(err), nil
		}
		evidence, err := digest(input.EvidenceSHA256)
		if err != nil {
			return failedToolResult(err), nil
		}
		item, err := s.attention.CreateApproval(ctx, attentionapp.CreateApprovalCommand{Actor: actor, AccountID: input.AccountID, ApprovalID: ids.ConsequentialApprovalID(idempotencyKey), OperationID: op, InvocationID: input.InvocationID, WorkItemID: input.WorkItemID, Capability: input.Capability, CanonicalPayload: input.Payload, EvidenceSHA256: evidence, PolicyVersion: input.PolicyVersion, RequireIndependentReview: input.IndependentReview, ExpiresAt: input.ExpiresAt, CorrelationID: idempotencyKey})
		if err != nil {
			return failedToolResult(wrapError("create approval", err)), nil
		}
		return structuredToolResult(approvalView(item))
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_approval_decide", Title: "Decide consequential approval", Description: "Approve or reject a consequential proposal at its expected version.", OutputSchema: approvalOutputSchema, Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input approvalDecideInput) (*mcp.CallToolResult, approvalOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageAgents, Mutation: true})
		if err != nil {
			return nil, approvalOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, approvalOutput{}, err
		}
		if err := expectedVersion(input.ExpectedVersion); err != nil {
			return nil, approvalOutput{}, err
		}
		item, err := s.attention.DecideApproval(ctx, attentionapp.DecideApprovalCommand{Actor: actor, AccountID: input.AccountID, ApprovalID: input.ApprovalID, Decision: input.Decision, ExpectedVersion: input.ExpectedVersion, Reason: input.Reason, CorrelationID: op})
		if err != nil {
			return nil, approvalOutput{}, wrapError("decide approval", err)
		}
		return nil, approvalView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_approval_cancel", Title: "Cancel consequential approval", Description: "Cancel an open consequential proposal at its expected version.", OutputSchema: approvalOutputSchema, Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input approvalCancelInput) (*mcp.CallToolResult, approvalOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageAgents, Mutation: true})
		if err != nil {
			return nil, approvalOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, approvalOutput{}, err
		}
		if err := expectedVersion(input.ExpectedVersion); err != nil {
			return nil, approvalOutput{}, err
		}
		item, err := s.attention.CancelApproval(ctx, attentionapp.CancelApprovalCommand{Actor: actor, AccountID: input.AccountID, ApprovalID: input.ApprovalID, ExpectedVersion: input.ExpectedVersion, Reason: input.Reason, CorrelationID: op})
		if err != nil {
			return nil, approvalOutput{}, wrapError("cancel approval", err)
		}
		return nil, approvalView(item), nil
	})
}
