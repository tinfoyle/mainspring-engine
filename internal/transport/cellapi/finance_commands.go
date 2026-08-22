package cellapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	financedomain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type financeLedgerDefinitionRequest struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Description string `json:"description"`
	Currency    string `json:"currency"`
}

type financeLedgerRevisionRequest struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Description string `json:"description"`
}

type financeClosePeriodRequest struct {
	Through  time.Time                 `json:"through"`
	Evidence []ids.KnowledgeEvidenceID `json:"evidence"`
}

type financePostingAccountDefinitionRequest struct {
	ParentAccountID ids.FinanceAccountID      `json:"parent_account_id,omitempty"`
	Code            string                    `json:"code"`
	Name            string                    `json:"name"`
	Description     string                    `json:"description"`
	Type            financedomain.AccountType `json:"type"`
	AllowPosting    bool                      `json:"allow_posting"`
}

type financePostingAccountRevisionRequest struct {
	ParentAccountID ids.FinanceAccountID `json:"parent_account_id,omitempty"`
	Code            string               `json:"code"`
	Name            string               `json:"name"`
	Description     string               `json:"description"`
	AllowPosting    bool                 `json:"allow_posting"`
}

type financeEntryDefinitionRequest struct {
	EntryDate   time.Time                   `json:"entry_date"`
	Description string                      `json:"description"`
	Reference   string                      `json:"reference"`
	Currency    string                      `json:"currency"`
	Lines       []financedomain.JournalLine `json:"lines"`
	Evidence    []ids.KnowledgeEvidenceID   `json:"evidence"`
}

type financeAgentEntryDefinitionRequest struct {
	RunID       ids.RunID                   `json:"run_id"`
	EntryDate   time.Time                   `json:"entry_date"`
	Description string                      `json:"description"`
	Reference   string                      `json:"reference"`
	Currency    string                      `json:"currency"`
	Lines       []financedomain.JournalLine `json:"lines"`
	Evidence    []ids.KnowledgeEvidenceID   `json:"evidence"`
}

type financeEntryRevisionRequest struct {
	EntryDate   time.Time                   `json:"entry_date"`
	Description string                      `json:"description"`
	Reference   string                      `json:"reference"`
	Lines       []financedomain.JournalLine `json:"lines"`
	Evidence    []ids.KnowledgeEvidenceID   `json:"evidence"`
}

type financeEntryReversalRequest struct {
	EntryDate   time.Time                 `json:"entry_date"`
	Description string                    `json:"description"`
	Reference   string                    `json:"reference"`
	Evidence    []ids.KnowledgeEvidenceID `json:"evidence"`
}

type financeReconciliationRequest struct {
	PostingAccountID ids.FinanceAccountID      `json:"posting_account_id"`
	AsOf             time.Time                 `json:"as_of"`
	StatementBalance financedomain.Money       `json:"statement_balance"`
	Evidence         []ids.KnowledgeEvidenceID `json:"evidence"`
}

type financeReversalResponse struct {
	Original financedomain.JournalEntry `json:"original"`
	Reversal financedomain.JournalEntry `json:"reversal"`
}

func (s *Server) financeLedgerCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	var body financeLedgerDefinitionRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	value, created, err := s.financeCommands.CreateLedger(routecontext.WithClaims(r.Context(), claims), financeapp.CreateLedgerCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, Name: body.Name, Code: body.Code, Description: body.Description, Currency: body.Currency,
	})
	if err != nil {
		s.writeFinanceError(w, "create Ledger", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/finance/ledgers/%s", accountID, value.ID))
	writeFinanceVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) financeLedgerRevise(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	ledgerID, version, ok := financeVersionTarget[ids.FinanceLedgerID](w, r, "ledgerID")
	if !ok {
		return
	}
	var body financeLedgerRevisionRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	value, err := s.financeCommands.ReviseLedger(routecontext.WithClaims(r.Context(), claims), financeapp.ReviseLedgerCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, LedgerID: ledgerID, ExpectedVersion: version,
		Name: body.Name, Code: body.Code, Description: body.Description,
	})
	s.writeFinanceAggregate(w, "revise Ledger", value, err)
}

func (s *Server) financePeriodClose(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	ledgerID, version, ok := financeVersionTarget[ids.FinanceLedgerID](w, r, "ledgerID")
	if !ok {
		return
	}
	var body financeClosePeriodRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	value, err := s.financeCommands.ClosePeriod(routecontext.WithClaims(r.Context(), claims), financeapp.ClosePeriodCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, LedgerID: ledgerID, ExpectedVersion: version,
		Through: body.Through, Evidence: body.Evidence,
	})
	s.writeFinanceAggregate(w, "close Ledger period", value, err)
}

func (s *Server) financeLedgerArchive(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	ledgerID, version, ok := financeVersionTarget[ids.FinanceLedgerID](w, r, "ledgerID")
	if !ok || !financeNoBody(w, r) {
		return
	}
	value, err := s.financeCommands.ArchiveLedger(routecontext.WithClaims(r.Context(), claims), financeapp.LedgerTransitionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, LedgerID: ledgerID, ExpectedVersion: version,
	})
	s.writeFinanceAggregate(w, "archive Ledger", value, err)
}

