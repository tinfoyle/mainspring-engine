package mcpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	financedomain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	mcpFinanceLedger  = "70000000-0000-4000-8000-000000000007"
	mcpFinanceAccount = "80000000-0000-4000-8000-000000000008"
	mcpFinanceEntry   = "90000000-0000-4000-8000-000000000009"
)

type financeMCPStub struct {
	now         time.Time
	entryCreate financeapp.CreateEntryCommand
	ledgerQuery financeapp.LedgerListQuery
	postError   error
}

func (stub *financeMCPStub) CreateLedger(context.Context, financeapp.CreateLedgerCommand) (financedomain.Ledger, bool, error) {
	return stub.ledger(), true, nil
}
func (stub *financeMCPStub) GetLedger(context.Context, access.Actor, ids.AccountID, ids.FinanceLedgerID) (financedomain.Ledger, error) {
	return stub.ledger(), nil
}
func (stub *financeMCPStub) ListLedgers(_ context.Context, _ access.Actor, _ ids.AccountID, query financeapp.LedgerListQuery) (financeapp.LedgerPage, error) {
	stub.ledgerQuery = query
	item := financeapp.LedgerSummary{ID: mcpFinanceLedger, AccountID: mcpAccount, Name: "Operations", Code: "OPS", Currency: "USD", State: financedomain.LedgerActive, Version: 1, UpdatedAt: stub.now}
	return financeapp.LedgerPage{Items: []financeapp.LedgerSummary{item}, NextCursor: &financeapp.LedgerCursor{Code: item.Code, ID: item.ID}}, nil
}
func (stub *financeMCPStub) ReviseLedger(context.Context, financeapp.ReviseLedgerCommand) (financedomain.Ledger, error) {
	return stub.ledger(), nil
}
func (stub *financeMCPStub) ClosePeriod(context.Context, financeapp.ClosePeriodCommand) (financedomain.Ledger, error) {
	return stub.ledger(), nil
}
func (stub *financeMCPStub) ArchiveLedger(context.Context, financeapp.LedgerTransitionCommand) (financedomain.Ledger, error) {
	return stub.ledger(), nil
}
func (stub *financeMCPStub) CreatePostingAccount(context.Context, financeapp.CreateAccountCommand) (financedomain.PostingAccount, bool, error) {
	return stub.postingAccount(), true, nil
}
func (stub *financeMCPStub) GetPostingAccount(context.Context, access.Actor, ids.AccountID, ids.FinanceAccountID) (financedomain.PostingAccount, error) {
	return stub.postingAccount(), nil
}
func (stub *financeMCPStub) ListPostingAccounts(context.Context, access.Actor, ids.AccountID, financeapp.PostingAccountListQuery) (financeapp.PostingAccountPage, error) {
	return financeapp.PostingAccountPage{Items: []financeapp.PostingAccountSummary{{Account: stub.postingAccount()}}}, nil
}
func (stub *financeMCPStub) RevisePostingAccount(context.Context, financeapp.RevisePostingAccountCommand) (financedomain.PostingAccount, error) {
	return stub.postingAccount(), nil
}
func (stub *financeMCPStub) ArchivePostingAccount(context.Context, financeapp.PostingAccountTransitionCommand) (financedomain.PostingAccount, error) {
	return stub.postingAccount(), nil
}
func (stub *financeMCPStub) CreateEntry(ctx context.Context, command financeapp.CreateEntryCommand) (financedomain.JournalEntry, bool, error) {
	claims, ok := routecontext.FromContext(ctx)
	if !ok || claims.Authority.PackageAccess.Code != string(catalog.PackageFinance) {
		return financedomain.JournalEntry{}, false, fmt.Errorf("missing Finance route claims")
	}
	stub.entryCreate = command
	return stub.entry(), true, nil
}
func (stub *financeMCPStub) GetEntry(context.Context, access.Actor, ids.AccountID, ids.FinanceEntryID) (financedomain.JournalEntry, error) {
	return stub.entry(), nil
}
func (stub *financeMCPStub) ListEntries(context.Context, access.Actor, ids.AccountID, financeapp.EntryListQuery) (financeapp.EntryPage, error) {
	return financeapp.EntryPage{}, nil
}
func (stub *financeMCPStub) ReviseEntry(context.Context, financeapp.ReviseEntryCommand) (financedomain.JournalEntry, error) {
	return stub.entry(), nil
}
func (stub *financeMCPStub) PostEntry(context.Context, financeapp.EntryTransitionCommand) (financedomain.JournalEntry, error) {
	if stub.postError != nil {
		return financedomain.JournalEntry{}, stub.postError
	}
	return stub.entry(), nil
}
func (stub *financeMCPStub) ReverseEntry(context.Context, financeapp.ReverseEntryCommand) (financedomain.JournalEntry, financedomain.JournalEntry, error) {
	return stub.entry(), stub.entry(), nil
}
func (stub *financeMCPStub) Reconcile(context.Context, financeapp.ReconcileCommand) (financedomain.Reconciliation, bool, error) {
	return stub.reconciliation(), true, nil
}
func (stub *financeMCPStub) GetReconciliation(context.Context, access.Actor, ids.AccountID, ids.FinanceReconciliationID) (financedomain.Reconciliation, error) {
	return stub.reconciliation(), nil
}
func (stub *financeMCPStub) ListReconciliations(context.Context, access.Actor, ids.AccountID, financeapp.ReconciliationListQuery) (financeapp.ReconciliationPage, error) {
	return financeapp.ReconciliationPage{}, nil
}
func (stub *financeMCPStub) ConfirmReconciliation(context.Context, financeapp.ConfirmReconciliationCommand) (financedomain.Reconciliation, error) {
	return stub.reconciliation(), nil
}

