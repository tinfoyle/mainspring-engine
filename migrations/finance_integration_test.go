package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestFinanceSchemaEnforcesIsolationPostingAndImmutableReversal(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)
	accountA, accountB := "fa000000-0000-4000-8000-000000000001", "fb000000-0000-4000-8000-000000000001"
	userID := "fa100000-0000-4000-8000-000000000001"
	ledgerID, cashID, revenueID := "fa200000-0000-4000-8000-000000000001", "fa300000-0000-4000-8000-000000000001", "fa300000-0000-4000-8000-000000000002"
	evidenceID, entryID, reversalID := "fa400000-0000-4000-8000-000000000001", "fa500000-0000-4000-8000-000000000001", "fa500000-0000-4000-8000-000000000002"
	reconciliationID, mismatchID := "fa600000-0000-4000-8000-000000000001", "fa600000-0000-4000-8000-000000000002"
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$3),($2,1,'active',$3)`, accountA, accountB, now); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.knowledge_evidence(account_id,id,source_kind,source_reference,source_revision,content_sha256,captured_at,created_by_kind,created_by_id,created_at)
		VALUES ($1,$2,'owner_statement','finance-test','1',decode(repeat('11',32),'hex'),$3,'user',$4,$3)`, accountA, evidenceID, now, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_ledgers(account_id,id,name,code,description,currency,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,$2,'Operating ledger','MAIN','','USD','active',1,'user',$3,$4,$4)`, accountA, ledgerID, userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_accounts(account_id,id,ledger_id,code,name,description,account_type,normal_balance,allow_posting,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,$5,$2,'1000','Cash','','asset','debit',true,'active',1,'user',$3,$4,$4),
		       ($1,$6,$2,'4000','Revenue','','income','credit',true,'active',1,'user',$3,$4,$4)`, accountA, ledgerID, userID, now, cashID, revenueID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entry_number_counters(account_id,ledger_id,next_number) VALUES ($1,$2,2)`, accountA, ledgerID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entries(account_id,id,ledger_id,entry_number,entry_date,description,reference,currency,total_minor,source,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,$2,$3,1,$4::date,'Recognize revenue','INV-1','USD',10000,'manual','draft',1,'user',$5,$6,$6)`, accountA, entryID, ledgerID, now, userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entry_lines(account_id,entry_id,line_number,ledger_id,posting_account_id,memo,debit_minor,credit_minor)
		VALUES ($1,$2,1,$3,$4,'Receipt',10000,0),($1,$2,2,$3,$5,'Revenue',0,10000)`, accountA, entryID, ledgerID, cashID, revenueID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entry_evidence(account_id,entry_id,evidence_id) VALUES ($1,$2,$3)`, accountA, entryID, evidenceID); err != nil {
		t.Fatal(err)
	}
	postedAt := now.Add(time.Minute)
	if _, err := owner.Exec(ctx, `UPDATE spyglass.finance_entries SET state='posted',version=2,posted_by_user_id=$3,posted_at=$4,updated_at=$4 WHERE account_id=$1 AND id=$2`, accountA, entryID, userID, postedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.finance_entry_lines SET memo='changed' WHERE account_id=$1 AND entry_id=$2`, accountA, entryID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("posted line mutation=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.finance_entries SET description='changed',version=3 WHERE account_id=$1 AND id=$2`, accountA, entryID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("posted entry mutation=%v", err)
	}

	reversedAt := now.Add(2 * time.Minute)
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entries(account_id,id,ledger_id,entry_number,entry_date,description,reference,currency,total_minor,source,state,reversal_of_id,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,$2,$3,2,$4::date,'Correct revenue','CORR-1','USD',10000,'reversal','draft',$5,1,'user',$6,$7,$7)`, accountA, reversalID, ledgerID, now.AddDate(0, 0, 1), entryID, userID, reversedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entry_lines(account_id,entry_id,line_number,ledger_id,posting_account_id,memo,debit_minor,credit_minor)
		VALUES ($1,$2,1,$3,$4,'Receipt',0,10000),($1,$2,2,$3,$5,'Revenue',10000,0)`, accountA, reversalID, ledgerID, cashID, revenueID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entry_evidence(account_id,entry_id,evidence_id) VALUES ($1,$2,$3)`, accountA, reversalID, evidenceID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.finance_entries SET state='posted',version=2,posted_by_user_id=$3,posted_at=$4,updated_at=$4 WHERE account_id=$1 AND id=$2`, accountA, reversalID, userID, reversedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.finance_entries SET state='reversed',version=3,reversed_by_id=$3,reversed_by_user_id=$4,reversed_at=$5,updated_at=$5 WHERE account_id=$1 AND id=$2`, accountA, entryID, reversalID, userID, reversedAt); err != nil {
		t.Fatal(err)
	}
	var originalState, reversalState string
	if err := owner.QueryRow(ctx, `SELECT o.state,r.state FROM spyglass.finance_entries o JOIN spyglass.finance_entries r ON r.account_id=o.account_id AND r.id=o.reversed_by_id WHERE o.account_id=$1 AND o.id=$2`, accountA, entryID).Scan(&originalState, &reversalState); err != nil || originalState != "reversed" || reversalState != "posted" {
		t.Fatalf("states=%s/%s err=%v", originalState, reversalState, err)
	}

	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_reconciliations(account_id,id,ledger_id,posting_account_id,as_of,currency,statement_balance_minor,ledger_balance_minor,difference_minor,primary_evidence_id,state,version,created_by_user_id,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5::date,'USD',0,0,0,$6,'proposed',1,$7,$8,$8),
		       ($1,$9,$3,$4,($5::date+1),'USD',100,0,100,$6,'discrepancy',1,$7,$8,$8)`, accountA, reconciliationID, ledgerID, cashID, now, evidenceID, userID, reversedAt, mismatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_reconciliation_evidence(account_id,reconciliation_id,evidence_id) VALUES ($1,$2,$4),($1,$3,$4)`, accountA, reconciliationID, mismatchID, evidenceID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.finance_reconciliations SET state='confirmed',version=2,confirmed_by_user_id=$3,confirmed_at=$4,updated_at=$4 WHERE account_id=$1 AND id=$2`, accountA, reconciliationID, userID, reversedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.finance_reconciliations SET state='confirmed',version=2,confirmed_by_user_id=$3,confirmed_at=$4,updated_at=$4 WHERE account_id=$1 AND id=$2`, accountA, mismatchID, userID, reversedAt.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("mismatch confirmation=%v", err)
	}

	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_ledger_close_evidence(account_id,ledger_id,ledger_version,evidence_id,closed_through,created_at) VALUES ($1,$2,2,$3,$4::date,$5)`, accountA, ledgerID, evidenceID, now, reversedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.finance_ledgers SET closed_through=$3::date,version=2,updated_at=$4 WHERE account_id=$1 AND id=$2`, accountA, ledgerID, now, reversedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entries(account_id,id,ledger_id,entry_number,entry_date,description,reference,currency,total_minor,source,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,'fa500000-0000-4000-8000-000000000099',$2,99,$3::date,'Closed period draft','','USD',1,'manual','draft',1,'user',$4,$5,$5)`, accountA, ledgerID, now, userID, reversedAt); err != nil {
		if !strings.Contains(err.Error(), "closed") {
			t.Fatalf("closed-period draft=%v", err)
		}
	} else {
		t.Fatal("closed-period draft was accepted")
	}

	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_events(account_id,id,aggregate_kind,aggregate_id,event_type,from_version,to_version,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,'fa700000-0000-4000-8000-000000000001','entry',$2,'posted',1,2,'user',$3,'fa800000-0000-4000-8000-000000000001','{"entry_number":1,"total_minor":10000,"currency":"USD"}',$4)`, accountA, entryID, userID, postedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.finance_events SET redacted_payload='{}' WHERE account_id=$1`, accountA); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("event mutation=%v", err)
	}

	role := "spyglass_finance_reader_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS; GRANT USAGE ON SCHEMA spyglass TO `+role+`; GRANT SELECT,INSERT ON ALL TABLES IN SCHEMA spyglass TO `+role); err != nil {
		t.Fatal(err)
	}
	reader := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+role)
		return err
	})
	defer func() {
		reader.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE IF EXISTS `+role)
	}()
	if _, err := reader.Exec(ctx, `SELECT set_config('app.account_id',$1,false)`, accountA); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := reader.QueryRow(ctx, `SELECT count(*) FROM spyglass.finance_ledgers`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Account A ledgers=%d err=%v", count, err)
	}
	if _, err := reader.Exec(ctx, `INSERT INTO spyglass.finance_ledgers(account_id,id,name,code,description,currency,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,'fb200000-0000-4000-8000-000000000099','Cross account','CROSS','','USD','active',1,'user',$2,$3,$3)`, accountB, userID, now); err == nil {
		t.Fatal("cross-Account Finance insert bypassed RLS")
	}
}
