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
	"github.com/tinfoyle/spyglass-engine/internal/application/agentqueueadmin"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestAgentQueueRecoveryIsExactAuditedAndExecuteOnly(t *testing.T) {
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
	invocationID := strings.Replace(rootID, "000000000001", "000000000051", 1)
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_dispatch_queue SET state='dead_letter',attempt_count=12,last_error_code='provision_failed',updated_at=$3 WHERE account_id=$1 AND invocation_id=$2;
		UPDATE spyglass.agent_result_projection_queue SET state='dead_letter',attempt_count=7,last_error_code='projection_failed',projected_at=NULL,updated_at=$3 WHERE account_id=$1 AND invocation_id=$2`, pgx.QueryExecModeSimpleProtocol, accountID, invocationID, now); err != nil {
		t.Fatal(err)
	}
	role := "spyglass_agent_queue_operator_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public TO `+role+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_agent_queue_dead_letters(uuid,text,text,text,text,integer) TO `+role+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_requeue_agent_queue_dead_letter(uuid,text,uuid,uuid,text,text,text) TO `+role); err != nil {
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
	if err := operator.QueryRow(ctx, `SELECT count(*) FROM spyglass.agent_dispatch_queue`).Scan(&forbidden); err == nil {
		t.Fatal("operator directly read Agent dispatch queue")
	}
	batch := "b1000000-0000-4000-8000-000000000001"
	repository := postgresadapter.NewAgentQueueAdminRepository(operator)
	records, err := repository.Inspect(ctx, agentqueueadmin.QueueDispatch, 10, agentqueueadmin.Change{BatchID: batch, Actor: "operator@example.com", Reason: "investigate dispatch failure", Environment: "test"})
	if err != nil || len(records) != 1 || records[0].Queue != "dispatch" || records[0].AccountID != accountID || records[0].InvocationID != invocationID || records[0].AttemptCount != 12 {
		t.Fatalf("inspection records=%+v err=%v", records, err)
	}
	requeueBatch := "b2000000-0000-4000-8000-000000000002"
	requeued, err := repository.Requeue(ctx, agentqueueadmin.Target{Queue: agentqueueadmin.QueueDispatch, AccountID: accountID, InvocationID: invocationID}, agentqueueadmin.Change{BatchID: requeueBatch, Actor: "operator@example.com", Reason: "dependency corrected after review", Environment: "test"})
	if err != nil || requeued.Queue != "dispatch" || requeued.AttemptCount != 12 || requeued.NextAttemptAt == nil {
		t.Fatalf("requeue record=%+v err=%v", requeued, err)
	}
	var state string
	if err := owner.QueryRow(ctx, `SELECT state FROM spyglass.agent_dispatch_queue WHERE account_id=$1 AND invocation_id=$2`, accountID, invocationID).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("state=%s err=%v", state, err)
	}
	projectionInspect := agentqueueadmin.Change{BatchID: "b4000000-0000-4000-8000-000000000004", Actor: "operator@example.com", Reason: "investigate projection failure", Environment: "test"}
	projectionRecords, err := repository.Inspect(ctx, agentqueueadmin.QueueProjection, 10, projectionInspect)
	if err != nil || len(projectionRecords) != 1 || projectionRecords[0].Queue != agentqueueadmin.QueueProjection || projectionRecords[0].AttemptCount != 7 {
		t.Fatalf("projection inspection records=%+v err=%v", projectionRecords, err)
	}
	projectionRequeue := agentqueueadmin.Change{BatchID: "b5000000-0000-4000-8000-000000000005", Actor: "operator@example.com", Reason: "projection dependency corrected", Environment: "test"}
	projectionRecord, err := repository.Requeue(ctx, agentqueueadmin.Target{Queue: agentqueueadmin.QueueProjection, AccountID: accountID, InvocationID: invocationID}, projectionRequeue)
	if err != nil || projectionRecord.Queue != agentqueueadmin.QueueProjection || projectionRecord.AttemptCount != 7 || projectionRecord.NextAttemptAt == nil {
		t.Fatalf("projection requeue record=%+v err=%v", projectionRecord, err)
	}
	if err := owner.QueryRow(ctx, `SELECT state FROM spyglass.agent_result_projection_queue WHERE account_id=$1 AND invocation_id=$2`, accountID, invocationID).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("projection state=%s err=%v", state, err)
	}
	if _, err := owner.Exec(ctx, `DELETE FROM spyglass.agent_queue_operator_events WHERE batch_id=$1`, requeueBatch); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("operator evidence mutation err=%v", err)
	}
	_, err = repository.Requeue(ctx, agentqueueadmin.Target{Queue: agentqueueadmin.QueueDispatch, AccountID: accountID, InvocationID: invocationID}, agentqueueadmin.Change{BatchID: "b3000000-0000-4000-8000-000000000003", Actor: "operator@example.com", Reason: "duplicate recovery attempt", Environment: "test"})
	if !errors.Is(err, agentqueueadmin.ErrStateConflict) {
		t.Fatalf("non-dead-letter requeue error=%v", err)
	}
	emptyBatch := "b6000000-0000-4000-8000-000000000006"
	empty, err := repository.Inspect(ctx, agentqueueadmin.QueueDispatch, 10, agentqueueadmin.Change{BatchID: emptyBatch, Actor: "operator@example.com", Reason: "confirm dispatch queue recovery", Environment: "test"})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty inspection records=%+v err=%v", empty, err)
	}
	var emptyEvidence int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.agent_queue_operator_events WHERE batch_id=$1 AND action='inspected' AND account_id IS NULL`, emptyBatch).Scan(&emptyEvidence); err != nil || emptyEvidence != 1 {
		t.Fatalf("empty inspection evidence=%d err=%v", emptyEvidence, err)
	}
}
