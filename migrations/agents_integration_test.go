package migrations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestAgentsProjectionIsAccountIsolatedDigestBoundAndAtomic(t *testing.T) {
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

	var now time.Time
	if err := owner.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	accountA, accountB, accountC := "11000000-0000-4000-8000-000000000001", "12000000-0000-4000-8000-000000000002", "13000000-0000-4000-8000-000000000003"
	invocationA, invocationB, invocationC := "61000000-0000-4000-8000-000000000001", "62000000-0000-4000-8000-000000000002", "63000000-0000-4000-8000-000000000003"
	runnerDigestA, runnerDigestB, runnerDigestC := bytes.Repeat([]byte{0x31}, 32), bytes.Repeat([]byte{0x32}, 32), bytes.Repeat([]byte{0x33}, 32)
	resultDigestA, resultDigestB := bytes.Repeat([]byte{0x41}, 32), bytes.Repeat([]byte{0x42}, 32)
	seedAgentProjectionFixture(t, ctx, owner, accountA, "31000000-0000-4000-8000-000000000001", "41000000-0000-4000-8000-000000000001", "51000000-0000-4000-8000-000000000001", invocationA, runnerDigestA, "completed", now)
	seedAgentProjectionFixture(t, ctx, owner, accountB, "32000000-0000-4000-8000-000000000002", "42000000-0000-4000-8000-000000000002", "52000000-0000-4000-8000-000000000002", invocationB, runnerDigestB, "completed", now)
	seedAgentProjectionFixture(t, ctx, owner, accountC, "33000000-0000-4000-8000-000000000003", "43000000-0000-4000-8000-000000000003", "53000000-0000-4000-8000-000000000003", invocationC, runnerDigestC, "execution_failed", now)
	// Seed one owner-only inconsistent message to force the projection's final
	// insert to fail. The function must roll back its preceding sequence and
	// invocation updates as one transaction.
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.agent_messages
		(account_id,id,conversation_id,run_id,invocation_id,sequence,role,persona_version_id,body,structured_result,result_digest,created_at)
		SELECT i.account_id,'72000000-0000-4000-8000-000000000002',r.conversation_id,i.run_id,i.id,1,'persona',i.persona_version_id,
		'preexisting owner fixture','{"contribution":"preexisting owner fixture"}'::jsonb,decode(repeat('77',32),'hex'),$2
		FROM spyglass.agent_invocations i JOIN spyglass.agent_runs r ON r.account_id=i.account_id AND r.id=i.run_id
		WHERE i.account_id=$1 AND i.id=$3`, accountB, now, invocationB); err != nil {
		t.Fatal(err)
	}

	projectorRole := "spyglass_agent_projector_" + randomSuffix(t)
	readerRole := "spyglass_agent_reader_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+projectorRole+` NOLOGIN NOBYPASSRLS; CREATE ROLE `+readerRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public,spyglass TO `+projectorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection(uuid,timestamptz,integer) TO `+projectorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,uuid,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,timestamptz,timestamptz) TO `+projectorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_failure(uuid,uuid,uuid,bytea,text,timestamptz,timestamptz) TO `+projectorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_fail_agent_result_projection(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) TO `+projectorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_agent_result_projection_stats(timestamptz) TO `+projectorRole+`;
		GRANT USAGE ON SCHEMA spyglass TO `+readerRole+`;
		GRANT SELECT ON spyglass.agent_invocations,spyglass.agent_messages,spyglass.agent_conversations TO `+readerRole); err != nil {
		t.Fatal(err)
	}
	projector := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+projectorRole)
		return err
	})
	reader := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+readerRole)
		return err
	})
	defer func() {
		projector.Close()
		reader.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+projectorRole+`; DROP OWNED BY `+readerRole+`; DROP ROLE IF EXISTS `+projectorRole+`; DROP ROLE IF EXISTS `+readerRole)
	}()

	var forbidden int
	if err := projector.QueryRow(ctx, `SELECT count(*) FROM spyglass.agent_invocations`).Scan(&forbidden); err == nil {
		t.Fatal("execute-only projector directly read agent invocations")
	}
	if err := projector.QueryRow(ctx, `SELECT count(*) FROM spyglass.agent_result_projection_queue`).Scan(&forbidden); err == nil {
		t.Fatal("execute-only projector directly read projection queue")
	}
	resultA := json.RawMessage(`{"contribution":"Reconcile the backlog.","findings":[],"recommendations":[],"questions":[],"citations":[],"proposed_actions":[],"delegations":[],"confidence":"high"}`)
	messageA := "71000000-0000-4000-8000-000000000001"
	leaseA := "73000000-0000-4000-8000-000000000001"
	var claimedAccount, claimedInvocation string
	if err := projector.QueryRow(ctx, `SELECT account_id,invocation_id FROM public.spyglass_claim_agent_result_projection($1,$2,300)`, leaseA, now.Add(45*time.Second)).Scan(&claimedAccount, &claimedInvocation); err != nil || claimedAccount != accountA || claimedInvocation != invocationA {
		t.Fatalf("claim A account=%s invocation=%s err=%v", claimedAccount, claimedInvocation, err)
	}
	var created bool
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success($1,$2,$3,$4,'openai','gpt-test','resp_a',$5,$6,$7::jsonb,$8,10,4,14,$9,$9)`,
		accountA, invocationA, leaseA, messageA, runnerDigestA, resultDigestA, resultA, "Reconcile the backlog.", now.Add(time.Minute)).Scan(&created); err != nil || !created {
		t.Fatalf("project success created=%v err=%v", created, err)
	}
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success($1,$2,$3,$4,'openai','gpt-test','resp_a',$5,$6,$7::jsonb,$8,10,4,14,$9,$9)`,
		accountA, invocationA, leaseA, messageA, runnerDigestA, resultDigestA, resultA, "Reconcile the backlog.", now.Add(time.Minute)).Scan(&created); err != nil || created {
		t.Fatalf("idempotent success created=%v err=%v", created, err)
	}
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewAgentRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	conversationA := ids.ConversationID("41000000-0000-4000-8000-000000000001")
	messagePage, err := repository.ListMessages(ctx, ids.AccountID(accountA), conversationA, agentapp.MessageListQuery{Limit: 10})
	if err != nil || len(messagePage.Items) != 1 || messagePage.Items[0].Role != agentapp.MessageRolePersona || messagePage.Items[0].InvocationID != ids.AgentInvocationID(invocationA) || messagePage.Items[0].Result == nil || messagePage.Items[0].Result.Confidence != agentdomain.ConfidenceHigh || messagePage.Items[0].Result.Contribution != "Reconcile the backlog." {
		t.Fatalf("projected message page=%+v err=%v", messagePage, err)
	}
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success($1,$2,$3,$4,'openai','gpt-test','resp_a',$5,$6,$7::jsonb,$8,10,4,14,$9,$9)`,
		accountB, invocationA, leaseA, messageA, runnerDigestA, resultDigestA, resultA, "Reconcile the backlog.", now.Add(time.Minute)).Scan(&created); err == nil {
		t.Fatal("cross-Account invocation projection succeeded")
	}
	leaseB := "73000000-0000-4000-8000-000000000002"
	if err := projector.QueryRow(ctx, `SELECT account_id,invocation_id FROM public.spyglass_claim_agent_result_projection($1,$2,300)`, leaseB, now.Add(45*time.Second)).Scan(&claimedAccount, &claimedInvocation); err != nil || claimedAccount != accountB || claimedInvocation != invocationB {
		t.Fatalf("claim B account=%s invocation=%s err=%v", claimedAccount, claimedInvocation, err)
	}
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success($1,$2,$3,$4,'openai','gpt-test','resp_b',$5,$6,$7::jsonb,$8,8,3,11,$9,$9)`,
		accountB, invocationB, leaseB, messageA, runnerDigestB, resultDigestB, resultA, "Reconcile the backlog.", now.Add(time.Minute)).Scan(&created); err == nil {
		t.Fatal("duplicate message identity unexpectedly committed")
	}
	leaseC := "73000000-0000-4000-8000-000000000003"
	if err := projector.QueryRow(ctx, `SELECT account_id,invocation_id FROM public.spyglass_claim_agent_result_projection($1,$2,300)`, leaseC, now.Add(45*time.Second)).Scan(&claimedAccount, &claimedInvocation); err != nil || claimedAccount != accountC || claimedInvocation != invocationC {
		t.Fatalf("claim C account=%s invocation=%s err=%v", claimedAccount, claimedInvocation, err)
	}
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_failure($1,$2,$3,$4,'provider_denied',$5,$5)`,
		accountC, invocationC, leaseC, runnerDigestC, now.Add(time.Minute)).Scan(&created); err != nil || !created {
		t.Fatalf("project failure created=%v err=%v", created, err)
	}

	if _, err := reader.Exec(ctx, `SELECT set_config('app.account_id',$1,false)`, accountA); err != nil {
		t.Fatal(err)
	}
	var status, body string
	var sequence, nextSequence int64
	if err := reader.QueryRow(ctx, `SELECT i.status,m.body,m.sequence,c.next_message_sequence FROM spyglass.agent_invocations i
		JOIN spyglass.agent_messages m ON m.account_id=i.account_id AND m.invocation_id=i.id
		JOIN spyglass.agent_conversations c ON c.account_id=m.account_id AND c.id=m.conversation_id WHERE i.id=$1`, invocationA).Scan(&status, &body, &sequence, &nextSequence); err != nil || status != "succeeded" || body != "Reconcile the backlog." || sequence != 1 || nextSequence != 2 {
		t.Fatalf("unexpected projection status=%s body=%q sequence=%d next=%d err=%v", status, body, sequence, nextSequence, err)
	}
	if _, err := reader.Exec(ctx, `SELECT set_config('app.account_id',$1,false)`, accountB); err != nil {
		t.Fatal(err)
	}
	var messages int
	if err := reader.QueryRow(ctx, `SELECT count(*) FROM spyglass.agent_messages`).Scan(&messages); err != nil || messages != 1 {
		t.Fatalf("failed atomic projection left messages=%d err=%v", messages, err)
	}
	if err := reader.QueryRow(ctx, `SELECT status FROM spyglass.agent_invocations WHERE id=$1`, invocationB).Scan(&status); err != nil || status != "queued" {
		t.Fatalf("failed atomic projection changed invocation status=%s err=%v", status, err)
	}
	if err := reader.QueryRow(ctx, `SELECT next_message_sequence FROM spyglass.agent_conversations`).Scan(&nextSequence); err != nil || nextSequence != 1 {
		t.Fatalf("failed atomic projection consumed sequence=%d err=%v", nextSequence, err)
	}

	var pruned int64
	if err := owner.QueryRow(ctx, `SELECT public.spyglass_prune_runner_terminal_payloads($1,$2,10)`, now.Add(90*time.Minute), now.Add(2*time.Hour)).Scan(&pruned); err != nil || pruned != 2 {
		t.Fatalf("retention pruned=%d err=%v", pruned, err)
	}
	var purgedA, purgedB, purgedC bool
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT terminal_payload_purged_at IS NOT NULL FROM spyglass.runner_invocation_exchanges WHERE account_id=$1 AND invocation_id=$4),
		(SELECT terminal_payload_purged_at IS NOT NULL FROM spyglass.runner_invocation_exchanges WHERE account_id=$2 AND invocation_id=$5),
		(SELECT terminal_payload_purged_at IS NOT NULL FROM spyglass.runner_invocation_exchanges WHERE account_id=$3 AND invocation_id=$6)`,
		accountA, accountB, accountC, invocationA, invocationB, invocationC).Scan(&purgedA, &purgedB, &purgedC); err != nil || !purgedA || purgedB || !purgedC {
		t.Fatalf("unexpected projection retention A=%v B=%v C=%v err=%v", purgedA, purgedB, purgedC, err)
	}
}

func seedAgentProjectionFixture(t *testing.T, ctx context.Context, owner interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, accountID, boardroomID, conversationID, runID, invocationID string, runnerDigest []byte, outcome string, now time.Time) {
	t.Helper()
	personaID := strings.Replace(boardroomID, "3", "8", 1)
	versionID := strings.Replace(boardroomID, "3", "9", 1)
	if _, err := owner.Exec(ctx, `
		INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$9);
		INSERT INTO spyglass.agent_boardrooms(account_id,id,name,purpose,state,version,created_at,updated_at) VALUES ($1,$2,'Operations','Coordinate work','active',1,$9,$9);
		INSERT INTO spyglass.agent_personas(account_id,id,boardroom_id,state,latest_version,created_at,updated_at) VALUES ($1,$3,$2,'active',1,$9,$9);
		INSERT INTO spyglass.agent_persona_versions(account_id,id,persona_id,version,name,role,description,system_instructions,policy,content_digest,created_by,created_at)
		VALUES ($1,$4,$3,1,'Operations Lead','Operations','Coordinates work','Coordinate operational work and report evidence clearly.','{}'::jsonb,decode(repeat('11',32),'hex'),'20000000-0000-4000-8000-000000000002',$9);
		INSERT INTO spyglass.agent_conversations(account_id,id,boardroom_id,subject,state,created_by,created_at,updated_at) VALUES ($1,$5,$2,'Backlog review','open','20000000-0000-4000-8000-000000000002',$9,$9);
		INSERT INTO spyglass.agent_runs(account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at)
		VALUES ($1,$6,$2,$5,'planned',1,1,decode(repeat('22',32),'hex'),1,'20000000-0000-4000-8000-000000000002',$9);
		INSERT INTO spyglass.agent_run_plan_turns(account_id,run_id,turn,persona_id,persona_version_id,persona_digest) VALUES ($1,$6,1,$3,$4,decode(repeat('11',32),'hex'));
		INSERT INTO spyglass.agent_invocations(account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,queued_at) VALUES ($1,$7,$6,1,$4,'queued','openai','gpt-test',$9);
		INSERT INTO spyglass.runner_invocation_queue(invocation_id,account_id,profile,processing_state,job_name,queued_at,completed_at) VALUES ($7,$1,'agent-small',$10,'runner-'||left($7::text,8),$9,$9::timestamptz+interval '30 seconds');
		INSERT INTO spyglass.runner_invocation_exchanges(account_id,invocation_id,request_ciphertext,request_nonce,request_key_version,request_digest,request_expires_at,
			bound_pod_uid,bound_at,last_fetched_at,fetch_count,result_outcome,result_ciphertext,result_nonce,result_key_version,result_digest,result_submitted_at,created_at)
		VALUES ($1,$7,decode(repeat('01',17),'hex'),decode(repeat('02',12),'hex'),1,decode(repeat('03',32),'hex'),$9::timestamptz+interval '1 hour',
			'99000000-0000-4000-8000-000000000009',$9,$9,1,$8,decode(repeat('04',17),'hex'),decode(repeat('05',12),'hex'),1,$11,$9::timestamptz+interval '30 seconds',$9)`,
		pgx.QueryExecModeSimpleProtocol, accountID, boardroomID, personaID, versionID, conversationID, runID, invocationID, outcome, now,
		map[string]string{"completed": "completed", "execution_failed": "execution_failed"}[outcome], runnerDigest); err != nil {
		t.Fatal(err)
	}
}
