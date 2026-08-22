package finance

import (
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type LedgerState string

const (
	LedgerActive   LedgerState = "active"
	LedgerArchived LedgerState = "archived"
)

type Ledger struct {
	ID            ids.FinanceLedgerID       `json:"id"`
	AccountID     ids.AccountID             `json:"account_id"`
	Name          string                    `json:"name"`
	Code          string                    `json:"code"`
	Description   string                    `json:"description"`
	Currency      Currency                  `json:"currency"`
	State         LedgerState               `json:"state"`
	ClosedThrough *time.Time                `json:"closed_through,omitempty"`
	CloseEvidence []ids.KnowledgeEvidenceID `json:"close_evidence,omitempty"`
	Version       uint64                    `json:"version"`
	CreatedBy     Actor                     `json:"created_by"`
	CreatedAt     time.Time                 `json:"created_at"`
	UpdatedAt     time.Time                 `json:"updated_at"`
}

type LedgerDraft struct {
	ID          ids.FinanceLedgerID
	AccountID   ids.AccountID
	Name        string
	Code        string
	Description string
	Currency    string
	CreatedBy   Actor
	CreatedAt   time.Time
}

func NewLedger(draft LedgerDraft, role accounts.MembershipRole) (Ledger, error) {
	if !canManage(role) || draft.CreatedBy.Kind != ActorUser {
		return Ledger{}, ErrRole
	}
	currency, err := NewCurrency(draft.Currency)
	if err != nil {
		return Ledger{}, err
	}
	ledger := Ledger{ID: draft.ID, AccountID: draft.AccountID, Name: strings.TrimSpace(draft.Name), Code: strings.ToUpper(strings.TrimSpace(draft.Code)),
		Description: strings.TrimSpace(draft.Description), Currency: currency, State: LedgerActive, Version: 1, CreatedBy: draft.CreatedBy,
		CreatedAt: draft.CreatedAt.UTC(), UpdatedAt: draft.CreatedAt.UTC()}
	return RestoreLedger(ledger)
}

func RestoreLedger(ledger Ledger) (Ledger, error) {
	ledger.Name = strings.TrimSpace(ledger.Name)
	ledger.Code = strings.ToUpper(strings.TrimSpace(ledger.Code))
	ledger.Description = strings.TrimSpace(ledger.Description)
	ledger.CreatedAt, ledger.UpdatedAt = ledger.CreatedAt.UTC(), ledger.UpdatedAt.UTC()
	currency, currencyErr := NewCurrency(string(ledger.Currency))
	evidence, evidenceErr := normalizeEvidence(ledger.CloseEvidence)
	if ids.Validate(string(ledger.ID)) != nil || ids.Validate(string(ledger.AccountID)) != nil || !validText(ledger.Name, MaximumLedgerName, true) ||
		!validText(ledger.Code, MaximumCode, true) || !validText(ledger.Description, MaximumDescriptionBytes, false) || currencyErr != nil ||
		(ledger.State != LedgerActive && ledger.State != LedgerArchived) || ledger.Version == 0 || !ledger.CreatedBy.valid() || ledger.CreatedAt.IsZero() || ledger.UpdatedAt.Before(ledger.CreatedAt) || evidenceErr != nil {
		return Ledger{}, ErrInvalid
	}
	ledger.Currency, ledger.CloseEvidence = currency, evidence
	if ledger.ClosedThrough == nil {
		if len(evidence) != 0 {
			return Ledger{}, ErrInvalid
		}
	} else {
		closed, err := normalizeDate(*ledger.ClosedThrough)
		if err != nil || len(evidence) == 0 {
			return Ledger{}, ErrInvalid
		}
		ledger.ClosedThrough = &closed
	}
	return ledger, nil
}

type LedgerRevision struct {
	Name            string
	Code            string
	Description     string
	ExpectedVersion uint64
	Actor           Actor
	Role            accounts.MembershipRole
	At              time.Time
}

func (ledger Ledger) Revise(command LedgerRevision) (Ledger, error) {
	if command.ExpectedVersion != ledger.Version {
		return Ledger{}, ErrConflict
	}
	if ledger.State != LedgerActive {
		return Ledger{}, ErrState
	}
	if command.Actor.Kind != ActorUser || !command.Actor.valid() || !canManage(command.Role) {
		return Ledger{}, ErrRole
	}
	if command.At.IsZero() || command.At.UTC().Before(ledger.UpdatedAt) {
		return Ledger{}, ErrInvalid
	}
	ledger.Name, ledger.Code, ledger.Description = strings.TrimSpace(command.Name), strings.ToUpper(strings.TrimSpace(command.Code)), strings.TrimSpace(command.Description)
	ledger.Version, ledger.UpdatedAt = ledger.Version+1, command.At.UTC()
	return RestoreLedger(ledger)
}

func (ledger Ledger) Archive(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (Ledger, error) {
	if expectedVersion != ledger.Version {
		return Ledger{}, ErrConflict
	}
	if actor.Kind != ActorUser || !actor.valid() || !canManage(role) {
		return Ledger{}, ErrRole
	}
	if ledger.State == LedgerArchived {
		return ledger, nil
	}
	if at.IsZero() || at.UTC().Before(ledger.UpdatedAt) {
		return Ledger{}, ErrInvalid
	}
	ledger.State, ledger.Version, ledger.UpdatedAt = LedgerArchived, ledger.Version+1, at.UTC()
	return RestoreLedger(ledger)
}

type ClosePeriodCommand struct {
	Through         time.Time
	Evidence        []ids.KnowledgeEvidenceID
	Actor           Actor
	Role            accounts.MembershipRole
	ExpectedVersion uint64
	At              time.Time
}

func (ledger Ledger) ClosePeriod(command ClosePeriodCommand) (Ledger, error) {
	if command.ExpectedVersion != ledger.Version {
		return Ledger{}, ErrConflict
	}
	if ledger.State != LedgerActive || command.Actor.Kind != ActorUser || !command.Actor.valid() || !canManage(command.Role) {
		return Ledger{}, ErrRole
	}
	through, err := normalizeDate(command.Through)
	if err != nil || command.At.IsZero() || command.At.UTC().Before(ledger.UpdatedAt) {
		return Ledger{}, ErrInvalid
	}
	evidence, err := normalizeEvidence(command.Evidence)
	if err != nil || len(evidence) == 0 {
		return Ledger{}, ErrEvidence
	}
	if ledger.ClosedThrough != nil && !through.After(*ledger.ClosedThrough) {
		if through.Equal(*ledger.ClosedThrough) && slicesEqualEvidence(evidence, ledger.CloseEvidence) {
			return ledger, nil
		}
		return Ledger{}, ErrState
	}
	ledger.ClosedThrough, ledger.CloseEvidence, ledger.Version, ledger.UpdatedAt = &through, evidence, ledger.Version+1, command.At.UTC()
	return RestoreLedger(ledger)
}

func slicesEqualEvidence(left, right []ids.KnowledgeEvidenceID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type AccountType string

const (
	AccountAsset     AccountType = "asset"
	AccountLiability AccountType = "liability"
	AccountEquity    AccountType = "equity"
	AccountIncome    AccountType = "income"
	AccountExpense   AccountType = "expense"
)

type NormalBalance string

const (
	NormalDebit  NormalBalance = "debit"
	NormalCredit NormalBalance = "credit"
)

type PostingAccount struct {
	ID              ids.FinanceAccountID `json:"id"`
	AccountID       ids.AccountID        `json:"account_id"`
	LedgerID        ids.FinanceLedgerID  `json:"ledger_id"`
	ParentAccountID ids.FinanceAccountID `json:"parent_account_id,omitempty"`
	Code            string               `json:"code"`
	Name            string               `json:"name"`
	Description     string               `json:"description"`
	Type            AccountType          `json:"type"`
	NormalBalance   NormalBalance        `json:"normal_balance"`
	AllowPosting    bool                 `json:"allow_posting"`
	State           LedgerState          `json:"state"`
	Version         uint64               `json:"version"`
	CreatedBy       Actor                `json:"created_by"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

type PostingAccountDraft struct {
	ID              ids.FinanceAccountID
	AccountID       ids.AccountID
	LedgerID        ids.FinanceLedgerID
	ParentAccountID ids.FinanceAccountID
	Code            string
	Name            string
	Description     string
	Type            AccountType
	AllowPosting    bool
	CreatedBy       Actor
	CreatedAt       time.Time
}

func NewPostingAccount(draft PostingAccountDraft, role accounts.MembershipRole) (PostingAccount, error) {
	if draft.CreatedBy.Kind != ActorUser || !draft.CreatedBy.valid() || !canManage(role) {
		return PostingAccount{}, ErrRole
	}
	normal, err := normalBalance(draft.Type)
	if err != nil {
		return PostingAccount{}, err
	}
	value := PostingAccount{ID: draft.ID, AccountID: draft.AccountID, LedgerID: draft.LedgerID, ParentAccountID: draft.ParentAccountID,
		Code: strings.ToUpper(strings.TrimSpace(draft.Code)), Name: strings.TrimSpace(draft.Name), Description: strings.TrimSpace(draft.Description),
		Type: draft.Type, NormalBalance: normal, AllowPosting: draft.AllowPosting, State: LedgerActive, Version: 1, CreatedBy: draft.CreatedBy,
		CreatedAt: draft.CreatedAt.UTC(), UpdatedAt: draft.CreatedAt.UTC()}
	return RestorePostingAccount(value)
}

func normalBalance(value AccountType) (NormalBalance, error) {
	switch value {
	case AccountAsset, AccountExpense:
		return NormalDebit, nil
	case AccountLiability, AccountEquity, AccountIncome:
		return NormalCredit, nil
	default:
		return "", ErrInvalid
	}
}

func RestorePostingAccount(account PostingAccount) (PostingAccount, error) {
	account.Code, account.Name, account.Description = strings.ToUpper(strings.TrimSpace(account.Code)), strings.TrimSpace(account.Name), strings.TrimSpace(account.Description)
	account.CreatedAt, account.UpdatedAt = account.CreatedAt.UTC(), account.UpdatedAt.UTC()
	normal, err := normalBalance(account.Type)
	if err != nil || normal != account.NormalBalance || ids.Validate(string(account.ID)) != nil || ids.Validate(string(account.AccountID)) != nil || ids.Validate(string(account.LedgerID)) != nil ||
		(account.ParentAccountID != "" && (ids.Validate(string(account.ParentAccountID)) != nil || account.ParentAccountID == account.ID)) ||
		!validText(account.Code, MaximumCode, true) || !validText(account.Name, MaximumLedgerName, true) || !validText(account.Description, MaximumDescriptionBytes, false) ||
		(account.State != LedgerActive && account.State != LedgerArchived) || account.Version == 0 || !account.CreatedBy.valid() || account.CreatedAt.IsZero() || account.UpdatedAt.Before(account.CreatedAt) {
		return PostingAccount{}, ErrInvalid
	}
	return account, nil
}

type PostingAccountRevision struct {
	ParentAccountID ids.FinanceAccountID
	Code            string
	Name            string
	Description     string
	AllowPosting    bool
	ExpectedVersion uint64
	Actor           Actor
	Role            accounts.MembershipRole
	At              time.Time
}

func (account PostingAccount) Revise(command PostingAccountRevision) (PostingAccount, error) {
	if command.ExpectedVersion != account.Version {
		return PostingAccount{}, ErrConflict
	}
	if account.State != LedgerActive {
		return PostingAccount{}, ErrState
	}
	if command.Actor.Kind != ActorUser || !command.Actor.valid() || !canManage(command.Role) {
		return PostingAccount{}, ErrRole
	}
	if command.At.IsZero() || command.At.UTC().Before(account.UpdatedAt) {
		return PostingAccount{}, ErrInvalid
	}
	account.ParentAccountID, account.Code, account.Name, account.Description, account.AllowPosting = command.ParentAccountID, strings.ToUpper(strings.TrimSpace(command.Code)), strings.TrimSpace(command.Name), strings.TrimSpace(command.Description), command.AllowPosting
	account.Version, account.UpdatedAt = account.Version+1, command.At.UTC()
	return RestorePostingAccount(account)
}

func (account PostingAccount) Archive(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (PostingAccount, error) {
	if expectedVersion != account.Version {
		return PostingAccount{}, ErrConflict
	}
	if actor.Kind != ActorUser || !actor.valid() || !canManage(role) {
		return PostingAccount{}, ErrRole
	}
	if account.State == LedgerArchived {
		return account, nil
	}
	if at.IsZero() || at.UTC().Before(account.UpdatedAt) {
		return PostingAccount{}, ErrInvalid
	}
	account.State, account.AllowPosting, account.Version, account.UpdatedAt = LedgerArchived, false, account.Version+1, at.UTC()
	return RestorePostingAccount(account)
}
