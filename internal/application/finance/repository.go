// Package finance defines the authorized application boundary for customer
// operational ledgers. Provider payment effects remain outside this package.
package finance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalid    = errors.New("finance command is invalid")
	ErrNotFound   = errors.New("finance record was not found")
	ErrConflict   = errors.New("finance command conflicts with durable state")
	ErrRepository = errors.New("finance repository unavailable")
)

type Mutation struct {
	EventID       string
	Kind          string
	Actor         domain.Actor
	CorrelationID string
	At            time.Time
}

func (mutation Mutation) Valid() bool {
	validKind := map[string]bool{"created": true, "revised": true, "archived": true, "period_closed": true, "posted": true,
		"reversed": true, "reconciliation_proposed": true, "reconciliation_discrepancy": true, "reconciliation_confirmed": true}[mutation.Kind]
	return ids.Validate(mutation.EventID) == nil && mutation.Actor.Valid() && ids.Validate(mutation.CorrelationID) == nil && validKind && !mutation.At.IsZero()
}

type Store interface {
	CreateLedger(context.Context, domain.LedgerDraft, accounts.MembershipRole, Mutation) (domain.Ledger, bool, error)
	GetLedger(context.Context, ids.AccountID, ids.FinanceLedgerID) (domain.Ledger, error)
	CreatePostingAccount(context.Context, domain.PostingAccountDraft, accounts.MembershipRole, Mutation) (domain.PostingAccount, bool, error)
	CreateEntry(context.Context, domain.EntryDraft, accounts.MembershipRole, Mutation) (domain.JournalEntry, bool, error)
	GetEntry(context.Context, ids.AccountID, ids.FinanceEntryID) (domain.JournalEntry, error)
	PostEntry(context.Context, ids.AccountID, ids.FinanceEntryID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.JournalEntry, error)
	ReverseEntry(context.Context, ids.AccountID, ids.FinanceEntryID, uint64, domain.ReverseCommand, Mutation) (domain.JournalEntry, domain.JournalEntry, error)
	CreateReconciliation(context.Context, domain.ReconciliationDraft, accounts.MembershipRole, Mutation) (domain.Reconciliation, bool, error)
	ConfirmReconciliation(context.Context, ids.AccountID, ids.FinanceReconciliationID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.Reconciliation, error)
}

func classify(err error) error {
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		return err
	}
	if errors.Is(err, domain.ErrConflict) {
		return ErrConflict
	}
	if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrState) || errors.Is(err, domain.ErrRole) || errors.Is(err, domain.ErrUnbalanced) ||
		errors.Is(err, domain.ErrOverflow) || errors.Is(err, domain.ErrPeriodClosed) || errors.Is(err, domain.ErrEvidence) || errors.Is(err, domain.ErrMismatch) {
		return errors.Join(ErrInvalid, err)
	}
	return errors.Join(ErrRepository, err)
}

// ClassifyForAdapter keeps infrastructure errors behind the application-owned
// vocabulary while allowing the PostgreSQL adapter to preserve domain causes.
func ClassifyForAdapter(err error) error { return classify(err) }

func normalizedDescription(value string) string { return strings.TrimSpace(value) }
