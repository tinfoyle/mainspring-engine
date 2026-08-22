package finance

import (
	"context"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultPageSize = 50
	MaximumPageSize = 100
)

type LedgerCursor struct {
	Code string              `json:"code"`
	ID   ids.FinanceLedgerID `json:"id"`
}

type LedgerListQuery struct {
	State domain.LedgerState
	After *LedgerCursor
	Limit int
}

func (query LedgerListQuery) Valid() bool {
	return query.Limit >= 1 && query.Limit <= MaximumPageSize && validLedgerState(query.State) &&
		(query.After == nil || (validCode(query.After.Code) && ids.Validate(string(query.After.ID)) == nil))
}

type LedgerSummary struct {
	ID            ids.FinanceLedgerID `json:"id"`
	AccountID     ids.AccountID       `json:"account_id"`
	Name          string              `json:"name"`
	Code          string              `json:"code"`
	Description   string              `json:"description"`
	Currency      domain.Currency     `json:"currency"`
	State         domain.LedgerState  `json:"state"`
	ClosedThrough *time.Time          `json:"closed_through,omitempty"`
	Version       uint64              `json:"version"`
	AccountCount  int64               `json:"account_count"`
	DraftCount    int64               `json:"draft_count"`
	IncomeMinor   int64               `json:"income_minor"`
	ExpenseMinor  int64               `json:"expense_minor"`
	NetMinor      int64               `json:"net_minor"`
	UpdatedAt     time.Time           `json:"updated_at"`
}

type LedgerPage struct {
	Items      []LedgerSummary `json:"items"`
	NextCursor *LedgerCursor   `json:"next_cursor,omitempty"`
}

type PostingAccountCursor struct {
	Code string               `json:"code"`
	ID   ids.FinanceAccountID `json:"id"`
}

type PostingAccountListQuery struct {
	LedgerID ids.FinanceLedgerID
	State    domain.LedgerState
	After    *PostingAccountCursor
	Limit    int
}

func (query PostingAccountListQuery) Valid() bool {
	return ids.Validate(string(query.LedgerID)) == nil && validLedgerState(query.State) && query.Limit >= 1 && query.Limit <= MaximumPageSize &&
		(query.After == nil || (validCode(query.After.Code) && ids.Validate(string(query.After.ID)) == nil))
}

type PostingAccountSummary struct {
	Account      domain.PostingAccount `json:"account"`
	BalanceMinor int64                 `json:"balance_minor"`
}

type PostingAccountPage struct {
	Items      []PostingAccountSummary `json:"items"`
	NextCursor *PostingAccountCursor   `json:"next_cursor,omitempty"`
}

type EntryCursor struct {
	EntryDate time.Time `json:"entry_date"`
	Number    uint64    `json:"number"`
}

type EntryListQuery struct {
	LedgerID ids.FinanceLedgerID
	State    domain.EntryState
	After    *EntryCursor
	Limit    int
}

func (query EntryListQuery) Valid() bool {
	return ids.Validate(string(query.LedgerID)) == nil && validEntryState(query.State) && query.Limit >= 1 && query.Limit <= MaximumPageSize &&
		(query.After == nil || (validDate(query.After.EntryDate) && query.After.Number > 0))
}