func (s *Server) financeAccountCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	ledgerID, ok := financeCommandTarget[ids.FinanceLedgerID](w, r, "ledgerID")
	if !ok {
		return
	}
	var body financePostingAccountDefinitionRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	value, created, err := s.financeCommands.CreatePostingAccount(routecontext.WithClaims(r.Context(), claims), financeapp.CreateAccountCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, LedgerID: ledgerID, ParentAccountID: body.ParentAccountID,
		Code: body.Code, Name: body.Name, Description: body.Description, Type: body.Type, AllowPosting: body.AllowPosting,
	})
	if err != nil {
		s.writeFinanceError(w, "create posting account", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/finance/accounts/%s", accountID, value.ID))
	writeFinanceVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) financeAccountRevise(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	postingAccountID, version, ok := financeVersionTarget[ids.FinanceAccountID](w, r, "postingAccountID")
	if !ok {
		return
	}
	var body financePostingAccountRevisionRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	value, err := s.financeCommands.RevisePostingAccount(routecontext.WithClaims(r.Context(), claims), financeapp.RevisePostingAccountCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, PostingAccountID: postingAccountID, ExpectedVersion: version,
		ParentAccountID: body.ParentAccountID, Code: body.Code, Name: body.Name, Description: body.Description, AllowPosting: body.AllowPosting,
	})
	s.writeFinanceAggregate(w, "revise posting account", value, err)
}

func (s *Server) financeAccountArchive(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	postingAccountID, version, ok := financeVersionTarget[ids.FinanceAccountID](w, r, "postingAccountID")
	if !ok || !financeNoBody(w, r) {
		return
	}
	value, err := s.financeCommands.ArchivePostingAccount(routecontext.WithClaims(r.Context(), claims), financeapp.PostingAccountTransitionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, PostingAccountID: postingAccountID, ExpectedVersion: version,
	})
	s.writeFinanceAggregate(w, "archive posting account", value, err)
}

func (s *Server) financeEntryCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	ledgerID, ok := financeCommandTarget[ids.FinanceLedgerID](w, r, "ledgerID")
	if !ok {
		return
	}
	var body financeEntryDefinitionRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	value, created, err := s.financeCommands.CreateEntry(routecontext.WithClaims(r.Context(), claims), financeapp.CreateEntryCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, LedgerID: ledgerID, EntryDate: body.EntryDate,
		Description: body.Description, Reference: body.Reference, Currency: body.Currency, Lines: body.Lines, Evidence: body.Evidence,
	})
	if err != nil {
		s.writeFinanceError(w, "create journal entry", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/finance/entries/%s", accountID, value.ID))
	writeFinanceVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) financeAgentEntryDraft(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	ledgerID, ok := financeCommandTarget[ids.FinanceLedgerID](w, r, "ledgerID")
	if !ok || claims.Authority.ActorKind != "workload" || actor.WorkloadID == "" {
		writeProblem(w, http.StatusForbidden, "finance_agent_draft_denied", "the Finance Agent draft boundary is unavailable")
		return
	}
	invocationRaw := strings.TrimPrefix(actor.WorkloadID, "runner-invocation:")
	if invocationRaw == actor.WorkloadID || ids.Validate(invocationRaw) != nil {
		writeProblem(w, http.StatusForbidden, "finance_agent_draft_denied", "the Finance Agent draft boundary is unavailable")
		return
	}
	var body financeAgentEntryDefinitionRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	value, created, err := s.financeCommands.CreateEntry(routecontext.WithClaims(r.Context(), claims), financeapp.CreateEntryCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, LedgerID: ledgerID, EntryDate: body.EntryDate,
		Description: body.Description, Reference: body.Reference, Currency: body.Currency, Lines: body.Lines, Evidence: body.Evidence,
		Provenance: financedomain.Provenance{Source: financedomain.SourceAgent, RunID: body.RunID, InvocationID: ids.AgentInvocationID(invocationRaw)},
	})
	if err != nil {
		s.writeFinanceError(w, "create Agent journal draft", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/finance/entries/%s", accountID, value.ID))
	writeFinanceVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) financeEntryRevise(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	entryID, version, ok := financeVersionTarget[ids.FinanceEntryID](w, r, "entryID")
	if !ok {
		return
	}
	var body financeEntryRevisionRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	value, err := s.financeCommands.ReviseEntry(routecontext.WithClaims(r.Context(), claims), financeapp.ReviseEntryCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, EntryID: entryID, ExpectedVersion: version,
		EntryDate: body.EntryDate, Description: body.Description, Reference: body.Reference, Lines: body.Lines, Evidence: body.Evidence,
	})
	s.writeFinanceAggregate(w, "revise journal entry", value, err)
}

func (s *Server) financeEntryPost(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	entryID, version, ok := financeVersionTarget[ids.FinanceEntryID](w, r, "entryID")
	if !ok || !financeNoBody(w, r) {
		return
	}
	value, err := s.financeCommands.PostEntry(routecontext.WithClaims(r.Context(), claims), financeapp.EntryTransitionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, EntryID: entryID, ExpectedVersion: version,
	})
	s.writeFinanceAggregate(w, "post journal entry", value, err)
}

