package finance

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const PackageCode = catalog.PackageFinance

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	authorizer Authorizer
	store      Store
	clock      Clock
}

func New(authorizer Authorizer, store Store, clock Clock) (*Service, error) {
	if authorizer == nil || store == nil || clock == nil {
		return nil, errors.New("Finance dependencies are required")
	}
	return &Service{authorizer: authorizer, store: store, clock: clock}, nil
}

type CreateLedgerCommand struct {
	Actor       access.Actor
	AccountID   ids.AccountID
	RequestID   string
	Name        string
	Code        string
	Description string
	Currency    string
}

func (service *Service) CreateLedger(ctx context.Context, command CreateLedgerCommand) (domain.Ledger, bool, error) {
	authorized, err := service.authorize(ctx, command.Actor, command.AccountID, true, true)
	if err != nil || ids.Validate(command.RequestID) != nil {
		if err != nil {
			return domain.Ledger{}, false, err
		}
		return domain.Ledger{}, false, ErrInvalid
	}
	now := service.clock.Now().UTC()
	actor := userActor(command.Actor)
	return service.store.CreateLedger(ctx, domain.LedgerDraft{ID: ids.FinanceLedgerID(command.RequestID), AccountID: command.AccountID,
		Name: command.Name, Code: command.Code, Description: command.Description, Currency: command.Currency, CreatedBy: actor, CreatedAt: now},
		authorized.Role, mutation(command.RequestID, "created", actor, now))
}

func (service *Service) GetLedger(ctx context.Context, actor access.Actor, accountID ids.AccountID, ledgerID ids.FinanceLedgerID) (domain.Ledger, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return domain.Ledger{}, err
	}
	if ids.Validate(string(ledgerID)) != nil {
		return domain.Ledger{}, ErrInvalid
	}
	return service.store.GetLedger(ctx, accountID, ledgerID)
}

type ReviseLedgerCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	LedgerID        ids.FinanceLedgerID
	ExpectedVersion uint64
	Name            string
	Code            string
	Description     string
}

func (service *Service) ReviseLedger(ctx context.Context, command ReviseLedgerCommand) (domain.Ledger, error) {
	authorized, actor, now, err := service.managementCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion)
	if err != nil || ids.Validate(string(command.LedgerID)) != nil {
		if err != nil {
			return domain.Ledger{}, err
		}
		return domain.Ledger{}, ErrInvalid
	}
	revision := domain.LedgerRevision{Name: command.Name, Code: command.Code, Description: command.Description, ExpectedVersion: command.ExpectedVersion,
		Actor: actor, Role: authorized.Role, At: now}
	return service.store.ReviseLedger(ctx, command.AccountID, command.LedgerID, revision, mutation(command.RequestID, "revised", actor, now))
}

type ClosePeriodCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	LedgerID        ids.FinanceLedgerID
	ExpectedVersion uint64
	Through         time.Time
	Evidence        []ids.KnowledgeEvidenceID
}

func (service *Service) ClosePeriod(ctx context.Context, command ClosePeriodCommand) (domain.Ledger, error) {
	authorized, actor, now, err := service.managementCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion)
	if err != nil || ids.Validate(string(command.LedgerID)) != nil {
		if err != nil {
			return domain.Ledger{}, err
		}
		return domain.Ledger{}, ErrInvalid
	}
	closeCommand := domain.ClosePeriodCommand{Through: command.Through, Evidence: command.Evidence, Actor: actor, Role: authorized.Role,
		ExpectedVersion: command.ExpectedVersion, At: now}
	return service.store.CloseLedgerPeriod(ctx, command.AccountID, command.LedgerID, closeCommand, mutation(command.RequestID, "period_closed", actor, now))
}

type LedgerTransitionCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	LedgerID        ids.FinanceLedgerID
	ExpectedVersion uint64
}

