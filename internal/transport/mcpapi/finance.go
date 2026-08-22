package mcpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	financedomain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type financeAccountInput struct {
	AccountID ids.AccountID `json:"account_id"`
}

type financeLedgerTargetInput struct {
	AccountID ids.AccountID       `json:"account_id"`
	LedgerID  ids.FinanceLedgerID `json:"ledger_id"`
}

type financeLedgerListInput struct {
	AccountID ids.AccountID             `json:"account_id"`
	State     financedomain.LedgerState `json:"state,omitempty"`
	Cursor    string                    `json:"cursor,omitempty"`
	Limit     int                       `json:"limit,omitempty"`
}

type financeLedgerCreateInput struct {
	AccountID   ids.AccountID `json:"account_id"`
	OperationID string        `json:"operation_id"`
	Name        string        `json:"name"`
	Code        string        `json:"code"`
	Description string        `json:"description"`
	Currency    string        `json:"currency"`
}

type financeLedgerReviseInput struct {
	AccountID       ids.AccountID       `json:"account_id"`
	OperationID     string              `json:"operation_id"`
	LedgerID        ids.FinanceLedgerID `json:"ledger_id"`
	ExpectedVersion uint64              `json:"expected_version"`
	Name            string              `json:"name"`
	Code            string              `json:"code"`
	Description     string              `json:"description"`
}

type financeLedgerCloseInput struct {
	AccountID       ids.AccountID             `json:"account_id"`
	OperationID     string                    `json:"operation_id"`
	LedgerID        ids.FinanceLedgerID       `json:"ledger_id"`
	ExpectedVersion uint64                    `json:"expected_version"`
	Through         time.Time                 `json:"through"`
	Evidence        []ids.KnowledgeEvidenceID `json:"evidence"`
}

type financeLedgerArchiveInput struct {
	AccountID       ids.AccountID       `json:"account_id"`
	OperationID     string              `json:"operation_id"`
	LedgerID        ids.FinanceLedgerID `json:"ledger_id"`
	ExpectedVersion uint64              `json:"expected_version"`
}

type financePostingAccountTargetInput struct {
	AccountID        ids.AccountID        `json:"account_id"`
	PostingAccountID ids.FinanceAccountID `json:"posting_account_id"`
}

type financePostingAccountListInput struct {
	AccountID ids.AccountID             `json:"account_id"`
	LedgerID  ids.FinanceLedgerID       `json:"ledger_id"`
	State     financedomain.LedgerState `json:"state,omitempty"`
	Cursor    string                    `json:"cursor,omitempty"`
	Limit     int                       `json:"limit,omitempty"`
}

type financePostingAccountCreateInput struct {
	AccountID       ids.AccountID             `json:"account_id"`
	OperationID     string                    `json:"operation_id"`
	LedgerID        ids.FinanceLedgerID       `json:"ledger_id"`
	ParentAccountID ids.FinanceAccountID      `json:"parent_account_id,omitempty"`
	Code            string                    `json:"code"`
	Name            string                    `json:"name"`
	Description     string                    `json:"description"`
	Type            financedomain.AccountType `json:"type"`
	AllowPosting    bool                      `json:"allow_posting"`
}

type financePostingAccountReviseInput struct {
	AccountID        ids.AccountID        `json:"account_id"`
	OperationID      string               `json:"operation_id"`
	PostingAccountID ids.FinanceAccountID `json:"posting_account_id"`
	ExpectedVersion  uint64               `json:"expected_version"`
	ParentAccountID  ids.FinanceAccountID `json:"parent_account_id,omitempty"`
	Code             string               `json:"code"`
	Name             string               `json:"name"`
	Description      string               `json:"description"`
	AllowPosting     bool                 `json:"allow_posting"`
}

type financePostingAccountArchiveInput struct {
	AccountID        ids.AccountID        `json:"account_id"`
	OperationID      string               `json:"operation_id"`
	PostingAccountID ids.FinanceAccountID `json:"posting_account_id"`
	ExpectedVersion  uint64               `json:"expected_version"`
}

type financeEntryTargetInput struct {
	AccountID ids.AccountID      `json:"account_id"`
	EntryID   ids.FinanceEntryID `json:"entry_id"`
}

