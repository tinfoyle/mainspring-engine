package finance

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testAccountID        ids.AccountID               = "10000000-0000-4000-8000-000000000001"
	testLedgerID         ids.FinanceLedgerID         = "20000000-0000-4000-8000-000000000002"
	testCashID           ids.FinanceAccountID        = "30000000-0000-4000-8000-000000000003"
	testRevenueID        ids.FinanceAccountID        = "40000000-0000-4000-8000-000000000004"
	testEntryID          ids.FinanceEntryID          = "50000000-0000-4000-8000-000000000005"
	testReversalID       ids.FinanceEntryID          = "60000000-0000-4000-8000-000000000006"
	testEvidenceID       ids.KnowledgeEvidenceID     = "70000000-0000-4000-8000-000000000007"
	testReconciliationID ids.FinanceReconciliationID = "80000000-0000-4000-8000-000000000008"
)

var (
	testNow  = time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	testUser = Actor{Kind: ActorUser, ID: "90000000-0000-4000-8000-000000000009"}
)

func TestMoneyAndLinesRejectInvalidCurrencyUnbalancedAndOverflow(t *testing.T) {
	if _, err := NewMoney("usd", 123); err != nil {
		t.Fatal(err)
	}
	if _, err := NewMoney("US1", 123); !errors.Is(err, ErrInvalid) {
		t.Fatalf("currency error=%v", err)
	}
	if _, _, err := normalizeLines([]JournalLine{{AccountID: testCashID, DebitMinor: 10}, {AccountID: testRevenueID, CreditMinor: 9}}); !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("unbalanced error=%v", err)
	}
	if _, _, err := normalizeLines([]JournalLine{{AccountID: testCashID, DebitMinor: math.MaxInt64}, {AccountID: testCashID, DebitMinor: 1}, {AccountID: testRevenueID, CreditMinor: math.MaxInt64}}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow error=%v", err)
	}
}

