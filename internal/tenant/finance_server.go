package tenant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tinfoyle/mainspring-engine/internal/finance"
	"github.com/tinfoyle/mainspring-engine/web/components"
)

func financeUserActor(user User) finance.Actor { return finance.Actor{Type: "user", ID: user.ID} }

func formatMinor(amount int64, currency string) string {
	sign := ""
	if amount < 0 {
		sign = "-"
		amount = -amount
	}
	return fmt.Sprintf("%s%s %d.%02d", sign, currency, amount/100, amount%100)
}

func parseMinor(value string) (int64, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	if value == "" {
		return 0, nil
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return 0, errors.New("amount has too many decimal points")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, errors.New("amount is invalid")
	}
	cents := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) > 2 {
			return 0, errors.New("amount may have at most two decimal places")
		}
		decimal := parts[1] + strings.Repeat("0", 2-len(parts[1]))
		cents, err = strconv.ParseInt(decimal, 10, 64)
		if err != nil {
			return 0, errors.New("amount is invalid")
		}
	}
	result := whole*100 + cents
	if negative {
		result = -result
	}
	return result, nil
}

func financeLedgerView(value finance.Ledger) components.FinanceLedgerView {
	return components.FinanceLedgerView{ID: value.ID, Name: value.Name, Code: value.Code, Description: value.Description, Currency: value.Currency, Status: value.Status, AccountCount: value.AccountCount, DraftCount: value.DraftCount, Income: formatMinor(value.IncomeMinor, value.Currency), Expenses: formatMinor(value.ExpenseMinor, value.Currency), Net: formatMinor(value.IncomeMinor-value.ExpenseMinor, value.Currency)}
}
func financeAccountViews(values []finance.Account, currency string) []components.FinanceAccountView {
	out := make([]components.FinanceAccountView, 0, len(values))
	for _, v := range values {
		out = append(out, components.FinanceAccountView{ID: v.ID, ParentAccountID: v.ParentAccountID, Code: v.Code, Name: v.Name, Description: v.Description, Type: v.Type, Status: v.Status, AllowPosting: v.AllowPosting, Balance: formatMinor(v.BalanceMinor, currency)})
	}
	return out
}
func financeEntryView(value finance.JournalEntry, currency string) components.FinanceEntryView {
	view := components.FinanceEntryView{ID: value.ID, LedgerID: value.LedgerID, Number: fmt.Sprintf("%06d", value.EntryNumber), Date: value.EntryDate.Format("2006-01-02"), Description: value.Description, Reference: value.Reference, Status: value.Status, Source: value.Source, Total: formatMinor(value.TotalMinor, currency), ReversalOfID: value.ReversalOfID}
	for _, l := range value.Lines {
		view.Lines = append(view.Lines, components.FinanceLineView{AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, Memo: l.Memo, Debit: minorInput(l.DebitMinor), Credit: minorInput(l.CreditMinor)})
	}
	return view
}
func minorInput(value int64) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%d.%02d", value/100, value%100)
}

func (s *Server) financePage(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		s.renderV2App(w, r, "Finance")
		return
	}
	s.renderFinancePage(w, r, http.StatusOK, "", financeNotice(r.URL.Query().Get("status")))
}
func financeNotice(status string) string {
	switch status {
	case "ledger-created":
		return "Ledger created with its chart of accounts."
	case "account-created":
		return "Account created."
	case "entry-created":
		return "Balanced draft entry created."
	case "entry-posted":
		return "Entry posted to the ledger."
	case "entry-voided":
		return "Entry reversed and voided without erasing its history."
	case "saved":
		return "Changes saved."
	}
	return ""
}

func (s *Server) financeToday(ctx context.Context) string {
	var timeZone string
	if err := s.store.pool.QueryRow(ctx, `SELECT timezone FROM tenant_settings WHERE tenant_id=$1`, s.config.TenantID.String()).Scan(&timeZone); err == nil {
		if location, loadErr := time.LoadLocation(timeZone); loadErr == nil {
			return time.Now().In(location).Format("2006-01-02")
		}
	}
	return time.Now().UTC().Format("2006-01-02")
}