type financeEntryListInput struct {
	AccountID ids.AccountID            `json:"account_id"`
	LedgerID  ids.FinanceLedgerID      `json:"ledger_id"`
	State     financedomain.EntryState `json:"state,omitempty"`
	Cursor    string                   `json:"cursor,omitempty"`
	Limit     int                      `json:"limit,omitempty"`
}

type financeEntryCreateInput struct {
	AccountID   ids.AccountID               `json:"account_id"`
	OperationID string                      `json:"operation_id"`
	LedgerID    ids.FinanceLedgerID         `json:"ledger_id"`
	EntryDate   time.Time                   `json:"entry_date"`
	Description string                      `json:"description"`
	Reference   string                      `json:"reference"`
	Currency    string                      `json:"currency"`
	Lines       []financedomain.JournalLine `json:"lines"`
	Evidence    []ids.KnowledgeEvidenceID   `json:"evidence"`
}

type financeEntryReviseInput struct {
	AccountID       ids.AccountID               `json:"account_id"`
	OperationID     string                      `json:"operation_id"`
	EntryID         ids.FinanceEntryID          `json:"entry_id"`
	ExpectedVersion uint64                      `json:"expected_version"`
	EntryDate       time.Time                   `json:"entry_date"`
	Description     string                      `json:"description"`
	Reference       string                      `json:"reference"`
	Lines           []financedomain.JournalLine `json:"lines"`
	Evidence        []ids.KnowledgeEvidenceID   `json:"evidence"`
}

type financeEntryTransitionInput struct {
	AccountID       ids.AccountID      `json:"account_id"`
	OperationID     string             `json:"operation_id"`
	EntryID         ids.FinanceEntryID `json:"entry_id"`
	ExpectedVersion uint64             `json:"expected_version"`
}

type financeEntryReverseInput struct {
	financeEntryTransitionInput
	EntryDate   time.Time                 `json:"entry_date"`
	Description string                    `json:"description"`
	Reference   string                    `json:"reference"`
	Evidence    []ids.KnowledgeEvidenceID `json:"evidence"`
}

type financeReconciliationTargetInput struct {
	AccountID        ids.AccountID               `json:"account_id"`
	ReconciliationID ids.FinanceReconciliationID `json:"reconciliation_id"`
}

type financeReconciliationListInput struct {
	AccountID ids.AccountID                     `json:"account_id"`
	LedgerID  ids.FinanceLedgerID               `json:"ledger_id"`
	State     financedomain.ReconciliationState `json:"state,omitempty"`
	Cursor    string                            `json:"cursor,omitempty"`
	Limit     int                               `json:"limit,omitempty"`
}

type financeReconciliationCreateInput struct {
	AccountID        ids.AccountID             `json:"account_id"`
	OperationID      string                    `json:"operation_id"`
	LedgerID         ids.FinanceLedgerID       `json:"ledger_id"`
	PostingAccountID ids.FinanceAccountID      `json:"posting_account_id"`
	AsOf             time.Time                 `json:"as_of"`
	StatementBalance financedomain.Money       `json:"statement_balance"`
	Evidence         []ids.KnowledgeEvidenceID `json:"evidence"`
}

type financeReconciliationConfirmInput struct {
	AccountID        ids.AccountID               `json:"account_id"`
	OperationID      string                      `json:"operation_id"`
	ReconciliationID ids.FinanceReconciliationID `json:"reconciliation_id"`
	ExpectedVersion  uint64                      `json:"expected_version"`
}

