package migrations_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"slices"
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
	invocationA2, invocationC2 := "61000000-0000-4000-8000-000000000012", "63000000-0000-4000-8000-000000000013"
	runnerDigestA, runnerDigestB, runnerDigestC := bytes.Repeat([]byte{0x31}, 32), bytes.Repeat([]byte{0x32}, 32), bytes.Repeat([]byte{0x33}, 32)
	resultDigestA, resultDigestB := bytes.Repeat([]byte{0x41}, 32), bytes.Repeat([]byte{0x42}, 32)
	seedAgentProjectionFixture(t, ctx, owner, accountA, "31000000-0000-4000-8000-000000000001", "41000000-0000-4000-8000-000000000001", "51000000-0000-4000-8000-000000000001", invocationA, runnerDigestA, "completed", now)
	seedAgentProjectionFixture(t, ctx, owner, accountB, "32000000-0000-4000-8000-000000000002", "42000000-0000-4000-8000-000000000002", "52000000-0000-4000-8000-000000000002", invocationB, runnerDigestB, "completed", now)
	seedAgentProjectionFixture(t, ctx, owner, accountC, "33000000-0000-4000-8000-000000000003", "43000000-0000-4000-8000-000000000003", "53000000-0000-4000-8000-000000000003", invocationC, runnerDigestC, "execution_failed", now)
	seedSecondAgentTurn(t, ctx, owner, accountA, "31000000-0000-4000-8000-000000000001", "41000000-0000-4000-8000-000000000001", "51000000-0000-4000-8000-000000000001", invocationA2, now)
	seedSecondAgentTurn(t, ctx, owner, accountC, "33000000-0000-4000-8000-000000000003", "43000000-0000-4000-8000-000000000003", "53000000-0000-4000-8000-000000000003", invocationC2, now)
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_invocations SET permitted_models=ARRAY['gpt-test','gpt-fallback'] WHERE account_id=$1 AND id=$2`, accountA, invocationA); err != nil {
		t.Fatal(err)
	}
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
		GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection_v3(uuid,timestamptz,integer) TO `+projectorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success_v2(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz,jsonb) TO `+projectorRole+`;
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
	resultA := json.RawMessage(`{"contribution":"Reconcile the backlog.","findings":[],"recommendations":[],"questions":[],"citations":[],"proposed_actions":[{"kind":"work.create","reason":"Track remediation","payload":{},"evidence":[]}],"delegations":[],"confidence":"high"}`)
	messageA := "71000000-0000-4000-8000-000000000001"
	leaseA := "73000000-0000-4000-8000-000000000001"
	var claimedAccount, claimedInvocation string
	var claimedModels []string
	var policyVersion int64
	var citationPolicy, actionPolicy, currentPersona string
	var actionCapabilities, delegatePersonas, citationBindings []string
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_persona_versions SET policy='{"action_policy":"propose","action_capabilities":["work.create"]}'::jsonb WHERE account_id=$1 AND persona_id=$2`, accountA, "81000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	if err := projector.QueryRow(ctx, `SELECT account_id,invocation_id,permitted_models,result_policy_version,citation_policy,action_policy,action_capabilities,current_persona_id,delegate_persona_ids,citation_bindings FROM public.spyglass_claim_agent_result_projection_v3($1,$2,300)`, leaseA, now.Add(45*time.Second)).Scan(
		&claimedAccount, &claimedInvocation, &claimedModels, &policyVersion, &citationPolicy, &actionPolicy, &actionCapabilities, &currentPersona, &delegatePersonas, &citationBindings); err != nil ||
		claimedAccount != accountA || claimedInvocation != invocationA || !slices.Equal(claimedModels, []string{"gpt-test", "gpt-fallback"}) || policyVersion != 2 || citationPolicy != "none" || actionPolicy != "propose" || !slices.Equal(actionCapabilities, []string{"work.create"}) ||
		currentPersona != "81000000-0000-4000-8000-000000000001" || !slices.Equal(delegatePersonas, []string{"81000000-0000-4000-8000-000000000012"}) || len(citationBindings) != 0 {
		t.Fatalf("claim A account=%s invocation=%s err=%v", claimedAccount, claimedInvocation, err)
	}
	proposalA, err := json.Marshal([]map[string]any{{"approval_id": "74000000-0000-4000-8000-000000000001", "operation_id": "75000000-0000-4000-8000-000000000001", "event_id": "76000000-0000-4000-8000-000000000001", "capability": "work.create", "payload_base64": base64.StdEncoding.EncodeToString([]byte(`{}`)), "input_sha256_base64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x51}, 32)), "evidence_sha256_base64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x61}, 32)), "proposer_id": "agent:81000000-0000-4000-8000-000000000001", "policy_version": 2, "expires_at": now.Add(time.Minute + 24*time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	var created bool
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success_v2($1,$2,$3,$4,'openai','gpt-fallback','gpt-fallback-2026','resp_a',$5,$6,$7::jsonb,$8,10,4,14,21,$9,$9,$10::jsonb)`,
		accountA, invocationA, leaseA, messageA, runnerDigestA, resultDigestA, resultA, "Reconcile the backlog.", now.Add(time.Minute), proposalA).Scan(&created); err != nil || !created {
		t.Fatalf("project success created=%v err=%v", created, err)
	}
	var nextContext, nextDispatch int64
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT context_sequence FROM spyglass.agent_invocation_execution_plans WHERE account_id=$1 AND invocation_id=$2),
		(SELECT count(*) FROM spyglass.agent_dispatch_queue WHERE account_id=$1 AND invocation_id=$2)`, accountA, invocationA2).Scan(&nextContext, &nextDispatch); err != nil || nextContext != 1 || nextDispatch != 1 {
		t.Fatalf("next turn context=%d dispatch=%d err=%v", nextContext, nextDispatch, err)
	}
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success_v2($1,$2,$3,$4,'openai','gpt-fallback','gpt-fallback-2026','resp_a',$5,$6,$7::jsonb,$8,10,4,14,21,$9,$9,$10::jsonb)`,
		accountA, invocationA, leaseA, messageA, runnerDigestA, resultDigestA, resultA, "Reconcile the backlog.", now.Add(time.Minute), proposalA).Scan(&created); err != nil || created {
		t.Fatalf("idempotent success created=%v err=%v", created, err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.agent_dispatch_queue WHERE account_id=$1 AND invocation_id=$2`, accountA, invocationA2).Scan(&nextDispatch); err != nil || nextDispatch != 1 {
		t.Fatalf("idempotent next turn dispatch=%d err=%v", nextDispatch, err)
	}
	var approvals, approvalEvents int
	if err := owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM spyglass.attention_consequential_approvals WHERE account_id=$1 AND invocation_id=$2),(SELECT count(*) FROM spyglass.attention_events WHERE account_id=$1 AND consequential_approval_id='74000000-0000-4000-8000-000000000001')`, accountA, invocationA).Scan(&approvals, &approvalEvents); err != nil || approvals != 1 || approvalEvents != 1 {
		t.Fatalf("projected approvals=%d events=%d err=%v", approvals, approvalEvents, err)
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
	var projectedCost, projectedTokens int64
	var selectedModel string
	if err := owner.QueryRow(ctx, `SELECT cost_micros,total_tokens,selected_model FROM spyglass.agent_invocations WHERE account_id=$1 AND id=$2`, accountA, invocationA).Scan(&projectedCost, &projectedTokens, &selectedModel); err != nil || projectedCost != 21 || projectedTokens != 14 || selectedModel != "gpt-fallback" {
		t.Fatalf("projected cost=%d tokens=%d err=%v", projectedCost, projectedTokens, err)
	}
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success_v2($1,$2,$3,$4,'openai','gpt-test','gpt-test','resp_a',$5,$6,$7::jsonb,$8,10,4,14,21,$9,$9,'[]'::jsonb)`,
		accountB, invocationA, leaseA, messageA, runnerDigestA, resultDigestA, resultA, "Reconcile the backlog.", now.Add(time.Minute)).Scan(&created); err == nil {
		t.Fatal("cross-Account invocation projection succeeded")
	}
	leaseB := "73000000-0000-4000-8000-000000000002"
	if err := projector.QueryRow(ctx, `SELECT account_id,invocation_id FROM public.spyglass_claim_agent_result_projection_v3($1,$2,300)`, leaseB, now.Add(45*time.Second)).Scan(&claimedAccount, &claimedInvocation); err != nil || claimedAccount != accountB || claimedInvocation != invocationB {
		t.Fatalf("claim B account=%s invocation=%s err=%v", claimedAccount, claimedInvocation, err)
	}
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success_v2($1,$2,$3,$4,'openai','gpt-test','gpt-test','resp_b',$5,$6,$7::jsonb,$8,8,3,11,17,$9,$9,'[]'::jsonb)`,
		accountB, invocationB, leaseB, messageA, runnerDigestB, resultDigestB, resultA, "Reconcile the backlog.", now.Add(time.Minute)).Scan(&created); err == nil {
		t.Fatal("duplicate message identity unexpectedly committed")
	}
	leaseC := "73000000-0000-4000-8000-000000000003"
	if err := projector.QueryRow(ctx, `SELECT account_id,invocation_id FROM public.spyglass_claim_agent_result_projection_v3($1,$2,300)`, leaseC, now.Add(45*time.Second)).Scan(&claimedAccount, &claimedInvocation); err != nil || claimedAccount != accountC || claimedInvocation != invocationC {
		t.Fatalf("claim C account=%s invocation=%s err=%v", claimedAccount, claimedInvocation, err)
	}
	if err := projector.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_failure($1,$2,$3,$4,'provider_denied',$5,$5)`,
		accountC, invocationC, leaseC, runnerDigestC, now.Add(time.Minute)).Scan(&created); err != nil || !created {
		t.Fatalf("project failure created=%v err=%v", created, err)
	}
	var downstreamState, downstreamFailure, failedRunState string
	if err := owner.QueryRow(ctx, `SELECT i.status,i.failure_code,r.state FROM spyglass.agent_invocations i
		JOIN spyglass.agent_runs r ON r.account_id=i.account_id AND r.id=i.run_id
		WHERE i.account_id=$1 AND i.id=$2`, accountC, invocationC2).Scan(&downstreamState, &downstreamFailure, &failedRunState); err != nil || downstreamState != "canceled" || downstreamFailure != "prior_turn_failed" || failedRunState != "failed" {
		t.Fatalf("downstream state=%s failure=%s run=%s err=%v", downstreamState, downstreamFailure, failedRunState, err)
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

func seedSecondAgentTurn(t *testing.T, ctx context.Context, owner interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, accountID, boardroomID, conversationID, runID, invocationID string, now time.Time) {
	t.Helper()
	personaID := strings.Replace(invocationID, "6", "8", 1)
	versionID := strings.Replace(invocationID, "6", "9", 1)
	modelOperationID := strings.Replace(invocationID, "6", "7", 1)
	if _, err := owner.Exec(ctx, `
		UPDATE spyglass.agent_runs SET turn_count=2 WHERE account_id=$1 AND id=$4;
		INSERT INTO spyglass.agent_personas(account_id,id,boardroom_id,state,latest_version,created_at,updated_at)
		VALUES ($1,$5,$2,'active',1,$8,$8);
		INSERT INTO spyglass.agent_persona_versions(account_id,id,persona_id,version,name,role,description,system_instructions,policy,content_digest,created_by,created_at)
		VALUES ($1,$6,$5,1,'Synthesis Lead','Synthesis','Synthesizes prior work','Synthesize prior Persona contributions into a clear decision.','{}'::jsonb,decode(repeat('12',32),'hex'),'20000000-0000-4000-8000-000000000002',$8);
		INSERT INTO spyglass.agent_run_plan_turns(account_id,run_id,turn,persona_id,persona_version_id,persona_digest)
		VALUES ($1,$4,2,$5,$6,decode(repeat('12',32),'hex'));
		INSERT INTO spyglass.agent_invocations(account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,queued_at)
		VALUES ($1,$7,$4,2,$6,'queued','openai','gpt-test',$8);
		INSERT INTO spyglass.agent_invocation_execution_plans
		(account_id,invocation_id,conversation_id,context_sequence,profile,model_operation_ids,tool_operation_ids,request_expires_at,created_at)
		VALUES ($1,$7,$3,99,'agent-small',ARRAY[$9::uuid],ARRAY[]::uuid[],$8::timestamptz+interval '1 hour',$8)`,
		pgx.QueryExecModeSimpleProtocol, accountID, boardroomID, conversationID, runID, personaID, versionID, invocationID, now, modelOperationID); err != nil {
		t.Fatal(err)
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
