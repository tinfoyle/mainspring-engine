package migrations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/agentdispatch"
	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type staticIDGenerator string

func (generator staticIDGenerator) New() string { return string(generator) }

func TestAgentServingCreatesImmutablePlanAndEncryptedDispatch(t *testing.T) {
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
	accountID := ids.AccountID("14000000-0000-4000-8000-000000000001")
	otherAccountID := ids.AccountID("15000000-0000-4000-8000-000000000002")
	userID := ids.UserID("24000000-0000-4000-8000-000000000001")
	boardroomID := ids.BoardroomID("34000000-0000-4000-8000-000000000001")
	personaID := ids.PersonaID("44000000-0000-4000-8000-000000000001")
	versionID := ids.PersonaVersionID("54000000-0000-4000-8000-000000000001")
	runID := ids.RunID("64000000-0000-4000-8000-000000000001")
	conversationID := ids.ConversationID("74000000-0000-4000-8000-000000000001")
	messageID := ids.MessageID("84000000-0000-4000-8000-000000000001")
	workItemID := ids.WorkItemID("86000000-0000-4000-8000-000000000001")
	leaseID := "94000000-0000-4000-8000-000000000001"
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',$3),($2,1,'active',$3)`, accountID, otherAccountID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.work_items
		(account_id,id,number,depth,kind,title,description,state,priority,responsibility,source,created_by_actor_kind,created_by_actor_id,capacity_reservation_id,version,created_at,updated_at)
		VALUES ($1,$2,1,0,'ticket','Inspect reef dependency','Frozen attachment body','open','high','shared','manual','user',$3,$4,3,$5,$5)`,
		accountID, workItemID, userID, "87000000-0000-4000-8000-000000000001", now); err != nil {
		t.Fatal(err)
	}

	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewAgentRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	boardroom, err := agentdomain.NewBoardroom(boardroomID, accountID, "Operations Boardroom", "Coordinate operational work", now)
	if err != nil {
		t.Fatal(err)
	}
	if stored, created, err := repository.CreateBoardroom(ctx, boardroom); err != nil || !created || stored != boardroom {
		t.Fatalf("create boardroom stored=%+v created=%v err=%v", stored, created, err)
	}
	delayedBoardroom, err := agentdomain.NewBoardroom(boardroomID, accountID, "Operations Boardroom", "Coordinate operational work", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if stored, created, err := repository.CreateBoardroom(ctx, delayedBoardroom); err != nil || created || stored.CreatedAt != now {
		t.Fatalf("delayed boardroom retry stored=%+v created=%v err=%v", stored, created, err)
	}
	policy := agentdomain.PersonaPolicy{
		Provider: "openai", Model: "gpt-5", ReasoningEffort: "medium",
		MaximumInputTokens: 128000, MaximumOutputTokens: 4096, MaximumCostMicros: 500000,
		MaximumToolSteps: 1, CitationPolicy: "best_effort", ActionPolicy: "propose",
		Tools:        []agentdomain.ToolGrant{{Name: "read_work_summary", Capability: "work.summary.read", Description: "Read a bounded Work summary", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}},
		OutputSchema: agentdomain.ResultSchema(),
	}
	version, err := agentdomain.NewPersonaVersion(agentdomain.PersonaVersionDraft{
		ID: versionID, PersonaID: personaID, AccountID: accountID, Version: 1,
		Name: "Operations Lead", Role: "Operations", Description: "Coordinates active operational work.",
		SystemInstructions: "Review the supplied evidence and provide a concise operational recommendation.",
		Policy:             policy, CreatedBy: userID, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := repository.PublishPersona(ctx, boardroomID, version, 0); err != nil || !created {
		t.Fatalf("publish persona created=%v err=%v", created, err)
	}
	delayedVersionDraft := version.PersonaVersionDraft
	delayedVersionDraft.CreatedAt = now.Add(time.Minute)
	delayedVersion, err := agentdomain.NewPersonaVersion(delayedVersionDraft)
	if err != nil {
		t.Fatal(err)
	}
	if stored, created, err := repository.PublishPersona(ctx, boardroomID, delayedVersion, 0); err != nil || created || stored.Published.CreatedAt != now {
		t.Fatalf("delayed persona retry stored=%+v created=%v err=%v", stored, created, err)
	}
	conflictingVersionDraft := delayedVersionDraft
	conflictingVersionDraft.Description = "Different content under the same operation identity."
	conflictingVersion, err := agentdomain.NewPersonaVersion(conflictingVersionDraft)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PublishPersona(ctx, boardroomID, conflictingVersion, 0); !errors.Is(err, agentapp.ErrConflict) {
		t.Fatalf("persona identity/content conflict=%v", err)
	}
	prompt := "Analyze the private reef backlog and identify the highest-risk dependency."
	run, created, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID,
		RunID: runID, ConversationID: conversationID, CreateConversation: true, UserMessageID: messageID,
		Subject: "Reef backlog", Prompt: prompt, PersonaIDs: []ids.PersonaID{personaID}, Context: agentapp.ContextSelection{WorkItemIDs: []ids.WorkItemID{workItemID}}, EntitlementVersion: 7,
		MaximumConcurrentRun: 1, CreatedAt: now, RequestExpiresAt: now.Add(time.Hour),
	})
	if err != nil || !created || len(run.InvocationIDs) != 1 {
		t.Fatalf("start run=%+v created=%v err=%v", run, created, err)
	}
	if repeated, repeatedCreated, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID,
		RunID: runID, ConversationID: conversationID, CreateConversation: true, UserMessageID: messageID,
		Subject: "Reef backlog", Prompt: prompt, PersonaIDs: []ids.PersonaID{personaID}, Context: agentapp.ContextSelection{WorkItemIDs: []ids.WorkItemID{workItemID}}, EntitlementVersion: 7,
		MaximumConcurrentRun: 1, CreatedAt: now, RequestExpiresAt: now.Add(time.Hour),
	}); err != nil || repeatedCreated || repeated.Plan.Digest != run.Plan.Digest {
		t.Fatalf("idempotent run=%+v created=%v err=%v", repeated, repeatedCreated, err)
	}
	conversationPage, err := repository.ListConversations(ctx, accountID, boardroomID, agentapp.ConversationListQuery{Limit: 1})
	if err != nil || len(conversationPage.Items) != 1 || conversationPage.Items[0].ID != conversationID || conversationPage.Items[0].MessageCount != 1 || conversationPage.NextCursor != nil {
		t.Fatalf("conversation page=%+v err=%v", conversationPage, err)
	}
	conversation, err := repository.GetConversation(ctx, accountID, conversationID)
	if err != nil || conversation.Subject != "Reef backlog" || conversation.CreatedBy != userID || conversation.MessageCount != 1 {
		t.Fatalf("conversation=%+v err=%v", conversation, err)
	}
	messagePage, err := repository.ListMessages(ctx, accountID, conversationID, agentapp.MessageListQuery{Limit: 1})
	if err != nil || len(messagePage.Items) != 1 || messagePage.Items[0].ID != messageID || messagePage.Items[0].Role != agentapp.MessageRoleUser || messagePage.Items[0].Body != prompt || messagePage.Items[0].CreatedBy != userID || messagePage.Items[0].Result != nil || messagePage.NextAfterSequence != nil {
		t.Fatalf("message page=%+v err=%v", messagePage, err)
	}
	if _, err := repository.GetConversation(ctx, otherAccountID, conversationID); !errors.Is(err, agentapp.ErrNotFound) {
		t.Fatalf("cross-Account conversation lookup=%v", err)
	}
	if _, err := repository.ListMessages(ctx, otherAccountID, conversationID, agentapp.MessageListQuery{Limit: 10}); !errors.Is(err, agentapp.ErrNotFound) {
		t.Fatalf("cross-Account message lookup=%v", err)
	}
	if _, _, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID,
		RunID: ids.RunID("65000000-0000-4000-8000-000000000002"), ConversationID: ids.ConversationID("75000000-0000-4000-8000-000000000002"), CreateConversation: true,
		UserMessageID: ids.MessageID("85000000-0000-4000-8000-000000000002"), Subject: "Second run", Prompt: "This run must be capacity limited.",
		PersonaIDs: []ids.PersonaID{personaID}, EntitlementVersion: 7, MaximumConcurrentRun: 1, CreatedAt: now, RequestExpiresAt: now.Add(time.Hour),
	}); err == nil {
		t.Fatal("concurrent run limit was not enforced")
	} else {
		var limit *agentapp.ConcurrentRunLimitError
		if !errors.As(err, &limit) || limit.Current != 1 || limit.Maximum != 1 {
			t.Fatalf("concurrent run error=%v", err)
		}
	}

	dispatcherRole := "spyglass_agent_dispatcher_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+dispatcherRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public,spyglass TO `+dispatcherRole+`;
		GRANT SELECT ON spyglass.agent_invocation_execution_plans,spyglass.agent_invocations,spyglass.agent_runs,
			spyglass.agent_persona_versions,spyglass.agent_user_messages,spyglass.agent_messages TO `+dispatcherRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_dispatch(uuid,timestamptz,integer) TO `+dispatcherRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_complete_agent_dispatch(uuid,uuid,uuid,bytea,timestamptz) TO `+dispatcherRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_fail_agent_dispatch(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) TO `+dispatcherRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_agent_dispatch_stats(timestamptz) TO `+dispatcherRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_provision_runner_invocation(uuid,uuid,text,timestamptz,bytea,bytea,integer,bytea,timestamptz) TO `+dispatcherRole); err != nil {
		t.Fatal(err)
	}
	dispatcher := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+dispatcherRole)
		return err
	})
	defer func() {
		dispatcher.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+dispatcherRole+`; DROP ROLE IF EXISTS `+dispatcherRole)
	}()
	var forbidden int
	if err := dispatcher.QueryRow(ctx, `SELECT count(*) FROM spyglass.agent_dispatch_queue`).Scan(&forbidden); err == nil {
		t.Fatal("dispatcher directly read the cross-Account dispatch queue")
	}
	if err := dispatcher.QueryRow(ctx, `SELECT count(*) FROM spyglass.runner_invocation_exchanges`).Scan(&forbidden); err == nil {
		t.Fatal("dispatcher directly read encrypted runner exchanges")
	}
	dispatchCell, err := database.NewCellPool(dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	dispatchRepository, err := postgresadapter.NewAgentDispatchRepository(dispatcher, dispatchCell)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := dispatchRepository.Load(ctx, agentdispatch.Claim{AccountID: accountID, InvocationID: string(run.InvocationIDs[0]), LeaseID: leaseID, Attempt: 1})
	if err != nil || snapshot.ContextItemCount != 1 || snapshot.ContextDigest == ([32]byte{}) || !bytes.Contains(snapshot.ContextPayload, []byte("Frozen attachment body")) {
		t.Fatalf("frozen context count=%d digest=%x payload=%s err=%v", snapshot.ContextItemCount, snapshot.ContextDigest, snapshot.ContextPayload, err)
	}
	brokerRepository, err := postgresadapter.NewRunnerBrokerRepository(dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := runnerbroker.NewCipher(map[int][]byte{1: bytes.Repeat([]byte{0x51}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := runnerbroker.NewProducer(brokerRepository, cipher, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := agentdispatch.New(dispatchRepository, producer, fixedClock{now: now}, staticIDGenerator(leaseID), 30*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessOne(ctx)
	if err != nil || !result.Worked || !result.Provisioned || result.DeadLetter {
		t.Fatalf("dispatch result=%+v err=%v", result, err)
	}
	if second, err := processor.ProcessOne(ctx); err != nil || second.Worked {
		t.Fatalf("second dispatch result=%+v err=%v", second, err)
	}

	var queueState string
	var dispatchDigest, exchangeDigest, ciphertext []byte
	var userMessages, executionPlans, dispatchRows, runnerRows, exchangeRows int
	if err := owner.QueryRow(ctx, `SELECT q.state,q.request_digest,x.request_digest,x.request_ciphertext,
		(SELECT count(*) FROM spyglass.agent_user_messages WHERE account_id=$1),
		(SELECT count(*) FROM spyglass.agent_invocation_execution_plans WHERE account_id=$1),
		(SELECT count(*) FROM spyglass.agent_dispatch_queue WHERE account_id=$1),
		(SELECT count(*) FROM spyglass.runner_invocation_queue WHERE account_id=$1),
		(SELECT count(*) FROM spyglass.runner_invocation_exchanges WHERE account_id=$1)
		FROM spyglass.agent_dispatch_queue q JOIN spyglass.runner_invocation_exchanges x
		ON x.account_id=q.account_id AND x.invocation_id=q.invocation_id
		WHERE q.account_id=$1 AND q.invocation_id=$2`, accountID, run.InvocationIDs[0]).Scan(
		&queueState, &dispatchDigest, &exchangeDigest, &ciphertext, &userMessages, &executionPlans, &dispatchRows, &runnerRows, &exchangeRows); err != nil {
		t.Fatal(err)
	}
	if queueState != "provisioned" || !bytes.Equal(dispatchDigest, exchangeDigest) || bytes.Contains(ciphertext, []byte("private reef")) ||
		userMessages != 1 || executionPlans != 1 || dispatchRows != 1 || runnerRows != 1 || exchangeRows != 1 {
		t.Fatalf("unsafe dispatch state=%s digest_match=%v counts=%d/%d/%d/%d/%d", queueState, bytes.Equal(dispatchDigest, exchangeDigest), userMessages, executionPlans, dispatchRows, runnerRows, exchangeRows)
	}
	if _, err := repository.GetRun(ctx, otherAccountID, runID); !errors.Is(err, agentapp.ErrNotFound) {
		t.Fatalf("cross-Account run lookup=%v", err)
	}
}