type financeLedgerPageOutput struct {
	Items      []financeapp.LedgerSummary `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

type financePostingAccountPageOutput struct {
	Items      []financeapp.PostingAccountSummary `json:"items"`
	NextCursor string                             `json:"next_cursor,omitempty"`
}

type financeEntryPageOutput struct {
	Items      []financeapp.EntrySummary `json:"items"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

type financeReconciliationPageOutput struct {
	Items      []financeapp.ReconciliationSummary `json:"items"`
	NextCursor string                             `json:"next_cursor,omitempty"`
}

type financeReversalOutput struct {
	Original financedomain.JournalEntry `json:"original"`
	Reversal financedomain.JournalEntry `json:"reversal"`
}

func (s *Server) registerFinance(server *mcp.Server, actor access.Actor) {
	read := access.Requirement{Package: catalog.PackageFinance}
	mutation := access.Requirement{Package: catalog.PackageFinance, Mutation: true}

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_ledger_list", Title: "List Finance ledgers", Description: "List bounded Account-owned operational Ledger summaries and calculated results.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeLedgerListInput) (*mcp.CallToolResult, financeLedgerPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, financeLedgerPageOutput{}, err
		}
		bounded, err := financeLimit(input.Limit)
		if err != nil {
			return nil, financeLedgerPageOutput{}, err
		}
		query := financeapp.LedgerListQuery{State: input.State, Limit: bounded}
		if input.Cursor != "" {
			cursor, err := decodeFinanceMCPCursor(input.Cursor, "ledger")
			if err != nil {
				return nil, financeLedgerPageOutput{}, err
			}
			query.After = &financeapp.LedgerCursor{Code: cursor.Code, ID: ids.FinanceLedgerID(cursor.ID)}
		}
		page, err := s.finance.ListLedgers(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, financeLedgerPageOutput{}, financeError(err)
		}
		output := financeLedgerPageOutput{Items: page.Items}
		if page.NextCursor != nil {
			output.NextCursor = encodeFinanceMCPCursor(financeMCPCursor{Version: 1, Kind: "ledger", Code: page.NextCursor.Code, ID: string(page.NextCursor.ID)})
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_ledger_get", Title: "Get Finance ledger", Description: "Get the current versioned operational Ledger.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeLedgerTargetInput) (*mcp.CallToolResult, financedomain.Ledger, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, financedomain.Ledger{}, err
		}
		value, err := s.finance.GetLedger(ctx, actor, input.AccountID, input.LedgerID)
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_ledger_create", Title: "Create Finance ledger", Description: "Create an Account-owned one-currency operational Ledger. Owner or Administrator only.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeLedgerCreateInput) (*mcp.CallToolResult, financedomain.Ledger, error) {
		ctx, op, err := s.financeCreateContext(ctx, actor, input.AccountID, input.OperationID, mutation)
		if err != nil {
			return nil, financedomain.Ledger{}, err
		}
		value, _, err := s.finance.CreateLedger(ctx, financeapp.CreateLedgerCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, Name: input.Name, Code: input.Code, Description: input.Description, Currency: input.Currency})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_ledger_revise", Title: "Revise Finance ledger", Description: "Revise Ledger identity fields using the current version. Owner or Administrator only.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeLedgerReviseInput) (*mcp.CallToolResult, financedomain.Ledger, error) {
		ctx, op, err := s.financeMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, financedomain.Ledger{}, err
		}
		value, err := s.finance.ReviseLedger(ctx, financeapp.ReviseLedgerCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, LedgerID: input.LedgerID, ExpectedVersion: input.ExpectedVersion, Name: input.Name, Code: input.Code, Description: input.Description})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_ledger_close_period", Title: "Close Finance period", Description: "Monotonically close a Ledger through a date with accepted Evidence. Owner or Administrator only.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeLedgerCloseInput) (*mcp.CallToolResult, financedomain.Ledger, error) {
		ctx, op, err := s.financeMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, financedomain.Ledger{}, err
		}
		value, err := s.finance.ClosePeriod(ctx, financeapp.ClosePeriodCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, LedgerID: input.LedgerID, ExpectedVersion: input.ExpectedVersion, Through: input.Through, Evidence: input.Evidence})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_ledger_archive", Title: "Archive Finance ledger", Description: "Archive an empty Ledger after its chart is archived and drafts are resolved. Owner or Administrator only.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeLedgerArchiveInput) (*mcp.CallToolResult, financedomain.Ledger, error) {
		ctx, op, err := s.financeMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, financedomain.Ledger{}, err
		}
		value, err := s.finance.ArchiveLedger(ctx, financeapp.LedgerTransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, LedgerID: input.LedgerID, ExpectedVersion: input.ExpectedVersion})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_posting_account_list", Title: "List Finance posting accounts", Description: "List a bounded chart page with calculated normal-balance amounts.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financePostingAccountListInput) (*mcp.CallToolResult, financePostingAccountPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, financePostingAccountPageOutput{}, err
		}
		bounded, err := financeLimit(input.Limit)
		if err != nil {
			return nil, financePostingAccountPageOutput{}, err
		}
		query := financeapp.PostingAccountListQuery{LedgerID: input.LedgerID, State: input.State, Limit: bounded}
		if input.Cursor != "" {
			cursor, err := decodeFinanceMCPCursor(input.Cursor, "account")
			if err != nil {
				return nil, financePostingAccountPageOutput{}, err
			}
			query.After = &financeapp.PostingAccountCursor{Code: cursor.Code, ID: ids.FinanceAccountID(cursor.ID)}
		}
		page, err := s.finance.ListPostingAccounts(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, financePostingAccountPageOutput{}, financeError(err)
		}
		output := financePostingAccountPageOutput{Items: page.Items}
		if page.NextCursor != nil {
			output.NextCursor = encodeFinanceMCPCursor(financeMCPCursor{Version: 1, Kind: "account", Code: page.NextCursor.Code, ID: string(page.NextCursor.ID)})
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_posting_account_get", Title: "Get Finance posting account", Description: "Get one versioned posting account and hierarchy position.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financePostingAccountTargetInput) (*mcp.CallToolResult, financedomain.PostingAccount, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, financedomain.PostingAccount{}, err
		}
		value, err := s.finance.GetPostingAccount(ctx, actor, input.AccountID, input.PostingAccountID)
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_posting_account_create", Title: "Create Finance posting account", Description: "Create a typed account in a Ledger chart. Owner or Administrator only.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financePostingAccountCreateInput) (*mcp.CallToolResult, financedomain.PostingAccount, error) {
		ctx, op, err := s.financeCreateContext(ctx, actor, input.AccountID, input.OperationID, mutation)
		if err != nil {
			return nil, financedomain.PostingAccount{}, err
		}
		value, _, err := s.finance.CreatePostingAccount(ctx, financeapp.CreateAccountCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, LedgerID: input.LedgerID, ParentAccountID: input.ParentAccountID, Code: input.Code, Name: input.Name, Description: input.Description, Type: input.Type, AllowPosting: input.AllowPosting})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_posting_account_revise", Title: "Revise Finance posting account", Description: "Revise chart identity, hierarchy and posting policy using the current version.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financePostingAccountReviseInput) (*mcp.CallToolResult, financedomain.PostingAccount, error) {
		ctx, op, err := s.financeMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, financedomain.PostingAccount{}, err
		}
		value, err := s.finance.RevisePostingAccount(ctx, financeapp.RevisePostingAccountCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, PostingAccountID: input.PostingAccountID, ExpectedVersion: input.ExpectedVersion, ParentAccountID: input.ParentAccountID, Code: input.Code, Name: input.Name, Description: input.Description, AllowPosting: input.AllowPosting})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_posting_account_archive", Title: "Archive Finance posting account", Description: "Archive an unused posting account after children and drafts are resolved.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financePostingAccountArchiveInput) (*mcp.CallToolResult, financedomain.PostingAccount, error) {
		ctx, op, err := s.financeMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, financedomain.PostingAccount{}, err
		}
		value, err := s.finance.ArchivePostingAccount(ctx, financeapp.PostingAccountTransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, PostingAccountID: input.PostingAccountID, ExpectedVersion: input.ExpectedVersion})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_entry_list", Title: "List Finance journal entries", Description: "List a bounded newest-first operational journal page.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeEntryListInput) (*mcp.CallToolResult, financeEntryPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, financeEntryPageOutput{}, err
		}
		bounded, err := financeLimit(input.Limit)
		if err != nil {
			return nil, financeEntryPageOutput{}, err
		}
		query := financeapp.EntryListQuery{LedgerID: input.LedgerID, State: input.State, Limit: bounded}
		if input.Cursor != "" {
			cursor, err := decodeFinanceMCPCursor(input.Cursor, "entry")
			if err != nil {
				return nil, financeEntryPageOutput{}, err
			}
			query.After = &financeapp.EntryCursor{EntryDate: cursor.Date, Number: cursor.Number}
		}
		page, err := s.finance.ListEntries(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, financeEntryPageOutput{}, financeError(err)
		}
		output := financeEntryPageOutput{Items: page.Items}
		if page.NextCursor != nil {
			output.NextCursor = encodeFinanceMCPCursor(financeMCPCursor{Version: 1, Kind: "entry", Date: page.NextCursor.EntryDate, Number: page.NextCursor.Number})
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_entry_get", Title: "Get Finance journal entry", Description: "Get a draft or immutable posted/reversed journal entry with lines, Evidence and provenance.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeEntryTargetInput) (*mcp.CallToolResult, financedomain.JournalEntry, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, financedomain.JournalEntry{}, err
		}
		value, err := s.finance.GetEntry(ctx, actor, input.AccountID, input.EntryID)
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_entry_create_draft", Title: "Create Finance journal draft", Description: "Create a balanced MCP-provenance journal draft. This never posts or executes a payment.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeEntryCreateInput) (*mcp.CallToolResult, financedomain.JournalEntry, error) {
		ctx, op, err := s.financeCreateContext(ctx, actor, input.AccountID, input.OperationID, mutation)
		if err != nil {
			return nil, financedomain.JournalEntry{}, err
		}
		value, _, err := s.finance.CreateEntry(ctx, financeapp.CreateEntryCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, LedgerID: input.LedgerID, EntryDate: input.EntryDate, Description: input.Description, Reference: input.Reference, Currency: input.Currency, Lines: input.Lines, Evidence: input.Evidence, Provenance: financedomain.Provenance{Source: financedomain.SourceMCP}})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_entry_revise_draft", Title: "Revise Finance journal draft", Description: "Replace a balanced draft's date, lines and Evidence using the current version.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeEntryReviseInput) (*mcp.CallToolResult, financedomain.JournalEntry, error) {
		ctx, op, err := s.financeMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, financedomain.JournalEntry{}, err
		}
		value, err := s.finance.ReviseEntry(ctx, financeapp.ReviseEntryCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, EntryID: input.EntryID, ExpectedVersion: input.ExpectedVersion, EntryDate: input.EntryDate, Description: input.Description, Reference: input.Reference, Lines: input.Lines, Evidence: input.Evidence})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_entry_post", Title: "Post Finance journal entry", Description: "Irreversibly post an evidence-bound draft. Owner or Administrator only; correction requires reversal.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeEntryTransitionInput) (*mcp.CallToolResult, financedomain.JournalEntry, error) {
		ctx, op, err := s.financeMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, financedomain.JournalEntry{}, err
		}
		value, err := s.finance.PostEntry(ctx, financeapp.EntryTransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, EntryID: input.EntryID, ExpectedVersion: input.ExpectedVersion})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_entry_reverse", Title: "Reverse Finance journal entry", Description: "Create and post a separately numbered evidence-bound reversal. Owner or Administrator only.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeEntryReverseInput) (*mcp.CallToolResult, financeReversalOutput, error) {
		ctx, op, err := s.financeMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, financeReversalOutput{}, err
		}
		original, reversal, err := s.finance.ReverseEntry(ctx, financeapp.ReverseEntryCommand{EntryTransitionCommand: financeapp.EntryTransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, EntryID: input.EntryID, ExpectedVersion: input.ExpectedVersion}, EntryDate: input.EntryDate, Description: input.Description, Reference: input.Reference, Evidence: input.Evidence})
		return nil, financeReversalOutput{Original: original, Reversal: reversal}, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_reconciliation_list", Title: "List Finance reconciliations", Description: "List bounded newest-first reconciliation summaries without hiding discrepancies.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeReconciliationListInput) (*mcp.CallToolResult, financeReconciliationPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, financeReconciliationPageOutput{}, err
		}
		bounded, err := financeLimit(input.Limit)
		if err != nil {
			return nil, financeReconciliationPageOutput{}, err
		}
		query := financeapp.ReconciliationListQuery{LedgerID: input.LedgerID, State: input.State, Limit: bounded}
		if input.Cursor != "" {
			cursor, err := decodeFinanceMCPCursor(input.Cursor, "reconciliation")
			if err != nil {
				return nil, financeReconciliationPageOutput{}, err
			}
			query.After = &financeapp.ReconciliationCursor{AsOf: cursor.Date, ID: ids.FinanceReconciliationID(cursor.ID)}
		}
		page, err := s.finance.ListReconciliations(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, financeReconciliationPageOutput{}, financeError(err)
		}
		output := financeReconciliationPageOutput{Items: page.Items}
		if page.NextCursor != nil {
			output.NextCursor = encodeFinanceMCPCursor(financeMCPCursor{Version: 1, Kind: "reconciliation", Date: page.NextCursor.AsOf, ID: string(page.NextCursor.ID)})
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_reconciliation_get", Title: "Get Finance reconciliation", Description: "Get Evidence, calculated balance, explicit difference and confirmation state.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeReconciliationTargetInput) (*mcp.CallToolResult, financedomain.Reconciliation, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, financedomain.Reconciliation{}, err
		}
		value, err := s.finance.GetReconciliation(ctx, actor, input.AccountID, input.ReconciliationID)
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_reconciliation_create", Title: "Create Finance reconciliation", Description: "Compare an evidence-bound statement balance with the database-calculated Ledger balance.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeReconciliationCreateInput) (*mcp.CallToolResult, financedomain.Reconciliation, error) {
		ctx, op, err := s.financeCreateContext(ctx, actor, input.AccountID, input.OperationID, mutation)
		if err != nil {
			return nil, financedomain.Reconciliation{}, err
		}
		value, _, err := s.finance.Reconcile(ctx, financeapp.ReconcileCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, LedgerID: input.LedgerID, PostingAccountID: input.PostingAccountID, AsOf: input.AsOf, StatementBalance: input.StatementBalance, Evidence: input.Evidence})
		return nil, value, financeError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_finance_reconciliation_confirm", Title: "Confirm Finance reconciliation", Description: "Confirm only a zero-difference reconciliation using the current version. Owner or Administrator only.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input financeReconciliationConfirmInput) (*mcp.CallToolResult, financedomain.Reconciliation, error) {
		ctx, op, err := s.financeMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, financedomain.Reconciliation{}, err
		}
		value, err := s.finance.ConfirmReconciliation(ctx, financeapp.ConfirmReconciliationCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, ReconciliationID: input.ReconciliationID, ExpectedVersion: input.ExpectedVersion})
		return nil, value, financeError(err)
	})
}