func (s *Server) renderFinancePage(w http.ResponseWriter, r *http.Request, status int, pageError, notice string) {
	session, _ := sessionFromContext(r.Context())
	ledgers, err := s.finance.ListLedgers(r.Context())
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "Financial ledgers are temporarily unavailable.")
		return
	}
	view := components.FinancePageView{Notice: notice, Error: pageError, Today: s.financeToday(r.Context())}
	for _, l := range ledgers {
		view.Ledgers = append(view.Ledgers, financeLedgerView(l))
	}
	selectedID := r.URL.Query().Get("ledger")
	if selectedID == "" && len(ledgers) > 0 {
		selectedID = ledgers[0].ID
	}
	if selectedID != "" {
		ledger, err := s.finance.GetLedger(r.Context(), selectedID)
		if err == nil {
			selected := financeLedgerView(ledger)
			for _, summary := range ledgers {
				if summary.ID == ledger.ID {
					selected = financeLedgerView(summary)
					break
				}
			}
			view.Selected = &selected
			accounts, accountErr := s.finance.Accounts(r.Context(), ledger.ID)
			entries, entryErr := s.finance.ListEntries(r.Context(), ledger.ID, 50)
			if accountErr != nil || entryErr != nil {
				s.renderError(w, http.StatusServiceUnavailable, "The selected ledger could not be loaded.")
				return
			}
			view.Accounts = financeAccountViews(accounts, ledger.Currency)
			for _, entry := range entries {
				view.Entries = append(view.Entries, financeEntryView(entry, ledger.Currency))
			}
		}
	}
	s.render(w, status, components.FinancePage(s.tenantName(r.Context()), s.userView(session.User), view, s.csrfToken(session)))
}