func (stub *financeMCPStub) ledger() financedomain.Ledger {
	return financedomain.Ledger{ID: mcpFinanceLedger, AccountID: mcpAccount, Name: "Operations", Code: "OPS", Description: "Operational Ledger", Currency: "USD", State: financedomain.LedgerActive, Version: 1, CreatedBy: financedomain.Actor{Kind: financedomain.ActorUser, ID: mcpUser}, CreatedAt: stub.now, UpdatedAt: stub.now}
}
func (stub *financeMCPStub) postingAccount() financedomain.PostingAccount {
	return financedomain.PostingAccount{ID: mcpFinanceAccount, AccountID: mcpAccount, LedgerID: mcpFinanceLedger, Code: "1000", Name: "Cash", Description: "Operating cash", Type: financedomain.AccountAsset, NormalBalance: financedomain.NormalDebit, AllowPosting: true, State: financedomain.LedgerActive, Version: 1, CreatedBy: financedomain.Actor{Kind: financedomain.ActorUser, ID: mcpUser}, CreatedAt: stub.now, UpdatedAt: stub.now}
}
func (stub *financeMCPStub) entry() financedomain.JournalEntry {
	return financedomain.JournalEntry{ID: mcpFinanceEntry, AccountID: mcpAccount, LedgerID: mcpFinanceLedger, Number: 1, EntryDate: stub.now, Description: "Receipt", Reference: "R-1", Currency: "USD", Lines: []financedomain.JournalLine{{AccountID: mcpFinanceAccount, DebitMinor: 100}, {AccountID: "81000000-0000-4000-8000-000000000018", CreditMinor: 100}}, TotalMinor: 100, Evidence: []ids.KnowledgeEvidenceID{"82000000-0000-4000-8000-000000000028"}, Provenance: financedomain.Provenance{Source: financedomain.SourceMCP}, State: financedomain.EntryStateDraft, Version: 1, CreatedBy: financedomain.Actor{Kind: financedomain.ActorUser, ID: mcpUser}, CreatedAt: stub.now, UpdatedAt: stub.now}
}
func (stub *financeMCPStub) reconciliation() financedomain.Reconciliation {
	money, _ := financedomain.NewMoney("USD", 100)
	return financedomain.Reconciliation{ID: "83000000-0000-4000-8000-000000000038", AccountID: mcpAccount, LedgerID: mcpFinanceLedger, PostingAccountID: mcpFinanceAccount, AsOf: stub.now, StatementBalance: money, LedgerBalance: money, Evidence: []ids.KnowledgeEvidenceID{"82000000-0000-4000-8000-000000000028"}, State: financedomain.ReconciliationProposed, Version: 1, CreatedBy: financedomain.Actor{Kind: financedomain.ActorUser, ID: mcpUser}, CreatedAt: stub.now, UpdatedAt: stub.now}
}

