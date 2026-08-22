package finance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type financeTestAuthorizer struct {
	role        accounts.MembershipRole
	requirement access.Requirement
}

func (authorizer *financeTestAuthorizer) Authorize(_ context.Context, _ access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	authorizer.requirement = requirement
	if len(requirement.Roles) > 0 {
		allowed := false
		for _, role := range requirement.Roles {
			allowed = allowed || role == authorizer.role
		}
		if !allowed {
			return access.AccountContext{}, &access.DeniedError{Code: access.DenialRole, Package: requirement.Package}
		}
	}
	return access.AccountContext{AccountID: accountID, Role: authorizer.role}, nil
}

type financeTestClock struct{ now time.Time }

func (clock financeTestClock) Now() time.Time { return clock.now }

type financeTestStore struct {
	ledgerDraft         domain.LedgerDraft
	entryDraft          domain.EntryDraft
	reverseCommand      domain.ReverseCommand
	reconciliationDraft domain.ReconciliationDraft
	mutation            Mutation
}

func (store *financeTestStore) CreateLedger(_ context.Context, draft domain.LedgerDraft, _ accounts.MembershipRole, mutation Mutation) (domain.Ledger, bool, error) {
	store.ledgerDraft, store.mutation = draft, mutation
	return domain.Ledger{ID: draft.ID}, true, nil
}
func (*financeTestStore) GetLedger(context.Context, ids.AccountID, ids.FinanceLedgerID) (domain.Ledger, error) {
	return domain.Ledger{}, nil
}
func (*financeTestStore) CreatePostingAccount(context.Context, domain.PostingAccountDraft, accounts.MembershipRole, Mutation) (domain.PostingAccount, bool, error) {
	return domain.PostingAccount{}, true, nil
}
func (store *financeTestStore) CreateEntry(_ context.Context, draft domain.EntryDraft, _ accounts.MembershipRole, mutation Mutation) (domain.JournalEntry, bool, error) {
	store.entryDraft, store.mutation = draft, mutation
	return domain.JournalEntry{ID: draft.ID}, true, nil
}
func (*financeTestStore) GetEntry(context.Context, ids.AccountID, ids.FinanceEntryID) (domain.JournalEntry, error) {
	return domain.JournalEntry{}, nil
}
func (store *financeTestStore) PostEntry(_ context.Context, _ ids.AccountID, entryID ids.FinanceEntryID, _ uint64, _ domain.Actor, _ accounts.MembershipRole, mutation Mutation) (domain.JournalEntry, error) {
	store.mutation = mutation
	return domain.JournalEntry{ID: entryID}, nil
}
func (store *financeTestStore) ReverseEntry(_ context.Context, _ ids.AccountID, entryID ids.FinanceEntryID, _ uint64, command domain.ReverseCommand, mutation Mutation) (domain.JournalEntry, domain.JournalEntry, error) {
	store.reverseCommand, store.mutation = command, mutation
	return domain.JournalEntry{ID: entryID}, domain.JournalEntry{ID: command.ReversalID}, nil
}
func (store *financeTestStore) CreateReconciliation(_ context.Context, draft domain.ReconciliationDraft, _ accounts.MembershipRole, mutation Mutation) (domain.Reconciliation, bool, error) {
	store.reconciliationDraft, store.mutation = draft, mutation
	return domain.Reconciliation{ID: draft.ID}, true, nil
}
func (store *financeTestStore) ConfirmReconciliation(_ context.Context, _ ids.AccountID, reconciliationID ids.FinanceReconciliationID, _ uint64, _ domain.Actor, _ accounts.MembershipRole, mutation Mutation) (domain.Reconciliation, error) {
	store.mutation = mutation
	return domain.Reconciliation{ID: reconciliationID}, nil
}