func (service *Service) ArchiveLedger(ctx context.Context, command LedgerTransitionCommand) (domain.Ledger, error) {
	authorized, actor, now, err := service.managementCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion)
	if err != nil || ids.Validate(string(command.LedgerID)) != nil {
		if err != nil {
			return domain.Ledger{}, err
		}
		return domain.Ledger{}, ErrInvalid
	}
	return service.store.ArchiveLedger(ctx, command.AccountID, command.LedgerID, command.ExpectedVersion, actor, authorized.Role,
		mutation(command.RequestID, "archived", actor, now))
}

type CreateAccountCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	LedgerID        ids.FinanceLedgerID
	ParentAccountID ids.FinanceAccountID
	Code            string
	Name            string
	Description     string
	Type            domain.AccountType
	AllowPosting    bool
}

func (service *Service) CreatePostingAccount(ctx context.Context, command CreateAccountCommand) (domain.PostingAccount, bool, error) {
	authorized, err := service.authorize(ctx, command.Actor, command.AccountID, true, true)
	if err != nil || ids.Validate(command.RequestID) != nil {
		if err != nil {
			return domain.PostingAccount{}, false, err
		}
		return domain.PostingAccount{}, false, ErrInvalid
	}
	now := service.clock.Now().UTC()
	actor := userActor(command.Actor)
	return service.store.CreatePostingAccount(ctx, domain.PostingAccountDraft{ID: ids.FinanceAccountID(command.RequestID), AccountID: command.AccountID,
		LedgerID: command.LedgerID, ParentAccountID: command.ParentAccountID, Code: command.Code, Name: command.Name, Description: command.Description,
		Type: command.Type, AllowPosting: command.AllowPosting, CreatedBy: actor, CreatedAt: now}, authorized.Role,
		mutation(command.RequestID, "created", actor, now))
}

type RevisePostingAccountCommand struct {
	Actor            access.Actor
	AccountID        ids.AccountID
	RequestID        string
	PostingAccountID ids.FinanceAccountID
	ExpectedVersion  uint64
	ParentAccountID  ids.FinanceAccountID
	Code             string
	Name             string
	Description      string
	AllowPosting     bool
}

func (service *Service) RevisePostingAccount(ctx context.Context, command RevisePostingAccountCommand) (domain.PostingAccount, error) {
	authorized, actor, now, err := service.managementCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion)
	if err != nil || ids.Validate(string(command.PostingAccountID)) != nil {
		if err != nil {
			return domain.PostingAccount{}, err
		}
		return domain.PostingAccount{}, ErrInvalid
	}
	revision := domain.PostingAccountRevision{ParentAccountID: command.ParentAccountID, Code: command.Code, Name: command.Name, Description: command.Description,
		AllowPosting: command.AllowPosting, ExpectedVersion: command.ExpectedVersion, Actor: actor, Role: authorized.Role, At: now}
	return service.store.RevisePostingAccount(ctx, command.AccountID, command.PostingAccountID, revision, mutation(command.RequestID, "revised", actor, now))
}

type PostingAccountTransitionCommand struct {
	Actor            access.Actor
	AccountID        ids.AccountID
	RequestID        string
	PostingAccountID ids.FinanceAccountID
	ExpectedVersion  uint64
}

func (service *Service) ArchivePostingAccount(ctx context.Context, command PostingAccountTransitionCommand) (domain.PostingAccount, error) {
	authorized, actor, now, err := service.managementCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion)
	if err != nil || ids.Validate(string(command.PostingAccountID)) != nil {
		if err != nil {
			return domain.PostingAccount{}, err
		}
		return domain.PostingAccount{}, ErrInvalid
	}
	return service.store.ArchivePostingAccount(ctx, command.AccountID, command.PostingAccountID, command.ExpectedVersion, actor, authorized.Role,
		mutation(command.RequestID, "archived", actor, now))
}

type CreateEntryCommand struct {
	Actor       access.Actor
	AccountID   ids.AccountID
	RequestID   string
	LedgerID    ids.FinanceLedgerID
	EntryDate   time.Time
	Description string
	Reference   string
	Currency    string
	Lines       []domain.JournalLine
	Evidence    []ids.KnowledgeEvidenceID
	Provenance  domain.Provenance
}

