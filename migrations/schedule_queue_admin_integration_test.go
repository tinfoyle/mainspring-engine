package migrations_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/schedulequeueadmin"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestScheduleQueueRecoveryIsExactAuditedAndExecuteOnly(t *testing.T) {
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
	accountID := ids.AccountID("a1000000-0000-4000-8000-000000000001")
	rootID := "a2000000-0000-4000-8000-000000000001"
	seedCellErasureAccount(t, ctx, owner, accountID, rootID, "a3000000-0000-4000-8000-000000000001", now.UTC(), false)
	scheduleID := strings.Replace(rootID, "000000000001", "000000000701", 1)
	triggerID := strings.Replace(rootID, "000000000001", "000000000731", 1)
	if _, err := owner.Exec(ctx, `UPDATE spyglass.schedule_dispatch_queue
		SET state='dead_letter',attempt_count=12,last_error_code='admission_failed',updated_at=$3
		WHERE account_id=$1 AND schedule_id=$2;
		UPDATE spyglass.schedule_trigger_queue
		SET state='dead_letter',attempt_count=7,last_error_code='dispatch_failed',updated_at=$3
		WHERE account_id=$1 AND trigger_id=$4`, pgx.QueryExecModeSimpleProtocol, accountID, scheduleID, now, triggerID); err != nil {
		t.Fatal(err)
	}
	role := "spyglass_schedule_queue_operator_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public TO `+role+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_schedule_queue_dead_letters(uuid,text,text,text,text,integer) TO `+role+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_requeue_schedule_queue_dead_letter(uuid,text,uuid,uuid,uuid,text,text,text) TO `+role); err != nil {
		t.Fatal(err)
	}
	operator := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+role)
		return err
	})
	defer func() {
		operator.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE IF EXISTS `+role)
	}()
	var forbidden int
	if err := operator.QueryRow(ctx, `SELECT count(*) FROM spyglass.schedule_dispatch_queue`).Scan(&forbidden); err == nil {
		t.Fatal("operator directly read Schedule dispatch queue")
	}
	repository := postgresadapter.NewScheduleQueueAdminRepository(operator)
	recurringBatch := "b1000000-0000-4000-8000-000000000001"
	records, err := repository.Inspect(ctx, schedulequeueadmin.QueueRecurring, 10, schedulequeueadmin.Change{BatchID: recurringBatch, Actor: "operator@example.com", Reason: "investigate recurring failure", Environment: "test"})
	if err != nil || len(records) != 1 || records[0].Queue != schedulequeueadmin.QueueRecurring || records[0].AccountID != accountID || records[0].ScheduleID != scheduleID || records[0].TriggerID != "" || records[0].AttemptCount != 12 {
		t.Fatalf("recurring inspection records=%+v err=%v", records, err)
	}
	requeueBatch := "b2000000-0000-4000-8000-000000000002"
	requeued, err := repository.Requeue(ctx, schedulequeueadmin.Target{Queue: schedulequeueadmin.QueueRecurring, AccountID: accountID, ScheduleID: scheduleID}, schedulequeueadmin.Change{BatchID: requeueBatch, Actor: "operator@example.com", Reason: "admission dependency corrected", Environment: "test"})
	if err != nil || requeued.Queue != schedulequeueadmin.QueueRecurring || requeued.AttemptCount != 12 || requeued.NextAttemptAt == nil {
		t.Fatalf("recurring requeue record=%+v err=%v", requeued, err)
	}
	var state string
	if err := owner.QueryRow(ctx, `SELECT state FROM spyglass.schedule_dispatch_queue WHERE account_id=$1 AND schedule_id=$2`, accountID, scheduleID).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("recurring state=%s err=%v", state, err)
	}
	triggerBatch := "b3000000-0000-4000-8000-000000000003"
	triggerRecords, err := repository.Inspect(ctx, schedulequeueadmin.QueueTrigger, 10, schedulequeueadmin.Change{BatchID: triggerBatch, Actor: "operator@example.com", Reason: "investigate trigger failure", Environment: "test"})
	if err != nil || len(triggerRecords) != 1 || triggerRecords[0].Queue != schedulequeueadmin.QueueTrigger || triggerRecords[0].ScheduleID != scheduleID || triggerRecords[0].TriggerID != triggerID || triggerRecords[0].AttemptCount != 7 {
		t.Fatalf("trigger inspection records=%+v err=%v", triggerRecords, err)
	}
	triggerRequeueBatch := "b4000000-0000-4000-8000-000000000004"
	triggerRecord, err := repository.Requeue(ctx, schedulequeueadmin.Target{Queue: schedulequeueadmin.QueueTrigger, AccountID: accountID, ScheduleID: scheduleID, TriggerID: triggerID}, schedulequeueadmin.Change{BatchID: triggerRequeueBatch, Actor: "operator@example.com", Reason: "dispatch dependency corrected", Environment: "test"})
	if err != nil || triggerRecord.Queue != schedulequeueadmin.QueueTrigger || triggerRecord.AttemptCount != 7 || triggerRecord.NextAttemptAt == nil {
		t.Fatalf("trigger requeue record=%+v err=%v", triggerRecord, err)
	}
	if err := owner.QueryRow(ctx, `SELECT state FROM spyglass.schedule_trigger_queue WHERE account_id=$1 AND trigger_id=$2`, accountID, triggerID).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("trigger state=%s err=%v", state, err)
	}
	if _, err := owner.Exec(ctx, `DELETE FROM spyglass.schedule_queue_operator_events WHERE batch_id=$1`, triggerRequeueBatch); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("operator evidence mutation err=%v", err)
	}
	_, err = repository.Requeue(ctx, schedulequeueadmin.Target{Queue: schedulequeueadmin.QueueRecurring, AccountID: accountID, ScheduleID: scheduleID}, schedulequeueadmin.Change{BatchID: "b5000000-0000-4000-8000-000000000005", Actor: "operator@example.com", Reason: "duplicate recurring recovery", Environment: "test"})
	if !errors.Is(err, schedulequeueadmin.ErrStateConflict) {
		t.Fatalf("non-dead-letter requeue error=%v", err)
	}
	emptyBatch := "b6000000-0000-4000-8000-000000000006"
	empty, err := repository.Inspect(ctx, schedulequeueadmin.QueueRecurring, 10, schedulequeueadmin.Change{BatchID: emptyBatch, Actor: "operator@example.com", Reason: "confirm recurring queue recovery", Environment: "test"})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty inspection records=%+v err=%v", empty, err)
	}
	var emptyEvidence int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.schedule_queue_operator_events WHERE batch_id=$1 AND action='inspected' AND account_id IS NULL`, emptyBatch).Scan(&emptyEvidence); err != nil || emptyEvidence != 1 {
		t.Fatalf("empty inspection evidence=%d err=%v", emptyEvidence, err)
	}
}
