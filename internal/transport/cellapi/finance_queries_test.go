package cellapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	financedomain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

const (
	financeAccountID        = "e1000000-0000-4000-8000-000000000001"
	financeUserID           = "e2000000-0000-4000-8000-000000000002"
	financeLedgerID         = "e3000000-0000-4000-8000-000000000003"
	financePostingAccountID = "e4000000-0000-4000-8000-000000000004"
	financeEntryID          = "e5000000-0000-4000-8000-000000000005"
	financeReconciliationID = "e6000000-0000-4000-8000-000000000006"
)

type financeQueryTransportService struct {
	ledgerQuery         financeapp.LedgerListQuery
	accountQuery        financeapp.PostingAccountListQuery
	entryQuery          financeapp.EntryListQuery
	reconciliationQuery financeapp.ReconciliationListQuery
	now                 time.Time
}

func (service *financeQueryTransportService) GetLedger(context.Context, access.Actor, ids.AccountID, ids.FinanceLedgerID) (financedomain.Ledger, error) {
	return financedomain.Ledger{ID: financeLedgerID, AccountID: financeAccountID, Name: "Operations", Code: "OPS", Currency: "USD",
		State: financedomain.LedgerActive, Version: 2, CreatedBy: financedomain.Actor{Kind: financedomain.ActorUser, ID: financeUserID}, CreatedAt: service.now, UpdatedAt: service.now}, nil
}

func (service *financeQueryTransportService) ListLedgers(_ context.Context, _ access.Actor, _ ids.AccountID, query financeapp.LedgerListQuery) (financeapp.LedgerPage, error) {
	service.ledgerQuery = query
	item := financeapp.LedgerSummary{ID: financeLedgerID, AccountID: financeAccountID, Name: "Operations", Code: "OPS", Currency: "USD",
		State: financedomain.LedgerActive, Version: 2, AccountCount: 2, IncomeMinor: 100, NetMinor: 100, UpdatedAt: service.now}
	return financeapp.LedgerPage{Items: []financeapp.LedgerSummary{item}, NextCursor: &financeapp.LedgerCursor{Code: item.Code, ID: item.ID}}, nil
}

func (service *financeQueryTransportService) GetPostingAccount(context.Context, access.Actor, ids.AccountID, ids.FinanceAccountID) (financedomain.PostingAccount, error) {
	return financedomain.PostingAccount{ID: financePostingAccountID, AccountID: financeAccountID, LedgerID: financeLedgerID, Code: "1000", Name: "Cash",
		Type: financedomain.AccountAsset, NormalBalance: financedomain.NormalDebit, AllowPosting: true, State: financedomain.LedgerActive, Version: 3,
		CreatedBy: financedomain.Actor{Kind: financedomain.ActorUser, ID: financeUserID}, CreatedAt: service.now, UpdatedAt: service.now}, nil
}

func (service *financeQueryTransportService) ListPostingAccounts(_ context.Context, _ access.Actor, _ ids.AccountID, query financeapp.PostingAccountListQuery) (financeapp.PostingAccountPage, error) {
	service.accountQuery = query
	account, _ := service.GetPostingAccount(context.Background(), access.Actor{}, "", "")
	return financeapp.PostingAccountPage{Items: []financeapp.PostingAccountSummary{{Account: account, BalanceMinor: 100}},
		NextCursor: &financeapp.PostingAccountCursor{Code: account.Code, ID: account.ID}}, nil
}

func (service *financeQueryTransportService) GetEntry(context.Context, access.Actor, ids.AccountID, ids.FinanceEntryID) (financedomain.JournalEntry, error) {
	return financedomain.JournalEntry{ID: financeEntryID, AccountID: financeAccountID, LedgerID: financeLedgerID, Number: 7, EntryDate: service.now,
		Description: "Receipt", Currency: "USD", TotalMinor: 100, State: financedomain.EntryStatePosted, Version: 2, UpdatedAt: service.now}, nil
}

func (service *financeQueryTransportService) ListEntries(_ context.Context, _ access.Actor, _ ids.AccountID, query financeapp.EntryListQuery) (financeapp.EntryPage, error) {
	service.entryQuery = query
	item := financeapp.EntrySummary{ID: financeEntryID, AccountID: financeAccountID, LedgerID: financeLedgerID, Number: 7, EntryDate: service.now,
		Description: "Receipt", Currency: "USD", TotalMinor: 100, Source: financedomain.SourceManual, State: financedomain.EntryStatePosted, Version: 2, UpdatedAt: service.now}
	return financeapp.EntryPage{Items: []financeapp.EntrySummary{item}, NextCursor: &financeapp.EntryCursor{EntryDate: item.EntryDate, Number: item.Number}}, nil
}

func (service *financeQueryTransportService) GetReconciliation(context.Context, access.Actor, ids.AccountID, ids.FinanceReconciliationID) (financedomain.Reconciliation, error) {
	money, _ := financedomain.NewMoney("USD", 100)
	return financedomain.Reconciliation{ID: financeReconciliationID, AccountID: financeAccountID, LedgerID: financeLedgerID,
		PostingAccountID: financePostingAccountID, AsOf: service.now, StatementBalance: money, LedgerBalance: money,
		State: financedomain.ReconciliationProposed, Version: 1, UpdatedAt: service.now}, nil
}

