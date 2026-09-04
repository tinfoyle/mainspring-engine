package migrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	workagent "github.com/tinfoyle/spyglass-engine/internal/application/workagentexecution"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type workExecutionAuthorizer struct {
	authorization workagent.Authorization
	err           error
}

func (authorizer workExecutionAuthorizer) Authorize(context.Context, workagent.Snapshot) (workagent.Authorization, error) {
	return authorizer.authorization, authorizer.err
}

func TestWorkAgentExecutionAtomicallyStartsLinksAndReconciles(t *testing.T) {
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
	var now time.Time
	if err := owner.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	accountID := ids.AccountID("11000000-0000-4000-8000-000000000001")
	userID := ids.UserID("21000000-0000-4000-8000-000000000002")
	boardroomID := ids.BoardroomID("31000000-0000-4000-8000-000000000003")
	personaID := ids.PersonaID("41000000-0000-4000-8000-000000000004")
	personaVersionID := ids.PersonaVersionID("51000000-0000-4000-8000-000000000005")
	workItemID := ids.WorkItemID("61000000-0000-4000-8000-000000000006")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',$2)`, accountID, now); err != nil {
		t.Fatal(err)
	}
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := postgresadapter.NewAgentRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	boardroom, err := agentdomain.NewBoardroom(boardroomID, accountID, "Operations", "Execute assigned operational Work", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := agents.CreateBoardroom(ctx, boardroom); err != nil || !created {
		t.Fatalf("create Boardroom created=%t err=%v", created, err)
	}
	persona, err := agentdomain.NewPersonaVersion(agentdomain.PersonaVersionDraft{
		ID: personaVersionID, PersonaID: personaID, AccountID: accountID, Version: 1,
		Name: "Operations analyst", Role: "Analyst", Description: "Executes assigned Work.",
		SystemInstructions: "Analyze the assigned Work and return a clear, evidence-bound result.",
		Policy: agentdomain.PersonaPolicy{Provider: "openai", Model: "gpt-5", FallbackModels: []string{"gpt-4.1"}, ReasoningEffort: "medium",
			MaximumInputTokens: 128000, MaximumOutputTokens: 4096, MaximumCostMicros: 500000, MaximumToolSteps: 1,
			CitationPolicy: "best_effort", ActionPolicy: "propose", OutputSchema: agentdomain.ResultSchema(),
			Tools: []agentdomain.ToolGrant{{Name: "read_work_summary", Capability: "work.summary.read", Description: "Read Work summary", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}}},
		CreatedBy: userID, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := agents.PublishPersona(ctx, boardroomID, persona, 0); err != nil || !created {
		t.Fatalf("publish Persona created=%t err=%v", created, err)
	}
	work, err := postgresadapter.NewWorkRepository(cell, ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	actor := workdomain.Actor{Kind: workdomain.ActorUser, ID: string(userID)}
	draft, err := workdomain.NewDraft(workdomain.Draft{ID: workItemID, AccountID: accountID, Kind: workdomain.KindTodo,
		Title: "Analyze current operations", Description: "Identify the highest-risk dependency and recommend the next action.",
		Priority: workdomain.PriorityHigh, Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared},
		Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: actor}, CapacityReservationID: string(workItemID)})
	if err != nil {
		t.Fatal(err)
	}
	item, err := work.Create(ctx, draft, workapp.Mutation{Kind: workapp.MutationCreated, Actor: actor,
		CorrelationID: "71000000-0000-4000-8000-000000000007", At: now})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := item.Assign(workdomain.AssignmentCommand{Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityPersona, PersonaID: string(personaID)},
		Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: item.Version, At: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work.Update(ctx, assigned, item.Version, workapp.Mutation{Kind: workapp.MutationAssigned, Actor: actor,
		CorrelationID: "72000000-0000-4000-8000-000000000008", At: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	workerRole := "spyglass_work_agent_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+workerRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public,spyglass TO `+workerRole+`;
		GRANT SELECT ON spyglass.agent_persona_versions TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_claim_work_agent_execution(uuid,timestamptz,integer) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_heartbeat_work_agent_execution(uuid,uuid,uuid,timestamptz,integer) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_load_work_agent_execution(uuid,uuid,uuid,uuid) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_start_link_work_agent_execution(uuid,uuid,uuid,bigint,bigint,bytea,uuid,uuid,uuid,text,text[],uuid[],uuid[],timestamptz,timestamptz) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_fail_work_agent_execution(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_work_agent_execution_stats(timestamptz) TO `+workerRole); err != nil {
		t.Fatal(err)
	}
	workerPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+workerRole)
		return err
	})
	defer func() {
		workerPool.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+workerRole+`; DROP ROLE IF EXISTS `+workerRole)
	}()
	workerCell, err := database.NewCellPool(workerPool)
	if err != nil {
		t.Fatal(err)
	}
	executions, err := postgresadapter.NewWorkAgentExecutionRepository(workerPool, workerCell)
	if err != nil {
		t.Fatal(err)
	}
	leaseID := "81000000-0000-4000-8000-000000000008"
	claim, found, err := executions.Claim(ctx, leaseID, now.Add(2*time.Second), 30*time.Second)
	if err != nil || !found {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	snapshot, err := executions.Load(ctx, claim)
	if err != nil || snapshot.Persona.ID != personaVersionID || snapshot.WorkVersion != assigned.Version {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if err := executions.Heartbeat(ctx, claim, now.Add(3*time.Second), 30*time.Second); err != nil {
		t.Fatal(err)
	}
	command := workagent.StartLinkCommand{Claim: claim, Snapshot: snapshot,
		Authorization: workagent.Authorization{EntitlementVersion: 7, MaximumConcurrentRun: 2}, At: now.Add(4 * time.Second)}
	started, err := executions.StartLink(ctx, command)
	if err != nil || !started.CreatedRun || !started.LinkedRun || started.Reconciled {
		t.Fatalf("start/link=%+v err=%v", started, err)
	}
	// A transport failure after COMMIT replays the deterministic command. The
	// linked pair is returned as success without creating a second Run.
	replayed, err := executions.StartLink(ctx, command)
	if err != nil || replayed.CreatedRun || !replayed.LinkedRun || !replayed.Reconciled {
		t.Fatalf("reconciled start/link=%+v err=%v", replayed, err)
	}
	linked, err := work.Get(ctx, accountID, workItemID)
	if err != nil || linked.State != workdomain.StateInProgress || linked.Provenance.RunID != string(snapshot.RunID) || linked.Provenance.ConversationID != string(snapshot.ConversationID) || linked.Version != assigned.Version+1 {
		t.Fatalf("linked Work=%+v err=%v", linked, err)
	}
	var runs, dispatches, linkedExecutions, modelTargets, modelOperations int
	var permittedModels []string
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM spyglass.agent_runs WHERE account_id=$1),
		(SELECT count(*) FROM spyglass.agent_dispatch_queue WHERE account_id=$1),
		(SELECT count(*) FROM spyglass.work_agent_execution_queue WHERE account_id=$1 AND state='linked'),
		(SELECT cardinality(permitted_models) FROM spyglass.agent_invocations WHERE account_id=$1),
		(SELECT cardinality(model_operation_ids) FROM spyglass.agent_invocation_execution_plans WHERE account_id=$1),
		(SELECT permitted_models FROM spyglass.agent_invocations WHERE account_id=$1)`, accountID).Scan(&runs, &dispatches, &linkedExecutions, &modelTargets, &modelOperations, &permittedModels); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || dispatches != 1 || linkedExecutions != 1 || modelTargets != 3 || modelOperations != 6 || !slices.Equal(permittedModels, []string{"pending-a", "pending-b", "pending-c"}) {
		t.Fatalf("runs=%d dispatches=%d linked executions=%d models=%v", runs, dispatches, linkedExecutions, permittedModels)
	}
	if _, err := workerPool.Exec(ctx, `UPDATE spyglass.work_items SET title='forbidden'`); err == nil {
		t.Fatal("Work Agent worker directly mutated Work")
	}
	if _, err := workerPool.Exec(ctx, `SELECT count(*) FROM spyglass.agent_runs`); err == nil {
		t.Fatal("Work Agent worker directly read Agent Runs")
	}
	if _, err := workerPool.Exec(ctx, `SELECT count(*) FROM spyglass.work_agent_executions`); err == nil {
		t.Fatal("Work Agent worker directly read execution intents")
	}
	if _, err := workerPool.Exec(ctx, `SELECT count(*) FROM spyglass.work_agent_execution_queue`); err == nil {
		t.Fatal("Work Agent worker directly read the cross-Account execution queue")
	}

	// Reassignment owns lease release in the same Work transaction. A worker
	// holding the old lease cannot start the now-stale Persona intent.
	secondWorkID := ids.WorkItemID("62000000-0000-4000-8000-000000000006")
	secondDraft, err := workdomain.NewDraft(workdomain.Draft{ID: secondWorkID, AccountID: accountID, Kind: workdomain.KindTodo,
		Title: "Reassign before execution", Priority: workdomain.PriorityNormal,
		Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared},
		Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: actor}, CapacityReservationID: string(secondWorkID)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := work.Create(ctx, secondDraft, workapp.Mutation{Kind: workapp.MutationCreated, Actor: actor,
		CorrelationID: "73000000-0000-4000-8000-000000000008", At: now.Add(5 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	secondPersona, err := second.Assign(workdomain.AssignmentCommand{Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityPersona, PersonaID: string(personaID)},
		Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: second.Version, At: now.Add(6 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	secondPersona, err = work.Update(ctx, secondPersona, second.Version, workapp.Mutation{Kind: workapp.MutationAssigned, Actor: actor,
		CorrelationID: "74000000-0000-4000-8000-000000000008", At: now.Add(6 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	staleClaim, found, err := executions.Claim(ctx, "82000000-0000-4000-8000-000000000009", now.Add(7*time.Second), 30*time.Second)
	if err != nil || !found || staleClaim.WorkItemID != secondWorkID {
		t.Fatalf("stale claim=%+v found=%t err=%v", staleClaim, found, err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.account_namespaces SET state='moving' WHERE account_id=$1`, accountID); err == nil || !strings.Contains(err.Error(), "unfinished Work Agent execution") {
		t.Fatalf("Account movement with active Work execution=%v", err)
	}
	secondShared, err := secondPersona.Assign(workdomain.AssignmentCommand{Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared},
		Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: secondPersona.Version, At: now.Add(8 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work.Update(ctx, secondShared, secondPersona.Version, workapp.Mutation{Kind: workapp.MutationAssigned, Actor: actor,
		CorrelationID: "75000000-0000-4000-8000-000000000008", At: now.Add(8 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := executions.Heartbeat(ctx, staleClaim, now.Add(9*time.Second), 30*time.Second); !errors.Is(err, workagent.ErrLeaseLost) {
		t.Fatalf("stale heartbeat=%v", err)
	}
	if _, found, err := executions.Claim(ctx, "83000000-0000-4000-8000-000000000009", now.Add(10*time.Second), 30*time.Second); err != nil || found {
		t.Fatalf("unexpected post-reassignment claim found=%t err=%v", found, err)
	}
	if _, _, err := executions.Claim(ctx, "84000000-0000-4000-8000-000000000009", now.Add(11*time.Second), 30*time.Second); err != nil {
		t.Fatal(err)
	}

	// A completed Work Agent turn with questions atomically publishes its
	// message, opens Attention requests and pauses Work. Supplying an accepted
	// fact resumes Work through a new deterministic execution carrying the
	// answer as bounded Account data.
	var invocationID string
	if err := owner.QueryRow(ctx, `SELECT id FROM spyglass.agent_invocations WHERE account_id=$1 AND run_id=$2`, accountID, snapshot.RunID).Scan(&invocationID); err != nil {
		t.Fatal(err)
	}
	// Agent-dispatch token admission replaces provider-neutral placeholders
	// before a runner can produce a result. This fixture begins at projection,
	// so model that private admission mutation directly.
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_invocations SET expected_provider='openai',requested_model='gpt-5',permitted_models=ARRAY['gpt-5','gpt-4.1']::text[]
		WHERE account_id=$1 AND id=$2`, accountID, invocationID); err != nil {
		t.Fatal(err)
	}
	runnerDigest := make([]byte, 32)
	resultDigest := make([]byte, 32)
	for index := range runnerDigest {
		runnerDigest[index], resultDigest[index] = 0x31, 0x41
	}
	projectionAt := now.Add(12 * time.Second)
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.runner_invocation_queue
		(invocation_id,account_id,profile,processing_state,job_name,queued_at,completed_at)
		VALUES ($1,$2,'agent-small','completed','owner-question-runner',$3,$4);
		INSERT INTO spyglass.runner_invocation_exchanges
		(account_id,invocation_id,request_ciphertext,request_nonce,request_key_version,request_digest,request_expires_at,
		 bound_pod_uid,bound_at,last_fetched_at,fetch_count,result_outcome,result_ciphertext,result_nonce,result_key_version,result_digest,result_submitted_at,created_at)
		VALUES ($2,$1,decode(repeat('01',17),'hex'),decode(repeat('02',12),'hex'),1,decode(repeat('03',32),'hex'),$4::timestamptz+interval '1 hour',
		 '99000000-0000-4000-8000-000000000009',$3,$3,1,'completed',decode(repeat('04',17),'hex'),decode(repeat('05',12),'hex'),1,$5,$4,$3)`,
		pgx.QueryExecModeSimpleProtocol, invocationID, accountID, now.Add(4*time.Second), projectionAt, runnerDigest); err != nil {
		t.Fatal(err)
	}
	projectionLease := "85000000-0000-4000-8000-000000000009"
	var claimedInvocation, claimedWork string
	if err := owner.QueryRow(ctx, `SELECT invocation_id,work_item_id FROM public.spyglass_claim_agent_result_projection_v4($1,$2,300)`, projectionLease, projectionAt).Scan(&claimedInvocation, &claimedWork); err != nil || claimedInvocation != invocationID || claimedWork != string(workItemID) {
		t.Fatalf("owner-question claim invocation=%s work=%s err=%v", claimedInvocation, claimedWork, err)
	}
	resultPayload := json.RawMessage(`{"contribution":"I need one owner fact before continuing.","findings":[],"recommendations":[],"questions":["Which system is the operational source of truth?"],"citations":[],"proposed_actions":[],"delegations":[],"confidence":"low"}`)
	requestID := "86000000-0000-4000-8000-000000000009"
	requestEventID := "87000000-0000-4000-8000-000000000009"
	workEventID := "88000000-0000-4000-8000-000000000009"
	requestPayload, err := json.Marshal([]map[string]any{{"request_id": requestID, "event_id": requestEventID,
		"fact_key": "agent.owner_question.30408080bd1a993a6db4ecc104a77b56", "question": "Which system is the operational source of truth?",
		"requester_id": "agent:" + string(personaID)}})
	if err != nil {
		t.Fatal(err)
	}
	var projected bool
	if err := owner.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success_v3(
		$1,$2,$3,$4,'openai','gpt-5','gpt-5-2026','owner-question-response',$5,$6,$7::jsonb,$8,10,5,15,20,$9,$9,'[]'::jsonb,$10::jsonb,$11)`,
		accountID, invocationID, projectionLease, "89000000-0000-4000-8000-000000000009", runnerDigest, resultDigest, resultPayload,
		"I need one owner fact before continuing.", projectionAt, requestPayload, workEventID).Scan(&projected); err != nil || !projected {
		t.Fatalf("owner-question projection projected=%v err=%v", projected, err)
	}
	var workState, runState, requestState string
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT state FROM spyglass.work_items WHERE account_id=$1 AND id=$2),
		(SELECT state FROM spyglass.agent_runs WHERE account_id=$1 AND id=$3),
		(SELECT state FROM spyglass.attention_information_requests WHERE account_id=$1 AND id=$4)`,
		accountID, workItemID, snapshot.RunID, requestID).Scan(&workState, &runState, &requestState); err != nil || workState != "waiting" || runState != "waiting_input" || requestState != "open" {
		t.Fatalf("owner-question states work=%s run=%s request=%s err=%v", workState, runState, requestState, err)
	}

	factID := "8a000000-0000-4000-8000-000000000009"
	claimID := "8b000000-0000-4000-8000-000000000009"
	evidenceID := "8c000000-0000-4000-8000-000000000009"
	resumeAt := now.Add(13 * time.Second)
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.knowledge_evidence
		(account_id,id,source_kind,source_reference,source_revision,content_sha256,captured_at,created_by_kind,created_by_id,created_at)
		VALUES ($1,$8,'owner_statement',$7,'1',decode(repeat('50',32),'hex'),$5,'user',$4,$5);
		INSERT INTO spyglass.knowledge_claims
		(account_id,id,scope_kind,fact_key,canonical_value,value_sha256,hash_version,confidence,sensitivity,proposed_by_kind,proposed_by_id,
		 state,version,created_at,updated_at)
		VALUES ($1,$2,'account','agent.owner_question.30408080bd1a993a6db4ecc104a77b56',$3,decode(repeat('51',32),'hex'),1,1000,'internal',
		 'user',$4,'proposed',1,$5,$5);
		INSERT INTO spyglass.knowledge_claim_citations(account_id,claim_id,evidence_id,evidence_kind,relation,locator,created_at)
		VALUES ($1,$2,$8,'owner_statement','supports','owner answer',$5);
		UPDATE spyglass.knowledge_claims SET state='accepted',decision_reason='Owner supplied the requested fact',decided_by_user_id=$4,
		 decided_at=$5,version=2,updated_at=$5 WHERE account_id=$1 AND id=$2;
		INSERT INTO spyglass.knowledge_facts
		(account_id,id,scope_kind,fact_key,current_claim_id,state,revision,accepted_by_user_id,accepted_at,created_at,updated_at)
		VALUES ($1,$6,'account','agent.owner_question.30408080bd1a993a6db4ecc104a77b56',$2,'active',1,$4,$5,$5,$5);
		INSERT INTO spyglass.knowledge_fact_revisions(account_id,fact_id,revision,claim_id,accepted_by_user_id,accepted_at)
		VALUES ($1,$6,1,$2,$4,$5);
		UPDATE spyglass.attention_information_requests SET state='answered',fact_id=$6,fact_version=1,answered_by_kind='user',answered_by_id=$4,
		 answered_at=$5,version=2,updated_at=$5 WHERE account_id=$1 AND id=$7`,
		pgx.QueryExecModeSimpleProtocol, accountID, claimID, []byte(`"Operations Hub"`), userID, resumeAt, factID, requestID, evidenceID); err != nil {
		t.Fatal(err)
	}
	resumed, err := work.ResumeAttentionParents(ctx, accountID, []ids.WorkItemID{workItemID}, accounts.RoleOwner, actor, "owner-question-resume", resumeAt)
	if err != nil || len(resumed) != 1 || resumed[0].State != workdomain.StateInProgress {
		t.Fatalf("owner-question resume=%+v err=%v", resumed, err)
	}
	var continuationState, originalRunState, continuationDescription string
	if err := owner.QueryRow(ctx, `SELECT queue.state,run.state,execution.description
		FROM spyglass.work_agent_execution_queue queue
		JOIN spyglass.work_agent_executions execution ON execution.account_id=queue.account_id AND execution.execution_id=queue.execution_id
		JOIN spyglass.agent_runs run ON run.account_id=execution.account_id AND run.id=$2
		WHERE queue.account_id=$1 AND queue.work_item_id=$3 AND queue.state='pending'`, accountID, snapshot.RunID, workItemID).Scan(&continuationState, &originalRunState, &continuationDescription); err != nil || continuationState != "pending" || originalRunState != "succeeded" || continuationDescription != assigned.Description {
		t.Fatalf("continuation state=%s original=%s description=%q err=%v", continuationState, originalRunState, continuationDescription, err)
	}

	// The resumed Agent can now finish the Work without another owner question.
	// Completion must close both the Run and the Work item atomically.
	continuationClaim, found, err := executions.Claim(ctx, "8d000000-0000-4000-8000-000000000009", now.Add(14*time.Second), 30*time.Second)
	if err != nil || !found || continuationClaim.WorkItemID != workItemID {
		t.Fatalf("continuation claim=%+v found=%t err=%v", continuationClaim, found, err)
	}
	continuationSnapshot, err := executions.Load(ctx, continuationClaim)
	if err != nil {
		t.Fatal(err)
	}
	continuationStarted, err := executions.StartLink(ctx, workagent.StartLinkCommand{
		Claim: continuationClaim, Snapshot: continuationSnapshot,
		Authorization: workagent.Authorization{EntitlementVersion: 7, MaximumConcurrentRun: 2}, At: now.Add(15 * time.Second),
	})
	if err != nil || !continuationStarted.CreatedRun || !continuationStarted.LinkedRun {
		t.Fatalf("continuation start/link=%+v err=%v", continuationStarted, err)
	}
	var continuationContext []byte
	if err := owner.QueryRow(ctx, `SELECT context_payload FROM spyglass.agent_runs WHERE account_id=$1 AND id=$2`, accountID, continuationSnapshot.RunID).Scan(&continuationContext); err != nil || !strings.Contains(string(continuationContext), "Operations Hub") {
		t.Fatalf("continuation context=%q err=%v", continuationContext, err)
	}
	var continuationInvocationID string
	if err := owner.QueryRow(ctx, `SELECT id FROM spyglass.agent_invocations WHERE account_id=$1 AND run_id=$2`, accountID, continuationSnapshot.RunID).Scan(&continuationInvocationID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_invocations SET expected_provider='openai',requested_model='gpt-5',permitted_models=ARRAY['gpt-5','gpt-4.1']::text[]
		WHERE account_id=$1 AND id=$2`, accountID, continuationInvocationID); err != nil {
		t.Fatal(err)
	}
	completionRunnerDigest := make([]byte, 32)
	completionResultDigest := make([]byte, 32)
	for index := range completionRunnerDigest {
		completionRunnerDigest[index], completionResultDigest[index] = 0x61, 0x71
	}
	completionAt := now.Add(16 * time.Second)
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.runner_invocation_queue
		(invocation_id,account_id,profile,processing_state,job_name,queued_at,completed_at)
		VALUES ($1,$2,'agent-small','completed','work-completion-runner',$3,$4);
		INSERT INTO spyglass.runner_invocation_exchanges
		(account_id,invocation_id,request_ciphertext,request_nonce,request_key_version,request_digest,request_expires_at,
		 bound_pod_uid,bound_at,last_fetched_at,fetch_count,result_outcome,result_ciphertext,result_nonce,result_key_version,result_digest,result_submitted_at,created_at)
		VALUES ($2,$1,decode(repeat('61',17),'hex'),decode(repeat('62',12),'hex'),1,decode(repeat('63',32),'hex'),$4::timestamptz+interval '1 hour',
		 '91000000-0000-4000-8000-000000000009',$3,$3,1,'completed',decode(repeat('64',17),'hex'),decode(repeat('65',12),'hex'),1,$5,$4,$3)`,
		pgx.QueryExecModeSimpleProtocol, continuationInvocationID, accountID, now.Add(15*time.Second), completionAt, completionRunnerDigest); err != nil {
		t.Fatal(err)
	}
	completionLease := "92000000-0000-4000-8000-000000000009"
	if err := owner.QueryRow(ctx, `SELECT invocation_id,work_item_id FROM public.spyglass_claim_agent_result_projection_v4($1,$2,300)`, completionLease, completionAt).Scan(&claimedInvocation, &claimedWork); err != nil || claimedInvocation != continuationInvocationID || claimedWork != string(workItemID) {
		t.Fatalf("completion claim invocation=%s work=%s err=%v", claimedInvocation, claimedWork, err)
	}
	completionPayload := json.RawMessage(`{"contribution":"The operating source of truth is confirmed and the dependency analysis is complete.","findings":["Operations Hub is the source of truth."],"recommendations":["Use Operations Hub for the next dependency review."],"questions":[],"citations":[],"proposed_actions":[],"delegations":[],"confidence":"high"}`)
	if err := owner.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success_v3(
		$1,$2,$3,$4,'openai','gpt-5','gpt-5-2026','work-completion-response',$5,$6,$7::jsonb,$8,10,5,15,20,$9,$9,'[]'::jsonb,'[]'::jsonb,$10)`,
		accountID, continuationInvocationID, completionLease, "93000000-0000-4000-8000-000000000009", completionRunnerDigest, completionResultDigest, completionPayload,
		"The operating source of truth is confirmed and the dependency analysis is complete.", completionAt, "94000000-0000-4000-8000-000000000009").Scan(&projected); err != nil || !projected {
		t.Fatalf("completion projection projected=%v err=%v", projected, err)
	}
	var completedAt *time.Time
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT state FROM spyglass.work_items WHERE account_id=$1 AND id=$2),
		(SELECT completed_at FROM spyglass.work_items WHERE account_id=$1 AND id=$2),
		(SELECT state FROM spyglass.agent_runs WHERE account_id=$1 AND id=$3)`,
		accountID, workItemID, continuationSnapshot.RunID).Scan(&workState, &completedAt, &runState); err != nil || workState != "done" || completedAt == nil || runState != "succeeded" {
		t.Fatalf("completion states work=%s completed_at=%v run=%s err=%v", workState, completedAt, runState, err)
	}

	// Run capacity is ordinary backpressure: it must remain promptly retryable
	// without consuming the execution's bounded failure attempts.
	capacityWorkID := ids.WorkItemID("66000000-0000-4000-8000-000000000006")
	capacityDraft, err := workdomain.NewDraft(workdomain.Draft{ID: capacityWorkID, AccountID: accountID, Kind: workdomain.KindTodo,
		Title: "Wait for agent capacity", Priority: workdomain.PriorityNormal,
		Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared},
		Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: actor}, CapacityReservationID: string(capacityWorkID)})
	if err != nil {
		t.Fatal(err)
	}
	capacityItem, err := work.Create(ctx, capacityDraft, workapp.Mutation{Kind: workapp.MutationCreated, Actor: actor,
		CorrelationID: "95000000-0000-4000-8000-000000000009", At: now.Add(17 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	capacityPersona, err := capacityItem.Assign(workdomain.AssignmentCommand{
		Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityPersona, PersonaID: string(personaID)},
		Role:       accounts.RoleOwner, Actor: actor, ExpectedVersion: capacityItem.Version, At: now.Add(18 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	capacityPersona, err = work.Update(ctx, capacityPersona, capacityItem.Version, workapp.Mutation{Kind: workapp.MutationAssigned, Actor: actor,
		CorrelationID: "96000000-0000-4000-8000-000000000009", At: now.Add(18 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	capacityClaim, found, err := executions.Claim(ctx, "97000000-0000-4000-8000-000000000009", now.Add(19*time.Second), 30*time.Second)
	if err != nil || !found || capacityClaim.WorkItemID != capacityWorkID || capacityClaim.Attempt != 1 {
		t.Fatalf("capacity claim=%+v found=%t err=%v", capacityClaim, found, err)
	}
	capacityState, err := executions.Fail(ctx, capacityClaim, true, now.Add(35*time.Second), "run_capacity", now.Add(20*time.Second), 12)
	if err != nil || capacityState != "retry" {
		t.Fatalf("capacity failure state=%s err=%v", capacityState, err)
	}
	var capacityAttempts int
	var capacityNext time.Time
	if err := owner.QueryRow(ctx, `SELECT attempt_count,next_attempt_at FROM spyglass.work_agent_execution_queue
		WHERE account_id=$1 AND execution_id=$2 AND state='retry'`, accountID, capacityClaim.ExecutionID).Scan(&capacityAttempts, &capacityNext); err != nil || capacityAttempts != 0 || !capacityNext.Equal(now.Add(25*time.Second)) {
		t.Fatalf("capacity deferral attempts=%d next=%v err=%v", capacityAttempts, capacityNext, err)
	}
	capacityShared, err := capacityPersona.Assign(workdomain.AssignmentCommand{Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared},
		Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: capacityPersona.Version, At: now.Add(21 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work.Update(ctx, capacityShared, capacityPersona.Version, workapp.Mutation{Kind: workapp.MutationAssigned, Actor: actor,
		CorrelationID: "98000000-0000-4000-8000-000000000009", At: now.Add(21 * time.Second)}); err != nil {
		t.Fatal(err)
	}

	// A genuine runner/model failure closes the failed Run and immediately
	// creates one new immutable execution for the still Persona-owned Work.
	failureWorkID := ids.WorkItemID("67000000-0000-4000-8000-000000000006")
	failureDraft, err := workdomain.NewDraft(workdomain.Draft{ID: failureWorkID, AccountID: accountID, Kind: workdomain.KindTodo,
		Title: "Recover failed agent work", Description: "Retry this Work after a transient model failure.", Priority: workdomain.PriorityHigh,
		Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared},
		Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: actor}, CapacityReservationID: string(failureWorkID)})
	if err != nil {
		t.Fatal(err)
	}
	failureItem, err := work.Create(ctx, failureDraft, workapp.Mutation{Kind: workapp.MutationCreated, Actor: actor,
		CorrelationID: "99000000-0000-4000-8000-000000000009", At: now.Add(22 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	failurePersona, err := failureItem.Assign(workdomain.AssignmentCommand{
		Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityPersona, PersonaID: string(personaID)},
		Role:       accounts.RoleOwner, Actor: actor, ExpectedVersion: failureItem.Version, At: now.Add(23 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work.Update(ctx, failurePersona, failureItem.Version, workapp.Mutation{Kind: workapp.MutationAssigned, Actor: actor,
		CorrelationID: "9a000000-0000-4000-8000-000000000009", At: now.Add(23 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	failureClaim, found, err := executions.Claim(ctx, "9b000000-0000-4000-8000-000000000009", now.Add(24*time.Second), 30*time.Second)
	if err != nil || !found || failureClaim.WorkItemID != failureWorkID {
		t.Fatalf("failure claim=%+v found=%t err=%v", failureClaim, found, err)
	}
	failureSnapshot, err := executions.Load(ctx, failureClaim)
	if err != nil {
		t.Fatal(err)
	}
	if started, err := executions.StartLink(ctx, workagent.StartLinkCommand{Claim: failureClaim, Snapshot: failureSnapshot,
		Authorization: workagent.Authorization{EntitlementVersion: 7, MaximumConcurrentRun: 2}, At: now.Add(25 * time.Second)}); err != nil || !started.LinkedRun {
		t.Fatalf("failure start/link=%+v err=%v", started, err)
	}
	var failureInvocationID string
	if err := owner.QueryRow(ctx, `SELECT id FROM spyglass.agent_invocations WHERE account_id=$1 AND run_id=$2`, accountID, failureSnapshot.RunID).Scan(&failureInvocationID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_invocations SET expected_provider='openai',requested_model='gpt-5',permitted_models=ARRAY['gpt-5','gpt-4.1']::text[]
		WHERE account_id=$1 AND id=$2`, accountID, failureInvocationID); err != nil {
		t.Fatal(err)
	}
	failureDigest := make([]byte, 32)
	for index := range failureDigest {
		failureDigest[index] = 0x81
	}
	failureAt := now.Add(26 * time.Second)
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.runner_invocation_queue
		(invocation_id,account_id,profile,processing_state,job_name,queued_at,completed_at)
		VALUES ($1,$2,'agent-small','execution_failed','failed-work-runner',$3,$4);
		INSERT INTO spyglass.runner_invocation_exchanges
		(account_id,invocation_id,request_ciphertext,request_nonce,request_key_version,request_digest,request_expires_at,
		 bound_pod_uid,bound_at,last_fetched_at,fetch_count,result_outcome,result_ciphertext,result_nonce,result_key_version,result_digest,result_submitted_at,created_at)
		VALUES ($2,$1,decode(repeat('81',17),'hex'),decode(repeat('82',12),'hex'),1,decode(repeat('83',32),'hex'),$4::timestamptz+interval '1 hour',
		 '9c000000-0000-4000-8000-000000000009',$3,$3,1,'execution_failed',decode(repeat('84',17),'hex'),decode(repeat('85',12),'hex'),1,$5,$4,$3)`,
		pgx.QueryExecModeSimpleProtocol, failureInvocationID, accountID, now.Add(25*time.Second), failureAt, failureDigest); err != nil {
		t.Fatal(err)
	}
	failureProjectionLease := "9d000000-0000-4000-8000-000000000009"
	if err := owner.QueryRow(ctx, `SELECT invocation_id,work_item_id FROM public.spyglass_claim_agent_result_projection_v4($1,$2,300)`, failureProjectionLease, failureAt).Scan(&claimedInvocation, &claimedWork); err != nil || claimedInvocation != failureInvocationID || claimedWork != string(failureWorkID) {
		t.Fatalf("failure projection claim invocation=%s work=%s err=%v", claimedInvocation, claimedWork, err)
	}
	if err := owner.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_failure($1,$2,$3,$4,'model_step_failed',$5,$5)`,
		accountID, failureInvocationID, failureProjectionLease, failureDigest, failureAt).Scan(&projected); err != nil || !projected {
		t.Fatalf("failure projection projected=%v err=%v", projected, err)
	}
	var failureRunState string
	var retryQueueState string
	var retryExecutionCount int
	var failureCurrentRun *string
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT state FROM spyglass.work_items WHERE account_id=$1 AND id=$2),
		(SELECT run_id::text FROM spyglass.work_items WHERE account_id=$1 AND id=$2),
		(SELECT state FROM spyglass.agent_runs WHERE account_id=$1 AND id=$3),
		(SELECT state FROM spyglass.work_agent_execution_queue WHERE account_id=$1 AND work_item_id=$2 AND state='pending'),
		(SELECT count(*) FROM spyglass.work_agent_executions WHERE account_id=$1 AND work_item_id=$2)`,
		accountID, failureWorkID, failureSnapshot.RunID).Scan(&workState, &failureCurrentRun, &failureRunState, &retryQueueState, &retryExecutionCount); err != nil ||
		workState != "open" || failureCurrentRun != nil || failureRunState != "failed" || retryQueueState != "pending" || retryExecutionCount != 2 {
		t.Fatalf("failure recovery work=%s current_run=%v run=%s queue=%s executions=%d err=%v", workState, failureCurrentRun, failureRunState, retryQueueState, retryExecutionCount, err)
	}
	if err := owner.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_failure($1,$2,$3,$4,'model_step_failed',$5,$5)`,
		accountID, failureInvocationID, failureProjectionLease, failureDigest, failureAt).Scan(&projected); err != nil || projected {
		t.Fatalf("failure replay projected=%v err=%v", projected, err)
	}
}