func (s *Server) financeMutationContext(ctx context.Context, actor access.Actor, accountID ids.AccountID, rawOperation string, requirement access.Requirement, version uint64) (context.Context, string, error) {
	ctx, op, err := s.financeCreateContext(ctx, actor, accountID, rawOperation, requirement)
	if err != nil {
		return nil, "", err
	}
	if version == 0 {
		return nil, "", safeError("finance_version_required")
	}
	return ctx, op, nil
}

func (s *Server) financeCreateContext(ctx context.Context, actor access.Actor, accountID ids.AccountID, rawOperation string, requirement access.Requirement) (context.Context, string, error) {
	ctx, err := s.toolContext(ctx, actor, accountID, requirement)
	if err != nil {
		return nil, "", err
	}
	op, err := operationID(rawOperation)
	if err != nil {
		return nil, "", err
	}
	return ctx, op, nil
}

func financeLimit(value int) (int, error) {
	if value == 0 {
		return financeapp.DefaultPageSize, nil
	}
	if value < 1 || value > financeapp.MaximumPageSize {
		return 0, safeError("invalid_finance_limit")
	}
	return value, nil
}

type financeMCPCursor struct {
	Version int       `json:"v"`
	Kind    string    `json:"kind"`
	Code    string    `json:"code,omitempty"`
	ID      string    `json:"id,omitempty"`
	Date    time.Time `json:"date,omitempty"`
	Number  uint64    `json:"number,omitempty"`
}

func encodeFinanceMCPCursor(value financeMCPCursor) string {
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeFinanceMCPCursor(raw, kind string) (financeMCPCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 1024 {
		return financeMCPCursor{}, safeError("invalid_finance_cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var value financeMCPCursor
	if decoder.Decode(&value) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || value.Version != 1 || value.Kind != kind {
		return financeMCPCursor{}, safeError("invalid_finance_cursor")
	}
	switch kind {
	case "ledger", "account":
		if value.Code == "" || ids.Validate(value.ID) != nil || !value.Date.IsZero() || value.Number != 0 {
			return financeMCPCursor{}, safeError("invalid_finance_cursor")
		}
	case "entry":
		if value.Date.IsZero() || value.Number == 0 || value.Code != "" || value.ID != "" {
			return financeMCPCursor{}, safeError("invalid_finance_cursor")
		}
	case "reconciliation":
		if value.Date.IsZero() || ids.Validate(value.ID) != nil || value.Code != "" || value.Number != 0 {
			return financeMCPCursor{}, safeError("invalid_finance_cursor")
		}
	default:
		return financeMCPCursor{}, safeError("invalid_finance_cursor")
	}
	return value, nil
}

var _ FinanceService = (*financeapp.Service)(nil)
