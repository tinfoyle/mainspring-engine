package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/agentdispatch"
	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	"github.com/tinfoyle/spyglass-engine/internal/application/agentusage"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type staticIDGenerator string

func (generator staticIDGenerator) New() string { return string(generator) }

type staticAgentTokenBroker struct {
	admission agentusage.Admission
	err       error
}

func (broker staticAgentTokenBroker) ReserveAgentTokens(context.Context, agentusage.ReserveCommand) (agentusage.Admission, error) {
	return broker.admission, broker.err
}
func (staticAgentTokenBroker) CloseAgentTokens(context.Context, agentusage.CloseCommand) error {
	return nil
}

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
	documentID := ids.KnowledgeDocumentID("d1000000-0000-4000-8000-000000000001")
	documentRevisionID := ids.KnowledgeDocumentRevisionID("d2000000-0000-4000-8000-000000000002")
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
	documentContent := "Frozen document evidence for the reef dependency."
	documentContentDigest := sha256.Sum256([]byte(documentContent))
	sourceDigest := sha256.Sum256([]byte("source object"))
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.knowledge_documents
		(account_id,id,title,sensitivity,current_revision_id,current_revision,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,$2,'Reef operating evidence','confidential',NULL,NULL,'processing',1,'user',$4,$5,$5);
		INSERT INTO spyglass.knowledge_document_revisions
		(account_id,id,document_id,revision,filename,declared_media_type,verified_media_type,byte_size,content_sha256,object_key,object_version,change_summary,
		 state,scan_state,scan_engine,scan_signature,scanned_at,extraction_state,extractor,text_sha256,text_bytes,extracted_at,index_state,index_generation,chunk_count,indexed_at,
		 failure_code,created_by_kind,created_by_id,created_at,updated_at,extracted_object_key,extracted_object_version)
		VALUES ($1,$3,$2,1,'reef.txt','text/plain','text/plain',13,$6,'accounts/'||$1::text||'/documents/'||$2::text||'/revisions/'||$3::text||'/source','source-v1','Initial evidence',
		 'ready','clean','clamav-test','',$5,'ready','tika-test',$7,$8,$5,'ready','knowledge-v1',1,$5,'','user',$4,$5,$5,
		 'accounts/'||$1::text||'/documents/'||$2::text||'/revisions/'||$3::text||'/extracted/text','extracted-v1');
		INSERT INTO spyglass.knowledge_document_chunks
		(account_id,id,revision_id,chunk_index,start_byte,end_byte,content,content_sha256,token_count,index_generation,created_at)
		VALUES ($1,'d3000000-0000-4000-8000-000000000003',$3,0,0,$8,$9,$7,8,'knowledge-v1',$5);
		UPDATE spyglass.knowledge_documents SET current_revision_id=$3,current_revision=1,state='ready',version=2,updated_at=$5
		WHERE account_id=$1 AND id=$2`,
		pgx.QueryExecModeSimpleProtocol, accountID, documentID, documentRevisionID, userID, now, sourceDigest[:], documentContentDigest[:], len(documentContent), documentContent); err != nil {
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
	if _, err := owner.Exec(ctx, `UPDATE spyglass.knowledge_documents SET sensitivity='restricted',version=version+1 WHERE account_id=$1 AND id=$2`, accountID, documentID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID,
		RunID: ids.RunID("65000000-0000-4000-8000-000000000005"), ConversationID: ids.ConversationID("75000000-0000-4000-8000-000000000005"), CreateConversation: true,
		UserMessageID: ids.MessageID("85000000-0000-4000-8000-000000000005"), Subject: "Restricted evidence", Prompt: "This attachment must remain undisclosed.",
		PersonaIDs: []ids.PersonaID{personaID}, Context: agentapp.ContextSelection{KnowledgeDocumentIDs: []ids.KnowledgeDocumentID{documentID}}, EntitlementVersion: 7,
		MaximumConcurrentRun: 1, CanReadRestricted: false, CreatedAt: now, RequestExpiresAt: now.Add(time.Hour),
	}); !errors.Is(err, agentapp.ErrNotFound) {
		t.Fatalf("restricted document attachment=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.knowledge_documents SET sensitivity='confidential',version=version+1 WHERE account_id=$1 AND id=$2`, accountID, documentID); err != nil {
		t.Fatal(err)
	}
	prompt := "Analyze the private reef backlog and identify the highest-risk dependency."
	run, created, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID,
		RunID: runID, ConversationID: conversationID, CreateConversation: true, UserMessageID: messageID,
		Subject: "Reef backlog", Prompt: prompt, PersonaIDs: []ids.PersonaID{personaID}, Context: agentapp.ContextSelection{WorkItemIDs: []ids.WorkItemID{workItemID}, KnowledgeDocumentIDs: []ids.KnowledgeDocumentID{documentID}}, EntitlementVersion: 7,
		MaximumConcurrentRun: 1, CreatedAt: now, RequestExpiresAt: now.Add(time.Hour),
	})
	if err != nil || !created || len(run.InvocationIDs) != 1 {
		t.Fatalf("start run=%+v created=%v err=%v", run, created, err)
	}
	if repeated, repeatedCreated, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID,
		RunID: runID, ConversationID: conversationID, CreateConversation: true, UserMessageID: messageID,
		Subject: "Reef backlog", Prompt: prompt, PersonaIDs: []ids.PersonaID{personaID}, Context: agentapp.ContextSelection{WorkItemIDs: []ids.WorkItemID{workItemID}, KnowledgeDocumentIDs: []ids.KnowledgeDocumentID{documentID}}, EntitlementVersion: 7,
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
		GRANT EXECUTE ON FUNCTION public.spyglass_admit_agent_invocation_tokens(uuid,uuid,uuid,uuid,jsonb,text,text[],text,bigint,bigint,uuid[],timestamptz) TO `+dispatcherRole+`;
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
	if err != nil || snapshot.ContextItemCount != 2 || snapshot.ContextDigest == ([32]byte{}) || !bytes.Contains(snapshot.ContextPayload, []byte("Frozen attachment body")) || !bytes.Contains(snapshot.ContextPayload, []byte(documentContent)) || !bytes.Contains(snapshot.ContextPayload, []byte(`"kind":"knowledge_document"`)) || !bytes.Contains(snapshot.ContextPayload, []byte(`"id":"d3000000-0000-4000-8000-000000000003"`)) {
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
	admission := agentusage.Admission{ReservationID: "95000000-0000-4000-8000-000000000001", RequestID: string(run.InvocationIDs[0]), State: aitokens.ReservationActive,
		Rate: catalog.AIComplexityRate{Code: "balanced_v1", Version: 1, Complexity: catalog.AIComplexityBalanced, InputPerThousand: 1, CachedInputPerThousand: 1,
			OutputPerThousand: 1, MinimumCharge: 1, MaximumReservation: 1000, EstimatedMinimum: 1, EstimatedMaximum: 500, InternalProvider: "openai", InternalModel: "gpt-5", InternalAdapterVersion: 1, InternalModelPolicyVersion: 1}}
	processor, err := agentdispatch.New(dispatchRepository, producer, staticAgentTokenBroker{admission: admission}, ids.CellID("cell-us-east-01"), fixedClock{now: now}, staticIDGenerator(leaseID), 30*time.Second, 5)
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

	// A pre-run commercial denial has no runner result, but must still make the
	// invocation and Run terminal and retire its otherwise-unclaimable
	// projection row.
	terminalDigest := sha256.Sum256([]byte("completed fixture runner result"))
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_invocations SET status='failed',runner_result_digest=$3,
		failure_code='fixture_complete',completed_at=$4 WHERE account_id=$1 AND id=$2;
		UPDATE spyglass.agent_runs SET state='failed',started_at=$4,completed_at=$4 WHERE account_id=$1 AND id=$5`,
		pgx.QueryExecModeSimpleProtocol, accountID, run.InvocationIDs[0], terminalDigest[:], now, runID); err != nil {
		t.Fatal(err)
	}
	deniedRunID := ids.RunID("65000000-0000-4000-8000-000000000003")
	deniedRun, created, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID,
		RunID: deniedRunID, ConversationID: ids.ConversationID("75000000-0000-4000-8000-000000000003"), CreateConversation: true,
		UserMessageID: ids.MessageID("85000000-0000-4000-8000-000000000003"), Subject: "Denied run", Prompt: "This run has no remaining AI Tokens.",
		PersonaIDs: []ids.PersonaID{personaID}, EntitlementVersion: 7, MaximumConcurrentRun: 1, CreatedAt: now.Add(time.Second), RequestExpiresAt: now.Add(time.Hour),
	})
	if err != nil || !created || len(deniedRun.InvocationIDs) != 1 {
		t.Fatalf("denied run=%+v created=%v err=%v", deniedRun, created, err)
	}
	deniedProcessor, err := agentdispatch.New(dispatchRepository, producer, staticAgentTokenBroker{err: aitokens.ErrInsufficient}, ids.CellID("cell-us-east-01"), fixedClock{now: now.Add(2 * time.Second)}, staticIDGenerator("94000000-0000-4000-8000-000000000003"), 30*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	deniedResult, err := deniedProcessor.ProcessOne(ctx)
	if !errors.Is(err, aitokens.ErrInsufficient) || !deniedResult.DeadLetter {
		t.Fatalf("denied dispatch result=%+v err=%v", deniedResult, err)
	}
	terminal, err := repository.GetRun(ctx, accountID, deniedRunID)
	if err != nil || terminal.State != "failed" || len(terminal.Invocations) != 1 || terminal.Invocations[0].Status != "failed" || terminal.Invocations[0].FailureCode != "ai_tokens_insufficient" {
		t.Fatalf("terminal denied run=%+v err=%v", terminal, err)
	}
	var deniedDispatchState, deniedProjectionState string
	if err := owner.QueryRow(ctx, `SELECT d.state,p.state FROM spyglass.agent_dispatch_queue d
		JOIN spyglass.agent_result_projection_queue p USING(account_id,invocation_id)
		WHERE d.account_id=$1 AND d.invocation_id=$2`, accountID, deniedRun.InvocationIDs[0]).Scan(&deniedDispatchState, &deniedProjectionState); err != nil {
		t.Fatal(err)
	}
	if deniedDispatchState != "dead_letter" || deniedProjectionState != "projected" {
		t.Fatalf("terminal queue states dispatch=%s projection=%s", deniedDispatchState, deniedProjectionState)
	}
}