func TestFinanceMCPPublishesCompleteTypedSurface(t *testing.T) {
	session, cleanup := connectFinanceMCP(t, &testAuthority{}, &financeMCPStub{now: time.Date(2026, 8, 23, 4, 0, 0, 0, time.UTC)})
	defer cleanup()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 36 {
		t.Fatalf("tool count=%d want=36", len(result.Tools))
	}
	names := make([]string, 0, len(result.Tools))
	financeCount := 0
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		if strings.HasPrefix(tool.Name, "spyglass_finance_") {
			financeCount++
			if tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil {
				t.Fatalf("incomplete Finance tool: %+v", tool)
			}
			if tool.Name == "spyglass_finance_entry_reverse" {
				raw := fmt.Sprint(tool.InputSchema)
				for _, field := range []string{"account_id", "operation_id", "entry_id", "expected_version", "entry_date", "evidence"} {
					if !strings.Contains(raw, field) {
						t.Fatalf("reversal schema missing %s: %s", field, raw)
					}
				}
			}
		}
	}
	if financeCount != 21 || !slices.IsSorted(names) {
		t.Fatalf("finance tools=%d sorted=%v names=%v", financeCount, slices.IsSorted(names), names)
	}
}

func TestFinanceMCPUsesCanonicalServiceAndOpaqueCursor(t *testing.T) {
	stub := &financeMCPStub{now: time.Date(2026, 8, 23, 4, 0, 0, 0, time.UTC)}
	authority := &testAuthority{}
	session, cleanup := connectFinanceMCP(t, authority, stub)
	defer cleanup()
	lines := []any{map[string]any{"account_id": mcpFinanceAccount, "memo": "Deposit", "debit_minor": 100, "credit_minor": 0}, map[string]any{"account_id": "81000000-0000-4000-8000-000000000018", "memo": "Revenue", "debit_minor": 0, "credit_minor": 100}}
	created, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_finance_entry_create_draft", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "ledger_id": mcpFinanceLedger, "entry_date": "2026-08-23T00:00:00Z", "description": "Receipt", "reference": "R-1", "currency": "USD", "lines": lines, "evidence": []any{"82000000-0000-4000-8000-000000000028"}}})
	if err != nil || created.IsError {
		t.Fatalf("create err=%v result=%+v", err, created)
	}
	if stub.entryCreate.RequestID != mcpOperation || stub.entryCreate.Provenance.Source != financedomain.SourceMCP || stub.entryCreate.Actor.UserID != mcpUser {
		t.Fatalf("command=%+v", stub.entryCreate)
	}
	listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_finance_ledger_list", Arguments: map[string]any{"account_id": mcpAccount, "limit": 25}})
	if err != nil || listed.IsError || stub.ledgerQuery.Limit != 25 {
		t.Fatalf("list err=%v result=%+v query=%+v", err, listed, stub.ledgerQuery)
	}
	raw := listed.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(raw, `"next_cursor":"`) || strings.Contains(raw, `"code":"OPS","id"`) {
		t.Fatalf("cursor is not opaque: %s", raw)
	}
	if len(authority.requirements) != 2 || !authority.requirements[0].Mutation || authority.requirements[0].Package != catalog.PackageFinance || authority.requirements[1].Mutation {
		t.Fatalf("requirements=%+v", authority.requirements)
	}
}

func TestFinanceMCPRejectsMissingVersionAndRedactsBackendFailure(t *testing.T) {
	stub := &financeMCPStub{now: time.Date(2026, 8, 23, 4, 0, 0, 0, time.UTC), postError: fmt.Errorf("database secret: %w", financeapp.ErrConflict)}
	session, cleanup := connectFinanceMCP(t, &testAuthority{}, stub)
	defer cleanup()
	missing, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_finance_entry_post", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "entry_id": mcpFinanceEntry, "expected_version": 0}})
	if err != nil || !missing.IsError || !strings.Contains(missing.Content[0].(*mcp.TextContent).Text, "finance_version_required") {
		t.Fatalf("missing version err=%v result=%+v", err, missing)
	}
	failed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_finance_entry_post", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "entry_id": mcpFinanceEntry, "expected_version": 1}})
	if err != nil || !failed.IsError {
		t.Fatalf("failure err=%v result=%+v", err, failed)
	}
	raw := failed.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(raw, "finance_version_conflict") || strings.Contains(raw, "database") || strings.Contains(raw, "secret") {
		t.Fatalf("unsafe error=%q", raw)
	}
}

func connectFinanceMCP(t *testing.T, authority Authority, finance FinanceService) (*mcp.ClientSession, func()) {
	t.Helper()
	server, err := New(authority, &attentionStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Version: "0.3.0-test", MaxBody: DefaultMaxBody, TrustedOrigins: []string{"https://trusted.example"}, ResourceMetadataURL: "https://auth.infiniteocean.net/.well-known/oauth-protected-resource"}, WithFinance(finance))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL, DisableStandaloneSSE: true, HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "spyglass-finance-test", Version: "1.0.0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		httpServer.Close()
		t.Fatal(err)
	}
	return session, func() { _ = session.Close(); httpServer.Close() }
}

var _ FinanceService = (*financeMCPStub)(nil)
