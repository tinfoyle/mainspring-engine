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

type reviewListInput struct {
	AccountID  ids.AccountID                   `json:"account_id"`
	State      attentiondomain.WorkReviewState `json:"state,omitempty"`
	ReviewerID ids.UserID                      `json:"reviewer_id,omitempty"`
	WorkItemID ids.WorkItemID                  `json:"work_item_id,omitempty"`
	Cursor     string                          `json:"cursor,omitempty"`
	Limit      int                             `json:"limit,omitempty"`
}

type reviewGetInput struct {
	AccountID ids.AccountID    `json:"account_id"`
	ReviewID  ids.WorkReviewID `json:"review_id"`
}

type reviewCreateInput struct {
	AccountID      ids.AccountID  `json:"account_id"`
	OperationID    string         `json:"operation_id"`
	WorkItemID     ids.WorkItemID `json:"work_item_id"`
	WorkVersion    uint64         `json:"work_version"`
	ProposalSHA256 string         `json:"proposal_sha256"`
	Question       string         `json:"question"`
	ReviewerID     ids.UserID     `json:"reviewer_id"`
}

type reviewDecideInput struct {
	AccountID       ids.AccountID                      `json:"account_id"`
	OperationID     string                             `json:"operation_id"`
	ReviewID        ids.WorkReviewID                   `json:"review_id"`
	ExpectedVersion uint64                             `json:"expected_version"`
	Decision        attentiondomain.WorkReviewDecision `json:"decision"`
	Reason          string                             `json:"reason"`
}

type reviewCancelInput struct {
	AccountID       ids.AccountID    `json:"account_id"`
	OperationID     string           `json:"operation_id"`
	ReviewID        ids.WorkReviewID `json:"review_id"`
	ExpectedVersion uint64           `json:"expected_version"`
	Reason          string           `json:"reason"`
}

func (s *Server) registerReviews(server *mcp.Server, actor access.Actor) {
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_review_list", Title: "List Work reviews", Description: "List a bounded, redacted Account queue of assigned Work reviews.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input reviewListInput) (*mcp.CallToolResult, reviewPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork})
		if err != nil {
			return nil, reviewPageOutput{}, err
		}
		bounded, err := limit(input.Limit)
		if err != nil {
			return nil, reviewPageOutput{}, err
		}
		query := attentionapp.WorkReviewListQuery{State: input.State, ReviewerID: input.ReviewerID, WorkItemID: input.WorkItemID, Limit: bounded}
		if input.Cursor != "" {
			cursor, err := decodeCursor(input.Cursor, "review")
			if err != nil {
				return nil, reviewPageOutput{}, err
			}
			query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.WorkReviewID(cursor.ID)
		}
		page, err := s.attention.ListWorkReviews(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, reviewPageOutput{}, wrapError("list reviews", err)
		}
		output := reviewPageOutput{Items: make([]reviewSummaryOutput, 0, len(page.Items))}
		for _, item := range page.Items {
			output.Items = append(output.Items, reviewSummaryView(item))
		}
		if page.NextCursor != nil {
			output.NextCursor = encodeCursor("review", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_review_get", Title: "Get Work review", Description: "Get authorized detail for one version-bound Work review.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input reviewGetInput) (*mcp.CallToolResult, reviewOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork})
		if err != nil {
			return nil, reviewOutput{}, err
		}
		item, err := s.attention.GetWorkReview(ctx, actor, input.AccountID, input.ReviewID)
		if err != nil {
			return nil, reviewOutput{}, wrapError("get review", err)
		}
		return nil, reviewView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_review_create", Title: "Create Work review", Description: "Create an assigned review bound to an exact Work version and proposal digest.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input reviewCreateInput) (*mcp.CallToolResult, reviewOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork, Mutation: true})
		if err != nil {
			return nil, reviewOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, reviewOutput{}, err
		}
		proposal, err := digest(input.ProposalSHA256)
		if err != nil {
			return nil, reviewOutput{}, err
		}
		item, err := s.attention.CreateWorkReview(ctx, attentionapp.CreateWorkReviewCommand{Actor: actor, AccountID: input.AccountID, ReviewID: ids.WorkReviewID(op), WorkItemID: input.WorkItemID, WorkVersion: input.WorkVersion, ProposalSHA256: proposal, Question: input.Question, ReviewerID: input.ReviewerID, CorrelationID: op})
		if err != nil {
			return nil, reviewOutput{}, wrapError("create review", err)
		}
		return nil, reviewView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_review_decide", Title: "Decide Work review", Description: "Approve or request changes on an assigned Work review at its expected version.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input reviewDecideInput) (*mcp.CallToolResult, reviewOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork, Mutation: true})
		if err != nil {
			return nil, reviewOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, reviewOutput{}, err
		}
		if err := expectedVersion(input.ExpectedVersion); err != nil {
			return nil, reviewOutput{}, err
		}
		item, err := s.attention.DecideWorkReview(ctx, attentionapp.DecideWorkReviewCommand{Actor: actor, AccountID: input.AccountID, ReviewID: input.ReviewID, Decision: input.Decision, ExpectedVersion: input.ExpectedVersion, Reason: input.Reason, CorrelationID: op})
		if err != nil {
			return nil, reviewOutput{}, wrapError("decide review", err)
		}
		return nil, reviewView(item), nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_attention_review_cancel", Title: "Cancel Work review", Description: "Cancel an open Work review at its expected version.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input reviewCancelInput) (*mcp.CallToolResult, reviewOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, access.Requirement{Package: catalog.PackageWork, Mutation: true})
		if err != nil {
			return nil, reviewOutput{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, reviewOutput{}, err
		}
		if err := expectedVersion(input.ExpectedVersion); err != nil {
			return nil, reviewOutput{}, err
		}
		item, err := s.attention.CancelWorkReview(ctx, attentionapp.CancelWorkReviewCommand{Actor: actor, AccountID: input.AccountID, ReviewID: input.ReviewID, ExpectedVersion: input.ExpectedVersion, Reason: input.Reason, CorrelationID: op})
		if err != nil {
			return nil, reviewOutput{}, wrapError("cancel review", err)
		}
		return nil, reviewView(item), nil
	})
}
