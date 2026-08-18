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
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresCellErasureIsExactIdempotentAndContentFree(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatal(err)
	}
	coveredTables := map[string]bool{"account_namespaces": true, "account_audit_events": true, "work_item_number_counters": true, "work_items": true, "work_item_events": true, "route_context_receipts": true, "work_capacity_release_queue": true, "route_context_receipt_cleanup_queue": true, "work_capacity_release_operator_events": true, "runner_account_scheduling": true, "runner_invocation_queue": true, "runner_invocation_exchanges": true, "runner_capability_events": true, "runner_action_authorizations": true, "runner_action_ledger": true, "runner_action_attempts": true, "agent_boardrooms": true, "agent_personas": true, "agent_persona_versions": true, "agent_conversations": true, "agent_runs": true, "agent_run_plan_turns": true, "agent_invocations": true, "agent_messages": true, "agent_result_projection_queue": true, "agent_user_messages": true, "agent_invocation_execution_plans": true, "agent_dispatch_queue": true, "agent_queue_operator_events": true}
	rows, err := owner.Query(ctx, `SELECT table_name FROM information_schema.columns WHERE table_schema='spyglass' AND column_name='account_id' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seenTables := map[string]bool{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		seenTables[table] = true
		if !coveredTables[table] {
			t.Fatalf("Account-owned cell table %s is missing from erasure policy coverage", table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seenTables) != len(coveredTables) {
		t.Fatalf("cell erasure policy coverage=%v want=%v", seenTables, coveredTables)
	}
	var now time.Time
	if err := owner.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	accountA := ids.AccountID("a1000000-0000-4000-8000-000000000001")
	accountB := ids.AccountID("b1000000-0000-4000-8000-000000000001")
	accountC := ids.AccountID("c1000000-0000-4000-8000-000000000001")
	seedCellErasureAccount(t, ctx, owner, accountA, "a2000000-0000-4000-8000-000000000001", "a3000000-0000-4000-8000-000000000001", now, true)
	seedCellErasureAccount(t, ctx, owner, accountB, "b2000000-0000-4000-8000-000000000001", "b3000000-0000-4000-8000-000000000001", now, false)
	seedCellErasureAccount(t, ctx, owner, accountC, "c2000000-0000-4000-8000-000000000001", "c3000000-0000-4000-8000-000000000001", now, true)

	functionRole := "spyglass_cell_eraser_function_" + randomSuffix(t)
	operatorRole := "spyglass_cell_eraser_operator_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+functionRole+` NOLOGIN NOBYPASSRLS;
		CREATE ROLE `+operatorRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public,spyglass TO `+functionRole+`,`+operatorRole+`;
		GRANT SELECT,DELETE ON spyglass.account_namespaces,spyglass.account_audit_events,spyglass.work_item_number_counters,
			spyglass.work_items,spyglass.work_item_events,spyglass.route_context_receipts,spyglass.work_capacity_release_queue,
			spyglass.route_context_receipt_cleanup_queue,spyglass.work_capacity_release_operator_events,
			spyglass.runner_account_scheduling,spyglass.runner_invocation_queue,spyglass.runner_invocation_exchanges,spyglass.runner_capability_events,
			spyglass.runner_action_authorizations,spyglass.runner_action_ledger,spyglass.runner_action_attempts,
			spyglass.agent_boardrooms,spyglass.agent_personas,spyglass.agent_persona_versions,spyglass.agent_conversations,
			spyglass.agent_runs,spyglass.agent_run_plan_turns,spyglass.agent_invocations,spyglass.agent_messages,
			spyglass.agent_result_projection_queue,spyglass.agent_user_messages,spyglass.agent_invocation_execution_plans,
			spyglass.agent_dispatch_queue,spyglass.agent_queue_operator_events TO `+functionRole+`;
		GRANT UPDATE ON spyglass.account_namespaces TO `+functionRole+`;
		ALTER TABLE spyglass.account_erasure_tombstones OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_runner_control(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_runner_exchange(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_runner_capability_audit(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_runner_actions(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_agents(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_agent_projection(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_agent_dispatch(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell_without_agent_queue_admin(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) OWNER TO `+functionRole+`;
		ALTER FUNCTION public.spyglass_attest_account_cell_erasure(uuid,bytea) OWNER TO `+functionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_attest_account_cell_erasure(uuid,bytea) TO `+operatorRole); err != nil {
		t.Fatalf("create cell erasure roles: %v", err)
	}
	operator := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+operatorRole)
		return err
	})
	defer func() {
		operator.Close()
		_, _ = owner.Exec(context.Background(), `REASSIGN OWNED BY `+functionRole+` TO spyglass; DROP OWNED BY `+operatorRole+`; DROP OWNED BY `+functionRole+`; DROP ROLE IF EXISTS `+operatorRole+`; DROP ROLE IF EXISTS `+functionRole)
	}()
	var unauthorized int
	if err := operator.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_items`).Scan(&unauthorized); err == nil {
		t.Fatal("cell erasure operator directly read customer Work")
	}

	fingerprint := sha256.Sum256([]byte("keyed-account-a-fingerprint"))
	operatorEvidence := sha256.Sum256([]byte("reviewed-operator-evidence"))
	exportDigest := sha256.Sum256([]byte("export-artifact"))
	command := accounterasure.CellEraseCommand{
		RequestID: "a4000000-0000-4000-8000-000000000001", AccountID: accountA, CellID: "cell-us-east-01",
		PlacementGeneration: 3, AccountFingerprint: fingerprint[:], PolicyVersion: 1, RequestVersion: 2,
		Environment: "test", ExportSHA256: exportDigest[:], OperatorEvidenceSHA256: operatorEvidence[:], BackupExpiresAt: now.Add(35 * 24 * time.Hour),
	}
	service, _ := accounterasure.NewCellExecutionService(postgresadapter.NewAccountErasureCellRepository(operator, "cell-us-east-01"), fixedClock{now: now})
	if _, err := owner.Exec(ctx, `UPDATE spyglass.work_capacity_release_queue SET processing_state='failed',completed_at=NULL,next_attempt_at=$2 WHERE account_id=$1`, accountA, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Erase(ctx, command); !errors.Is(err, accounterasure.ErrNotEligible) {
		t.Fatalf("unfinished release erasure=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.work_capacity_release_queue SET processing_state='completed',completed_at=$2,next_attempt_at=NULL WHERE account_id=$1`, accountA, now); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.runner_invocation_queue SET processing_state='failed',job_name=NULL,completed_at=NULL,next_attempt_at=$2 WHERE account_id=$1`, accountA, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Erase(ctx, command); !errors.Is(err, accounterasure.ErrNotEligible) {
		t.Fatalf("unfinished runner erasure=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.runner_invocation_queue SET processing_state='completed',job_name='runner-erasure-a',completed_at=$2,next_attempt_at=NULL WHERE account_id=$1`, accountA, now); err != nil {
		t.Fatal(err)
	}

	tombstone, err := service.Erase(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	expectedCounts := map[string]int64{"route_context_receipts": 1, "route_context_receipt_cleanup_queue": 1, "work_capacity_release_queue": 2, "work_item_events": 2, "work_items": 2, "work_item_number_counters": 1, "account_audit_events": 1, "work_capacity_release_operator_events": 1, "runner_invocation_queue": 1, "runner_account_scheduling": 1, "runner_invocation_exchanges": 1, "runner_capability_events": 1, "account_namespaces": 1, "agent_boardrooms": 1, "agent_personas": 1, "agent_persona_versions": 1, "agent_conversations": 1, "agent_runs": 1, "agent_run_plan_turns": 1, "agent_invocations": 1, "agent_messages": 1, "agent_result_projection_queue": 1, "agent_user_messages": 1, "agent_invocation_execution_plans": 1, "agent_dispatch_queue": 1, "agent_queue_operator_events": 1}
	for name, expected := range expectedCounts {
		if tombstone.RowCounts[name] != expected {
			t.Fatalf("row count %s=%d want=%d; all=%v", name, tombstone.RowCounts[name], expected, tombstone.RowCounts)
		}
	}
	if !bytes.Equal(tombstone.AccountFingerprint, fingerprint[:]) || !bytes.Equal(tombstone.ExportSHA256, exportDigest[:]) {
		t.Fatalf("unexpected tombstone evidence: %+v", tombstone)
	}

	assertCellAccountRows(t, ctx, owner, accountA, 0)
	assertCellAccountRows(t, ctx, owner, accountB, 1)
	repeated, err := service.Erase(ctx, command)
	if err != nil || !repeated.ErasedAt.Equal(tombstone.ErasedAt) || repeated.RowCounts["work_items"] != 2 {
		t.Fatalf("idempotent erasure=%+v err=%v", repeated, err)
	}
	concurrentFingerprint := sha256.Sum256([]byte("keyed-account-c-fingerprint"))
	concurrentCommand := command
	concurrentCommand.RequestID = "c4000000-0000-4000-8000-000000000001"
	concurrentCommand.AccountID = accountC
	concurrentCommand.AccountFingerprint = concurrentFingerprint[:]
	type eraseResult struct {
		tombstone accounterasure.CellTombstone
		err       error
	}
	start := make(chan struct{})
	results := make(chan eraseResult, 2)
	for range 2 {
		go func() {
			<-start
			value, err := service.Erase(ctx, concurrentCommand)
			results <- eraseResult{tombstone: value, err: err}
		}()
	}
	close(start)
	firstConcurrent, secondConcurrent := <-results, <-results
	if firstConcurrent.err != nil || secondConcurrent.err != nil || !firstConcurrent.tombstone.ErasedAt.Equal(secondConcurrent.tombstone.ErasedAt) {
		t.Fatalf("concurrent erasure first=%+v second=%+v", firstConcurrent, secondConcurrent)
	}
	assertCellAccountRows(t, ctx, owner, accountC, 0)
	attested, err := service.Attest(ctx, "cell-us-east-01", command.RequestID, fingerprint[:])
	if err != nil || !attested.ErasedAt.Equal(tombstone.ErasedAt) {
		t.Fatalf("cell attestation=%+v err=%v", attested, err)
	}
	wrongFingerprint := sha256.Sum256([]byte("different-account"))
	if _, err := service.Attest(ctx, "cell-us-east-01", command.RequestID, wrongFingerprint[:]); !errors.Is(err, accounterasure.ErrStateConflict) {
		t.Fatalf("mismatched attestation=%v", err)
	}
	missing := command
	missing.RequestID = "a4000000-0000-4000-8000-000000000002"
	missing.AccountFingerprint = wrongFingerprint[:]
	if _, err := service.Erase(ctx, missing); !errors.Is(err, accounterasure.ErrNotFound) {
		t.Fatalf("missing namespace without tombstone=%v", err)
	}

	var serialized string
	if err := owner.QueryRow(ctx, `SELECT to_jsonb(t)::text FROM spyglass.account_erasure_tombstones t WHERE request_id=$1`, command.RequestID).Scan(&serialized); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{string(accountA), "a2000000-0000-4000-8000-000000000001", "owner@example.com", "reviewed-operator-evidence"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("cell tombstone leaked %q: %s", forbidden, serialized)
		}
	}
	if _, err := owner.Exec(ctx, `DELETE FROM spyglass.work_capacity_release_operator_events WHERE account_id=$1`, accountB); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("ordinary operator audit deletion=%v", err)
	}
}

func seedCellErasureAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID ids.AccountID, rootID, childID string, now time.Time, frozen bool) {
	t.Helper()
	state := "active"
	if frozen {
		state = "frozen"
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,3,$2,$3)`, accountID, state, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	invocationID := strings.Replace(rootID, "000000000001", "000000000051", 1)
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.runner_account_scheduling(account_id,weight,concurrency_limit,active_count,updated_at) VALUES ($1,1,1,0,$2);
		INSERT INTO spyglass.runner_invocation_queue(invocation_id,account_id,profile,processing_state,attempt_count,job_name,queued_at,launched_at,completed_at)
		VALUES ($3,$1,'agent-small','completed',1,$4,$2,$2,$2);
		SELECT set_config('app.account_id',$1::text,true);
		INSERT INTO spyglass.runner_invocation_exchanges(account_id,invocation_id,request_ciphertext,request_nonce,request_key_version,request_digest,request_expires_at,bound_pod_uid,bound_at,last_fetched_at,fetch_count,created_at)
		VALUES ($1,$3,decode(repeat('aa',17),'hex'),decode(repeat('bb',12),'hex'),1,decode(repeat('cc',32),'hex'),$2::timestamptz+interval '1 hour',replace($3::text,'51','61')::uuid,$2::timestamptz,$2::timestamptz,1,$2::timestamptz);
		INSERT INTO spyglass.runner_capability_events(account_id,event_id,invocation_id,pod_uid,operation_id,capability,effect,decision,error_code,occurred_at)
		VALUES ($1,replace($3::text,'51','71')::uuid,$3,replace($3::text,'51','61')::uuid,replace($3::text,'51','81')::uuid,'work:read','read_only','succeeded',NULL,$2::timestamptz);
		INSERT INTO spyglass.runner_action_authorizations(account_id,operation_id,invocation_id,approval_id,capability,input_sha256,hash_version,evidence_sha256,proposer_kind,proposer_id,approved_by_user_id,policy_version,state,approved_at,expires_at)
		VALUES ($1,replace($3::text,'51','81')::uuid,$3,replace($3::text,'51','91')::uuid,'email.send',decode(repeat('dd',32),'hex'),1,decode(repeat('ee',32),'hex'),'workload','runner-test',replace($3::text,'51','92')::uuid,1,'approved',$2::timestamptz,$2::timestamptz+interval '30 minutes');
		INSERT INTO spyglass.runner_action_ledger(account_id,operation_id,invocation_id,capability,input_sha256,idempotency_key,state,current_attempt_id,attempt_count,started_at,updated_at,completed_at)
		VALUES ($1,replace($3::text,'51','81')::uuid,$3,'email.send',decode(repeat('dd',32),'hex'),replace($3::text,'51','81')::uuid,'succeeded',replace($3::text,'51','93')::uuid,1,$2,$2,$2);
		INSERT INTO spyglass.runner_action_attempts(account_id,operation_id,attempt_id,mode,outcome,started_at,lease_expires_at,completed_at)
		VALUES ($1,replace($3::text,'51','81')::uuid,replace($3::text,'51','93')::uuid,'execute','succeeded',$2,$2::timestamptz+interval '2 minutes',$2)`, pgx.QueryExecModeSimpleProtocol, accountID, now, invocationID, "runner-erasure-"+rootID); err != nil {
		t.Fatal(err)
	}
	boardroomID := strings.Replace(rootID, "000000000001", "000000000101", 1)
	personaID := strings.Replace(rootID, "000000000001", "000000000102", 1)
	personaVersionID := strings.Replace(rootID, "000000000001", "000000000103", 1)
	conversationID := strings.Replace(rootID, "000000000001", "000000000104", 1)
	runID := strings.Replace(rootID, "000000000001", "000000000105", 1)
	messageID := strings.Replace(rootID, "000000000001", "000000000106", 1)
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.agent_boardrooms(account_id,id,name,purpose,state,version,created_at,updated_at)
		VALUES ($1,$3,'Erasure room','Verify exact erasure','active',1,$2,$2);
		INSERT INTO spyglass.agent_personas(account_id,id,boardroom_id,state,latest_version,created_at,updated_at)
		VALUES ($1,$4,$3,'active',1,$2,$2);
		INSERT INTO spyglass.agent_persona_versions(account_id,id,persona_id,version,name,role,description,system_instructions,policy,content_digest,created_by,created_at)
		VALUES ($1,$5,$4,1,'Erasure Agent','Operations','Erasure fixture','Review durable erasure behavior and report exact evidence.','{}',decode(repeat('11',32),'hex'),replace($3::text,'01','11')::uuid,$2);
		INSERT INTO spyglass.agent_conversations(account_id,id,boardroom_id,subject,state,next_message_sequence,created_by,created_at,updated_at)
		VALUES ($1,$6,$3,'Erasure conversation','open',2,replace($3::text,'01','11')::uuid,$2,$2);
		INSERT INTO spyglass.agent_runs(account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at,started_at,completed_at)
		VALUES ($1,$7,$3,$6,'succeeded',1,1,decode(repeat('22',32),'hex'),1,replace($3::text,'01','11')::uuid,$2,$2,$2);
		INSERT INTO spyglass.agent_run_plan_turns(account_id,run_id,turn,persona_id,persona_version_id,persona_digest)
		VALUES ($1,$7,1,$4,$5,decode(repeat('11',32),'hex'));
		INSERT INTO spyglass.agent_invocations(account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,response_model,provider_response_id,
			runner_result_digest,result_digest,result_payload,input_tokens,output_tokens,total_tokens,queued_at,started_at,completed_at)
		VALUES ($1,$9,$7,1,$5,'succeeded','openai','gpt-test','gpt-test','resp-erasure',decode(repeat('33',32),'hex'),decode(repeat('44',32),'hex'),
			'{"contribution":"Erasure result"}',1,1,2,$2,$2,$2);
		INSERT INTO spyglass.agent_messages(account_id,id,conversation_id,run_id,invocation_id,sequence,role,persona_version_id,body,structured_result,result_digest,created_at)
		VALUES ($1,$8,$6,$7,$9,1,'persona',$5,'Erasure result','{"contribution":"Erasure result"}',decode(repeat('44',32),'hex'),$2);
		INSERT INTO spyglass.agent_user_messages(account_id,id,conversation_id,sequence,body,created_by,created_at)
		VALUES ($1,replace($8::text,'106','107')::uuid,$6,2,'Erasure request',replace($3::text,'01','11')::uuid,$2);
		INSERT INTO spyglass.agent_invocation_execution_plans(account_id,invocation_id,conversation_id,context_sequence,profile,model_operation_ids,tool_operation_ids,request_expires_at,created_at)
		VALUES ($1,$9,$6,2,'agent-small',ARRAY[replace($9::text,'051','052')::uuid],ARRAY[]::uuid[],$2::timestamptz+interval '1 hour',$2)`,
		pgx.QueryExecModeSimpleProtocol, accountID, now, boardroomID, personaID, personaVersionID, conversationID, runID, messageID, invocationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.work_item_number_counters(account_id,next_number) VALUES ($1,3)`, accountID); err != nil {
		t.Fatal(err)
	}
	for index, item := range []struct {
		id, parent string
		depth      int
	}{{rootID, "", 0}, {childID, rootID, 1}} {
		reservation := strings.Replace(item.id, "a2", "a5", 1)
		reservation = strings.Replace(reservation, "a3", "a6", 1)
		reservation = strings.Replace(reservation, "b2", "b5", 1)
		reservation = strings.Replace(reservation, "b3", "b6", 1)
		if _, err := pool.Exec(ctx, `INSERT INTO spyglass.work_items(account_id,id,number,parent_id,depth,kind,title,description,state,priority,responsibility,source,created_by_actor_kind,created_by_actor_id,completed_at,capacity_reservation_id,capacity_released_at,version,created_at,updated_at) VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,'todo','Erasure test item','content','done','normal','shared','manual','user','test-actor',$6,$7,$6,1,$6,$6)`, accountID, item.id, index+1, item.parent, item.depth, now, reservation); err != nil {
			t.Fatal(err)
		}
		eventID := strings.Replace(item.id, "000000000001", "000000000011", 1)
		if _, err := pool.Exec(ctx, `INSERT INTO spyglass.work_item_events(account_id,id,work_item_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at) VALUES ($1,$2,$3,'created',0,1,'user','test-actor','created','test-correlation','{}',$4)`, accountID, eventID, item.id, now); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO spyglass.work_capacity_release_queue(account_id,work_item_id,reservation_id,processing_state,attempt_count,queued_at,completed_at) VALUES ($1,$2,$3,'completed',1,$4,$4)`, accountID, item.id, reservation, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.account_audit_events(account_id,id,event_type,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at) VALUES ($1,$2,'test','user','test-actor','test-correlation','{}',$3)`, accountID, strings.Replace(rootID, "000000000001", "000000000021", 1), now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.route_context_receipts(account_id,request_id,placement_generation,entitlement_version,actor_kind,actor_id,method,target_sha256,body_sha256,issued_at,expires_at,consumed_at) VALUES ($1,$2,3,1,'user','test-actor','GET',$3,$3,$4,$5,$4)`, accountID, strings.Replace(rootID, "000000000001", "000000000031", 1), bytes.Repeat([]byte{1}, 32), now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	reservation := strings.Replace(rootID, "a2", "a5", 1)
	reservation = strings.Replace(reservation, "b2", "b5", 1)
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.work_capacity_release_operator_events(batch_id,event_sequence,action,account_id,work_item_id,reservation_id,actor,reason,environment,previous_attempt_count,created_at) VALUES ($1,0,'requeued',$2,$3,$4,'operator@example.com','requeued after review','test',1,$5)`, strings.Replace(rootID, "000000000001", "000000000041", 1), accountID, rootID, reservation, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.agent_queue_operator_events(batch_id,event_sequence,action,queue_name,account_id,invocation_id,actor,reason,environment,previous_attempt_count,created_at) VALUES ($1,0,'inspected','dispatch',$2,$3,'operator@example.com','inspected after failure','test',1,$4)`, strings.Replace(rootID, "000000000001", "000000000042", 1), accountID, invocationID, now); err != nil {
		t.Fatal(err)
	}
}

func assertCellAccountRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID ids.AccountID, expected int) {
	t.Helper()
	for _, table := range []string{"account_namespaces", "account_audit_events", "work_item_number_counters", "work_items", "work_item_events", "route_context_receipts", "work_capacity_release_queue", "route_context_receipt_cleanup_queue", "work_capacity_release_operator_events", "runner_account_scheduling", "runner_invocation_queue", "runner_invocation_exchanges", "runner_capability_events", "runner_action_authorizations", "runner_action_ledger", "runner_action_attempts", "agent_boardrooms", "agent_personas", "agent_persona_versions", "agent_conversations", "agent_runs", "agent_run_plan_turns", "agent_invocations", "agent_messages", "agent_result_projection_queue", "agent_user_messages", "agent_invocation_execution_plans", "agent_dispatch_queue", "agent_queue_operator_events"} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.`+table+` WHERE account_id=$1`, accountID).Scan(&count); err != nil || (expected == 0 && count != 0) || (expected == 1 && count == 0) {
			t.Fatalf("table %s Account %s rows=%d expected-presence=%d err=%v", table, accountID, count, expected, err)
		}
	}
}