func TestLedgerCloseIsEvidenceBoundMonotonicAndOptimistic(t *testing.T) {
	ledger, err := NewLedger(LedgerDraft{ID: testLedgerID, AccountID: testAccountID, Name: "Operating ledger", Code: "main", Currency: "usd", CreatedBy: testUser, CreatedAt: testNow}, accounts.RoleOwner)
	if err != nil || ledger.Currency != "USD" || ledger.Code != "MAIN" {
		t.Fatalf("ledger=%+v err=%v", ledger, err)
	}
	if _, err := ledger.ClosePeriod(ClosePeriodCommand{Through: testNow, Actor: testUser, Role: accounts.RoleOwner, ExpectedVersion: 1, At: testNow.Add(time.Hour)}); !errors.Is(err, ErrEvidence) {
		t.Fatalf("missing evidence error=%v", err)
	}
	closed, err := ledger.ClosePeriod(ClosePeriodCommand{Through: testNow, Evidence: []ids.KnowledgeEvidenceID{testEvidenceID}, Actor: testUser, Role: accounts.RoleAdministrator, ExpectedVersion: 1, At: testNow.Add(time.Hour)})
	if err != nil || closed.Version != 2 || closed.ClosedThrough == nil {
		t.Fatalf("closed=%+v err=%v", closed, err)
	}
	if _, err := closed.ClosePeriod(ClosePeriodCommand{Through: testNow.AddDate(0, 0, -1), Evidence: []ids.KnowledgeEvidenceID{testEvidenceID}, Actor: testUser, Role: accounts.RoleOwner, ExpectedVersion: 2, At: testNow.Add(2 * time.Hour)}); !errors.Is(err, ErrState) {
		t.Fatalf("backward close error=%v", err)
	}
	if _, err := closed.ClosePeriod(ClosePeriodCommand{Through: testNow.AddDate(0, 0, 1), Evidence: []ids.KnowledgeEvidenceID{testEvidenceID}, Actor: testUser, Role: accounts.RoleOwner, ExpectedVersion: 1, At: testNow.Add(2 * time.Hour)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale close error=%v", err)
	}
}

func TestJournalPostingRequiresHumanEvidenceAndOpenPeriod(t *testing.T) {
	entry := validEntry(t, testUser, Provenance{Source: SourceManual}, nil)
	if _, err := entry.Post(PostCommand{Actor: testUser, Role: accounts.RoleOwner, ExpectedVersion: 1, At: testNow.Add(time.Hour)}, nil); !errors.Is(err, ErrEvidence) {
		t.Fatalf("missing evidence error=%v", err)
	}
	entry = validEntry(t, testUser, Provenance{Source: SourceManual}, []ids.KnowledgeEvidenceID{testEvidenceID})
	workload := Actor{Kind: ActorWorkload, ID: "agent-projection-worker"}
	if _, err := entry.Post(PostCommand{Actor: workload, Role: accounts.RoleOwner, ExpectedVersion: 1, At: testNow.Add(time.Hour)}, nil); !errors.Is(err, ErrRole) {
		t.Fatalf("workload post error=%v", err)
	}
	closed := testNow
	if _, err := entry.Post(PostCommand{Actor: testUser, Role: accounts.RoleOwner, ExpectedVersion: 1, At: testNow.Add(time.Hour)}, &closed); !errors.Is(err, ErrPeriodClosed) {
		t.Fatalf("closed period error=%v", err)
	}
	posted, err := entry.Post(PostCommand{Actor: testUser, Role: accounts.RoleOwner, ExpectedVersion: 1, At: testNow.Add(time.Hour)}, nil)
	if err != nil || posted.State != EntryStatePosted || posted.Version != 2 || posted.PostedBy == nil {
		t.Fatalf("posted=%+v err=%v", posted, err)
	}
	if _, err := posted.Revise(EntryRevision{ExpectedVersion: 2, Actor: testUser, Role: accounts.RoleOwner, At: testNow.Add(2 * time.Hour)}, nil); !errors.Is(err, ErrState) {
		t.Fatalf("posted revise error=%v", err)
	}
}

func TestAgentMayDraftButCannotPostAndMustCarryRunProvenance(t *testing.T) {
	workload := Actor{Kind: ActorWorkload, ID: "agent-projection-worker"}
	provenance := Provenance{Source: SourceAgent, RunID: "a0000000-0000-4000-8000-00000000000a", InvocationID: "b0000000-0000-4000-8000-00000000000b"}
	entry := validEntry(t, workload, provenance, []ids.KnowledgeEvidenceID{testEvidenceID})
	if entry.Provenance.Source != SourceAgent {
		t.Fatal("agent provenance was not frozen")
	}
	bad := validEntryDraft(workload, Provenance{Source: SourceAgent}, []ids.KnowledgeEvidenceID{testEvidenceID})
	if _, err := NewJournalEntry(bad, accounts.RoleMember); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing run provenance error=%v", err)
	}
	if _, err := entry.Post(PostCommand{Actor: workload, Role: accounts.RoleOwner, ExpectedVersion: 1, At: testNow.Add(time.Hour)}, nil); !errors.Is(err, ErrRole) {
		t.Fatalf("agent post error=%v", err)
	}
}

