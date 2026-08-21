package mcpapi

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	attentiondomain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type informationListInput struct {
	AccountID        ids.AccountID                           `json:"account_id"`
	State            attentiondomain.InformationRequestState `json:"state,omitempty"`
	ParentWorkItemID ids.WorkItemID                          `json:"parent_work_item_id,omitempty"`
	Cursor           string                                  `json:"cursor,omitempty"`
	Limit            int                                     `json:"limit,omitempty"`
}

type informationGetInput struct {
	AccountID ids.AccountID            `json:"account_id"`
	RequestID ids.InformationRequestID `json:"request_id"`
}

type informationCreateInput struct {
	AccountID        ids.AccountID    `json:"account_id"`
	OperationID      string           `json:"operation_id"`
	ParentWorkItemID ids.WorkItemID   `json:"parent_work_item_id"`
	Requirement      requirementInput `json:"requirement"`
	Question         string           `json:"question"`
}

type informationAnswerInput struct {
	AccountID       ids.AccountID            `json:"account_id"`
	OperationID     string                   `json:"operation_id"`
	RequestID       ids.InformationRequestID `json:"request_id"`
	ExpectedVersion uint64                   `json:"expected_version"`
	FactID          string                   `json:"fact_id"`
	FactVersion     uint64                   `json:"fact_version"`
	Requirement     requirementInput         `json:"requirement"`
}

type informationCancelInput struct {
	AccountID       ids.AccountID            `json:"account_id"`
	OperationID     string                   `json:"operation_id"`
	RequestID       ids.InformationRequestID `json:"request_id"`
	ExpectedVersion uint64                   `json:"expected_version"`
	Reason          string                   `json:"reason"`
}

func (s *Server) registerInformation(server *mcp.Server, actor access.Actor) {
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_information_list", Title: "List information requests", Description: "List a bounded, redacted Account Attention queue for human information requests.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input informationListInput) (*mcp.CallToolResult, informationPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork})
		if err != nil {
			return nil, informationPageOutput{}, err
		}
		bounded, err := limit(input.Limit)
		if err != nil {
			return nil, informationPageOutput{}, err
		}
		query := attentionapp.InformationListQuery{State: input.State, ParentWorkItem: input.ParentWorkItemID, Limit: bounded}
		if input.Cursor != "" {
			cursor, err := decodeCursor(input.Cursor, "information")
			if err != nil {
				return nil, informationPageOutput{}, err
			}
			query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.InformationRequestID(cursor.ID)
		}
		page, err := s.attention.ListInformation(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, informationPageOutput{}, wrapError("list information", err)
		}
		output := informationPageOutput{Items: make([]informationSummaryOutput, 0, len(page.Items))}
		for _, item := range page.Items {
			output.Items = append(output.Items, informationSummaryView(item))
		}
		if page.NextCursor != nil {
			output.NextCursor = encodeCursor("information", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_information_get", Title: "Get information request", Description: "Get authorized detail for one information request.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input informationGetInput) (*mcp.CallToolResult, informationOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork})
		if err != nil {
			return nil, informationOutput{}, err
		}
		item, err := s.attention.GetInformation(ctx, actor, input.AccountID, input.RequestID)
		if err != nil {
			return nil, informationOutput{}, wrapError("get information", err)
		}
		return nil, informationView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_information_create", Title: "Create information request", Description: "Create an exact fact-bound information request for a parent Work item.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input informationCreateInput) (*mcp.CallToolResult, informationOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork, Mutation: true})
		if err != nil {
			return nil, informationOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, informationOutput{}, err
		}
		item, err := s.attention.CreateInformation(ctx, attentionapp.CreateInformationCommand{Actor: actor, AccountID: input.AccountID, RequestID: ids.InformationRequestID(op), ParentWorkItemID: input.ParentWorkItemID, Requirement: input.Requirement.domain(), Question: input.Question, CorrelationID: op})
		if err != nil {
			return nil, informationOutput{}, wrapError("create information", err)
		}
		return nil, informationView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_information_answer", Title: "Answer information request", Description: "Answer the exact eligible information cohort and resume only unblocked parent Work items.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input informationAnswerInput) (*mcp.CallToolResult, informationCompletionOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork, Mutation: true})
		if err != nil {
			return nil, informationCompletionOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, informationCompletionOutput{}, err
		}
		if err := expectedVersion(input.ExpectedVersion); err != nil {
			return nil, informationCompletionOutput{}, err
		}
		completion, err := s.attention.AnswerInformation(ctx, attentionapp.AnswerInformationCommand{Actor: actor, AccountID: input.AccountID, RequestID: input.RequestID, ExpectedVersion: input.ExpectedVersion, CorrelationID: op, Fact: attentiondomain.FactReference{ID: input.FactID, Version: input.FactVersion, Requirement: input.Requirement.domain()}})
		if err != nil {
			return nil, informationCompletionOutput{}, wrapError("answer information", err)
		}
		output := informationCompletionOutput{Answered: make([]informationOutput, 0, len(completion.Answered)), ResumableParentIDs: completion.ResumableParents}
		for _, item := range completion.Answered {
			output.Answered = append(output.Answered, informationView(item))
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_information_cancel", Title: "Cancel information request", Description: "Cancel an open information request at its expected version.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input informationCancelInput) (*mcp.CallToolResult, informationOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork, Mutation: true})
		if err != nil {
			return nil, informationOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, informationOutput{}, err
		}
		if err := expectedVersion(input.ExpectedVersion); err != nil {
			return nil, informationOutput{}, err
		}
		item, err := s.attention.CancelInformation(ctx, attentionapp.CancelInformationCommand{Actor: actor, AccountID: input.AccountID, RequestID: input.RequestID, ExpectedVersion: input.ExpectedVersion, Reason: input.Reason, CorrelationID: op})
		if err != nil {
			return nil, informationOutput{}, wrapError("cancel information", err)
		}
		return nil, informationView(item), nil
	})
}
