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
	financedomain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

const financeOperationID = "e7000000-0000-4000-8000-000000000007"

type financeCommandTransportService struct {
	now                   time.Time
	ledgerCreate          financeapp.CreateLedgerCommand
	ledgerRevise          financeapp.ReviseLedgerCommand
	ledgerClose           financeapp.ClosePeriodCommand
	ledgerArchive         financeapp.LedgerTransitionCommand
	accountCreate         financeapp.CreateAccountCommand
	accountRevise         financeapp.RevisePostingAccountCommand
	accountArchive        financeapp.PostingAccountTransitionCommand
	entryCreate           financeapp.CreateEntryCommand
	entryRevise           financeapp.ReviseEntryCommand
	entryTransition       financeapp.EntryTransitionCommand
	entryReverse          financeapp.ReverseEntryCommand
	reconciliationCreate  financeapp.ReconcileCommand
	reconciliationConfirm financeapp.ConfirmReconciliationCommand
}

func (service *financeCommandTransportService) CreateLedger(_ context.Context, command financeapp.CreateLedgerCommand) (financedomain.Ledger, bool, error) {
	service.ledgerCreate = command
	return service.ledger(ids.FinanceLedgerID(command.RequestID), 1), true, nil
}

func (service *financeCommandTransportService) ReviseLedger(_ context.Context, command financeapp.ReviseLedgerCommand) (financedomain.Ledger, error) {
	service.ledgerRevise = command
	return service.ledger(command.LedgerID, command.ExpectedVersion+1), nil
}

func (service *financeCommandTransportService) ClosePeriod(_ context.Context, command financeapp.ClosePeriodCommand) (financedomain.Ledger, error) {
	service.ledgerClose = command
	value := service.ledger(command.LedgerID, command.ExpectedVersion+1)
	value.ClosedThrough, value.CloseEvidence = &command.Through, command.Evidence
	return value, nil
}

func (service *financeCommandTransportService) ArchiveLedger(_ context.Context, command financeapp.LedgerTransitionCommand) (financedomain.Ledger, error) {
	service.ledgerArchive = command
	value := service.ledger(command.LedgerID, command.ExpectedVersion+1)
	value.State = financedomain.LedgerArchived
	return value, nil
}

func (service *financeCommandTransportService) CreatePostingAccount(_ context.Context, command financeapp.CreateAccountCommand) (financedomain.PostingAccount, bool, error) {
	service.accountCreate = command
	return service.postingAccount(ids.FinanceAccountID(command.RequestID), command.LedgerID, 1), true, nil
}

func (service *financeCommandTransportService) RevisePostingAccount(_ context.Context, command financeapp.RevisePostingAccountCommand) (financedomain.PostingAccount, error) {
	service.accountRevise = command
	return service.postingAccount(command.PostingAccountID, financeLedgerID, command.ExpectedVersion+1), nil
}

func (service *financeCommandTransportService) ArchivePostingAccount(_ context.Context, command financeapp.PostingAccountTransitionCommand) (financedomain.PostingAccount, error) {
	service.accountArchive = command
	value := service.postingAccount(command.PostingAccountID, financeLedgerID, command.ExpectedVersion+1)
	value.State = financedomain.LedgerArchived
	return value, nil
}

func (service *financeCommandTransportService) CreateEntry(_ context.Context, command financeapp.CreateEntryCommand) (financedomain.JournalEntry, bool, error) {
	service.entryCreate = command
	return service.entry(ids.FinanceEntryID(command.RequestID), command.LedgerID, 1, financedomain.EntryStateDraft), true, nil
}

func (service *financeCommandTransportService) ReviseEntry(_ context.Context, command financeapp.ReviseEntryCommand) (financedomain.JournalEntry, error) {
	service.entryRevise = command
	return service.entry(command.EntryID, financeLedgerID, command.ExpectedVersion+1, financedomain.EntryStateDraft), nil
}

func (service *financeCommandTransportService) PostEntry(_ context.Context, command financeapp.EntryTransitionCommand) (financedomain.JournalEntry, error) {
	service.entryTransition = command
	return service.entry(command.EntryID, financeLedgerID, command.ExpectedVersion+1, financedomain.EntryStatePosted), nil
}