func (s *Server) financeEntryReverse(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	entryID, version, ok := financeVersionTarget[ids.FinanceEntryID](w, r, "entryID")
	if !ok {
		return
	}
	var body financeEntryReversalRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	original, reversal, err := s.financeCommands.ReverseEntry(routecontext.WithClaims(r.Context(), claims), financeapp.ReverseEntryCommand{
		EntryTransitionCommand: financeapp.EntryTransitionCommand{Actor: actor, AccountID: accountID, RequestID: requestID, EntryID: entryID, ExpectedVersion: version},
		EntryDate:              body.EntryDate, Description: body.Description, Reference: body.Reference, Evidence: body.Evidence,
	})
	if err != nil {
		s.writeFinanceError(w, "reverse journal entry", err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/finance/entries/%s", accountID, reversal.ID))
	writeJSON(w, http.StatusCreated, financeReversalResponse{Original: original, Reversal: reversal})
}

func (s *Server) financeReconciliationCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	ledgerID, ok := financeCommandTarget[ids.FinanceLedgerID](w, r, "ledgerID")
	if !ok {
		return
	}
	var body financeReconciliationRequest
	if !decodeFinanceJSON(w, r, &body) {
		return
	}
	value, created, err := s.financeCommands.Reconcile(routecontext.WithClaims(r.Context(), claims), financeapp.ReconcileCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, LedgerID: ledgerID, PostingAccountID: body.PostingAccountID,
		AsOf: body.AsOf, StatementBalance: body.StatementBalance, Evidence: body.Evidence,
	})
	if err != nil {
		s.writeFinanceError(w, "create reconciliation", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/finance/reconciliations/%s", accountID, value.ID))
	writeFinanceVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) financeReconciliationConfirm(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.financeCommandRequest(w, r)
	if !ok {
		return
	}
	reconciliationID, version, ok := financeVersionTarget[ids.FinanceReconciliationID](w, r, "reconciliationID")
	if !ok || !financeNoBody(w, r) {
		return
	}
	value, err := s.financeCommands.ConfirmReconciliation(routecontext.WithClaims(r.Context(), claims), financeapp.ConfirmReconciliationCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, ReconciliationID: reconciliationID, ExpectedVersion: version,
	})
	s.writeFinanceAggregate(w, "confirm reconciliation", value, err)
}

func (s *Server) financeCommandRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if s.financeCommands == nil {
		writeProblem(w, http.StatusServiceUnavailable, "finance_unavailable", "Finance commands are not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	actor := access.Actor{}
	if claims.Authority.ActorKind == "user" {
		actor.UserID = ids.UserID(claims.Authority.ActorID)
	} else {
		actor.WorkloadID = claims.Authority.ActorID
	}
	return claims, actor, accountID, claims.Authority.OperationID, true
}

func financeCommandTarget[T ~string](w http.ResponseWriter, r *http.Request, pathName string) (T, bool) {
	var zero T
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_command", "Finance commands do not accept query parameters")
		return zero, false
	}
	value := T(r.PathValue(pathName))
	if ids.Validate(string(value)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_command", "the Finance target is invalid")
		return zero, false
	}
	return value, true
}

func financeVersionTarget[T ~string](w http.ResponseWriter, r *http.Request, pathName string) (T, uint64, bool) {
	value, ok := financeCommandTarget[T](w, r, pathName)
	if !ok {
		return value, 0, false
	}
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		writeProblem(w, http.StatusPreconditionRequired, "finance_version_required", "If-Match with the current Finance version is required")
		return value, 0, false
	}
	version, err := parseAttentionVersion(values)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_version", "If-Match must contain exactly one weak Finance version ETag")
		return value, 0, false
	}
	return value, version, true
}

func decodeFinanceJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_command", "Finance commands do not accept query parameters")
		return false
	}
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Finance commands require application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_command", "the Finance command body is invalid")
		return false
	}
	return true
}

func financeNoBody(w http.ResponseWriter, r *http.Request) bool {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_command", "Finance commands do not accept query parameters")
		return false
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(strings.TrimSpace(string(raw))) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_command", "this Finance command does not accept a body")
		return false
	}
	return true
}

func (s *Server) writeFinanceAggregate(w http.ResponseWriter, operation string, value any, err error) {
	if err != nil {
		s.writeFinanceError(w, operation, err)
		return
	}
	switch aggregate := value.(type) {
	case financedomain.Ledger:
		writeFinanceVersion(w, aggregate.Version)
	case financedomain.PostingAccount:
		writeFinanceVersion(w, aggregate.Version)
	case financedomain.JournalEntry:
		writeFinanceVersion(w, aggregate.Version)
	case financedomain.Reconciliation:
		writeFinanceVersion(w, aggregate.Version)
	}
	writeJSON(w, http.StatusOK, value)
}

var _ FinanceCommandService = (*financeapp.Service)(nil)
