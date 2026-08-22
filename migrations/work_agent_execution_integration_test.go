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
	if runs != 1 || dispatches != 1 || linkedExecutions != 1 || modelTargets != 2 || modelOperations != 4 || !slices.Equal(permittedModels, []string{"gpt-5", "gpt-4.1"}) {
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
}