func (service *financeCommandTransportService) ReverseEntry(_ context.Context, command financeapp.ReverseEntryCommand) (financedomain.JournalEntry, financedomain.JournalEntry, error) {
	service.entryReverse = command
	original := service.entry(command.EntryID, financeLedgerID, command.ExpectedVersion+1, financedomain.EntryStateReversed)
	reversal := service.entry(ids.FinanceEntryID(financeOperationID), financeLedgerID, 2, financedomain.EntryStatePosted)
	return original, reversal, nil
}

func (service *financeCommandTransportService) Reconcile(_ context.Context, command financeapp.ReconcileCommand) (financedomain.Reconciliation, bool, error) {
	service.reconciliationCreate = command
	return service.reconciliation(ids.FinanceReconciliationID(command.RequestID), 1, financedomain.ReconciliationProposed), true, nil
}

func (service *financeCommandTransportService) ConfirmReconciliation(_ context.Context, command financeapp.ConfirmReconciliationCommand) (financedomain.Reconciliation, error) {
	service.reconciliationConfirm = command
	return service.reconciliation(command.ReconciliationID, command.ExpectedVersion+1, financedomain.ReconciliationConfirmed), nil
}

func (service *financeCommandTransportService) ledger(id ids.FinanceLedgerID, version uint64) financedomain.Ledger {
	return financedomain.Ledger{ID: id, AccountID: financeAccountID, Name: "Operations", Code: "OPS", Description: "Primary operations", Currency: "USD",
		State: financedomain.LedgerActive, Version: version, CreatedBy: financedomain.Actor{Kind: financedomain.ActorUser, ID: financeUserID},
		CreatedAt: service.now, UpdatedAt: service.now}
}

func (service *financeCommandTransportService) postingAccount(id ids.FinanceAccountID, ledgerID ids.FinanceLedgerID, version uint64) financedomain.PostingAccount {
	return financedomain.PostingAccount{ID: id, AccountID: financeAccountID, LedgerID: ledgerID, Code: "1000", Name: "Cash", Description: "Operating cash",
		Type: financedomain.AccountAsset, NormalBalance: financedomain.NormalDebit, AllowPosting: true, State: financedomain.LedgerActive, Version: version,
		CreatedBy: financedomain.Actor{Kind: financedomain.ActorUser, ID: financeUserID}, CreatedAt: service.now, UpdatedAt: service.now}
}

func (service *financeCommandTransportService) entry(id ids.FinanceEntryID, ledgerID ids.FinanceLedgerID, version uint64, state financedomain.EntryState) financedomain.JournalEntry {
	actor := financedomain.Actor{Kind: financedomain.ActorUser, ID: financeUserID}
	value := financedomain.JournalEntry{ID: id, AccountID: financeAccountID, LedgerID: ledgerID, Number: 12, EntryDate: service.now,
		Description: "Receipt", Reference: "R-12", Currency: "USD", Lines: []financedomain.JournalLine{
			{AccountID: financePostingAccountID, DebitMinor: 100}, {AccountID: "e4100000-0000-4000-8000-000000000014", CreditMinor: 100}},
		TotalMinor: 100, Evidence: []ids.KnowledgeEvidenceID{"e4200000-0000-4000-8000-000000000024"},
		Provenance: financedomain.Provenance{Source: financedomain.SourceManual}, State: state, Version: version, CreatedBy: actor,
		CreatedAt: service.now, UpdatedAt: service.now}
	if state == financedomain.EntryStatePosted || state == financedomain.EntryStateReversed {
		value.PostedBy, value.PostedAt = &actor, &service.now
	}
	if state == financedomain.EntryStateReversed {
		value.ReversedByID = financeOperationID
		value.ReversedBy, value.ReversedAt = &actor, &service.now
	}
	return value
}