func TestFinanceServiceSeparatesDraftAndManagementRoles(t *testing.T) {
	now := time.Date(2026, 8, 22, 21, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	actor := access.Actor{UserID: "20000000-0000-4000-8000-000000000002"}
	authorizer := &financeTestAuthorizer{role: accounts.RoleMember}
	store := &financeTestStore{}
	service, err := New(authorizer, store, financeTestClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	requestID := "30000000-0000-4000-8000-000000000003"
	if _, _, err := service.CreateLedger(context.Background(), CreateLedgerCommand{Actor: actor, AccountID: accountID, RequestID: requestID, Name: "Ledger", Code: "MAIN", Currency: "USD"}); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("member create Ledger error=%v", err)
	}
	entry, _, err := service.CreateEntry(context.Background(), CreateEntryCommand{Actor: actor, AccountID: accountID, RequestID: requestID,
		LedgerID: "40000000-0000-4000-8000-000000000004", EntryDate: now, Description: "Draft", Currency: "USD",
		Lines: []domain.JournalLine{{AccountID: "50000000-0000-4000-8000-000000000005", DebitMinor: 1}, {AccountID: "60000000-0000-4000-8000-000000000006", CreditMinor: 1}}})
	if err != nil || entry.ID != ids.FinanceEntryID(requestID) || store.entryDraft.Provenance.Source != domain.SourceManual {
		t.Fatalf("entry=%+v draft=%+v err=%v", entry, store.entryDraft, err)
	}
	if len(authorizer.requirement.Roles) != 3 || authorizer.requirement.Package != PackageCode || !authorizer.requirement.Mutation {
		t.Fatalf("draft requirement=%+v", authorizer.requirement)
	}
	if _, err := service.PostEntry(context.Background(), EntryTransitionCommand{Actor: actor, AccountID: accountID, RequestID: "70000000-0000-4000-8000-000000000007", EntryID: entry.ID, ExpectedVersion: 1}); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("member post error=%v", err)
	}
}

func TestFinanceServiceDerivesReversalAndDoesNotTrustLedgerBalance(t *testing.T) {
	now := time.Date(2026, 8, 22, 22, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	actor := access.Actor{UserID: "20000000-0000-4000-8000-000000000002"}
	authorizer := &financeTestAuthorizer{role: accounts.RoleOwner}
	store := &financeTestStore{}
	service, _ := New(authorizer, store, financeTestClock{now: now})
	requestID := "80000000-0000-4000-8000-000000000008"
	entryID := ids.FinanceEntryID("90000000-0000-4000-8000-000000000009")
	_, reversal, err := service.ReverseEntry(context.Background(), ReverseEntryCommand{EntryTransitionCommand: EntryTransitionCommand{Actor: actor, AccountID: accountID,
		RequestID: requestID, EntryID: entryID, ExpectedVersion: 2}, EntryDate: now, Description: "Correction", Evidence: []ids.KnowledgeEvidenceID{"a0000000-0000-4000-8000-00000000000a"}})
	wantReversalID, _ := ids.Derive(requestID, "finance-reversal")
	if err != nil || reversal.ID != ids.FinanceEntryID(wantReversalID) || store.reverseCommand.ReversalID != ids.FinanceEntryID(wantReversalID) || store.reverseCommand.Actor.Kind != domain.ActorUser {
		t.Fatalf("reversal=%+v command=%+v err=%v", reversal, store.reverseCommand, err)
	}
	statement, _ := domain.NewMoney("USD", 123)
	_, _, err = service.Reconcile(context.Background(), ReconcileCommand{Actor: actor, AccountID: accountID, RequestID: "b0000000-0000-4000-8000-00000000000b",
		LedgerID: "c0000000-0000-4000-8000-00000000000c", PostingAccountID: "d0000000-0000-4000-8000-00000000000d", AsOf: now,
		StatementBalance: statement, Evidence: []ids.KnowledgeEvidenceID{"a0000000-0000-4000-8000-00000000000a"}})
	if err != nil || store.reconciliationDraft.LedgerBalance != (domain.Money{}) || store.mutation.Kind != "reconciliation_proposed" {
		t.Fatalf("reconciliation draft=%+v mutation=%+v err=%v", store.reconciliationDraft, store.mutation, err)
	}
}

func TestFinanceServiceRejectsMissingDependencies(t *testing.T) {
	if _, err := New(nil, &financeTestStore{}, financeTestClock{}); err == nil {
		t.Fatal("nil authorizer was accepted")
	}
	if !errors.Is(classify(domain.ErrConflict), ErrConflict) {
		t.Fatal("domain conflict was not classified")
	}
}