func (service *Service) CreateEntry(ctx context.Context, command CreateEntryCommand) (domain.JournalEntry, bool, error) {
	authorized, err := service.authorize(ctx, command.Actor, command.AccountID, true, false)
	if err != nil || ids.Validate(command.RequestID) != nil {
		if err != nil {
			return domain.JournalEntry{}, false, err
		}
		return domain.JournalEntry{}, false, ErrInvalid
	}
	now := service.clock.Now().UTC()
	actor := userActor(command.Actor)
	if command.Provenance.Source == "" {
		command.Provenance.Source = domain.SourceManual
	}
	return service.store.CreateEntry(ctx, domain.EntryDraft{ID: ids.FinanceEntryID(command.RequestID), AccountID: command.AccountID,
		LedgerID: command.LedgerID, EntryDate: command.EntryDate, Description: command.Description, Reference: command.Reference,
		Currency: command.Currency, Lines: command.Lines, Evidence: command.Evidence, Provenance: command.Provenance, CreatedBy: actor, CreatedAt: now},
		authorized.Role, mutation(command.RequestID, "created", actor, now))
}

func (service *Service) GetEntry(ctx context.Context, actor access.Actor, accountID ids.AccountID, entryID ids.FinanceEntryID) (domain.JournalEntry, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return domain.JournalEntry{}, err
	}
	if ids.Validate(string(entryID)) != nil {
		return domain.JournalEntry{}, ErrInvalid
	}
	return service.store.GetEntry(ctx, accountID, entryID)
}

type EntryTransitionCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	EntryID         ids.FinanceEntryID
	ExpectedVersion uint64
}

func (service *Service) PostEntry(ctx context.Context, command EntryTransitionCommand) (domain.JournalEntry, error) {
	authorized, err := service.authorize(ctx, command.Actor, command.AccountID, true, true)
	if err != nil || ids.Validate(command.RequestID) != nil || ids.Validate(string(command.EntryID)) != nil || command.ExpectedVersion == 0 {
		if err != nil {
			return domain.JournalEntry{}, err
		}
		return domain.JournalEntry{}, ErrInvalid
	}
	now := service.clock.Now().UTC()
	actor := userActor(command.Actor)
	return service.store.PostEntry(ctx, command.AccountID, command.EntryID, command.ExpectedVersion, actor, authorized.Role,
		mutation(command.RequestID, "posted", actor, now))
}

type ReverseEntryCommand struct {
	EntryTransitionCommand
	EntryDate   time.Time
	Description string
	Reference   string
	Evidence    []ids.KnowledgeEvidenceID
}

func (service *Service) ReverseEntry(ctx context.Context, command ReverseEntryCommand) (domain.JournalEntry, domain.JournalEntry, error) {
	authorized, err := service.authorize(ctx, command.Actor, command.AccountID, true, true)
	if err != nil || ids.Validate(command.RequestID) != nil || ids.Validate(string(command.EntryID)) != nil || command.ExpectedVersion == 0 {
		if err != nil {
			return domain.JournalEntry{}, domain.JournalEntry{}, err
		}
		return domain.JournalEntry{}, domain.JournalEntry{}, ErrInvalid
	}
	now := service.clock.Now().UTC()
	actor := userActor(command.Actor)
	reversalID, err := ids.Derive(command.RequestID, "finance-reversal")
	if err != nil {
		return domain.JournalEntry{}, domain.JournalEntry{}, ErrInvalid
	}
	reverse := domain.ReverseCommand{ReversalID: ids.FinanceEntryID(reversalID), EntryDate: command.EntryDate, Description: command.Description,
		Reference: command.Reference, Evidence: command.Evidence, Actor: actor, Role: authorized.Role, ExpectedVersion: command.ExpectedVersion, At: now}
	return service.store.ReverseEntry(ctx, command.AccountID, command.EntryID, command.ExpectedVersion, reverse,
		mutation(command.RequestID, "reversed", actor, now))
}

