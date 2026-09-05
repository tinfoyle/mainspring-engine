package migrations_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	scheduleapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	scheduledomain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestScheduleRepositoryPersistsAndIsolatesDefinitions(t *testing.T) {
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

	now := time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("13000000-0000-4000-8000-000000000001")
	otherAccountID := ids.AccountID("13000000-0000-4000-8000-000000000002")
	userID := ids.UserID("23000000-0000-4000-8000-000000000001")
	boardroomID := ids.BoardroomID("33000000-0000-4000-8000-000000000001")
	personaID := ids.PersonaID("43000000-0000-4000-8000-000000000001")
	scheduleID := ids.ScheduleID("53000000-0000-4000-8000-000000000001")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',$3),($2,1,'active',$3)`, accountID, otherAccountID, now); err != nil {
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
	if _, _, err := agents.CreateBoardroom(ctx, mustBoardroom(t, boardroomID, accountID, now)); err != nil {
		t.Fatal(err)
	}
	persona := mustPersonaVersion(t, "63000000-0000-4000-8000-000000000001", personaID, accountID, 1, "Daily Reviewer", userID, now)
	if _, _, err := agents.PublishPersona(ctx, boardroomID, persona, 0); err != nil {
		t.Fatal(err)
	}

	value, err := scheduledomain.New(scheduledomain.Draft{
		ID: scheduleID, AccountID: accountID, Name: "Daily operating review", Timezone: "America/New_York",
		Recurrence:      scheduledomain.Recurrence{Frequency: scheduledomain.FrequencyDaily, LocalHour: 9, GapPolicy: scheduledomain.GapSkip, OverlapPolicy: scheduledomain.OverlapFirst},
		MissedRunPolicy: scheduledomain.MissedCatchUpOne,
		Template:        scheduledomain.AgentRunTemplate{BoardroomID: boardroomID, Mode: "selected", PersonaIDs: []ids.PersonaID{personaID}, Subject: "Daily review", Prompt: "Review current operating priorities."},
		CreatedBy:       userID, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewScheduleRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	createdMutation := scheduleapp.Mutation{EventID: "73000000-0000-4000-8000-000000000001", Kind: "created", ActorUserID: userID, Reason: "Created daily review", CorrelationID: "schedule-create", At: now}
	created, changed, err := repository.Create(ctx, value, createdMutation)
	if err != nil || !changed || !reflect.DeepEqual(created, value) {
		t.Fatalf("created=%+v changed=%v err=%v", created, changed, err)
	}
	replayed, changed, err := repository.Create(ctx, value, createdMutation)
	if err != nil || changed || !reflect.DeepEqual(replayed, value) {
		t.Fatalf("replayed=%+v changed=%v err=%v", replayed, changed, err)
	}
	loaded, err := repository.Get(ctx, accountID, scheduleID)
	if err != nil || !reflect.DeepEqual(loaded, value) {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if _, err := repository.Get(ctx, otherAccountID, scheduleID); !errors.Is(err, scheduleapp.ErrNotFound) {
		t.Fatalf("cross-Account Get err=%v", err)
	}

	paused, err := value.Pause(value.Version, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	spoofedPause := paused
	spoofedPause.CreatedBy = ids.UserID("23000000-0000-4000-8000-000000000099")
	if _, err := repository.Update(ctx, spoofedPause, value.Version, scheduleapp.Mutation{EventID: "73000000-0000-4000-8000-000000000099", Kind: "paused", ActorUserID: userID, Reason: "Spoofed immutable identity", CorrelationID: "schedule-pause-spoof", At: now.Add(time.Minute)}); !errors.Is(err, scheduleapp.ErrInvalid) {
		t.Fatalf("spoofed identity Update err=%v", err)
	}
	paused, err = repository.Update(ctx, paused, value.Version, scheduleapp.Mutation{EventID: "73000000-0000-4000-8000-000000000002", Kind: "paused", ActorUserID: userID, Reason: "Paused daily review", CorrelationID: "schedule-pause", At: now.Add(time.Minute)})
	if err != nil || paused.State != scheduledomain.StatePaused || paused.Version != 2 || paused.NextRunAt != nil {
		t.Fatalf("paused=%+v err=%v", paused, err)
	}
	resumed, err := paused.Resume(paused.Version, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	spoofedNext := resumed
	shiftedNext := resumed.NextRunAt.Add(24 * time.Hour)
	spoofedNext.NextRunAt = &shiftedNext
	if _, err := repository.Update(ctx, spoofedNext, paused.Version, scheduleapp.Mutation{EventID: "73000000-0000-4000-8000-000000000098", Kind: "resumed", ActorUserID: userID, Reason: "Spoofed recurrence advance", CorrelationID: "schedule-resume-spoof", At: now.Add(2 * time.Minute)}); !errors.Is(err, scheduleapp.ErrInvalid) {
		t.Fatalf("spoofed next run Update err=%v", err)
	}
	resumed, err = repository.Update(ctx, resumed, paused.Version, scheduleapp.Mutation{EventID: "73000000-0000-4000-8000-000000000003", Kind: "resumed", ActorUserID: userID, Reason: "Resumed daily review", CorrelationID: "schedule-resume", At: now.Add(2 * time.Minute)})
	if err != nil || resumed.State != scheduledomain.StateActive || resumed.Version != 3 || resumed.NextRunAt == nil {
		t.Fatalf("resumed=%+v err=%v", resumed, err)
	}
	revisedAt := now.Add(3 * time.Minute)
	revised, err := resumed.Revise(resumed.Version, scheduledomain.Revision{Name: "Daily priorities", Timezone: resumed.Timezone,
		Recurrence: resumed.Recurrence, MissedRunPolicy: scheduledomain.MissedSkip, Template: resumed.Template}, revisedAt)
	if err != nil {
		t.Fatal(err)
	}
	updatedMutation := scheduleapp.Mutation{EventID: "73000000-0000-4000-8000-000000000004", Kind: "updated", ActorUserID: userID, Reason: "Updated daily review", CorrelationID: "schedule-update", At: revisedAt}
	revised, err = repository.Update(ctx, revised, resumed.Version, updatedMutation)
	if err != nil || revised.Version != 4 || revised.Name != "Daily priorities" || revised.NextRunAt == nil || !revised.NextRunAt.After(revisedAt) {
		t.Fatalf("revised=%+v err=%v", revised, err)
	}
	retryValue := revised
	retryValue.UpdatedAt = revisedAt.Add(time.Minute)
	retryMutation := updatedMutation
	retryMutation.At = retryValue.UpdatedAt
	replayedRevision, err := repository.Update(ctx, retryValue, resumed.Version, retryMutation)
	if err != nil || !reflect.DeepEqual(replayedRevision, revised) {
		t.Fatalf("replayed revision=%+v err=%v", replayedRevision, err)
	}
	page, err := repository.List(ctx, accountID, scheduleapp.ListQuery{Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != scheduleID {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	deletedAt := now.Add(5 * time.Minute)
	deleted, err := revised.Delete(revised.Version, deletedAt)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err = repository.Update(ctx, deleted, revised.Version, scheduleapp.Mutation{EventID: "73000000-0000-4000-8000-000000000005", Kind: "deleted", ActorUserID: userID, Reason: "Deleted daily review", CorrelationID: "schedule-delete", At: deletedAt})
	if err != nil || deleted.State != scheduledomain.StateDeleted || deleted.NextRunAt != nil || deleted.Version != 5 {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	page, err = repository.List(ctx, accountID, scheduleapp.ListQuery{Limit: 10})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("deleted page=%+v err=%v", page, err)
	}
	var dueCount int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.schedule_dispatch_queue WHERE account_id=$1 AND schedule_id=$2`, accountID, scheduleID).Scan(&dueCount); err != nil || dueCount != 0 {
		t.Fatalf("deleted due rows=%d err=%v", dueCount, err)
	}
	var eventCount int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.schedule_events WHERE account_id=$1 AND schedule_id=$2`, accountID, scheduleID).Scan(&eventCount); err != nil || eventCount != 5 {
		t.Fatalf("event count=%d err=%v", eventCount, err)
	}
}

type scheduleExecutionAuthorizer struct {
	value scheduleapp.ExecutionAuthorization
}

func (authorizer scheduleExecutionAuthorizer) Authorize(context.Context, scheduleapp.ExecutionSnapshot) (scheduleapp.ExecutionAuthorization, error) {
	return authorizer.value, nil
}

func TestScheduleExecutionAtomicallyCreatesRunAndAdvancesDefinition(t *testing.T) {
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
	var createdAt time.Time
	if err := owner.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	createdAt = createdAt.UTC()
	accountID := ids.AccountID("14000000-0000-4000-8000-000000000001")
	userID := ids.UserID("24000000-0000-4000-8000-000000000001")
	boardroomID := ids.BoardroomID("34000000-0000-4000-8000-000000000001")
	personaID := ids.PersonaID("44000000-0000-4000-8000-000000000001")
	scheduleID := ids.ScheduleID("54000000-0000-4000-8000-000000000001")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2)`, accountID, createdAt); err != nil {
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
	if _, _, err := agents.CreateBoardroom(ctx, mustBoardroom(t, boardroomID, accountID, createdAt)); err != nil {
		t.Fatal(err)
	}
	persona := mustPersonaVersion(t, "64000000-0000-4000-8000-000000000001", personaID, accountID, 1, "Scheduled Reviewer", userID, createdAt)
	if _, _, err := agents.PublishPersona(ctx, boardroomID, persona, 0); err != nil {
		t.Fatal(err)
	}
	schedule, err := scheduledomain.New(scheduledomain.Draft{
		ID: scheduleID, AccountID: accountID, Name: "Scheduled operating review", Timezone: "America/New_York",
		Recurrence:      scheduledomain.Recurrence{Frequency: scheduledomain.FrequencyDaily, LocalHour: 9, GapPolicy: scheduledomain.GapSkip, OverlapPolicy: scheduledomain.OverlapFirst},
		MissedRunPolicy: scheduledomain.MissedCatchUpOne,
		Template:        scheduledomain.AgentRunTemplate{BoardroomID: boardroomID, Mode: "selected", PersonaIDs: []ids.PersonaID{personaID}, Subject: "Operating review", Prompt: "Review the scheduled operating priorities."},
		CreatedBy:       userID, CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := postgresadapter.NewScheduleRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := definitions.Create(ctx, schedule, scheduleapp.Mutation{EventID: "74000000-0000-4000-8000-000000000001", Kind: "created", ActorUserID: userID, Reason: "Created scheduled review", CorrelationID: "schedule-execution-create", At: createdAt}); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	executions, err := postgresadapter.NewScheduleExecutionRepository(owner, cell)
	if err != nil {
		t.Fatal(err)
	}
	processAt := schedule.NextRunAt.Add(time.Minute + 567*time.Nanosecond)
	processor, err := scheduleapp.NewExecutionProcessor(executions,
		scheduleExecutionAuthorizer{value: scheduleapp.ExecutionAuthorization{EntitlementVersion: 9, MaximumConcurrentRun: 4, CanReadRestricted: true}},
		fixedClock{now: processAt}, fixedIDGenerator{value: "84000000-0000-4000-8000-000000000001"}, scheduleapp.DefaultExecutionLease, scheduleapp.DefaultExecutionMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessOne(ctx)
	if err != nil || !result.Worked || !result.Dispatched || result.Skipped {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	occurrenceID, _ := ids.Derive(string(scheduleID), "occurrence/"+schedule.NextRunAt.UTC().Format(time.RFC3339Nano))
	runID, _ := ids.Derive(occurrenceID, "run")
	conversationID, _ := ids.Derive(occurrenceID, "conversation")
	loadedRun, err := agents.GetRun(ctx, accountID, ids.RunID(runID))
	if err != nil || loadedRun.Plan.CreatedBy != userID || loadedRun.Plan.EntitlementVersion != 9 || loadedRun.Plan.ConversationID != ids.ConversationID(conversationID) || len(loadedRun.Plan.Turns) != 1 || loadedRun.Plan.Turns[0].PersonaID != personaID {
		t.Fatalf("run=%+v err=%v", loadedRun, err)
	}
	if !loadedRun.Plan.CreatedAt.Equal(processAt.Truncate(time.Microsecond)) {
		t.Fatalf("scheduled run timestamp was not normalized: %s", loadedRun.Plan.CreatedAt)
	}
	advanced, err := definitions.Get(ctx, accountID, scheduleID)
	if err != nil || advanced.Version != 2 || advanced.NextRunAt == nil || !advanced.NextRunAt.After(processAt) {
		t.Fatalf("advanced=%+v err=%v", advanced, err)
	}
	var outcome, initiator string
	var storedRun, storedConversation string
	if err := owner.QueryRow(ctx, `SELECT outcome,run_id,conversation_id,initiated_by_id FROM spyglass.schedule_occurrences
		WHERE account_id=$1 AND id=$2`, accountID, occurrenceID).Scan(&outcome, &storedRun, &storedConversation, &initiator); err != nil || outcome != "dispatched" || storedRun != runID || storedConversation != conversationID || initiator != "schedule-execution-worker" {
		t.Fatalf("outcome=%s run=%s conversation=%s initiator=%s err=%v", outcome, storedRun, storedConversation, initiator, err)
	}
	var scheduleEvents, agentDispatches, dueRows int
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM spyglass.schedule_events WHERE account_id=$1 AND schedule_id=$2),
		(SELECT count(*) FROM spyglass.agent_dispatch_queue WHERE account_id=$1),
		(SELECT count(*) FROM spyglass.schedule_dispatch_queue WHERE account_id=$1 AND schedule_id=$2 AND state='pending')`, accountID, scheduleID).Scan(&scheduleEvents, &agentDispatches, &dueRows); err != nil || scheduleEvents != 2 || agentDispatches != 1 || dueRows != 1 {
		t.Fatalf("schedule events=%d agent dispatches=%d due=%d err=%v", scheduleEvents, agentDispatches, dueRows, err)
	}

	triggerID := "75000000-0000-4000-8000-000000000001"
	triggerAt := processAt.Add(time.Minute).Truncate(time.Microsecond)
	triggerRequest := scheduleapp.TriggerRequest{ID: triggerID, AccountID: accountID, ScheduleID: scheduleID, ScheduleVersion: advanced.Version, RequestedBy: userID, RequestedAt: triggerAt}
	triggerMutation := scheduleapp.Mutation{EventID: triggerID, Kind: "trigger_requested", ActorUserID: userID, Reason: "Run the review now", CorrelationID: triggerID, At: triggerAt}
	trigger, created, err := definitions.EnqueueTrigger(ctx, triggerRequest, triggerMutation)
	if err != nil || !created || !trigger.Valid() {
		t.Fatalf("trigger=%+v created=%v err=%v", trigger, created, err)
	}
	replayRequest := triggerRequest
	replayRequest.RequestedAt = triggerAt.Add(time.Minute)
	replayMutation := triggerMutation
	replayMutation.At = replayRequest.RequestedAt
	if replayed, created, err := definitions.EnqueueTrigger(ctx, replayRequest, replayMutation); err != nil || created || replayed.ID != trigger.ID ||
		replayed.ScheduleID != trigger.ScheduleID || replayed.ScheduleVersion != trigger.ScheduleVersion || !replayed.RequestedAt.Equal(trigger.RequestedAt) {
		t.Fatalf("trigger replay=%+v created=%v err=%v", replayed, created, err)
	}
	triggerProcessor, err := scheduleapp.NewExecutionProcessor(executions,
		scheduleExecutionAuthorizer{value: scheduleapp.ExecutionAuthorization{EntitlementVersion: 10, MaximumConcurrentRun: 4, CanReadRestricted: true}},
		fixedClock{now: triggerAt.Add(time.Second)}, fixedIDGenerator{value: "85000000-0000-4000-8000-000000000001"}, scheduleapp.DefaultExecutionLease, scheduleapp.DefaultExecutionMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	triggerResult, err := triggerProcessor.ProcessOne(ctx)
	if err != nil || !triggerResult.Worked || !triggerResult.Dispatched || triggerResult.Skipped {
		t.Fatalf("trigger result=%+v err=%v", triggerResult, err)
	}
	afterTrigger, err := definitions.Get(ctx, accountID, scheduleID)
	if err != nil || afterTrigger.Version != advanced.Version || afterTrigger.NextRunAt == nil || !afterTrigger.NextRunAt.Equal(*advanced.NextRunAt) {
		t.Fatalf("trigger mutated recurrence: before=%+v after=%+v err=%v", advanced, afterTrigger, err)
	}
	triggerOccurrenceID, _ := ids.Derive(triggerID, "occurrence")
	triggerRunID, _ := ids.Derive(triggerOccurrenceID, "run")
	triggerRun, err := agents.GetRun(ctx, accountID, ids.RunID(triggerRunID))
	if err != nil || triggerRun.Plan.EntitlementVersion != 10 || triggerRun.Plan.CreatedBy != userID {
		t.Fatalf("trigger run=%+v err=%v", triggerRun, err)
	}
	var occurrenceKind, storedTrigger, requestedBy string
	if err := owner.QueryRow(ctx, `SELECT occurrence_kind,trigger_id,requested_by_user_id FROM spyglass.schedule_occurrences
		WHERE account_id=$1 AND id=$2`, accountID, triggerOccurrenceID).Scan(&occurrenceKind, &storedTrigger, &requestedBy); err != nil ||
		occurrenceKind != "triggered" || storedTrigger != triggerID || requestedBy != string(userID) {
		t.Fatalf("trigger occurrence kind=%s trigger=%s requester=%s err=%v", occurrenceKind, storedTrigger, requestedBy, err)
	}
	var triggerQueue, triggerEvents int
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM spyglass.schedule_trigger_queue WHERE account_id=$1 AND trigger_id=$2),
		(SELECT count(*) FROM spyglass.schedule_events WHERE account_id=$1 AND schedule_id=$3 AND event_type IN ('trigger_requested','trigger_dispatched'))`,
		accountID, triggerID, scheduleID).Scan(&triggerQueue, &triggerEvents); err != nil || triggerQueue != 0 || triggerEvents != 2 {
		t.Fatalf("trigger queue=%d events=%d err=%v", triggerQueue, triggerEvents, err)
	}
}

func TestScheduleDefinitionsQueueLeasesAndLeastPrivilege(t *testing.T) {
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
	accountA := "11000000-0000-4000-8000-000000000001"
	accountB := "12000000-0000-4000-8000-000000000002"
	scheduleA := "21000000-0000-4000-8000-000000000001"
	scheduleB := "22000000-0000-4000-8000-000000000002"
	userID := "31000000-0000-4000-8000-000000000001"
	recurrence := `{"frequency":"daily","local_hour":9,"local_minute":0,"weekdays":[],"gap_policy":"skip","overlap_policy":"first"}`
	template := `{"boardroom_id":"41000000-0000-4000-8000-000000000001","mode":"selected","persona_ids":["51000000-0000-4000-8000-000000000001"],"subject":"Daily review","prompt":"Review priorities.","work_item_ids":[],"knowledge_fact_ids":[],"knowledge_document_ids":[],"baseline_assessment_ids":[]}`
	createdAt := now.Add(-time.Hour)
	laterAt := now.Add(time.Hour)
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$3),($2,1,'active',$3);
		INSERT INTO spyglass.schedules(account_id,id,name,timezone,recurrence,missed_run_policy,execution_template,state,version,next_run_at,created_by_user_id,created_at,updated_at)
		VALUES ($1,$4,'Daily operating review','America/New_York',$6::jsonb,'catch_up_one',$7::jsonb,'active',1,$3,$8,$9,$9),
		       ($2,$5,'Later operating review','America/New_York',$6::jsonb,'skip',$7::jsonb,'active',1,$10,$8,$9,$9);
		INSERT INTO spyglass.schedule_events(account_id,id,schedule_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,'61000000-0000-4000-8000-000000000001',$4,'created',0,1,'user',$8,'Created schedule','schedule-create','{"state":"active"}',$9)`,
		pgx.QueryExecModeSimpleProtocol, accountA, accountB, now, scheduleA, scheduleB, recurrence, template, userID, createdAt, laterAt); err != nil {
		t.Fatal(err)
	}

	workerRole := "spyglass_schedule_test_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+workerRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_claim_schedule_dispatch(uuid,timestamptz,integer) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_heartbeat_schedule_dispatch(uuid,uuid,uuid,timestamptz,timestamptz,integer) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_fail_schedule_dispatch(uuid,uuid,uuid,timestamptz,boolean,timestamptz,text,timestamptz,integer) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_schedule_dispatch_stats(timestamptz) TO `+workerRole); err != nil {
		t.Fatal(err)
	}
	worker := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+workerRole)
		return err
	})
	defer func() {
		worker.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+workerRole+`; DROP ROLE IF EXISTS `+workerRole)
	}()

	leaseID := "71000000-0000-4000-8000-000000000001"
	var claimedAccount, claimedSchedule, claimedLease string
	var version int64
	var scheduledFor time.Time
	var attempt int
	if err := worker.QueryRow(ctx, `SELECT account_id,schedule_id,schedule_version,scheduled_for,lease_id,attempt_count
		FROM public.spyglass_claim_schedule_dispatch($1,$2,30)`, leaseID, now).Scan(
		&claimedAccount, &claimedSchedule, &version, &scheduledFor, &claimedLease, &attempt); err != nil ||
		claimedAccount != accountA || claimedSchedule != scheduleA || claimedLease != leaseID || version != 1 || attempt != 1 || !scheduledFor.Equal(now) {
		t.Fatalf("claim account=%s schedule=%s version=%d at=%s lease=%s attempt=%d err=%v", claimedAccount, claimedSchedule, version, scheduledFor, claimedLease, attempt, err)
	}
	if _, err := worker.Exec(ctx, `SELECT count(*) FROM spyglass.schedules`); err == nil {
		t.Fatal("schedule worker directly read customer definitions")
	}
	if _, err := worker.Exec(ctx, `SELECT count(*) FROM spyglass.schedule_dispatch_queue`); err == nil {
		t.Fatal("schedule worker directly read cross-Account queue")
	}
	if _, err := worker.Exec(ctx, `SELECT count(*) FROM spyglass.schedule_occurrences`); err == nil {
		t.Fatal("schedule worker directly read Account occurrence history")
	}
	var heartbeat bool
	if err := worker.QueryRow(ctx, `SELECT public.spyglass_heartbeat_schedule_dispatch($1,$2,$3,$4,$5,30)`,
		accountA, scheduleA, leaseID, now, now).Scan(&heartbeat); err != nil || !heartbeat {
		t.Fatalf("heartbeat=%v err=%v", heartbeat, err)
	}
	if _, err := worker.Exec(ctx, `SELECT public.spyglass_fail_schedule_dispatch($1,$2,$3,$4,true,$5,'temporary_unavailable',$6,4)`,
		accountA, scheduleA, "72000000-0000-4000-8000-000000000002", now, now.Add(time.Minute), now); err == nil || !strings.Contains(err.Error(), "lease lost") {
		t.Fatalf("wrong lease failure=%v", err)
	}
	var state string
	if err := worker.QueryRow(ctx, `SELECT public.spyglass_fail_schedule_dispatch($1,$2,$3,$4,true,$5,'temporary_unavailable',$6,4)`,
		accountA, scheduleA, leaseID, now, now.Add(time.Minute), now).Scan(&state); err != nil || state != "retry" {
		t.Fatalf("retry state=%s err=%v", state, err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.schedule_events SET reason='changed' WHERE account_id=$1`, accountA); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("mutable schedule event=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.schedules SET state='paused',next_run_at=NULL,version=2,updated_at=$3 WHERE account_id=$1 AND id=$2`, accountA, scheduleA, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var queueRows int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.schedule_dispatch_queue WHERE account_id=$1 AND schedule_id=$2`, accountA, scheduleA).Scan(&queueRows); err != nil || queueRows != 0 {
		t.Fatalf("paused queue rows=%d err=%v", queueRows, err)
	}
}