func (s *Server) createFinanceLedger(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, "The ledger form was invalid.", "")
		return
	}
	ledger, err := s.finance.CreateLedger(r.Context(), finance.CreateLedgerInput{Name: r.FormValue("name"), Code: r.FormValue("code"), Description: r.FormValue("description"), Currency: r.FormValue("currency"), CreateStandardAccounts: r.FormValue("standard_accounts") == "yes"}, financeUserActor(session.User))
	if err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/finance?ledger="+ledger.ID+"&status=ledger-created", http.StatusSeeOther)
}
func (s *Server) updateFinanceLedger(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	id := chi.URLParam(r, "ledgerID")
	if err := r.ParseForm(); err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, "The ledger form was invalid.", "")
		return
	}
	_, err := s.finance.UpdateLedger(r.Context(), id, finance.UpdateLedgerInput{Name: r.FormValue("name"), Code: r.FormValue("code"), Description: r.FormValue("description"), Currency: r.FormValue("currency"), Status: r.FormValue("status")}, financeUserActor(session.User))
	if err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/finance?ledger="+id+"&status=saved", http.StatusSeeOther)
}
func (s *Server) createFinanceAccount(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	ledgerID := chi.URLParam(r, "ledgerID")
	if err := r.ParseForm(); err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, "The account form was invalid.", "")
		return
	}
	_, err := s.finance.CreateAccount(r.Context(), finance.CreateAccountInput{LedgerID: ledgerID, ParentAccountID: r.FormValue("parent_account_id"), Code: r.FormValue("code"), Name: r.FormValue("name"), Description: r.FormValue("description"), Type: r.FormValue("account_type"), AllowPosting: r.FormValue("allow_posting") == "yes"}, financeUserActor(session.User))
	if err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/finance?ledger="+ledgerID+"&status=account-created", http.StatusSeeOther)
}
func (s *Server) updateFinanceAccount(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	id := chi.URLParam(r, "accountID")
	if err := r.ParseForm(); err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, "The account form was invalid.", "")
		return
	}
	ledgerID := r.FormValue("ledger_id")
	_, err := s.finance.UpdateAccount(r.Context(), id, finance.UpdateAccountInput{ParentAccountID: r.FormValue("parent_account_id"), Code: r.FormValue("code"), Name: r.FormValue("name"), Description: r.FormValue("description"), Status: r.FormValue("status"), AllowPosting: r.FormValue("allow_posting") == "yes"}, financeUserActor(session.User))
	if err != nil {
		r.URL.RawQuery = "ledger=" + ledgerID
		s.renderFinancePage(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/finance?ledger="+ledgerID+"&status=saved", http.StatusSeeOther)
}

func parseFinanceLines(r *http.Request) ([]finance.EntryLineInput, error) {
	var lines []finance.EntryLineInput
	for i := 1; i <= 8; i++ {
		account := strings.TrimSpace(r.FormValue(fmt.Sprintf("line_%d_account", i)))
		debit, err := parseMinor(r.FormValue(fmt.Sprintf("line_%d_debit", i)))
		if err != nil {
			return nil, fmt.Errorf("line %d debit: %w", i, err)
		}
		credit, err := parseMinor(r.FormValue(fmt.Sprintf("line_%d_credit", i)))
		if err != nil {
			return nil, fmt.Errorf("line %d credit: %w", i, err)
		}
		if account == "" && debit == 0 && credit == 0 {
			continue
		}
		lines = append(lines, finance.EntryLineInput{AccountID: account, Memo: r.FormValue(fmt.Sprintf("line_%d_memo", i)), DebitMinor: debit, CreditMinor: credit})
	}
	return lines, nil
}
func (s *Server) createFinanceEntry(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	ledgerID := chi.URLParam(r, "ledgerID")
	if err := r.ParseForm(); err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, "The entry form was invalid.", "")
		return
	}
	lines, err := parseFinanceLines(r)
	if err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	date, err := time.Parse("2006-01-02", r.FormValue("entry_date"))
	if err != nil {
		s.renderFinancePage(w, r, http.StatusBadRequest, "Enter a valid entry date.", "")
		return
	}
	entry, err := s.finance.CreateEntry(r.Context(), finance.CreateEntryInput{LedgerID: ledgerID, Description: r.FormValue("description"), Reference: r.FormValue("reference"), Source: "manual", EntryDate: date, WorkItemID: r.FormValue("work_item_id"), Lines: lines}, financeUserActor(session.User))
	if err != nil {
		r.URL.RawQuery = "ledger=" + ledgerID
		s.renderFinancePage(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/finance/entries/"+entry.ID, http.StatusSeeOther)
}

func (s *Server) financeEntryPage(w http.ResponseWriter, r *http.Request) {
	s.renderFinanceEntryPage(w, r, http.StatusOK, "")
}
func (s *Server) renderFinanceEntryPage(w http.ResponseWriter, r *http.Request, status int, pageError string) {
	session, _ := sessionFromContext(r.Context())
	entry, err := s.finance.GetEntry(r.Context(), chi.URLParam(r, "entryID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "The journal entry was not found.")
		return
	}
	ledger, err := s.finance.GetLedger(r.Context(), entry.LedgerID)
	if err != nil {
		s.renderError(w, http.StatusNotFound, "The ledger was not found.")
		return
	}
	accounts, err := s.finance.Accounts(r.Context(), entry.LedgerID)
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "The chart of accounts could not be loaded.")
		return
	}
	s.render(w, status, components.FinanceEntryPage(s.tenantName(r.Context()), s.userView(session.User), financeLedgerView(ledger), financeEntryView(entry, ledger.Currency), financeAccountViews(accounts, ledger.Currency), s.csrfToken(session), pageError))
}
func (s *Server) updateFinanceEntry(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	id := chi.URLParam(r, "entryID")
	if err := r.ParseForm(); err != nil {
		s.renderFinanceEntryPage(w, r, http.StatusBadRequest, "The entry form was invalid.")
		return
	}
	lines, err := parseFinanceLines(r)
	if err != nil {
		s.renderFinanceEntryPage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	date, err := time.Parse("2006-01-02", r.FormValue("entry_date"))
	if err != nil {
		s.renderFinanceEntryPage(w, r, http.StatusBadRequest, "Enter a valid entry date.")
		return
	}
	if _, err = s.finance.UpdateEntry(r.Context(), id, finance.UpdateEntryInput{Description: r.FormValue("description"), Reference: r.FormValue("reference"), EntryDate: date, Lines: lines}, financeUserActor(session.User)); err != nil {
		s.renderFinanceEntryPage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, "/finance/entries/"+id+"?status=saved", http.StatusSeeOther)
}
func (s *Server) postFinanceEntry(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	id := chi.URLParam(r, "entryID")
	entry, err := s.finance.PostEntry(r.Context(), id, financeUserActor(session.User))
	if err != nil {
		s.renderFinanceEntryPage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, "/finance?ledger="+entry.LedgerID+"&status=entry-posted", http.StatusSeeOther)
}
func (s *Server) voidFinanceEntry(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	id := chi.URLParam(r, "entryID")
	entry, err := s.finance.VoidEntry(r.Context(), id, financeUserActor(session.User))
	if err != nil {
		s.renderFinanceEntryPage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, "/finance?ledger="+entry.LedgerID+"&status=entry-voided", http.StatusSeeOther)
}
