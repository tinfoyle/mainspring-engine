package cellapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	financedomain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type financeCursorEnvelope struct {
	Version int       `json:"v"`
	Kind    string    `json:"kind"`
	Code    string    `json:"code,omitempty"`
	ID      string    `json:"id,omitempty"`
	Date    time.Time `json:"date,omitempty"`
	Number  uint64    `json:"number,omitempty"`
}

func (s *Server) financeLedgerList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.financeReadRequest(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "state", "cursor", "limit") {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance query is invalid")
		return
	}
	limit, err := parseLimit(r, financeapp.DefaultPageSize)
	if err != nil {
		s.writeFinanceError(w, "list Ledgers", financeapp.ErrInvalid)
		return
	}
	query := financeapp.LedgerListQuery{State: financedomain.LedgerState(r.URL.Query().Get("state")), Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeFinanceCursor(raw, "ledger")
		if err != nil {
			s.writeFinanceError(w, "decode Ledger cursor", err)
			return
		}
		query.After = &financeapp.LedgerCursor{Code: cursor.Code, ID: ids.FinanceLedgerID(cursor.ID)}
	}
	page, err := s.finance.ListLedgers(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeFinanceError(w, "list Ledgers", err)
		return
	}
	response := map[string]any{"items": page.Items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeFinanceCursor(financeCursorEnvelope{Version: 1, Kind: "ledger", Code: page.NextCursor.Code, ID: string(page.NextCursor.ID)})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) financeLedgerGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.financeReadRequest(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance target is invalid")
		return
	}
	ledgerID, ok := financeLedgerTarget(w, r)
	if !ok {
		return
	}
	value, err := s.finance.GetLedger(routecontext.WithClaims(r.Context(), claims), actor, accountID, ledgerID)
	if err != nil {
		s.writeFinanceError(w, "get Ledger", err)
		return
	}
	writeFinanceVersion(w, value.Version)
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) financeAccountList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.financeReadRequest(w, r)
	if !ok {
		return
	}
	ledgerID, ok := financeLedgerTarget(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "state", "cursor", "limit") {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance query is invalid")
		return
	}
	limit, err := parseLimit(r, financeapp.DefaultPageSize)
	if err != nil {
		s.writeFinanceError(w, "list posting accounts", financeapp.ErrInvalid)
		return
	}
	query := financeapp.PostingAccountListQuery{LedgerID: ledgerID, State: financedomain.LedgerState(r.URL.Query().Get("state")), Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeFinanceCursor(raw, "account")
		if err != nil {
			s.writeFinanceError(w, "decode posting-account cursor", err)
			return
		}
		query.After = &financeapp.PostingAccountCursor{Code: cursor.Code, ID: ids.FinanceAccountID(cursor.ID)}
	}
	page, err := s.finance.ListPostingAccounts(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeFinanceError(w, "list posting accounts", err)
		return
	}
	response := map[string]any{"items": page.Items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeFinanceCursor(financeCursorEnvelope{Version: 1, Kind: "account", Code: page.NextCursor.Code, ID: string(page.NextCursor.ID)})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) financeAccountGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.financeReadRequest(w, r)
	if !ok {
		return
	}
	postingAccountID := ids.FinanceAccountID(r.PathValue("postingAccountID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(postingAccountID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance target is invalid")
		return
	}
	value, err := s.finance.GetPostingAccount(routecontext.WithClaims(r.Context(), claims), actor, accountID, postingAccountID)
	if err != nil {
		s.writeFinanceError(w, "get posting account", err)
		return
	}
	writeFinanceVersion(w, value.Version)
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) financeEntryList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.financeReadRequest(w, r)
	if !ok {
		return
	}
	ledgerID, ok := financeLedgerTarget(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "state", "cursor", "limit") {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance query is invalid")
		return
	}
	limit, err := parseLimit(r, financeapp.DefaultPageSize)
	if err != nil {
		s.writeFinanceError(w, "list journal entries", financeapp.ErrInvalid)
		return
	}
	query := financeapp.EntryListQuery{LedgerID: ledgerID, State: financedomain.EntryState(r.URL.Query().Get("state")), Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeFinanceCursor(raw, "entry")
		if err != nil {
			s.writeFinanceError(w, "decode journal cursor", err)
			return
		}
		query.After = &financeapp.EntryCursor{EntryDate: cursor.Date, Number: cursor.Number}
	}
	page, err := s.finance.ListEntries(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeFinanceError(w, "list journal entries", err)
		return
	}
	response := map[string]any{"items": page.Items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeFinanceCursor(financeCursorEnvelope{Version: 1, Kind: "entry", Date: page.NextCursor.EntryDate, Number: page.NextCursor.Number})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) financeEntryGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.financeReadRequest(w, r)
	if !ok {
		return
	}
	entryID := ids.FinanceEntryID(r.PathValue("entryID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(entryID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance target is invalid")
		return
	}
	value, err := s.finance.GetEntry(routecontext.WithClaims(r.Context(), claims), actor, accountID, entryID)
	if err != nil {
		s.writeFinanceError(w, "get journal entry", err)
		return
	}
	writeFinanceVersion(w, value.Version)
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) financeReconciliationList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.financeReadRequest(w, r)
	if !ok {
		return
	}
	ledgerID, ok := financeLedgerTarget(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "state", "cursor", "limit") {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance query is invalid")
		return
	}
	limit, err := parseLimit(r, financeapp.DefaultPageSize)
	if err != nil {
		s.writeFinanceError(w, "list reconciliations", financeapp.ErrInvalid)
		return
	}
	query := financeapp.ReconciliationListQuery{LedgerID: ledgerID, State: financedomain.ReconciliationState(r.URL.Query().Get("state")), Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeFinanceCursor(raw, "reconciliation")
		if err != nil {
			s.writeFinanceError(w, "decode reconciliation cursor", err)
			return
		}
		query.After = &financeapp.ReconciliationCursor{AsOf: cursor.Date, ID: ids.FinanceReconciliationID(cursor.ID)}
	}
	page, err := s.finance.ListReconciliations(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeFinanceError(w, "list reconciliations", err)
		return
	}
	response := map[string]any{"items": page.Items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeFinanceCursor(financeCursorEnvelope{Version: 1, Kind: "reconciliation", Date: page.NextCursor.AsOf, ID: string(page.NextCursor.ID)})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) financeReconciliationGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.financeReadRequest(w, r)
	if !ok {
		return
	}
	reconciliationID := ids.FinanceReconciliationID(r.PathValue("reconciliationID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(reconciliationID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance target is invalid")
		return
	}
	value, err := s.finance.GetReconciliation(routecontext.WithClaims(r.Context(), claims), actor, accountID, reconciliationID)
	if err != nil {
		s.writeFinanceError(w, "get reconciliation", err)
		return
	}
	writeFinanceVersion(w, value.Version)
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) financeReadRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if s.finance == nil {
		writeProblem(w, http.StatusServiceUnavailable, "finance_unavailable", "Finance is not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	actor := access.Actor{}
	if claims.Authority.ActorKind == "user" {
		actor.UserID = ids.UserID(claims.Authority.ActorID)
	} else {
		actor.WorkloadID = claims.Authority.ActorID
	}
	return claims, actor, accountID, true
}

func financeLedgerTarget(w http.ResponseWriter, r *http.Request) (ids.FinanceLedgerID, bool) {
	ledgerID := ids.FinanceLedgerID(r.PathValue("ledgerID"))
	if ids.Validate(string(ledgerID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance target is invalid")
		return "", false
	}
	return ledgerID, true
}

func encodeFinanceCursor(value financeCursorEnvelope) string {
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeFinanceCursor(raw, kind string) (financeCursorEnvelope, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 1024 {
		return financeCursorEnvelope{}, financeapp.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var value financeCursorEnvelope
	if decoder.Decode(&value) != nil || value.Version != 1 || value.Kind != kind {
		return financeCursorEnvelope{}, financeapp.ErrInvalid
	}
	switch kind {
	case "ledger", "account":
		if value.Code == "" || ids.Validate(value.ID) != nil || !value.Date.IsZero() || value.Number != 0 {
			return financeCursorEnvelope{}, financeapp.ErrInvalid
		}
	case "entry":
		if value.Date.IsZero() || value.Number == 0 || value.Code != "" || value.ID != "" {
			return financeCursorEnvelope{}, financeapp.ErrInvalid
		}
	case "reconciliation":
		if value.Date.IsZero() || ids.Validate(value.ID) != nil || value.Code != "" || value.Number != 0 {
			return financeCursorEnvelope{}, financeapp.ErrInvalid
		}
	default:
		return financeCursorEnvelope{}, financeapp.ErrInvalid
	}
	return value, nil
}

func writeFinanceVersion(w http.ResponseWriter, version uint64) {
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, version))
}

func (s *Server) writeFinanceError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, financeapp.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_finance_query", "the Finance query is invalid")
	case errors.Is(err, financeapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "finance_record_not_found", "the Finance record was not found")
	case errors.Is(err, financeapp.ErrConflict):
		writeProblem(w, http.StatusConflict, "finance_operation_conflict", "the operation conflicts with durable Finance state")
	case errors.Is(err, financeapp.ErrAggregateOverflow):
		writeProblem(w, http.StatusConflict, "finance_aggregate_overflow", "the Finance aggregate exceeds the supported minor-unit range")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this Finance operation")
	default:
		s.logger.Error("Finance operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "finance_unavailable", "the Finance operation could not be completed")
	}
}

var _ FinanceService = (*financeapp.Service)(nil)