func (service *financeCommandTransportService) reconciliation(id ids.FinanceReconciliationID, version uint64, state financedomain.ReconciliationState) financedomain.Reconciliation {
	money, _ := financedomain.NewMoney("USD", 100)
	actor := financedomain.Actor{Kind: financedomain.ActorUser, ID: financeUserID}
	value := financedomain.Reconciliation{ID: id, AccountID: financeAccountID, LedgerID: financeLedgerID, PostingAccountID: financePostingAccountID,
		AsOf: service.now, StatementBalance: money, LedgerBalance: money, Evidence: []ids.KnowledgeEvidenceID{"e4200000-0000-4000-8000-000000000024"},
		State: state, Version: version, CreatedBy: actor, CreatedAt: service.now, UpdatedAt: service.now}
	if state == financedomain.ReconciliationConfirmed {
		value.ConfirmedBy, value.ConfirmedAt = &actor, &service.now
	}
	return value
}

func TestFinanceCommandRoutesBindAuthorityVersionAndLifecycle(t *testing.T) {
	service := &financeCommandTransportService{now: time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)}
	server, err := New(claimAcceptor{claims: financeCommandClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithFinanceCommands(service))
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/accounts/" + financeAccountID + "/finance"
	evidence := `"e4200000-0000-4000-8000-000000000024"`
	lines := `[{"account_id":"` + financePostingAccountID + `","memo":"Deposit","debit_minor":100,"credit_minor":0},{"account_id":"e4100000-0000-4000-8000-000000000014","memo":"Revenue","debit_minor":0,"credit_minor":100}]`

	tests := []struct {
		name, method, target, body, version string
		status                              int
		check                               func() bool
	}{
		{"create ledger", http.MethodPost, base + "/ledgers", `{"name":"Operations","code":"OPS","description":"Primary operations","currency":"USD"}`, "", http.StatusCreated, func() bool {
			return service.ledgerCreate.RequestID == financeOperationID && service.ledgerCreate.Currency == "USD"
		}},
		{"revise ledger", http.MethodPut, base + "/ledgers/" + financeLedgerID, `{"name":"Operations","code":"OPS","description":"Revised"}`, `W/"2"`, http.StatusOK, func() bool {
			return service.ledgerRevise.ExpectedVersion == 2 && service.ledgerRevise.LedgerID == financeLedgerID
		}},
		{"close period", http.MethodPost, base + "/ledgers/" + financeLedgerID + "/period-closes", `{"through":"2026-07-31T00:00:00Z","evidence":[` + evidence + `]}`, `W/"3"`, http.StatusOK, func() bool { return service.ledgerClose.ExpectedVersion == 3 && len(service.ledgerClose.Evidence) == 1 }},
		{"archive ledger", http.MethodDelete, base + "/ledgers/" + financeLedgerID, "", `W/"4"`, http.StatusOK, func() bool { return service.ledgerArchive.ExpectedVersion == 4 }},
		{"create account", http.MethodPost, base + "/ledgers/" + financeLedgerID + "/accounts", `{"code":"1000","name":"Cash","description":"Operating cash","type":"asset","allow_posting":true}`, "", http.StatusCreated, func() bool {
			return service.accountCreate.LedgerID == financeLedgerID && service.accountCreate.Type == financedomain.AccountAsset
		}},
		{"revise account", http.MethodPut, base + "/accounts/" + financePostingAccountID, `{"code":"1000","name":"Cash","description":"Revised","allow_posting":true}`, `W/"5"`, http.StatusOK, func() bool { return service.accountRevise.ExpectedVersion == 5 }},
		{"archive account", http.MethodDelete, base + "/accounts/" + financePostingAccountID, "", `W/"6"`, http.StatusOK, func() bool { return service.accountArchive.ExpectedVersion == 6 }},
		{"create entry", http.MethodPost, base + "/ledgers/" + financeLedgerID + "/entries", `{"entry_date":"2026-08-01T00:00:00Z","description":"Receipt","reference":"R-12","currency":"USD","lines":` + lines + `,"evidence":[` + evidence + `]}`, "", http.StatusCreated, func() bool {
			return service.entryCreate.LedgerID == financeLedgerID && len(service.entryCreate.Lines) == 2
		}},
		{"revise entry", http.MethodPut, base + "/entries/" + financeEntryID, `{"entry_date":"2026-08-02T00:00:00Z","description":"Receipt revised","reference":"R-12","lines":` + lines + `,"evidence":[` + evidence + `]}`, `W/"7"`, http.StatusOK, func() bool { return service.entryRevise.ExpectedVersion == 7 }},
		{"post entry", http.MethodPost, base + "/entries/" + financeEntryID + "/postings", "", `W/"8"`, http.StatusOK, func() bool { return service.entryTransition.ExpectedVersion == 8 }},
		{"reverse entry", http.MethodPost, base + "/entries/" + financeEntryID + "/reversals", `{"entry_date":"2026-08-03T00:00:00Z","description":"Reverse receipt","reference":"REV-12","evidence":[` + evidence + `]}`, `W/"9"`, http.StatusCreated, func() bool {
			return service.entryReverse.ExpectedVersion == 9 && len(service.entryReverse.Evidence) == 1
		}},
		{"create reconciliation", http.MethodPost, base + "/ledgers/" + financeLedgerID + "/reconciliations", `{"posting_account_id":"` + financePostingAccountID + `","as_of":"2026-08-03T00:00:00Z","statement_balance":{"currency":"USD","minor":100},"evidence":[` + evidence + `]}`, "", http.StatusCreated, func() bool { return service.reconciliationCreate.PostingAccountID == financePostingAccountID }},
		{"confirm reconciliation", http.MethodPost, base + "/reconciliations/" + financeReconciliationID + "/confirmations", "", `W/"10"`, http.StatusOK, func() bool { return service.reconciliationConfirm.ExpectedVersion == 10 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := financeCommandRequestForTest(handler, test.method, test.target, test.body, test.version, financeOperationID)
			if response.Code != test.status || !test.check() {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if err := contract.ValidateResponse(test.method, test.target, response.Code, response.Header(), response.Body.Bytes()); err != nil {
				t.Fatal(err)
			}
			if test.status == http.StatusOK && test.name != "reverse entry" && response.Header().Get("ETag") == "" {
				t.Fatalf("missing ETag: headers=%v", response.Header())
			}
		})
	}
}

func TestFinanceCommandsRejectMissingRouteAuthorityAndPreconditions(t *testing.T) {
	service := &financeCommandTransportService{now: time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)}
	server, _ := New(claimAcceptor{claims: financeCommandClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithFinanceCommands(service))
	target := "/api/v1/accounts/" + financeAccountID + "/finance/entries/" + financeEntryID + "/postings"

	missingVersion := financeCommandRequestForTest(server.Handler(), http.MethodPost, target, "", "", financeOperationID)
	if missingVersion.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing version=%d body=%s", missingVersion.Code, missingVersion.Body.String())
	}
	wrongOperation := financeCommandRequestForTest(server.Handler(), http.MethodPost, target, "", `W/"1"`, "e8000000-0000-4000-8000-000000000008")
	if wrongOperation.Code != http.StatusBadRequest || !strings.Contains(wrongOperation.Body.String(), "invalid_idempotency_key") {
		t.Fatalf("wrong operation=%d body=%s", wrongOperation.Code, wrongOperation.Body.String())
	}
	crossAccount := financeCommandRequestForTest(server.Handler(), http.MethodPost, strings.Replace(target, financeAccountID, "e9000000-0000-4000-8000-000000000009", 1), "", `W/"1"`, financeOperationID)
	if crossAccount.Code != http.StatusNotFound {
		t.Fatalf("cross Account=%d body=%s", crossAccount.Code, crossAccount.Body.String())
	}
}

func financeCommandRequestForTest(handler http.Handler, method, target, body, version, operationID string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Idempotency-Key", operationID)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if version != "" {
		request.Header.Set("If-Match", version)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func financeCommandClaims() routecontext.Claims {
	value := financeQueryClaims()
	value.Authority.Role = "owner"
	value.Authority.OperationID = financeOperationID
	return value
}

var _ FinanceCommandService = (*financeCommandTransportService)(nil)