type ReconcileCommand struct {
	Actor            access.Actor
	AccountID        ids.AccountID
	RequestID        string
	LedgerID         ids.FinanceLedgerID
	PostingAccountID ids.FinanceAccountID
	AsOf             time.Time
	StatementBalance domain.Money
	Evidence         []ids.KnowledgeEvidenceID
}

func (service *Service) Reconcile(ctx context.Context, command ReconcileCommand) (domain.Reconciliation, bool, error) {
	authorized, err := service.authorize(ctx, command.Actor, command.AccountID, true, false)
	if err != nil || ids.Validate(command.RequestID) != nil {
		if err != nil {
			return domain.Reconciliation{}, false, err
		}
		return domain.Reconciliation{}, false, ErrInvalid
	}
	now := service.clock.Now().UTC()
	actor := userActor(command.Actor)
	return service.store.CreateReconciliation(ctx, domain.ReconciliationDraft{ID: ids.FinanceReconciliationID(command.RequestID), AccountID: command.AccountID,
		LedgerID: command.LedgerID, PostingAccountID: command.PostingAccountID, AsOf: command.AsOf, StatementBalance: command.StatementBalance,
		Evidence: command.Evidence, CreatedBy: actor, CreatedAt: now}, authorized.Role, mutation(command.RequestID, "reconciliation_proposed", actor, now))
}

type ConfirmReconciliationCommand struct {
	Actor            access.Actor
	AccountID        ids.AccountID
	RequestID        string
	ReconciliationID ids.FinanceReconciliationID
	ExpectedVersion  uint64
}

func (service *Service) ConfirmReconciliation(ctx context.Context, command ConfirmReconciliationCommand) (domain.Reconciliation, error) {
	authorized, err := service.authorize(ctx, command.Actor, command.AccountID, true, true)
	if err != nil || ids.Validate(command.RequestID) != nil || ids.Validate(string(command.ReconciliationID)) != nil || command.ExpectedVersion == 0 {
		if err != nil {
			return domain.Reconciliation{}, err
		}
		return domain.Reconciliation{}, ErrInvalid
	}
	now := service.clock.Now().UTC()
	actor := userActor(command.Actor)
	return service.store.ConfirmReconciliation(ctx, command.AccountID, command.ReconciliationID, command.ExpectedVersion, actor, authorized.Role,
		mutation(command.RequestID, "reconciliation_confirmed", actor, now))
}

func (service *Service) authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, mutation bool, manage bool) (access.AccountContext, error) {
	if !actor.Valid() || actor.UserID == "" || ids.Validate(string(accountID)) != nil {
		return access.AccountContext{}, ErrInvalid
	}
	requirement := access.Requirement{Package: PackageCode, Mutation: mutation}
	if mutation {
		requirement.Roles = []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember}
		if manage {
			requirement.Roles = requirement.Roles[:2]
		}
	}
	return service.authorizer.Authorize(ctx, actor, accountID, requirement)
}

func (service *Service) managementCommand(ctx context.Context, actor access.Actor, accountID ids.AccountID, requestID string, expected uint64) (access.AccountContext, domain.Actor, time.Time, error) {
	authorized, err := service.authorize(ctx, actor, accountID, true, true)
	if err != nil {
		return access.AccountContext{}, domain.Actor{}, time.Time{}, err
	}
	if ids.Validate(requestID) != nil || expected == 0 {
		return access.AccountContext{}, domain.Actor{}, time.Time{}, ErrInvalid
	}
	now := service.clock.Now().UTC()
	return authorized, userActor(actor), now, nil
}

func userActor(actor access.Actor) domain.Actor {
	return domain.Actor{Kind: domain.ActorUser, ID: string(actor.UserID)}
}

func mutation(requestID, kind string, actor domain.Actor, at time.Time) Mutation {
	return Mutation{EventID: requestID, Kind: kind, Actor: actor, CorrelationID: requestID, At: at}
}