type EntrySummary struct {
	ID           ids.FinanceEntryID  `json:"id"`
	AccountID    ids.AccountID       `json:"account_id"`
	LedgerID     ids.FinanceLedgerID `json:"ledger_id"`
	Number       uint64              `json:"number"`
	EntryDate    time.Time           `json:"entry_date"`
	Description  string              `json:"description"`
	Reference    string              `json:"reference"`
	Currency     domain.Currency     `json:"currency"`
	TotalMinor   int64               `json:"total_minor"`
	Source       domain.EntrySource  `json:"source"`
	State        domain.EntryState   `json:"state"`
	ReversalOfID ids.FinanceEntryID  `json:"reversal_of_id,omitempty"`
	ReversedByID ids.FinanceEntryID  `json:"reversed_by_id,omitempty"`
	Version      uint64              `json:"version"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

type EntryPage struct {
	Items      []EntrySummary `json:"items"`
	NextCursor *EntryCursor   `json:"next_cursor,omitempty"`
}

type ReconciliationCursor struct {
	AsOf time.Time                   `json:"as_of"`
	ID   ids.FinanceReconciliationID `json:"id"`
}

type ReconciliationListQuery struct {
	LedgerID ids.FinanceLedgerID
	State    domain.ReconciliationState
	After    *ReconciliationCursor
	Limit    int
}

func (query ReconciliationListQuery) Valid() bool {
	return ids.Validate(string(query.LedgerID)) == nil && validReconciliationState(query.State) && query.Limit >= 1 && query.Limit <= MaximumPageSize &&
		(query.After == nil || (validDate(query.After.AsOf) && ids.Validate(string(query.After.ID)) == nil))
}

type ReconciliationSummary struct {
	ID               ids.FinanceReconciliationID `json:"id"`
	AccountID        ids.AccountID               `json:"account_id"`
	LedgerID         ids.FinanceLedgerID         `json:"ledger_id"`
	PostingAccountID ids.FinanceAccountID        `json:"posting_account_id"`
	AsOf             time.Time                   `json:"as_of"`
	StatementBalance domain.Money                `json:"statement_balance"`
	LedgerBalance    domain.Money                `json:"ledger_balance"`
	DifferenceMinor  int64                       `json:"difference_minor"`
	State            domain.ReconciliationState  `json:"state"`
	Version          uint64                      `json:"version"`
	UpdatedAt        time.Time                   `json:"updated_at"`
}

type ReconciliationPage struct {
	Items      []ReconciliationSummary `json:"items"`
	NextCursor *ReconciliationCursor   `json:"next_cursor,omitempty"`
}

func (service *Service) ListLedgers(ctx context.Context, actor access.Actor, accountID ids.AccountID, query LedgerListQuery) (LedgerPage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return LedgerPage{}, err
	}
	defaultLimit(&query.Limit)
	if !query.Valid() {
		return LedgerPage{}, ErrInvalid
	}
	return service.store.ListLedgers(ctx, accountID, query)
}

func (service *Service) GetPostingAccount(ctx context.Context, actor access.Actor, accountID ids.AccountID, postingAccountID ids.FinanceAccountID) (domain.PostingAccount, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return domain.PostingAccount{}, err
	}
	if ids.Validate(string(postingAccountID)) != nil {
		return domain.PostingAccount{}, ErrInvalid
	}
	return service.store.GetPostingAccount(ctx, accountID, postingAccountID)
}

func (service *Service) ListPostingAccounts(ctx context.Context, actor access.Actor, accountID ids.AccountID, query PostingAccountListQuery) (PostingAccountPage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return PostingAccountPage{}, err
	}
	defaultLimit(&query.Limit)
	if !query.Valid() {
		return PostingAccountPage{}, ErrInvalid
	}
	return service.store.ListPostingAccounts(ctx, accountID, query)
}

func (service *Service) ListEntries(ctx context.Context, actor access.Actor, accountID ids.AccountID, query EntryListQuery) (EntryPage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return EntryPage{}, err
	}
	defaultLimit(&query.Limit)
	if !query.Valid() {
		return EntryPage{}, ErrInvalid
	}
	return service.store.ListEntries(ctx, accountID, query)
}

func (service *Service) GetReconciliation(ctx context.Context, actor access.Actor, accountID ids.AccountID, reconciliationID ids.FinanceReconciliationID) (domain.Reconciliation, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return domain.Reconciliation{}, err
	}
	if ids.Validate(string(reconciliationID)) != nil {
		return domain.Reconciliation{}, ErrInvalid
	}
	return service.store.GetReconciliation(ctx, accountID, reconciliationID)
}

func (service *Service) ListReconciliations(ctx context.Context, actor access.Actor, accountID ids.AccountID, query ReconciliationListQuery) (ReconciliationPage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return ReconciliationPage{}, err
	}
	defaultLimit(&query.Limit)
	if !query.Valid() {
		return ReconciliationPage{}, ErrInvalid
	}
	return service.store.ListReconciliations(ctx, accountID, query)
}

func defaultLimit(limit *int) {
	if *limit == 0 {
		*limit = DefaultPageSize
	}
}

func validCode(value string) bool {
	return value != "" && len(value) <= domain.MaximumCode && value == strings.ToUpper(strings.TrimSpace(value)) && !strings.ContainsRune(value, '\x00')
}

func validDate(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	utc := value.UTC()
	return utc.Equal(time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC))
}

func validLedgerState(value domain.LedgerState) bool {
	return value == "" || value == domain.LedgerActive || value == domain.LedgerArchived
}

func validEntryState(value domain.EntryState) bool {
	return value == "" || value == domain.EntryStateDraft || value == domain.EntryStatePosted || value == domain.EntryStateReversed
}

func validReconciliationState(value domain.ReconciliationState) bool {
	return value == "" || value == domain.ReconciliationProposed || value == domain.ReconciliationDiscrepancy || value == domain.ReconciliationConfirmed
}