func TestReversalCreatesSwappedPostedEntryAndFreezesOriginal(t *testing.T) {
	entry := validEntry(t, testUser, Provenance{Source: SourceManual}, []ids.KnowledgeEvidenceID{testEvidenceID})
	posted, err := entry.Post(PostCommand{Actor: testUser, Role: accounts.RoleOwner, ExpectedVersion: 1, At: testNow.Add(time.Hour)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reversed, reversal, err := posted.Reverse(ReverseCommand{ReversalID: testReversalID, ReversalNumber: 2, EntryDate: testNow.AddDate(0, 0, 1), Description: "Correct revenue classification", Reference: "CORR-1", Evidence: []ids.KnowledgeEvidenceID{testEvidenceID}, Actor: testUser, Role: accounts.RoleOwner, ExpectedVersion: 2, At: testNow.Add(2 * time.Hour)}, nil)
	if err != nil || reversed.State != EntryStateReversed || reversed.ReversedByID != reversal.ID || reversal.State != EntryStatePosted || reversal.ReversalOfID != posted.ID {
		t.Fatalf("original=%+v reversal=%+v err=%v", reversed, reversal, err)
	}
	if reversal.Lines[0].CreditMinor != posted.Lines[0].DebitMinor || reversal.Lines[1].DebitMinor != posted.Lines[1].CreditMinor {
		t.Fatalf("reversal lines=%+v original=%+v", reversal.Lines, posted.Lines)
	}
	if _, _, err := reversed.Reverse(ReverseCommand{ExpectedVersion: reversed.Version}, nil); !errors.Is(err, ErrState) {
		t.Fatalf("second reversal error=%v", err)
	}
}

func TestReconciliationExposesMismatchAndRequiresManagerToConfirm(t *testing.T) {
	statement, _ := NewMoney("USD", 10100)
	ledger, _ := NewMoney("USD", 10000)
	mismatch, err := NewReconciliation(ReconciliationDraft{ID: testReconciliationID, AccountID: testAccountID, LedgerID: testLedgerID, PostingAccountID: testCashID, AsOf: testNow, StatementBalance: statement, LedgerBalance: ledger, Evidence: []ids.KnowledgeEvidenceID{testEvidenceID}, CreatedBy: testUser, CreatedAt: testNow}, accounts.RoleMember)
	if err != nil || mismatch.State != ReconciliationDiscrepancy || mismatch.DifferenceMinor != 100 {
		t.Fatalf("mismatch=%+v err=%v", mismatch, err)
	}
	if _, err := mismatch.Confirm(1, testUser, accounts.RoleOwner, testNow.Add(time.Hour)); !errors.Is(err, ErrMismatch) {
		t.Fatalf("mismatch confirmation error=%v", err)
	}
	ledger = statement
	matched, err := NewReconciliation(ReconciliationDraft{ID: testReconciliationID, AccountID: testAccountID, LedgerID: testLedgerID, PostingAccountID: testCashID, AsOf: testNow, StatementBalance: statement, LedgerBalance: ledger, Evidence: []ids.KnowledgeEvidenceID{testEvidenceID}, CreatedBy: testUser, CreatedAt: testNow}, accounts.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := matched.Confirm(1, testUser, accounts.RoleMember, testNow.Add(time.Hour)); !errors.Is(err, ErrRole) {
		t.Fatalf("member confirmation error=%v", err)
	}
	confirmed, err := matched.Confirm(1, testUser, accounts.RoleAdministrator, testNow.Add(time.Hour))
	if err != nil || confirmed.State != ReconciliationConfirmed || confirmed.Version != 2 {
		t.Fatalf("confirmed=%+v err=%v", confirmed, err)
	}
}

func TestPostingAccountNormalBalanceIsDerived(t *testing.T) {
	account := PostingAccount{ID: testCashID, AccountID: testAccountID, LedgerID: testLedgerID, Code: "1000", Name: "Cash", Type: AccountAsset, NormalBalance: NormalCredit,
		AllowPosting: true, State: LedgerActive, Version: 1, CreatedBy: testUser, CreatedAt: testNow, UpdatedAt: testNow}
	if _, err := RestorePostingAccount(account); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong normal balance error=%v", err)
	}
	account.NormalBalance = NormalDebit
	if _, err := RestorePostingAccount(account); err != nil {
		t.Fatal(err)
	}
}

func validEntry(t *testing.T, actor Actor, provenance Provenance, evidence []ids.KnowledgeEvidenceID) JournalEntry {
	t.Helper()
	entry, err := NewJournalEntry(validEntryDraft(actor, provenance, evidence), accounts.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func validEntryDraft(actor Actor, provenance Provenance, evidence []ids.KnowledgeEvidenceID) EntryDraft {
	return EntryDraft{ID: testEntryID, AccountID: testAccountID, LedgerID: testLedgerID, Number: 1, EntryDate: testNow, Description: "Recognize revenue", Reference: "INV-1", Currency: "USD",
		Lines: []JournalLine{{AccountID: testCashID, Memo: "Receipt", DebitMinor: 10000}, {AccountID: testRevenueID, Memo: "Revenue", CreditMinor: 10000}}, Evidence: evidence,
		Provenance: provenance, CreatedBy: actor, CreatedAt: testNow}
}
