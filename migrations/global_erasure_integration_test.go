package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/restoregate"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type failingCellExecutor struct {
	delegate accounterasure.CellExecutor
	err      error
}

func (e failingCellExecutor) Erase(context.Context, accounterasure.CellEraseCommand) (accounterasure.CellTombstone, error) {
	return accounterasure.CellTombstone{}, e.err
}

func (e failingCellExecutor) AttestErasure(ctx context.Context, cellID ids.CellID, requestID string, fingerprint []byte) (accounterasure.CellTombstone, error) {
	return e.delegate.AttestErasure(ctx, cellID, requestID, fingerprint)
}

func TestPostgresGlobalErasureIsCrossStoreExactAndIdempotent(t *testing.T) {
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
	for _, target := range []migrations.Target{migrations.Global, migrations.Development, migrations.Cell} {
		if _, err := migrations.Apply(ctx, owner, target); err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
	}
	initialGlobalGate, _ := restoregate.New(owner, restoregate.Global, restoregate.InitialCheckpoint())
	initialCellGate, _ := restoregate.New(owner, restoregate.Cell, restoregate.InitialCheckpoint())
	if err := initialGlobalGate.Ready(ctx); err != nil {
		t.Fatalf("initial global restore checkpoint: %v", err)
	}
	if err := initialCellGate.Ready(ctx); err != nil {
		t.Fatalf("initial cell restore checkpoint: %v", err)
	}
	var now time.Time
	if err := owner.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	accountA := ids.AccountID("a8100000-0000-4000-8000-000000000001")
	accountB := ids.AccountID("b8100000-0000-4000-8000-000000000001")
	userA := ids.UserID("a8200000-0000-4000-8000-000000000001")
	userB := ids.UserID("b8200000-0000-4000-8000-000000000001")
	closureA := "a8300000-0000-4000-8000-000000000001"
	closureB := "b8300000-0000-4000-8000-000000000001"
	seedGlobalErasureAccount(t, ctx, owner, accountA, userA, closureA, "a", now)
	seedGlobalErasureAccount(t, ctx, owner, accountB, userB, closureB, "b", now)
	seedCellErasureAccount(t, ctx, owner, accountA, "a8400000-0000-4000-8000-000000000001", "a8500000-0000-4000-8000-000000000001", now, true)
	seedCellErasureAccount(t, ctx, owner, accountB, "b8400000-0000-4000-8000-000000000001", "b8500000-0000-4000-8000-000000000001", now, true)
	if _, err := owner.Exec(ctx, `UPDATE cells SET assigned_accounts=2 WHERE id='cell-us-east-01'`); err != nil {
		t.Fatal(err)
	}

	preparationIDs := &erasureIDs{values: []string{
		"a8600000-0000-4000-8000-000000000001", "a8700000-0000-4000-8000-000000000001",
		"a8800000-0000-4000-8000-000000000001", "a8900000-0000-4000-8000-000000000001",
	}}
	ownerGlobal := postgresadapter.NewAccountErasureRepository(owner)
	ownerCell := postgresadapter.NewAccountErasureCellRepository(owner, "cell-us-east-01")
	preparation, err := accounterasure.NewService(ownerGlobal, ownerCell, preparationIDs, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparation.Prepare(ctx, accounterasure.PrepareCommand{
		AccountID: accountA, PolicyVersion: 1,
		Export:          accounterasure.ExportEvidence{Disposition: accounterasure.ExportNotApplicable, Reason: "policy-approved test fixture"},
		BackupExpiresAt: now.Add(35 * 24 * time.Hour), Actor: "requester@example.com",
		Reason: "prepare retained Account for final erasure", Environment: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := preparation.Approve(ctx, prepared.ID, prepared.Version, "reviewer@example.com", "approve exact cross-store erasure", "test")
	if err != nil || approved.Version != 2 {
		t.Fatalf("approve request=%+v err=%v", approved, err)
	}

	globalFunctionRole := "spyglass_global_eraser_function_" + randomSuffix(t)
	globalOperatorRole := "spyglass_global_eraser_operator_" + randomSuffix(t)
	cellFunctionRole := "spyglass_cell_eraser_function_" + randomSuffix(t)
	cellOperatorRole := "spyglass_cell_eraser_operator_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+globalFunctionRole+` NOLOGIN NOBYPASSRLS;
		CREATE ROLE `+globalOperatorRole+` NOLOGIN NOBYPASSRLS;
		CREATE ROLE `+cellFunctionRole+` NOLOGIN NOBYPASSRLS;
		CREATE ROLE `+cellOperatorRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public,spyglass TO `+globalFunctionRole+`,`+globalOperatorRole+`,`+cellFunctionRole+`,`+cellOperatorRole+`;
		GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO `+globalFunctionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_assert_account_erasure_eligible(uuid) TO `+globalFunctionRole+`;
		ALTER TABLE public.account_erasure_tombstones OWNER TO `+globalFunctionRole+`;
		ALTER FUNCTION public.spyglass_claim_account_erasure_execution(uuid,uuid,uuid,bigint,uuid,bigint,bytea,bytea,text,text,text) OWNER TO `+globalFunctionRole+`;
		ALTER FUNCTION public.spyglass_record_account_cell_erasure(uuid,uuid,bigint,uuid,bytea,timestamptz,jsonb,bytea,text,text,text) OWNER TO `+globalFunctionRole+`;
		ALTER FUNCTION public.spyglass_finalize_account_erasure(uuid,uuid,bigint,uuid,bytea,bytea,text) OWNER TO `+globalFunctionRole+`;
		ALTER FUNCTION public.spyglass_attest_global_account_erasure(uuid,bytea) OWNER TO `+globalFunctionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_claim_account_erasure_execution(uuid,uuid,uuid,bigint,uuid,bigint,bytea,bytea,text,text,text) TO `+globalOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_record_account_cell_erasure(uuid,uuid,bigint,uuid,bytea,timestamptz,jsonb,bytea,text,text,text) TO `+globalOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_finalize_account_erasure(uuid,uuid,bigint,uuid,bytea,bytea,text) TO `+globalOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_attest_global_account_erasure(uuid,bytea) TO `+globalOperatorRole+`;
		GRANT SELECT,DELETE,UPDATE ON spyglass.account_namespaces,spyglass.account_audit_events,spyglass.work_item_number_counters,
			spyglass.work_items,spyglass.work_item_events,spyglass.route_context_receipts,spyglass.work_capacity_release_queue,
			spyglass.route_context_receipt_cleanup_queue,spyglass.work_capacity_release_operator_events,
			spyglass.runner_account_scheduling,spyglass.runner_invocation_queue,spyglass.runner_invocation_exchanges TO `+cellFunctionRole+`;
		ALTER TABLE spyglass.account_erasure_tombstones OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_runner_control(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_runner_exchange(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_attest_account_cell_erasure(uuid,bytea) OWNER TO `+cellFunctionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) TO `+cellOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_attest_account_cell_erasure(uuid,bytea) TO `+cellOperatorRole); err != nil {
		t.Fatalf("create split erasure roles: %v", err)
	}
	globalOperator := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+globalOperatorRole)
		return err
	})
	cellOperator := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+cellOperatorRole)
		return err
	})
	defer func() {
		globalOperator.Close()
		cellOperator.Close()
		_, _ = owner.Exec(context.Background(), `REASSIGN OWNED BY `+globalFunctionRole+` TO postgres; REASSIGN OWNED BY `+cellFunctionRole+` TO spyglass;
			DROP OWNED BY `+globalOperatorRole+`; DROP OWNED BY `+cellOperatorRole+`; DROP OWNED BY `+globalFunctionRole+`; DROP OWNED BY `+cellFunctionRole+`;
			DROP ROLE IF EXISTS `+globalOperatorRole+`; DROP ROLE IF EXISTS `+cellOperatorRole+`; DROP ROLE IF EXISTS `+globalFunctionRole+`; DROP ROLE IF EXISTS `+cellFunctionRole)
	}()
	var unauthorized int
	if err := globalOperator.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&unauthorized); err == nil {
		t.Fatal("global erasure operator directly read Accounts")
	}
	if err := cellOperator.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_items`).Scan(&unauthorized); err == nil {
		t.Fatal("cell erasure operator directly read Work")
	}

	evidenceKey := bytes.Repeat([]byte{0x5a}, 32)
	globalExecutionRepository := postgresadapter.NewAccountErasureRepository(globalOperator)
	cellExecutionRepository := postgresadapter.NewAccountErasureCellRepository(cellOperator, "cell-us-east-01")
	crash := errors.New("simulated crash before cell commit")
	failedExecution, err := accounterasure.NewExecutionService(
		globalExecutionRepository, failingCellExecutor{delegate: cellExecutionRepository, err: crash},
		&erasureIDs{values: []string{"aa100000-0000-4000-8000-000000000001", "aa200000-0000-4000-8000-000000000001"}},
		fixedClock{now: now}, evidenceKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	command := accounterasure.ExecuteCommand{RequestID: approved.ID, AccountID: accountA, ExpectedVersion: approved.Version, LeaseDuration: 5 * time.Minute, Actor: "executor@example.com", Reason: "execute independently approved Account erasure", Environment: "test"}
	if _, err := failedExecution.Execute(ctx, command); !errors.Is(err, crash) {
		t.Fatalf("pre-cell simulated crash=%v", err)
	}
	var claimedState string
	var claimedVersion, cellRequestVersion uint64
	if err := owner.QueryRow(ctx, `SELECT state,version,cell_request_version FROM account_erasure_requests WHERE id=$1`, approved.ID).Scan(&claimedState, &claimedVersion, &cellRequestVersion); err != nil {
		t.Fatal(err)
	}
	if claimedState != "cell_erasing" || claimedVersion != 3 || cellRequestVersion != 3 {
		t.Fatalf("durable cell claim state=%s version=%d cell_version=%d", claimedState, claimedVersion, cellRequestVersion)
	}
	immediateRetry, _ := accounterasure.NewExecutionService(
		globalExecutionRepository, cellExecutionRepository,
		&erasureIDs{values: []string{"aa300000-0000-4000-8000-000000000001", "aa400000-0000-4000-8000-000000000001"}},
		fixedClock{now: now}, evidenceKey,
	)
	command.ExpectedVersion = claimedVersion
	if _, err := immediateRetry.Execute(ctx, command); !errors.Is(err, accounterasure.ErrStateConflict) {
		t.Fatalf("active execution lease retry=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE account_erasure_requests SET lease_expires_at=statement_timestamp()-interval '1 second' WHERE id=$1`, approved.ID); err != nil {
		t.Fatal(err)
	}
	executionIDs := &erasureIDs{values: []string{
		"ab100000-0000-4000-8000-000000000001", "ab200000-0000-4000-8000-000000000001",
		"ab300000-0000-4000-8000-000000000001", "ab400000-0000-4000-8000-000000000001",
		"ab500000-0000-4000-8000-000000000001",
	}}
	execution, err := accounterasure.NewExecutionService(
		globalExecutionRepository, cellExecutionRepository,
		executionIDs, fixedClock{now: now}, evidenceKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	tombstone, err := execution.Execute(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if tombstone.RequestID != approved.ID || tombstone.FinalRequestVersion != 6 || tombstone.GlobalRowCounts["accounts"] != 1 || tombstone.GlobalRowCounts["cell_capacity"] != 1 || tombstone.CellRowCounts["work_items"] != 2 {
		t.Fatalf("unexpected global tombstone: %+v", tombstone)
	}
	for _, ledger := range []struct {
		target restoregate.Target
		table  string
	}{{restoregate.Global, "public.account_erasure_restore_ledger"}, {restoregate.Cell, "spyglass.account_erasure_restore_ledger"}} {
		var sequence uint64
		var root []byte
		if err := owner.QueryRow(ctx, `SELECT sequence,root FROM `+ledger.table+` ORDER BY sequence DESC LIMIT 1`).Scan(&sequence, &root); err != nil {
			t.Fatal(err)
		}
		checkpoint, err := restoregate.NewCheckpoint(sequence, root)
		if err != nil {
			t.Fatal(err)
		}
		gate, _ := restoregate.New(owner, ledger.target, checkpoint)
		if err := gate.Ready(ctx); err != nil {
			t.Fatalf("current %s restore checkpoint: %v", ledger.target, err)
		}
		missingCheckpoint, _ := restoregate.NewCheckpoint(sequence+1, bytes.Repeat([]byte{0x7f}, 32))
		missingGate, _ := restoregate.New(owner, ledger.target, missingCheckpoint)
		if err := missingGate.Ready(ctx); !errors.Is(err, restoregate.ErrReplayRequired) {
			t.Fatalf("missing %s restore replay checkpoint=%v", ledger.target, err)
		}
		historicalGate := initialGlobalGate
		if ledger.target == restoregate.Cell {
			historicalGate = initialCellGate
		}
		if err := historicalGate.Ready(ctx); err != nil {
			t.Fatalf("historical %s checkpoint disappeared after newer erasure: %v", ledger.target, err)
		}
	}
	assertGlobalAccountRows(t, ctx, owner, accountA, false)
	assertGlobalAccountRows(t, ctx, owner, accountB, true)
	assertCellAccountRows(t, ctx, owner, accountA, 0)
	assertCellAccountRows(t, ctx, owner, accountB, 1)
	var userCount, assignedAccounts int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM users WHERE id IN ($1,$2)`, userA, userB).Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT assigned_accounts FROM cells WHERE id='cell-us-east-01'`).Scan(&assignedAccounts); err != nil {
		t.Fatal(err)
	}
	if userCount != 2 || assignedAccounts != 1 {
		t.Fatalf("shared identity/capacity after erasure: users=%d assigned=%d", userCount, assignedAccounts)
	}

	repeated, err := execution.Execute(ctx, command)
	if err != nil || !repeated.CompletedAt.Equal(tombstone.CompletedAt) {
		t.Fatalf("idempotent global completion=%+v err=%v", repeated, err)
	}
	var afterRetryCapacity int
	_ = owner.QueryRow(ctx, `SELECT assigned_accounts FROM cells WHERE id='cell-us-east-01'`).Scan(&afterRetryCapacity)
	if afterRetryCapacity != 1 {
		t.Fatalf("repeated execution decremented capacity to %d", afterRetryCapacity)
	}

	postErasurePayload := []byte(`{"id":"evt_after_global_erasure","protected":"must-not-persist"}`)
	postErasureHash := sha256.Sum256(postErasurePayload)
	accepted, err := postgresadapter.NewBillingInbox(owner).Accept(ctx, billing.InboxEntry{ProviderEventID: "evt_after_global_erasure", AccountID: accountA, EventType: "customer.subscription.updated", ProviderCreatedAt: now, Mode: "test", PayloadHash: postErasureHash, SignatureVerifiedAt: now, ProcessingState: "accepted", CreatedAt: now}, postErasurePayload)
	if err != nil || accepted {
		t.Fatalf("post-erasure provider retry accepted=%v err=%v", accepted, err)
	}
	var serialized string
	if err := owner.QueryRow(ctx, `SELECT to_jsonb(t)::text FROM account_erasure_tombstones t WHERE request_id=$1`, approved.ID).Scan(&serialized); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{string(accountA), string(userA), "erasure-a@example.com", "erasure-a-account", "requester@example.com", "execute independently", "cus_a", "sub_a"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("global tombstone leaked %q: %s", forbidden, serialized)
		}
	}
	var ledgerSerialization string
	if err := owner.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(e) ORDER BY sequence)::text FROM account_erasure_restore_ledger e`).Scan(&ledgerSerialization); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{string(accountA), string(userA), "erasure-a@example.com", "erasure-a-account", "cus_a", "sub_a"} {
		if strings.Contains(ledgerSerialization, forbidden) {
			t.Fatalf("restore ledger leaked %q: %s", forbidden, ledgerSerialization)
		}
	}
	if _, err := owner.Exec(ctx, `DELETE FROM account_erasure_restore_ledger WHERE sequence=1`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("global restore ledger mutation=%v", err)
	}
	if _, err := owner.Exec(ctx, `DELETE FROM account_lifecycle_events WHERE account_id=$1`, accountB); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("ordinary lifecycle audit deletion=%v", err)
	}
}

func seedGlobalErasureAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID ids.AccountID, userID ids.UserID, closureID, marker string, now time.Time) {
	t.Helper()
	ownerMembershipID := marker + "9000000-0000-4000-8000-000000000001"
	memberMembershipID := marker + "9100000-0000-4000-8000-000000000001"
	memberUserID := marker + "9200000-0000-4000-8000-000000000001"
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at)
		VALUES ($1,$2,'Erasure Owner','active',$4,1,$4),($3,$5,'Erasure Member','active',$4,1,$4);
		INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,created_by_user_id,created_at,version)
		VALUES ($6,$7,$8,'free','closed','cell-us-east-01',3,1,$1,$4,7);
		INSERT INTO account_directory(account_id,cell_id,placement_generation,state,data_region,updated_at) VALUES ($6,'cell-us-east-01',3,'frozen','us-east',$4);
		INSERT INTO account_closure_requests(id,account_id,state,requested_by_user_id,reason,account_version,requested_at,execute_after,next_attempt_at,attempt_count,closed_at,delete_after)
		VALUES ($9,$6,'closed',$1,'customer requested closure',7,$4::timestamptz-interval '50 days',$4::timestamptz-interval '49 days',$4::timestamptz-interval '49 days',1,$4::timestamptz-interval '40 days',$4::timestamptz-interval '10 days');
		INSERT INTO memberships(id,account_id,user_id,role,state,version,created_at)
		VALUES ($10,$6,$1,'owner','active',1,$4),($11,$6,$3,'member','active',1,$4);
		INSERT INTO invitations(id,account_id,email,role,state,invited_by_user_id,token_hash,expires_at,created_at)
		VALUES (gen_random_uuid(),$6,$12,'viewer','revoked',$1,sha256(convert_to($12,'UTF8')),$4::timestamptz+interval '1 day',$4);
		INSERT INTO billing_profiles(account_id,stripe_customer_id,billing_email,version,created_at,updated_at) VALUES ($6,$13,$2,1,$4,$4);
		INSERT INTO subscriptions(id,account_id,provider,provider_subscription_id,state,offer_code,offer_version,last_synced_at,created_at,updated_at,provider_customer_id,provider_mode)
		VALUES (gen_random_uuid(),$6,'stripe',$14,'canceled','team-monthly-v1',1,$4,$4,$4,$13,'test');
		INSERT INTO billing_reconciliation_queue(provider_subscription_id,reason,requested_at,next_attempt_at,processing_state,completed_at) VALUES ($14,'erasure fixture',$4,$4,'completed',$4);
		INSERT INTO billing_checkout_attempts(request_id,account_id,provider,mode,offer_code,state,expires_at,created_at,updated_at) VALUES (gen_random_uuid(),$6,'stripe','test','team-monthly-v1','expired',$4::timestamptz-interval '1 day',$4::timestamptz-interval '2 days',$4);
		INSERT INTO entitlement_grants(id,account_id,package_code,package_version,mode,source,source_reference,limits,starts_at,priority,reason,created_at) VALUES (gen_random_uuid(),$6,'knowledge',1,'enabled','free_plan','erasure-fixture','{}',$4,1,'fixture',$4);
		INSERT INTO entitlement_snapshots(account_id,version,catalog_version,evaluated_at,source_hash,effective_packages) VALUES ($6,1,(SELECT max(version) FROM catalog_publications),$4,sha256(convert_to($7,'UTF8')),'{}');
		INSERT INTO entitlement_usage_counters(account_id,package_code,limit_code,current_value,version,updated_at) VALUES ($6,'work','active_items',0,1,$4);
		INSERT INTO entitlement_usage_reservations(id,account_id,request_id,package_code,limit_code,amount,maximum_at_admission,entitlement_version,state,created_at,closed_at) VALUES (gen_random_uuid(),$6,gen_random_uuid(),'work','active_items',1,10,1,'released',$4,$4);
		INSERT INTO identity_notification_outbox(id,kind,ciphertext,nonce,key_version,processing_state,attempt_count,created_at,account_id) VALUES (gen_random_uuid(),'invitation',convert_to($8,'UTF8'),decode(repeat('01',12),'hex'),1,'queued',0,$4,$6);
		INSERT INTO billing_event_inbox(provider_event_id,event_type,provider_created_at,mode,payload_hash,payload_reference,payload,signature_verified_at,processing_state,attempt_count,created_at,account_id) VALUES ($15,'customer.subscription.deleted',$4,'test',sha256(convert_to($8,'UTF8')),'postgres:inline',convert_to($8,'UTF8'),$4,'processed',1,$4,$6);
		INSERT INTO account_membership_events(id,account_id,actor_user_id,action,target_membership_id,previous_role,new_role,reason,occurred_at) VALUES (gen_random_uuid(),$6,$1,'role_changed',$11,'viewer','member','reviewed role change',$4);
		INSERT INTO account_lifecycle_events(id,account_id,closure_request_id,action,from_state,to_state,actor_kind,actor_id,reason,occurred_at) VALUES (gen_random_uuid(),$6,$9,'account_closed','closing','closed','workload','account-lifecycle-worker','retention scheduled',$4);`,
		pgx.QueryExecModeSimpleProtocol, userID, "erasure-"+marker+"@example.com", memberUserID, now, "member-"+marker+"@example.com",
		accountID, "erasure-"+marker+"-account", "Erasure "+marker+" Account", closureID,
		ownerMembershipID, memberMembershipID, "invite-"+marker+"@example.com", "cus_"+marker, "sub_"+marker, "evt_erasure_"+marker); err != nil {
		t.Fatalf("seed global erasure Account %s: %v", accountID, err)
	}
	var rolloutID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM entitlement_catalog_rollouts ORDER BY created_at,id LIMIT 1`).Scan(&rolloutID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entitlement_recompute_queue(rollout_id,account_id,processing_state,attempt_count,created_at,completed_at) VALUES ($1,$2,'completed',1,$3,$3)`, rolloutID, accountID, now); err != nil {
		t.Fatal(err)
	}
	if marker == "a" {
		if _, err := pool.Exec(ctx, `UPDATE entitlement_catalog_rollouts SET cursor_created_at=$2,cursor_account_id=$1 WHERE id=$3`, accountID, now, rolloutID); err != nil {
			t.Fatal(err)
		}
	}
}

func assertGlobalAccountRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID ids.AccountID, wantPresent bool) {
	t.Helper()
	accountTables := []string{"accounts", "account_directory", "memberships", "invitations", "billing_profiles", "subscriptions", "billing_event_inbox", "entitlement_grants", "entitlement_snapshots", "billing_checkout_attempts", "entitlement_recompute_queue", "entitlement_usage_counters", "entitlement_usage_reservations", "account_membership_events", "account_closure_requests", "account_lifecycle_events", "identity_notification_outbox"}
	for _, table := range accountTables {
		var count int
		column := "account_id"
		if table == "accounts" {
			column = "id"
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.`+table+` WHERE `+column+`=$1`, accountID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if wantPresent && count == 0 {
			t.Fatalf("other Account rows missing from %s", table)
		}
		if !wantPresent && count != 0 {
			t.Fatalf("erased Account left %d rows in %s", count, table)
		}
	}
	if !wantPresent {
		for _, table := range []string{"account_erasure_requests", "account_erasure_operator_events"} {
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.`+table+` WHERE account_id=$1`, accountID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("erasure workflow rows remain in %s: count=%d err=%v", table, count, err)
			}
		}
	}
	var reconciliationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_reconciliation_queue WHERE provider_subscription_id=$1`, "sub_"+string(accountID)[0:1]).Scan(&reconciliationCount); err != nil {
		t.Fatal(err)
	}
	if reconciliationCount != boolCount(wantPresent) {
		t.Fatalf("billing reconciliation presence=%d want=%v", reconciliationCount, wantPresent)
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
