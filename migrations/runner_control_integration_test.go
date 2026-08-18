package migrations_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestRunnerControlFairnessRecoveryAndLeastPrivilege(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatalf("apply cell migrations: %v", err)
	}

	now := time.Date(2026, 8, 18, 20, 0, 0, 0, time.UTC)
	accountA := ids.AccountID("11000000-0000-4000-8000-000000000001")
	accountB := ids.AccountID("22000000-0000-4000-8000-000000000002")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces
		(account_id,placement_generation,state,created_at) VALUES
		($1,1,'active',$3),($2,1,'active',$3)`, accountA, accountB, now); err != nil {
		t.Fatalf("seed runner Accounts: %v", err)
	}

	producerRole := "spyglass_runner_producer_" + randomSuffix(t)
	controllerRole := "spyglass_runner_controller_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+producerRole+` NOLOGIN;
		CREATE ROLE `+controllerRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA public TO `+producerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_configure_runner_account(uuid,integer,integer,timestamptz) TO `+producerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_enqueue_runner_invocation(uuid,uuid,text,timestamptz) TO `+producerRole+`;
		GRANT USAGE ON SCHEMA spyglass TO `+controllerRole+`;
		GRANT SELECT,UPDATE ON spyglass.runner_account_scheduling TO `+controllerRole+`;
		GRANT SELECT,UPDATE ON spyglass.runner_invocation_queue TO `+controllerRole); err != nil {
		t.Fatalf("create split runner roles: %v", err)
	}
	producer := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+producerRole)
		return err
	})
	controller := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+controllerRole)
		return err
	})
	defer func() {
		producer.Close()
		controller.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+producerRole+`; DROP OWNED BY `+controllerRole+`; DROP ROLE IF EXISTS `+producerRole+`; DROP ROLE IF EXISTS `+controllerRole)
	}()

	var forbiddenCount int
	if err := controller.QueryRow(ctx, `SELECT count(*) FROM spyglass.account_namespaces`).Scan(&forbiddenCount); err == nil {
		t.Fatal("runner controller read Account namespaces")
	}
	if err := controller.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_items`).Scan(&forbiddenCount); err == nil {
		t.Fatal("runner controller read customer Work")
	}
	if _, err := controller.Exec(ctx, `INSERT INTO spyglass.runner_invocation_queue(invocation_id,account_id,profile,processing_state,next_attempt_at,queued_at) VALUES ('34000000-0000-4000-8000-000000000004',$1,'agent-small','queued',$2,$2)`, accountA, now); err == nil {
		t.Fatal("runner controller manufactured an invocation")
	}
	if err := producer.QueryRow(ctx, `SELECT count(*) FROM spyglass.runner_invocation_queue`).Scan(&forbiddenCount); err == nil {
		t.Fatal("runner producer directly read the queue")
	}

	producerQueue, err := postgresadapter.NewRunnerControlQueue(producer, ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	controllerQueue, err := postgresadapter.NewRunnerControlQueue(controller, ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []runnercontrol.AccountPolicy{
		{AccountID: accountA, Weight: 1, ConcurrencyLimit: 1},
		{AccountID: accountB, Weight: 1, ConcurrencyLimit: 1},
	} {
		if err := producerQueue.Configure(ctx, policy, now); err != nil {
			t.Fatalf("configure runner Account %s: %v", policy.AccountID, err)
		}
	}

	invocationA1 := runnercontrol.Invocation{ID: "31000000-0000-4000-8000-000000000001", AccountID: accountA, Profile: "agent-small", QueuedAt: now}
	invocationA2 := runnercontrol.Invocation{ID: "32000000-0000-4000-8000-000000000002", AccountID: accountA, Profile: "agent-small", QueuedAt: now}
	invocationB1 := runnercontrol.Invocation{ID: "33000000-0000-4000-8000-000000000003", AccountID: accountB, Profile: "agent-small", QueuedAt: now}
	for _, invocation := range []runnercontrol.Invocation{invocationA1, invocationA2, invocationB1} {
		created, err := producerQueue.Enqueue(ctx, invocation)
		if err != nil || !created {
			t.Fatalf("enqueue runner invocation %s: created=%v err=%v", invocation.ID, created, err)
		}
	}
	if created, err := producerQueue.Enqueue(ctx, invocationA1); err != nil || created {
		t.Fatalf("idempotent runner enqueue: created=%v err=%v", created, err)
	}
	conflict := invocationA1
	conflict.AccountID = accountB
	if _, err := producerQueue.Enqueue(ctx, conflict); !errors.Is(err, runnercontrol.ErrInvocationConflict) {
		t.Fatalf("conflicting runner enqueue: %v", err)
	}

	const lease = 30 * time.Second
	first, found, err := controllerQueue.ClaimFair(ctx, now, lease)
	if err != nil || !found || first.AccountID != accountA || first.ID != invocationA1.ID || first.AttemptCount != 1 {
		t.Fatalf("first fair claim=%+v found=%v err=%v", first, found, err)
	}
	if err := controllerQueue.MarkLaunched(ctx, first, "runner-a1", now); err != nil {
		t.Fatal(err)
	}
	second, found, err := controllerQueue.ClaimFair(ctx, now, lease)
	if err != nil || !found || second.AccountID != accountB || second.ID != invocationB1.ID {
		t.Fatalf("noisy-neighbor claim=%+v found=%v err=%v", second, found, err)
	}
	if err := controllerQueue.MarkLaunched(ctx, second, "runner-b1", now); err != nil {
		t.Fatal(err)
	}
	if blocked, found, err := controllerQueue.ClaimFair(ctx, now, lease); err != nil || found {
		t.Fatalf("capacity-exhausted claim=%+v found=%v err=%v", blocked, found, err)
	}

	if err := controllerQueue.Complete(ctx, first.ID, "runner-a1", "completed", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, first.ID, "runner-a1", "completed", now.Add(2*time.Second)); err != nil {
		t.Fatalf("idempotent runner completion: %v", err)
	}
	third, found, err := controllerQueue.ClaimFair(ctx, now.Add(2*time.Second), lease)
	if err != nil || !found || third.ID != invocationA2.ID || third.AttemptCount != 1 {
		t.Fatalf("released-capacity claim=%+v found=%v err=%v", third, found, err)
	}

	reclaimed, found, err := controllerQueue.ClaimFair(ctx, now.Add(33*time.Second), lease)
	if err != nil || !found || reclaimed.ID != third.ID || reclaimed.AttemptCount != 2 || reclaimed.LeaseID == third.LeaseID {
		t.Fatalf("expired launch reclaim=%+v found=%v err=%v", reclaimed, found, err)
	}
	if err := controllerQueue.FailLaunch(ctx, third, now.Add(34*time.Second), "stale_controller", false); !errors.Is(err, runnercontrol.ErrLeaseLost) {
		t.Fatalf("stale launch lease result=%v", err)
	}
	if err := controllerQueue.MarkLaunched(ctx, reclaimed, "runner-a2", now.Add(33*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, reclaimed.ID, "runner-a2", "execution_failed", now.Add(34*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, second.ID, "runner-b1", "canceled", now.Add(34*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.account_namespaces SET state='frozen' WHERE account_id=$1`, accountB); err != nil {
		t.Fatal(err)
	}
	if _, err := producerQueue.Enqueue(ctx, runnercontrol.Invocation{ID: "35000000-0000-4000-8000-000000000005", AccountID: accountB, Profile: "agent-small", QueuedAt: now.Add(35 * time.Second)}); err == nil {
		t.Fatal("runner producer enqueued work for a frozen Account")
	}

	parallelA1 := runnercontrol.Invocation{ID: "36000000-0000-4000-8000-000000000006", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(35 * time.Second)}
	parallelA2 := runnercontrol.Invocation{ID: "37000000-0000-4000-8000-000000000007", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(35 * time.Second)}
	for _, invocation := range []runnercontrol.Invocation{parallelA1, parallelA2} {
		if created, err := producerQueue.Enqueue(ctx, invocation); err != nil || !created {
			t.Fatalf("enqueue parallel runner %s: created=%v err=%v", invocation.ID, created, err)
		}
	}
	type claimResult struct {
		invocation runnercontrol.Invocation
		found      bool
		err        error
	}
	claims := make(chan claimResult, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			invocation, found, err := controllerQueue.ClaimFair(ctx, now.Add(35*time.Second), lease)
			claims <- claimResult{invocation: invocation, found: found, err: err}
		}()
	}
	group.Wait()
	close(claims)
	var parallelClaim runnercontrol.Invocation
	foundCount := 0
	for result := range claims {
		if result.err != nil {
			t.Fatalf("parallel runner claim: %v", result.err)
		}
		if result.found {
			foundCount++
			parallelClaim = result.invocation
		}
	}
	if foundCount != 1 {
		t.Fatalf("parallel runner claims=%d want=1", foundCount)
	}
	if err := controllerQueue.MarkLaunched(ctx, parallelClaim, "runner-parallel-1", now.Add(35*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, parallelClaim.ID, "runner-parallel-1", "completed", now.Add(36*time.Second)); err != nil {
		t.Fatal(err)
	}
	last, found, err := controllerQueue.ClaimFair(ctx, now.Add(36*time.Second), lease)
	if err != nil || !found {
		t.Fatalf("second parallel runner claim=%+v found=%v err=%v", last, found, err)
	}
	if err := controllerQueue.MarkLaunched(ctx, last, "runner-parallel-2", now.Add(36*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, last.ID, "runner-parallel-2", "completed", now.Add(37*time.Second)); err != nil {
		t.Fatal(err)
	}

	stats, err := controllerQueue.Stats(ctx, now.Add(38*time.Second))
	if err != nil || stats.Ready != 0 || stats.Launching != 0 || stats.Launched != 0 || stats.DeadLetter != 0 {
		t.Fatalf("terminal runner stats=%+v err=%v", stats, err)
	}
	var active int
	if err := controller.QueryRow(ctx, `SELECT coalesce(sum(active_count),0) FROM spyglass.runner_account_scheduling`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("runner active capacity=%d err=%v", active, err)
	}
}
