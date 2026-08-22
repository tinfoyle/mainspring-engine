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
	var eventCount int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.schedule_events WHERE account_id=$1 AND schedule_id=$2`, accountID, scheduleID).Scan(&eventCount); err != nil || eventCount != 3 {
		t.Fatalf("event count=%d err=%v", eventCount, err)
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
