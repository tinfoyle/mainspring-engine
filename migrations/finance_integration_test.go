package migrations_test

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	financedomain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
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
	boardroomID, personaID, personaVersionID := "fa810000-0000-4000-8000-000000000001", "fa820000-0000-4000-8000-000000000001", "fa830000-0000-4000-8000-000000000001"
	conversationID, agentRunA, agentRunB := "fa840000-0000-4000-8000-000000000001", "fa850000-0000-4000-8000-000000000001", "fa850000-0000-4000-8000-000000000002"
	invocationA, invocationB := "fa860000-0000-4000-8000-000000000001", "fa860000-0000-4000-8000-000000000002"
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.agent_boardrooms(account_id,id,name,purpose,state,version,created_at,updated_at)
		VALUES ($1,$3,'Finance room','Verify Finance provenance','active',1,$2,$2);
		INSERT INTO spyglass.agent_personas(account_id,id,boardroom_id,state,latest_version,created_at,updated_at)
		VALUES ($1,$4,$3,'active',1,$2,$2);
		INSERT INTO spyglass.agent_persona_versions(account_id,id,persona_id,version,name,role,description,system_instructions,policy,content_digest,created_by,created_at)
		VALUES ($1,$5,$4,1,'Finance Agent','Finance','Finance provenance fixture','Prepare Finance drafts with exact immutable provenance.','{}',decode(repeat('31',32),'hex'),$6,$2);
		INSERT INTO spyglass.agent_conversations(account_id,id,boardroom_id,subject,state,next_message_sequence,created_by,created_at,updated_at)
		VALUES ($1,$7,$3,'Finance provenance','open',1,$6,$2,$2);
		INSERT INTO spyglass.agent_runs(account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at,started_at,completed_at)
		VALUES ($1,$8,$3,$7,'succeeded',1,1,decode(repeat('32',32),'hex'),1,$6,$2,$2,$2),
		       ($1,$9,$3,$7,'succeeded',1,1,decode(repeat('33',32),'hex'),1,$6,$2,$2,$2);
		INSERT INTO spyglass.agent_run_plan_turns(account_id,run_id,turn,persona_id,persona_version_id,persona_digest)
		VALUES ($1,$8,1,$4,$5,decode(repeat('31',32),'hex')),($1,$9,1,$4,$5,decode(repeat('31',32),'hex'));
		INSERT INTO spyglass.agent_invocations(account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,response_model,provider_response_id,runner_result_digest,result_digest,result_payload,input_tokens,output_tokens,total_tokens,queued_at,started_at,completed_at)
		VALUES ($1,$10,$8,1,$5,'succeeded','openai','gpt-test','gpt-test','resp-finance-a',decode(repeat('34',32),'hex'),decode(repeat('35',32),'hex'),'{}',1,1,2,$2,$2,$2),
		       ($1,$11,$9,1,$5,'succeeded','openai','gpt-test','gpt-test','resp-finance-b',decode(repeat('36',32),'hex'),decode(repeat('37',32),'hex'),'{}',1,1,2,$2,$2,$2)`,
		pgx.QueryExecModeSimpleProtocol, accountA, now, boardroomID, personaID, personaVersionID, userID, conversationID, agentRunA, agentRunB, invocationA, invocationB); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entries(account_id,id,ledger_id,entry_number,entry_date,description,reference,currency,total_minor,source,run_id,invocation_id,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,'fa870000-0000-4000-8000-000000000001',$2,50,$3::date,'Agent draft','','USD',1,'agent',$4::uuid,$5::uuid,'draft',1,'workload','runner-invocation:'||$5::text,$3,$3)`, accountA, ledgerID, now, agentRunA, invocationA); err != nil {
		t.Fatalf("matching Agent provenance=%v", err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.finance_entries(account_id,id,ledger_id,entry_number,entry_date,description,reference,currency,total_minor,source,run_id,invocation_id,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,'fa870000-0000-4000-8000-000000000002',$2,51,$3::date,'Spoofed Agent draft','','USD',1,'agent',$4::uuid,$5::uuid,'draft',1,'workload','runner-invocation:'||$5::text,$3,$3)`, accountA, ledgerID, now, agentRunB, invocationA); err == nil || !strings.Contains(err.Error(), "finance_entries_agent_provenance") {
		t.Fatalf("mismatched Agent provenance=%v", err)
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

func TestFinanceRepositoryReplaysAndRestoresLifecycle(t *testing.T) {
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

	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("fc000000-0000-4000-8000-000000000001")
	otherAccountID := ids.AccountID("fd000000-0000-4000-8000-000000000001")
	userID := "fc100000-0000-4000-8000-000000000001"
	evidenceID := ids.KnowledgeEvidenceID("fc200000-0000-4000-8000-000000000001")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$3),($2,1,'active',$3)`, accountID, otherAccountID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.knowledge_evidence(account_id,id,source_kind,source_reference,source_revision,content_sha256,captured_at,created_by_kind,created_by_id,created_at)
		VALUES ($1,$2,'owner_statement','finance-repository-test','1',decode(repeat('22',32),'hex'),$3,'user',$4,$3)`, accountID, evidenceID, now, userID); err != nil {
		t.Fatal(err)
	}
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewFinanceRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	actor := financedomain.Actor{Kind: financedomain.ActorUser, ID: userID}
	mutation := func(id, kind string, at time.Time) financeapp.Mutation {
		return financeapp.Mutation{EventID: id, Kind: kind, Actor: actor, CorrelationID: id, At: at}
	}

	ledgerID := ids.FinanceLedgerID("fc300000-0000-4000-8000-000000000001")
	ledgerEvent := "fc400000-0000-4000-8000-000000000001"
	ledgerDraft := financedomain.LedgerDraft{ID: ledgerID, AccountID: accountID, Name: "Operating ledger", Code: "main", Currency: "USD", CreatedBy: actor, CreatedAt: now}
	ledger, created, err := repository.CreateLedger(ctx, ledgerDraft, accounts.RoleOwner, mutation(ledgerEvent, "created", now))
	if err != nil || !created || ledger.Code != "MAIN" {
		t.Fatalf("ledger=%+v created=%v err=%v", ledger, created, err)
	}
	retryDraft := ledgerDraft
	retryDraft.CreatedAt = now.Add(time.Minute)
	replayed, created, err := repository.CreateLedger(ctx, retryDraft, accounts.RoleOwner, mutation(ledgerEvent, "created", now.Add(time.Minute)))
	if err != nil || created || !reflect.DeepEqual(replayed, ledger) {
		t.Fatalf("ledger replay=%+v created=%v err=%v", replayed, created, err)
	}
	if _, err := repository.GetLedger(ctx, otherAccountID, ledgerID); !errors.Is(err, financeapp.ErrNotFound) {
		t.Fatalf("cross-Account ledger err=%v", err)
	}
	ledgerRevisionEvent := "fc400000-0000-4000-8000-000000000002"
	ledgerRevision := financedomain.LedgerRevision{Name: "Primary operating ledger", Code: "ops", Description: "Internal operations", ExpectedVersion: 1,
		Actor: actor, Role: accounts.RoleOwner, At: now.Add(30 * time.Second)}
	ledger, err = repository.ReviseLedger(ctx, accountID, ledgerID, ledgerRevision, mutation(ledgerRevisionEvent, "revised", ledgerRevision.At))
	if err != nil || ledger.Version != 2 || ledger.Code != "OPS" {
		t.Fatalf("revised ledger=%+v err=%v", ledger, err)
	}
	retryLedgerRevision := ledgerRevision
	retryLedgerRevision.At = now.Add(45 * time.Second)
	replayedLedger, err := repository.ReviseLedger(ctx, accountID, ledgerID, retryLedgerRevision, mutation(ledgerRevisionEvent, "revised", retryLedgerRevision.At))
	if err != nil || replayedLedger.Version != ledger.Version || replayedLedger.UpdatedAt != ledger.UpdatedAt {
		t.Fatalf("ledger revision replay=%+v err=%v", replayedLedger, err)
	}
	secondaryLedgerID := ids.FinanceLedgerID("fc300000-0000-4000-8000-000000000002")
	if _, created, err := repository.CreateLedger(ctx, financedomain.LedgerDraft{ID: secondaryLedgerID, AccountID: accountID, Name: "Auxiliary ledger", Code: "AUX",
		Currency: "USD", CreatedBy: actor, CreatedAt: now.Add(time.Minute)}, accounts.RoleOwner,
		mutation("fc400000-0000-4000-8000-000000000003", "created", now.Add(time.Minute))); err != nil || !created {
		t.Fatalf("secondary ledger created=%v err=%v", created, err)
	}

	cashID := ids.FinanceAccountID("fc500000-0000-4000-8000-000000000001")
	revenueID := ids.FinanceAccountID("fc500000-0000-4000-8000-000000000002")
	assetRootID := ids.FinanceAccountID("fc500000-0000-4000-8000-000000000003")
	for index, value := range []struct {
		id      ids.FinanceAccountID
		code    string
		name    string
		kind    financedomain.AccountType
		eventID string
	}{{cashID, "1000", "Cash", financedomain.AccountAsset, "fc600000-0000-4000-8000-000000000001"},
		{revenueID, "4000", "Revenue", financedomain.AccountIncome, "fc600000-0000-4000-8000-000000000002"},
		{assetRootID, "100", "Assets", financedomain.AccountAsset, "fc600000-0000-4000-8000-000000000003"}} {
		_, created, err := repository.CreatePostingAccount(ctx, financedomain.PostingAccountDraft{ID: value.id, AccountID: accountID, LedgerID: ledgerID,
			Code: value.code, Name: value.name, Type: value.kind, AllowPosting: value.id != assetRootID, CreatedBy: actor, CreatedAt: now.Add(time.Duration(index+1) * time.Minute)},
			accounts.RoleOwner, mutation(value.eventID, "created", now.Add(time.Duration(index+1)*time.Minute)))
		if err != nil || !created {
			t.Fatalf("account %s created=%v err=%v", value.code, created, err)
		}
	}
	accountRevisionEvent := "fc600000-0000-4000-8000-000000000004"
	accountRevision := financedomain.PostingAccountRevision{ParentAccountID: assetRootID, Code: "1010", Name: "Operating cash", Description: "Available cash",
		AllowPosting: true, ExpectedVersion: 1, Actor: actor, Role: accounts.RoleAdministrator, At: now.Add(4 * time.Minute)}
	revisedCash, err := repository.RevisePostingAccount(ctx, accountID, cashID, accountRevision, mutation(accountRevisionEvent, "revised", accountRevision.At))
	if err != nil || revisedCash.Version != 2 || revisedCash.ParentAccountID != assetRootID || revisedCash.Code != "1010" {
		t.Fatalf("revised cash=%+v err=%v", revisedCash, err)
	}
	retryAccountRevision := accountRevision
	retryAccountRevision.At = now.Add(4*time.Minute + 15*time.Second)
	replayedCash, err := repository.RevisePostingAccount(ctx, accountID, cashID, retryAccountRevision, mutation(accountRevisionEvent, "revised", retryAccountRevision.At))
	if err != nil || replayedCash.Version != revisedCash.Version || replayedCash.UpdatedAt != revisedCash.UpdatedAt {
		t.Fatalf("account revision replay=%+v err=%v", replayedCash, err)
	}
	cycleRevision := financedomain.PostingAccountRevision{ParentAccountID: cashID, Code: "100", Name: "Assets", ExpectedVersion: 1,
		Actor: actor, Role: accounts.RoleOwner, At: now.Add(4*time.Minute + 30*time.Second)}
	if _, err := repository.RevisePostingAccount(ctx, accountID, assetRootID, cycleRevision,
		mutation("fc600000-0000-4000-8000-000000000005", "revised", cycleRevision.At)); !errors.Is(err, financeapp.ErrInvalid) {
		t.Fatalf("cycle revision err=%v", err)
	}
	accountPage, err := repository.ListPostingAccounts(ctx, accountID, financeapp.PostingAccountListQuery{LedgerID: ledgerID, Limit: 2})
	if err != nil || len(accountPage.Items) != 2 || accountPage.NextCursor == nil || accountPage.Items[0].Account.Code != "100" || accountPage.Items[1].Account.Code != "1010" {
		t.Fatalf("account page=%+v err=%v", accountPage, err)
	}
	accountRemainder, err := repository.ListPostingAccounts(ctx, accountID, financeapp.PostingAccountListQuery{LedgerID: ledgerID, After: accountPage.NextCursor, Limit: 2})
	if err != nil || len(accountRemainder.Items) != 1 || accountRemainder.NextCursor != nil || accountRemainder.Items[0].Account.Code != "4000" {
		t.Fatalf("account remainder=%+v err=%v", accountRemainder, err)
	}
	loadedCash, err := repository.GetPostingAccount(ctx, accountID, cashID)
	if err != nil || loadedCash.ParentAccountID != assetRootID || loadedCash.Version != 2 {
		t.Fatalf("loaded cash=%+v err=%v", loadedCash, err)
	}

	entryID := ids.FinanceEntryID("fc700000-0000-4000-8000-000000000001")
	entryEvent := "fc800000-0000-4000-8000-000000000001"
	entryAt := now.Add(3 * time.Minute)
	entryDraft := financedomain.EntryDraft{ID: entryID, AccountID: accountID, LedgerID: ledgerID, EntryDate: now, Description: "Recognize revenue", Reference: "INV-1", Currency: "USD",
		Lines: []financedomain.JournalLine{{AccountID: cashID, DebitMinor: 10000}, {AccountID: revenueID, CreditMinor: 10000}}, Evidence: []ids.KnowledgeEvidenceID{evidenceID},
		Provenance: financedomain.Provenance{Source: financedomain.SourceManual}, CreatedBy: actor, CreatedAt: entryAt}
	entry, created, err := repository.CreateEntry(ctx, entryDraft, accounts.RoleMember, mutation(entryEvent, "created", entryAt))
	if err != nil || !created || entry.Number != 1 {
		t.Fatalf("entry=%+v created=%v err=%v", entry, created, err)
	}
	retryEntryDraft := entryDraft
	retryEntryDraft.CreatedAt = entryAt.Add(time.Minute)
	replayedEntry, created, err := repository.CreateEntry(ctx, retryEntryDraft, accounts.RoleMember, mutation(entryEvent, "created", entryAt.Add(time.Minute)))
	if err != nil || created || !reflect.DeepEqual(replayedEntry, entry) {
		t.Fatalf("entry replay=%+v created=%v err=%v", replayedEntry, created, err)
	}
	reviseEntryAt := now.Add(4*time.Minute + 15*time.Second)
	reviseEntryEvent := "fc800000-0000-4000-8000-000000000004"
	reviseEntry := financedomain.EntryRevision{EntryDate: now, Description: "Recognize revised revenue", Reference: "INV-1-R",
		Lines:    []financedomain.JournalLine{{AccountID: cashID, Memo: "Revised receipt", DebitMinor: 12000}, {AccountID: revenueID, Memo: "Revised revenue", CreditMinor: 12000}},
		Evidence: []ids.KnowledgeEvidenceID{evidenceID}, ExpectedVersion: 1, Actor: actor, Role: accounts.RoleMember, At: reviseEntryAt}
	revisedEntry, err := repository.ReviseEntry(ctx, accountID, entryID, reviseEntry, mutation(reviseEntryEvent, "revised", reviseEntryAt))
	if err != nil || revisedEntry.Version != 2 || revisedEntry.TotalMinor != 12000 || revisedEntry.Description != "Recognize revised revenue" {
		t.Fatalf("revised entry=%+v err=%v", revisedEntry, err)
	}
	retryEntryRevision := reviseEntry
	retryEntryRevision.At = reviseEntryAt.Add(10 * time.Second)
	replayedEntryRevision, err := repository.ReviseEntry(ctx, accountID, entryID, retryEntryRevision, mutation(reviseEntryEvent, "revised", retryEntryRevision.At))
	if err != nil || replayedEntryRevision.Version != revisedEntry.Version || replayedEntryRevision.UpdatedAt != revisedEntry.UpdatedAt || !reflect.DeepEqual(replayedEntryRevision.Lines, revisedEntry.Lines) {
		t.Fatalf("entry revision replay=%+v err=%v", replayedEntryRevision, err)
	}
	ledgerPage, err := repository.ListLedgers(ctx, accountID, financeapp.LedgerListQuery{Limit: 1})
	if err != nil || len(ledgerPage.Items) != 1 || ledgerPage.NextCursor == nil || ledgerPage.Items[0].ID != secondaryLedgerID {
		t.Fatalf("ledger page=%+v err=%v", ledgerPage, err)
	}
	ledgerRemainder, err := repository.ListLedgers(ctx, accountID, financeapp.LedgerListQuery{After: ledgerPage.NextCursor, Limit: 1})
	if err != nil || len(ledgerRemainder.Items) != 1 || ledgerRemainder.Items[0].ID != ledgerID || ledgerRemainder.Items[0].AccountCount != 3 || ledgerRemainder.Items[0].DraftCount != 1 {
		t.Fatalf("ledger remainder=%+v err=%v", ledgerRemainder, err)
	}
	if _, err := repository.ArchivePostingAccount(ctx, accountID, cashID, 2, actor, accounts.RoleOwner,
		mutation("fc600000-0000-4000-8000-000000000006", "archived", now.Add(4*time.Minute+45*time.Second))); !errors.Is(err, financeapp.ErrInvalid) {
		t.Fatalf("posting account with draft entry archive err=%v", err)
	}
	postAt := now.Add(5 * time.Minute)
	postEvent := "fc800000-0000-4000-8000-000000000002"
	posted, err := repository.PostEntry(ctx, accountID, entryID, 2, actor, accounts.RoleOwner, mutation(postEvent, "posted", postAt))
	if err != nil || posted.State != financedomain.EntryStatePosted || posted.Version != 3 {
		t.Fatalf("posted=%+v err=%v", posted, err)
	}
	replayedPost, err := repository.PostEntry(ctx, accountID, entryID, 2, actor, accounts.RoleOwner, mutation(postEvent, "posted", postAt.Add(time.Minute)))
	if err != nil || replayedPost.ID != posted.ID || replayedPost.Version != posted.Version || replayedPost.State != posted.State || replayedPost.PostedAt == nil || !replayedPost.PostedAt.Equal(*posted.PostedAt) {
		t.Fatalf("post replay=%+v err=%v", replayedPost, err)
	}
	ledgersAfterPost, err := repository.ListLedgers(ctx, accountID, financeapp.LedgerListQuery{Limit: 10})
	if err != nil || len(ledgersAfterPost.Items) != 2 || ledgersAfterPost.Items[1].ID != ledgerID || ledgersAfterPost.Items[1].IncomeMinor != 12000 || ledgersAfterPost.Items[1].NetMinor != 12000 {
		t.Fatalf("ledgers after post=%+v err=%v", ledgersAfterPost, err)
	}
	balancesAfterPost, err := repository.ListPostingAccounts(ctx, accountID, financeapp.PostingAccountListQuery{LedgerID: ledgerID, Limit: 10})
	if err != nil || balancesAfterPost.Items[1].Account.ID != cashID || balancesAfterPost.Items[1].BalanceMinor != 12000 || balancesAfterPost.Items[2].BalanceMinor != 12000 {
		t.Fatalf("balances after post=%+v err=%v", balancesAfterPost, err)
	}

	reversalID := ids.FinanceEntryID("fc700000-0000-4000-8000-000000000002")
	reverseEvent := "fc800000-0000-4000-8000-000000000003"
	reverseAt := now.Add(6 * time.Minute)
	reverseCommand := financedomain.ReverseCommand{ReversalID: reversalID, EntryDate: now.AddDate(0, 0, 1), Description: "Correct revenue", Evidence: []ids.KnowledgeEvidenceID{evidenceID}, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: 3, At: reverseAt}
	original, reversal, err := repository.ReverseEntry(ctx, accountID, entryID, 3, reverseCommand, mutation(reverseEvent, "reversed", reverseAt))
	if err != nil || original.State != financedomain.EntryStateReversed || reversal.State != financedomain.EntryStatePosted || reversal.Number != 2 {
		t.Fatalf("original=%+v reversal=%+v err=%v", original, reversal, err)
	}
	retryReverseCommand := reverseCommand
	retryReverseCommand.At = reverseAt.Add(time.Minute)
	replayedOriginal, replayedReversal, err := repository.ReverseEntry(ctx, accountID, entryID, 3, retryReverseCommand, mutation(reverseEvent, "reversed", reverseAt.Add(time.Minute)))
	if err != nil || replayedOriginal.ID != original.ID || replayedOriginal.State != original.State || replayedOriginal.ReversedByID != original.ReversedByID ||
		replayedReversal.ID != reversal.ID || replayedReversal.State != reversal.State || replayedReversal.ReversalOfID != reversal.ReversalOfID {
		t.Fatalf("reverse replay original=%+v reversal=%+v err=%v", replayedOriginal, replayedReversal, err)
	}
	entryPage, err := repository.ListEntries(ctx, accountID, financeapp.EntryListQuery{LedgerID: ledgerID, Limit: 1})
	if err != nil || len(entryPage.Items) != 1 || entryPage.NextCursor == nil || entryPage.Items[0].ID != reversalID {
		t.Fatalf("entry page=%+v err=%v", entryPage, err)
	}
	entryRemainder, err := repository.ListEntries(ctx, accountID, financeapp.EntryListQuery{LedgerID: ledgerID, After: entryPage.NextCursor, Limit: 1})
	if err != nil || len(entryRemainder.Items) != 1 || entryRemainder.Items[0].ID != entryID || entryRemainder.Items[0].State != financedomain.EntryStateReversed {
		t.Fatalf("entry remainder=%+v err=%v", entryRemainder, err)
	}

	asOf := now.AddDate(0, 0, 2)
	statementMismatch, _ := financedomain.NewMoney("USD", 100)
	ledgerBalance, _ := financedomain.NewMoney("USD", 0)
	mismatchID := ids.FinanceReconciliationID("fc900000-0000-4000-8000-000000000001")
	mismatch, created, err := repository.CreateReconciliation(ctx, financedomain.ReconciliationDraft{ID: mismatchID, AccountID: accountID, LedgerID: ledgerID,
		PostingAccountID: cashID, AsOf: asOf, StatementBalance: statementMismatch, Evidence: []ids.KnowledgeEvidenceID{evidenceID}, CreatedBy: actor, CreatedAt: reverseAt.Add(time.Minute)},
		accounts.RoleMember, mutation("fca00000-0000-4000-8000-000000000001", "reconciliation_proposed", reverseAt.Add(time.Minute)))
	if err != nil || !created || mismatch.State != financedomain.ReconciliationDiscrepancy {
		t.Fatalf("mismatch=%+v created=%v err=%v", mismatch, created, err)
	}
	matchedID := ids.FinanceReconciliationID("fc900000-0000-4000-8000-000000000002")
	matched, created, err := repository.CreateReconciliation(ctx, financedomain.ReconciliationDraft{ID: matchedID, AccountID: accountID, LedgerID: ledgerID,
		PostingAccountID: cashID, AsOf: asOf, StatementBalance: ledgerBalance, Evidence: []ids.KnowledgeEvidenceID{evidenceID}, CreatedBy: actor, CreatedAt: reverseAt.Add(2 * time.Minute)},
		accounts.RoleMember, mutation("fca00000-0000-4000-8000-000000000002", "reconciliation_proposed", reverseAt.Add(2*time.Minute)))
	if err != nil || !created || matched.State != financedomain.ReconciliationProposed {
		t.Fatalf("matched=%+v created=%v err=%v", matched, created, err)
	}
	confirmed, err := repository.ConfirmReconciliation(ctx, accountID, matchedID, 1, actor, accounts.RoleAdministrator,
		mutation("fca00000-0000-4000-8000-000000000003", "reconciliation_confirmed", reverseAt.Add(3*time.Minute)))
	if err != nil || confirmed.State != financedomain.ReconciliationConfirmed {
		t.Fatalf("confirmed=%+v err=%v", confirmed, err)
	}
	reconciliationPage, err := repository.ListReconciliations(ctx, accountID, financeapp.ReconciliationListQuery{LedgerID: ledgerID, Limit: 1})
	if err != nil || len(reconciliationPage.Items) != 1 || reconciliationPage.NextCursor == nil {
		t.Fatalf("reconciliation page=%+v err=%v", reconciliationPage, err)
	}
	reconciliationRemainder, err := repository.ListReconciliations(ctx, accountID, financeapp.ReconciliationListQuery{LedgerID: ledgerID,
		After: reconciliationPage.NextCursor, Limit: 1})
	if err != nil || len(reconciliationRemainder.Items) != 1 || reconciliationRemainder.Items[0].ID == reconciliationPage.Items[0].ID {
		t.Fatalf("reconciliation remainder=%+v err=%v", reconciliationRemainder, err)
	}
	loadedReconciliation, err := repository.GetReconciliation(ctx, accountID, matchedID)
	if err != nil || loadedReconciliation.State != financedomain.ReconciliationConfirmed || len(loadedReconciliation.Evidence) != 1 {
		t.Fatalf("loaded reconciliation=%+v err=%v", loadedReconciliation, err)
	}

	closeAt := now.Add(10 * time.Minute)
	closeEvent := "fcb00000-0000-4000-8000-000000000001"
	closeCommand := financedomain.ClosePeriodCommand{Through: asOf, Evidence: []ids.KnowledgeEvidenceID{evidenceID}, Actor: actor,
		Role: accounts.RoleOwner, ExpectedVersion: 2, At: closeAt}
	closed, err := repository.CloseLedgerPeriod(ctx, accountID, ledgerID, closeCommand, mutation(closeEvent, "period_closed", closeAt))
	if err != nil || closed.Version != 3 || closed.ClosedThrough == nil || !closed.ClosedThrough.Equal(time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("closed ledger=%+v err=%v", closed, err)
	}
	retryClose := closeCommand
	retryClose.At = closeAt.Add(time.Minute)
	replayedClose, err := repository.CloseLedgerPeriod(ctx, accountID, ledgerID, retryClose, mutation(closeEvent, "period_closed", retryClose.At))
	if err != nil || replayedClose.Version != closed.Version || replayedClose.UpdatedAt != closed.UpdatedAt {
		t.Fatalf("close replay=%+v err=%v", replayedClose, err)
	}
	if _, err := repository.ArchiveLedger(ctx, accountID, ledgerID, 3, actor, accounts.RoleOwner,
		mutation("fcb00000-0000-4000-8000-000000000002", "archived", closeAt.Add(2*time.Minute))); !errors.Is(err, financeapp.ErrInvalid) {
		t.Fatalf("ledger with active accounts archive err=%v", err)
	}
	archiveAccount := func(id ids.FinanceAccountID, expected uint64, eventID string, at time.Time) financedomain.PostingAccount {
		value, err := repository.ArchivePostingAccount(ctx, accountID, id, expected, actor, accounts.RoleAdministrator, mutation(eventID, "archived", at))
		if err != nil || value.State != financedomain.LedgerArchived || value.AllowPosting {
			t.Fatalf("archived account=%+v err=%v", value, err)
		}
		return value
	}
	archivedCash := archiveAccount(cashID, 2, "fcb00000-0000-4000-8000-000000000003", closeAt.Add(3*time.Minute))
	archiveAccount(revenueID, 1, "fcb00000-0000-4000-8000-000000000004", closeAt.Add(4*time.Minute))
	archiveAccount(assetRootID, 1, "fcb00000-0000-4000-8000-000000000005", closeAt.Add(5*time.Minute))
	replayedArchive, err := repository.ArchivePostingAccount(ctx, accountID, cashID, 2, actor, accounts.RoleAdministrator,
		mutation("fcb00000-0000-4000-8000-000000000003", "archived", closeAt.Add(6*time.Minute)))
	if err != nil || replayedArchive.Version != archivedCash.Version || replayedArchive.UpdatedAt != archivedCash.UpdatedAt {
		t.Fatalf("account archive replay=%+v err=%v", replayedArchive, err)
	}
	archiveLedgerEvent := "fcb00000-0000-4000-8000-000000000006"
	archivedLedger, err := repository.ArchiveLedger(ctx, accountID, ledgerID, 3, actor, accounts.RoleOwner,
		mutation(archiveLedgerEvent, "archived", closeAt.Add(7*time.Minute)))
	if err != nil || archivedLedger.State != financedomain.LedgerArchived || archivedLedger.Version != 4 {
		t.Fatalf("archived ledger=%+v err=%v", archivedLedger, err)
	}
	replayedArchivedLedger, err := repository.ArchiveLedger(ctx, accountID, ledgerID, 3, actor, accounts.RoleOwner,
		mutation(archiveLedgerEvent, "archived", closeAt.Add(8*time.Minute)))
	if err != nil || replayedArchivedLedger.Version != archivedLedger.Version || replayedArchivedLedger.UpdatedAt != archivedLedger.UpdatedAt {
		t.Fatalf("ledger archive replay=%+v err=%v", replayedArchivedLedger, err)
	}

	overflowAssetID := ids.FinanceAccountID("fcc00000-0000-4000-8000-000000000001")
	overflowIncomeID := ids.FinanceAccountID("fcc00000-0000-4000-8000-000000000002")
	for index, value := range []struct {
		id      ids.FinanceAccountID
		code    string
		kind    financedomain.AccountType
		eventID string
	}{{overflowAssetID, "1000", financedomain.AccountAsset, "fcc10000-0000-4000-8000-000000000001"},
		{overflowIncomeID, "4000", financedomain.AccountIncome, "fcc10000-0000-4000-8000-000000000002"}} {
		at := closeAt.Add(time.Duration(20+index) * time.Minute)
		if _, created, err := repository.CreatePostingAccount(ctx, financedomain.PostingAccountDraft{ID: value.id, AccountID: accountID,
			LedgerID: secondaryLedgerID, Code: value.code, Name: value.code, Type: value.kind, AllowPosting: true, CreatedBy: actor, CreatedAt: at},
			accounts.RoleOwner, mutation(value.eventID, "created", at)); err != nil || !created {
			t.Fatalf("overflow account created=%v err=%v", created, err)
		}
	}
	for index := 0; index < 2; index++ {
		entryID := ids.FinanceEntryID([]string{"fcc20000-0000-4000-8000-000000000001", "fcc20000-0000-4000-8000-000000000002"}[index])
		createEvent := []string{"fcc30000-0000-4000-8000-000000000001", "fcc30000-0000-4000-8000-000000000002"}[index]
		postEvent := []string{"fcc40000-0000-4000-8000-000000000001", "fcc40000-0000-4000-8000-000000000002"}[index]
		createAt := closeAt.Add(time.Duration(22+index*2) * time.Minute)
		value, created, err := repository.CreateEntry(ctx, financedomain.EntryDraft{ID: entryID, AccountID: accountID, LedgerID: secondaryLedgerID,
			EntryDate: asOf.AddDate(0, 0, index+1), Description: "Aggregate boundary", Currency: "USD",
			Lines:    []financedomain.JournalLine{{AccountID: overflowAssetID, DebitMinor: math.MaxInt64}, {AccountID: overflowIncomeID, CreditMinor: math.MaxInt64}},
			Evidence: []ids.KnowledgeEvidenceID{evidenceID}, Provenance: financedomain.Provenance{Source: financedomain.SourceManual}, CreatedBy: actor, CreatedAt: createAt},
			accounts.RoleMember, mutation(createEvent, "created", createAt))
		if err != nil || !created {
			t.Fatalf("overflow entry=%+v created=%v err=%v", value, created, err)
		}
		postAt := createAt.Add(time.Minute)
		if _, err := repository.PostEntry(ctx, accountID, entryID, 1, actor, accounts.RoleOwner, mutation(postEvent, "posted", postAt)); err != nil {
			t.Fatalf("overflow entry post err=%v", err)
		}
	}
	if _, err := repository.ListPostingAccounts(ctx, accountID, financeapp.PostingAccountListQuery{LedgerID: secondaryLedgerID, Limit: 10}); !errors.Is(err, financeapp.ErrAggregateOverflow) {
		t.Fatalf("posting-account aggregate overflow err=%v", err)
	}
	if _, err := repository.ListLedgers(ctx, accountID, financeapp.LedgerListQuery{Limit: 10}); !errors.Is(err, financeapp.ErrAggregateOverflow) {
		t.Fatalf("Ledger aggregate overflow err=%v", err)
	}
}
