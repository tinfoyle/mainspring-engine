package mcpapi

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tinfoyle/spyglass-engine/internal/application/actionrecovery"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type actionListInput struct {
	AccountID ids.AccountID        `json:"account_id"`
	State     actionrecovery.State `json:"state,omitempty"`
	Cursor    string               `json:"cursor,omitempty"`
	Limit     int                  `json:"limit,omitempty"`
}

type actionGetInput struct {
	AccountID   ids.AccountID `json:"account_id"`
	OperationID string        `json:"operation_id"`
}

type actionRequestResolutionInput struct {
	AccountID    ids.AccountID        `json:"account_id"`
	OperationID  string               `json:"operation_id"`
	ResolutionID string               `json:"resolution_id"`
	Outcome      actionrecovery.State `json:"outcome"`
	Reason       string               `json:"reason"`
}

type actionConfirmResolutionInput struct {
	AccountID    ids.AccountID `json:"account_id"`
	OperationID  string        `json:"operation_id"`
	ResolutionID string        `json:"resolution_id"`
}

func (s *Server) registerActionRecovery(server *mcp.Server, actor access.Actor) {
	requirement := access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}, Package: catalog.PackageAgents}
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_action_list", Title: "List consequential action recovery", Description: "List an Owner or Administrator bounded, content-redacted consequential-action recovery queue.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input actionListInput) (*mcp.CallToolResult, actionPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, requirement)
		if err != nil {
			return nil, actionPageOutput{}, err
		}
		bounded, err := limit(input.Limit)
		if err != nil {
			return nil, actionPageOutput{}, err
		}
		query := actionrecovery.ListQuery{State: input.State, Limit: bounded}
		if input.Cursor != "" {
			cursor, err := decodeCursor(input.Cursor, "action")
			if err != nil {
				return nil, actionPageOutput{}, err
			}
			query.AfterUpdatedAt, query.AfterOperationID = &cursor.UpdatedAt, cursor.ID
		}
		page, err := s.actions.List(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, actionPageOutput{}, actionRecoveryError(err)
		}
		output := actionPageOutput{Items: make([]actionSummaryOutput, 0, len(page.Items))}
		for _, item := range page.Items {
			output.Items = append(output.Items, actionSummaryView(item))
		}
		if page.NextCursor != nil {
			output.NextCursor = encodeCursor("action", page.NextCursor.UpdatedAt, page.NextCursor.OperationID)
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_action_get", Title: "Get consequential action recovery", Description: "Get content-redacted execution and recovery status for one consequential action.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input actionGetInput) (*mcp.CallToolResult, actionDetailOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, requirement)
		if err != nil {
			return nil, actionDetailOutput{}, err
		}
		item, err := s.actions.Get(ctx, actor, input.AccountID, input.OperationID)
		if err != nil {
			return nil, actionDetailOutput{}, actionRecoveryError(err)
		}
		return nil, actionDetailView(item), nil
	})

	mutation := requirement
	mutation.Mutation = true
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_action_request_resolution", Title: "Request consequential action resolution", Description: "Request a succeeded or failed manual outcome. A second eligible Owner or Administrator must confirm it.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input actionRequestResolutionInput) (*mcp.CallToolResult, actionDetailOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, mutation)
		if err != nil {
			return nil, actionDetailOutput{}, err
		}
		item, err := s.actions.Request(ctx, actionrecovery.RequestCommand{Actor: actor, AccountID: input.AccountID, OperationID: input.OperationID, ResolutionID: input.ResolutionID, Outcome: input.Outcome, Reason: input.Reason})
		if err != nil {
			return nil, actionDetailOutput{}, actionRecoveryError(err)
		}
		return nil, actionDetailView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_action_confirm_resolution", Title: "Confirm consequential action resolution", Description: "Confirm another eligible Owner or Administrator's pending manual outcome.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input actionConfirmResolutionInput) (*mcp.CallToolResult, actionDetailOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, mutation)
		if err != nil {
			return nil, actionDetailOutput{}, err
		}
		item, err := s.actions.Confirm(ctx, actionrecovery.ConfirmCommand{Actor: actor, AccountID: input.AccountID, OperationID: input.OperationID, ResolutionID: input.ResolutionID})
		if err != nil {
			return nil, actionDetailOutput{}, actionRecoveryError(err)
		}
		return nil, actionDetailView(item), nil
	})
}
