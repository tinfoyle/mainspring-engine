package finance

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLedgerLifecycleIntegration(t *testing.T) {
	databaseURL := os.Getenv("MAINSPRING_FINANCE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MAINSPRING_FINANCE_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pool)
	actor := Actor{Type: "agent", ID: "integration-test-agent"}
	ledger, err := service.CreateLedger(ctx, CreateLedgerInput{Name: "Finance integration test", Code: "TEST-" + time.Now().Format("150405.000000"), Currency: "USD", CreateStandardAccounts: true}, actor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM financial_ledger_events WHERE ledger_id=$1`, ledger.ID)
		_, _ = pool.Exec(ctx, `UPDATE financial_journal_entries SET reversal_of_id=NULL WHERE ledger_id=$1`, ledger.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM financial_journal_entries WHERE ledger_id=$1`, ledger.ID)
		_, _ = pool.Exec(ctx, `UPDATE financial_accounts SET parent_account_id=NULL WHERE ledger_id=$1`, ledger.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM financial_accounts WHERE ledger_id=$1`, ledger.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM financial_ledgers WHERE id=$1`, ledger.ID)
		pool.Close()
	})
	accounts, err := service.Accounts(ctx, ledger.ID)
	if err != nil {
		t.Fatal(err)
	}
	byCode := map[string]Account{}
	for _, a := range accounts {
		byCode[a.Code] = a
	}
	if len(byCode) != 6 {
		t.Fatalf("standard account count=%d; want 6", len(byCode))
	}
	sub, err := service.CreateAccount(ctx, CreateAccountInput{LedgerID: ledger.ID, ParentAccountID: byCode["5000"].ID, Code: "5100", Name: "Software subscriptions", Type: "expense", AllowPosting: true}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if sub.ParentAccountID != byCode["5000"].ID {
		t.Fatal("sub-account parent was not retained")
	}
	entry, err := service.CreateEntry(ctx, CreateEntryInput{LedgerID: ledger.ID, Description: "Pay software subscription", EntryDate: time.Now(), Source: "agent", Lines: []EntryLineInput{{AccountID: sub.ID, DebitMinor: 2500}, {AccountID: byCode["1000"].ID, CreditMinor: 2500}}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	posted, err := service.PostEntry(ctx, entry.ID, actor)
	if err != nil {
		t.Fatal(err)
	}
	if posted.Status != "posted" {
		t.Fatalf("status=%q", posted.Status)
	}
	accounts, err = service.Accounts(ctx, ledger.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		if a.ID == sub.ID && a.BalanceMinor != 2500 {
			t.Fatalf("posted expense balance=%d", a.BalanceMinor)
		}
	}
	reversal, err := service.VoidEntry(ctx, entry.ID, actor)
	if err != nil {
		t.Fatal(err)
	}
	if reversal.ReversalOfID != entry.ID || reversal.Status != "posted" {
		t.Fatalf("unexpected reversal: %#v", reversal)
	}
	accounts, err = service.Accounts(ctx, ledger.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		if (a.ID == sub.ID || a.ID == byCode["1000"].ID) && a.BalanceMinor != 0 {
			t.Fatalf("balance after reversal for %s=%d", a.Code, a.BalanceMinor)
		}
	}
	var events int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM financial_ledger_events WHERE ledger_id=$1`, ledger.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events < 5 {
		t.Fatalf("audit event count=%d; want at least 5", events)
	}
}