func (service *financeQueryTransportService) ListReconciliations(_ context.Context, _ access.Actor, _ ids.AccountID, query financeapp.ReconciliationListQuery) (financeapp.ReconciliationPage, error) {
	service.reconciliationQuery = query
	money, _ := financedomain.NewMoney("USD", 100)
	item := financeapp.ReconciliationSummary{ID: financeReconciliationID, AccountID: financeAccountID, LedgerID: financeLedgerID,
		PostingAccountID: financePostingAccountID, AsOf: service.now, StatementBalance: money, LedgerBalance: money,
		State: financedomain.ReconciliationProposed, Version: 1, UpdatedAt: service.now}
	return financeapp.ReconciliationPage{Items: []financeapp.ReconciliationSummary{item},
		NextCursor: &financeapp.ReconciliationCursor{AsOf: item.AsOf, ID: item.ID}}, nil
}

func TestFinanceQueryRoutesBindAccountAndExposeOpaqueKeysets(t *testing.T) {
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	service := &financeQueryTransportService{now: now}
	server, err := New(claimAcceptor{claims: financeQueryClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithFinance(service))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/accounts/" + financeAccountID + "/finance"
	ledgers := financeQueryRequest(server.Handler(), base+"/ledgers?state=active&limit=25")
	if ledgers.Code != http.StatusOK || service.ledgerQuery.Limit != 25 || service.ledgerQuery.State != financedomain.LedgerActive || !strings.Contains(ledgers.Body.String(), `"next_cursor":"`) {
		t.Fatalf("ledgers=%d body=%s query=%+v", ledgers.Code, ledgers.Body.String(), service.ledgerQuery)
	}
	if err := contract.ValidateResponse(http.MethodGet, base+"/ledgers", ledgers.Code, ledgers.Header(), ledgers.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	ledger := financeQueryRequest(server.Handler(), base+"/ledgers/"+financeLedgerID)
	if ledger.Code != http.StatusOK || ledger.Header().Get("ETag") != `W/"2"` {
		t.Fatalf("ledger=%d ETag=%q body=%s", ledger.Code, ledger.Header().Get("ETag"), ledger.Body.String())
	}
	if err := contract.ValidateResponse(http.MethodGet, base+"/ledgers/"+financeLedgerID, ledger.Code, ledger.Header(), ledger.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	accounts := financeQueryRequest(server.Handler(), base+"/ledgers/"+financeLedgerID+"/accounts?limit=10")
	if accounts.Code != http.StatusOK || service.accountQuery.LedgerID != ids.FinanceLedgerID(financeLedgerID) || service.accountQuery.Limit != 10 {
		t.Fatalf("accounts=%d body=%s query=%+v", accounts.Code, accounts.Body.String(), service.accountQuery)
	}
	if err := contract.ValidateResponse(http.MethodGet, base+"/ledgers/"+financeLedgerID+"/accounts", accounts.Code, accounts.Header(), accounts.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	entries := financeQueryRequest(server.Handler(), base+"/ledgers/"+financeLedgerID+"/entries?state=posted")
	if entries.Code != http.StatusOK || service.entryQuery.State != financedomain.EntryStatePosted || service.entryQuery.Limit != financeapp.DefaultPageSize {
		t.Fatalf("entries=%d body=%s query=%+v", entries.Code, entries.Body.String(), service.entryQuery)
	}
	if err := contract.ValidateResponse(http.MethodGet, base+"/ledgers/"+financeLedgerID+"/entries", entries.Code, entries.Header(), entries.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	reconciliations := financeQueryRequest(server.Handler(), base+"/ledgers/"+financeLedgerID+"/reconciliations?state=proposed")
	if reconciliations.Code != http.StatusOK || service.reconciliationQuery.State != financedomain.ReconciliationProposed {
		t.Fatalf("reconciliations=%d body=%s query=%+v", reconciliations.Code, reconciliations.Body.String(), service.reconciliationQuery)
	}
	if err := contract.ValidateResponse(http.MethodGet, base+"/ledgers/"+financeLedgerID+"/reconciliations", reconciliations.Code, reconciliations.Header(), reconciliations.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestFinanceQueryRoutesRejectCrossAccountAndCursorKindConfusion(t *testing.T) {
	service := &financeQueryTransportService{now: time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)}
	server, _ := New(claimAcceptor{claims: financeQueryClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithFinance(service))
	wrong := financeQueryRequest(server.Handler(), "/api/v1/accounts/e9000000-0000-4000-8000-000000000009/finance/ledgers")
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("cross Account=%d body=%s", wrong.Code, wrong.Body.String())
	}
	entryCursor := encodeFinanceCursor(financeCursorEnvelope{Version: 1, Kind: "entry", Date: service.now, Number: 7})
	confused := financeQueryRequest(server.Handler(), "/api/v1/accounts/"+financeAccountID+"/finance/ledgers?cursor="+entryCursor)
	if confused.Code != http.StatusBadRequest || !strings.Contains(confused.Body.String(), "invalid_finance_query") {
		t.Fatalf("cursor confusion=%d body=%s", confused.Code, confused.Body.String())
	}
	unknown := financeQueryRequest(server.Handler(), "/api/v1/accounts/"+financeAccountID+"/finance/ledgers?unknown=value")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown query=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func financeQueryRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func financeQueryClaims() routecontext.Claims {
	return routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(financeAccountID), ActorKind: "user", ActorID: financeUserID,
		Role: "viewer", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 3,
		PackageAccess: &routecontext.PackageAccess{Code: "finance", Version: 1, Mode: "enabled"}}}
}

var _ FinanceService = (*financeQueryTransportService)(nil)
