package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountmovement"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestAccountMovementCopiesReconcilesSwitchesRollsBackAndRetires(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	globalURL, cleanupGlobal := createDatabase(t, ctx, adminURL)
	defer cleanupGlobal()
	sourceURL, cleanupSource := createDatabase(t, ctx, adminURL)
	defer cleanupSource()
	destinationURL, cleanupDestination := createDatabase(t, ctx, adminURL)
	defer cleanupDestination()
	global := openPool(t, ctx, globalURL, nil)
	source := openPool(t, ctx, sourceURL, nil)
	destination := openPool(t, ctx, destinationURL, nil)
	defer global.Close()
	defer source.Close()
	defer destination.Close()
	if _, err := migrations.Apply(ctx, global, migrations.Global); err != nil {
		t.Fatal(err)
	}
	for _, cell := range []*pgxpool.Pool{source, destination} {
		if _, err := migrations.Apply(ctx, cell, migrations.Cell); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedMovementCells(t, ctx, global, now)
	mover, err := postgresadapter.NewAccountCellMover(source, destination)
	if err != nil {
		t.Fatal(err)
	}
	service, err := accountmovement.NewService(postgresadapter.NewAccountMovementRepository(global), mover, ids.RandomGenerator{}, registration.SystemClock{}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	rollbackAccount := ids.AccountID("91000000-0000-4000-8000-000000000001")
	retireAccount := ids.AccountID("92000000-0000-4000-8000-000000000001")
	seedMovementAccount(t, ctx, global, source, rollbackAccount, "rollback-account", "91100000-0000-4000-8000-000000000001", now)
	seedMovementAccount(t, ctx, global, source, retireAccount, "retire-account", "92100000-0000-4000-8000-000000000001", now)

	preparedRollbackMove := assertMovementRequiresQuiescentQueues(t, ctx, service, global, source, rollbackAccount, now)
	rollbackMove := moveToSwitched(t, ctx, service, rollbackAccount, preparedRollbackMove)
	rolledBack, err := service.Rollback(ctx, rollbackMove.ID, "operator@example.com", "rollback verified Account movement", "test")
	if err != nil || rolledBack.State != accountmovement.StateRolledBack || rolledBack.RollbackGeneration != 3 {
		t.Fatalf("rollback=%+v err=%v", rolledBack, err)
	}
	assertMovementPlacement(t, ctx, global, source, destination, rollbackAccount, "cell-us-east-01", 3, "active", "moving")

	retireMove := moveToSwitched(t, ctx, service, retireAccount, accountmovement.Move{})
	if _, err := global.Exec(ctx, `UPDATE account_moves SET rollback_expires_at=statement_timestamp()-interval '1 second' WHERE id=$1`, retireMove.ID); err != nil {
		t.Fatal(err)
	}
	retired, err := service.Retire(ctx, retireMove.ID, "operator@example.com", "retire verified source Account copy", "test")
	if err != nil || retired.State != accountmovement.StateCompleted {
		t.Fatalf("retire=%+v err=%v", retired, err)
	}
	sourceNamespace := movementCellCount(t, ctx, source, retireAccount, "account_namespaces")
	sourceWork := movementCellCount(t, ctx, source, retireAccount, "work_items")
	sourceAudit := movementCellCount(t, ctx, source, retireAccount, "work_capacity_release_operator_events")
	destinationAudit := movementCellCount(t, ctx, destination, retireAccount, "work_capacity_release_operator_events")
	if sourceNamespace != 0 || sourceWork != 0 || sourceAudit != 0 || destinationAudit != 1 {
		t.Fatalf("retired source namespace=%d work=%d audit=%d destination audit=%d", sourceNamespace, sourceWork, sourceAudit, destinationAudit)
	}
	assertMovementPlacement(t, ctx, global, source, destination, retireAccount, "cell-us-west-01", 2, "", "active")
}

func moveToSwitched(t *testing.T, ctx context.Context, service *accountmovement.Service, accountID ids.AccountID, move accountmovement.Move) accountmovement.Move {
	t.Helper()
	var err error
	if move.ID == "" {
		move, err = service.Prepare(ctx, accountmovement.PrepareCommand{AccountID: accountID, DestinationCellID: "cell-us-west-01", RollbackWindow: 5 * time.Minute, Actor: "operator@example.com", Reason: "prepare verified Account movement", Environment: "test"})
		if err != nil {
			t.Fatal(err)
		}
	}
	paused, err := service.Pause(ctx, move.ID, move.Version, true, "operator@example.com", "pause before Account movement", "test")
	if err != nil || paused.State != accountmovement.StatePaused {
		t.Fatalf("pause=%+v err=%v", paused, err)
	}
	resumeState := move.State
	move, err = service.Pause(ctx, move.ID, paused.Version, false, "operator@example.com", "resume reviewed Account movement", "test")
	if err != nil || move.State != resumeState {
		t.Fatalf("resume=%+v err=%v", move, err)
	}
	wanted := []accountmovement.State{accountmovement.StateDrained, accountmovement.StateCopied, accountmovement.StateReady, accountmovement.StateSwitched}
	if move.State == accountmovement.StateDrained {
		wanted = wanted[1:]
	}
	for _, state := range wanted {
		move, err = service.Advance(ctx, move.ID, "operator@example.com", "advance reviewed Account movement", "test")
		if err != nil || move.State != state {
			t.Fatalf("advance want=%s move=%+v err=%v", state, move, err)
		}
	}
	assertMovementPlacement(t, ctx, nil, nil, nil, accountID, "", 0, "moving", "active")
	return move
}

func assertMovementRequiresQuiescentQueues(t *testing.T, ctx context.Context, service *accountmovement.Service, global, source *pgxpool.Pool, accountID ids.AccountID, now time.Time) accountmovement.Move {
	t.Helper()
	move, err := service.Prepare(ctx, accountmovement.PrepareCommand{AccountID: accountID, DestinationCellID: "cell-us-west-01", RollbackWindow: 5 * time.Minute, Actor: "operator@example.com", Reason: "prepare queue gate Account movement", Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `INSERT INTO spyglass.work_capacity_release_queue
		(account_id,work_item_id,reservation_id,processing_state,next_attempt_at,queued_at)
		SELECT account_id,id,capacity_reservation_id,'pending',$2,$2 FROM spyglass.work_items WHERE account_id=$1`, accountID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Advance(ctx, move.ID, "operator@example.com", "verify pending release queue gate", "test"); err == nil {
		t.Fatal("Account movement accepted an unfinished capacity release")
	}
	expireMovementLease(t, ctx, global, move.ID)
	if _, err := source.Exec(ctx, `UPDATE spyglass.work_capacity_release_queue SET processing_state='completed',next_attempt_at=NULL,completed_at=$2 WHERE account_id=$1;
		INSERT INTO spyglass.runner_invocation_queue(invocation_id,account_id,profile,processing_state,next_attempt_at,queued_at)
		VALUES('91900000-0000-4000-8000-000000000001',$1,'default','queued',$2,$2)`, pgx.QueryExecModeSimpleProtocol, accountID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Advance(ctx, move.ID, "operator@example.com", "verify pending runner queue gate", "test"); err == nil {
		t.Fatal("Account movement accepted an unfinished runner invocation")
	}
	if _, err := source.Exec(ctx, `UPDATE spyglass.runner_invocation_queue SET processing_state='canceled',next_attempt_at=NULL,
		job_name='queue-gate-terminal',cancel_requested_at=$2,completed_at=$2 WHERE account_id=$1`, accountID, now); err != nil {
		t.Fatal(err)
	}
	expireMovementLease(t, ctx, global, move.ID)
	move, err = service.Advance(ctx, move.ID, "operator@example.com", "advance after queues reached terminal state", "test")
	if err != nil || move.State != accountmovement.StateDrained {
		t.Fatalf("queue-gated drain=%+v err=%v", move, err)
	}
	if _, err := source.Exec(ctx, `SELECT set_config('app.account_id',$1::text,false);
		UPDATE spyglass.work_items SET title='late stale write' WHERE account_id=$1`, pgx.QueryExecModeSimpleProtocol, accountID); err == nil {
		t.Fatal("source namespace accepted a stale durable write after drain")
	}
	return move
}

func expireMovementLease(t *testing.T, ctx context.Context, global *pgxpool.Pool, moveID string) {
	t.Helper()
	if _, err := global.Exec(ctx, `UPDATE account_moves SET lease_expires_at=statement_timestamp()-interval '1 second' WHERE id=$1`, moveID); err != nil {
		t.Fatal(err)
	}
}

func seedMovementCells(t *testing.T, ctx context.Context, global *pgxpool.Pool, now time.Time) {
	t.Helper()
	if _, err := global.Exec(ctx, `INSERT INTO cells(id,region,state,assigned_accounts,soft_account_limit,created_at,route_origin) VALUES
		('cell-us-east-01','us-east','active',0,10,$1,'https://cell-a.internal'),
		('cell-us-west-01','us-west','active',0,10,$1,'https://cell-b.internal')`, now); err != nil {
		t.Fatal(err)
	}
}

func seedMovementAccount(t *testing.T, ctx context.Context, global, source *pgxpool.Pool, accountID ids.AccountID, slug, workID string, now time.Time) {
	t.Helper()
	userID := ids.UserID(workID[:8] + "-0000-4000-8000-000000000099")
	if _, err := global.Exec(ctx, `INSERT INTO users(id,primary_email,display_name,state,email_verified_at,created_at) VALUES ($1,$2,'Movement Owner','active',$3,$3);
		INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,last_catalog_reconciled_version,version,created_by_user_id,created_at)
		VALUES ($4,$5,'Movement Account','free','active','cell-us-east-01',1,1,1,1,$1,$3);
		INSERT INTO account_directory(account_id,cell_id,placement_generation,state,data_region,updated_at) VALUES ($4,'cell-us-east-01',1,'active','us-east',$3);
		UPDATE cells SET assigned_accounts=assigned_accounts+1 WHERE id='cell-us-east-01'`, pgx.QueryExecModeSimpleProtocol, userID, slug+"@example.com", now, accountID, slug); err != nil {
		t.Fatal(err)
	}
	reservationID := workID[:8] + "-0000-4000-8000-000000000002"
	if _, err := source.Exec(ctx, `SELECT set_config('app.account_id',$1::text,false);
		INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2);
		INSERT INTO spyglass.work_item_number_counters(account_id,next_number) VALUES ($1,2);
		INSERT INTO spyglass.work_items(account_id,id,number,depth,kind,title,description,state,priority,responsibility,source,
			created_by_actor_kind,created_by_actor_id,completed_at,capacity_reservation_id,capacity_released_at,version,created_at,updated_at)
		VALUES ($1,$3,1,0,'todo','Move fixture','Cross-cell copy evidence','done','normal','shared','manual','user','movement-test',$2,$4,$2,1,$2,$2)`,
		pgx.QueryExecModeSimpleProtocol, accountID, now, workID, reservationID); err != nil {
		t.Fatal(err)
	}
	batchID := workID[:8] + "-0000-4000-8000-000000000003"
	if _, err := source.Exec(ctx, `INSERT INTO spyglass.work_capacity_release_operator_events
		(batch_id,event_sequence,action,account_id,work_item_id,reservation_id,actor,reason,environment,previous_attempt_count,created_at)
		VALUES($1,0,'requeued',$2,$3,$4,'operator@example.com','seed Account movement immutable audit','test',1,$5)`,
		batchID, accountID, workID, reservationID, now); err != nil {
		t.Fatal(err)
	}
}

func assertMovementPlacement(t *testing.T, ctx context.Context, global, source, destination *pgxpool.Pool, accountID ids.AccountID, cell string, generation uint64, sourceState, destinationState string) {
	t.Helper()
	if global != nil {
		var actualCell string
		var actualGeneration uint64
		if err := global.QueryRow(ctx, `SELECT cell_id,placement_generation FROM account_directory WHERE account_id=$1`, accountID).Scan(&actualCell, &actualGeneration); err != nil || actualCell != cell || actualGeneration != generation {
			t.Fatalf("global placement cell=%s generation=%d err=%v", actualCell, actualGeneration, err)
		}
	}
	for _, check := range []struct {
		pool  *pgxpool.Pool
		state string
	}{{source, sourceState}, {destination, destinationState}} {
		if check.pool == nil || check.state == "" {
			continue
		}
		connection, err := check.pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = connection.Exec(ctx, `SELECT set_config('app.account_id',$1::text,false)`, accountID); err != nil {
			connection.Release()
			t.Fatal(err)
		}
		var actual string
		err = connection.QueryRow(ctx, `SELECT state FROM spyglass.account_namespaces WHERE account_id=$1`, accountID).Scan(&actual)
		connection.Release()
		if err != nil || actual != check.state {
			t.Fatalf("cell namespace state=%s want=%s err=%v", actual, check.state, err)
		}
	}
}

func movementCellCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID ids.AccountID, table string) int {
	t.Helper()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SELECT set_config('app.account_id',$1::text,false)`, accountID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM spyglass.`+table+` WHERE account_id=$1`, accountID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
