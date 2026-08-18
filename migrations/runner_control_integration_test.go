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
		GRANT EXECUTE ON FUNCTION public.spyglass_cancel_runner_invocation(uuid,uuid,timestamptz) TO `+producerRole+`;
		GRANT USAGE ON SCHEMA public TO `+controllerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_prune_runner_terminal_payloads(timestamptz,timestamptz,integer) TO `+controllerRole+`;
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
	if err := controller.QueryRow(ctx, `SELECT count(*) FROM spyglass.runner_invocation_exchanges`).Scan(&forbiddenCount); err == nil {
		t.Fatal("runner controller directly read encrypted exchanges")
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
	inspectedFirst, err := controllerQueue.ClaimReconciliationCandidates(ctx, now, runnercontrol.InspectionInterval, 1)
	if err != nil || len(inspectedFirst) != 1 || inspectedFirst[0].ID != first.ID {
		t.Fatalf("first inspection claim=%+v err=%v", inspectedFirst, err)
	}
	inspectedSecond, err := controllerQueue.ClaimReconciliationCandidates(ctx, now, runnercontrol.InspectionInterval, 1)
	if err != nil || len(inspectedSecond) != 1 || inspectedSecond[0].ID != second.ID {
		t.Fatalf("second inspection claim=%+v err=%v", inspectedSecond, err)
	}
	if inspectedAgain, err := controllerQueue.ClaimReconciliationCandidates(ctx, now, runnercontrol.InspectionInterval, 10); err != nil || len(inspectedAgain) != 0 {
		t.Fatalf("duplicate inspection claim=%+v err=%v", inspectedAgain, err)
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
	if state, err := producerQueue.RequestCancellation(ctx, accountA, first.ID, now.Add(2*time.Second)); err != nil || state != "completed" {
		t.Fatalf("completion-wins cancellation state=%q err=%v", state, err)
	}
	retentionTx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retentionTx.Exec(ctx, `SELECT set_config('app.account_id',$1::text,true)`, accountA); err != nil {
		t.Fatal(err)
	}
	requestDigest := make([]byte, 32)
	resultDigest := make([]byte, 32)
	requestDigest[0], resultDigest[0] = 1, 2
	if _, err := retentionTx.Exec(ctx, `INSERT INTO spyglass.runner_invocation_exchanges
		(account_id,invocation_id,request_ciphertext,request_nonce,request_key_version,request_digest,request_expires_at,
		 bound_pod_uid,bound_at,last_fetched_at,fetch_count,result_outcome,result_ciphertext,result_nonce,result_key_version,result_digest,result_submitted_at,created_at)
		VALUES ($1,$2,decode(repeat('aa',17),'hex'),decode(repeat('bb',12),'hex'),7,$3,$4,
		'35000000-0000-4000-8000-000000000005',$5,$5,1,'completed',decode(repeat('cc',17),'hex'),decode(repeat('dd',12),'hex'),7,$6,$5,$5)`,
		accountA, first.ID, requestDigest, now.Add(time.Hour), now, resultDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := retentionTx.Exec(ctx, `INSERT INTO spyglass.runner_invocation_exchanges
		(account_id,invocation_id,request_ciphertext,request_nonce,request_key_version,request_digest,request_expires_at,created_at)
		VALUES ($1,$2,decode(repeat('aa',17),'hex'),decode(repeat('bb',12),'hex'),7,$3,$4,$5)`,
		accountA, invocationA2.ID, requestDigest, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if err := retentionTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	pruned, err := controllerQueue.PruneTerminalPayloads(ctx, now.Add(2*time.Second), now.Add(3*time.Second), 100)
	if err != nil || pruned != 1 {
		t.Fatalf("terminal payload prune count=%d err=%v", pruned, err)
	}
	if pruned, err := controllerQueue.PruneTerminalPayloads(ctx, now.Add(2*time.Second), now.Add(4*time.Second), 100); err != nil || pruned != 0 {
		t.Fatalf("idempotent terminal payload prune count=%d err=%v", pruned, err)
	}
	verificationTx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer verificationTx.Rollback(context.Background())
	if _, err := verificationTx.Exec(ctx, `SELECT set_config('app.account_id',$1::text,true)`, accountA); err != nil {
		t.Fatal(err)
	}
	var requestBytes, resultBytes int
	var storedRequestDigest, storedResultDigest []byte
	var payloadPurgedAt time.Time
	if err := verificationTx.QueryRow(ctx, `SELECT octet_length(request_ciphertext),octet_length(result_ciphertext),request_digest,result_digest,terminal_payload_purged_at
		FROM spyglass.runner_invocation_exchanges WHERE account_id=$1 AND invocation_id=$2`, accountA, first.ID).Scan(&requestBytes, &resultBytes, &storedRequestDigest, &storedResultDigest, &payloadPurgedAt); err != nil {
		t.Fatal(err)
	}
	if requestBytes != 17 || resultBytes != 17 || storedRequestDigest[0] != 1 || storedResultDigest[0] != 2 || !payloadPurgedAt.Equal(now.Add(3*time.Second)) {
		t.Fatalf("retained terminal metadata request=%d result=%d request_digest=%x result_digest=%x purged=%v", requestBytes, resultBytes, storedRequestDigest, storedResultDigest, payloadPurgedAt)
	}
	var requestSentinel, resultSentinel bool
	if err := verificationTx.QueryRow(ctx, `SELECT request_ciphertext=decode(repeat('00',17),'hex'),result_ciphertext=decode(repeat('00',17),'hex')
		FROM spyglass.runner_invocation_exchanges WHERE account_id=$1 AND invocation_id=$2`, accountA, first.ID).Scan(&requestSentinel, &resultSentinel); err != nil || !requestSentinel || !resultSentinel {
		t.Fatalf("terminal envelopes were not destroyed request=%v result=%v err=%v", requestSentinel, resultSentinel, err)
	}
	var liveUntouched bool
	var livePurgedAt *time.Time
	if err := verificationTx.QueryRow(ctx, `SELECT request_ciphertext=decode(repeat('aa',17),'hex'),terminal_payload_purged_at
		FROM spyglass.runner_invocation_exchanges WHERE account_id=$1 AND invocation_id=$2`, accountA, invocationA2.ID).Scan(&liveUntouched, &livePurgedAt); err != nil || !liveUntouched || livePurgedAt != nil {
		t.Fatalf("nonterminal envelope changed untouched=%v purged=%v err=%v", liveUntouched, livePurgedAt, err)
	}
	if err := verificationTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	third, found, err := controllerQueue.ClaimFair(ctx, now.Add(2*time.Second), lease)
	if err != nil || !found || third.ID != invocationA2.ID || third.AttemptCount != 1 {
		t.Fatalf("released-capacity claim=%+v found=%v err=%v", third, found, err)
	}

	reclaimed, found, err := controllerQueue.ClaimFair(ctx, now.Add(33*time.Second), lease)
	if err != nil || !found || reclaimed.ID != third.ID || reclaimed.AttemptCount != 2 || reclaimed.LeaseID == third.LeaseID {
		t.Fatalf("expired launch reclaim=%+v found=%v err=%v", reclaimed, found, err)
	}
	if err := controllerQueue.FailLaunch(ctx, third, now.Add(34*time.Second), now.Add(35*time.Second), "stale_controller", false); !errors.Is(err, runnercontrol.ErrLeaseLost) {
		t.Fatalf("stale launch lease result=%v", err)
	}
	if err := controllerQueue.MarkLaunched(ctx, reclaimed, "runner-a2", now.Add(33*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, reclaimed.ID, "runner-a2", "execution_failed", now.Add(34*time.Second)); err != nil {
		t.Fatal(err)
	}
	if state, err := producerQueue.RequestCancellation(ctx, accountB, second.ID, now.Add(34*time.Second)); err != nil || state != "canceling" {
		t.Fatalf("request runner-b1 cancellation state=%q err=%v", state, err)
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

	queuedCancellation := runnercontrol.Invocation{ID: "41000000-0000-4000-8000-000000000001", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(40 * time.Second)}
	if created, err := producerQueue.Enqueue(ctx, queuedCancellation); err != nil || !created {
		t.Fatalf("enqueue queued cancellation: created=%v err=%v", created, err)
	}
	if state, err := producerQueue.RequestCancellation(ctx, accountA, queuedCancellation.ID, now.Add(41*time.Second)); err != nil || state != "canceled" {
		t.Fatalf("queued cancellation state=%q err=%v", state, err)
	}
	if state, err := producerQueue.RequestCancellation(ctx, accountA, queuedCancellation.ID, now.Add(42*time.Second)); err != nil || state != "canceled" {
		t.Fatalf("idempotent queued cancellation state=%q err=%v", state, err)
	}
	if _, err := producerQueue.RequestCancellation(ctx, accountB, queuedCancellation.ID, now.Add(42*time.Second)); !errors.Is(err, runnercontrol.ErrInvocationNotFound) {
		t.Fatalf("cross-Account cancellation result=%v", err)
	}
	if _, err := producerQueue.RequestCancellation(ctx, accountA, "49000000-0000-4000-8000-000000000009", now.Add(42*time.Second)); !errors.Is(err, runnercontrol.ErrInvocationNotFound) {
		t.Fatalf("unknown cancellation result=%v", err)
	}
	var queuedState string
	var queuedJob *string
	var queuedCompleted, queuedRequested *time.Time
	if err := controller.QueryRow(ctx, `SELECT processing_state,job_name,completed_at,cancel_requested_at FROM spyglass.runner_invocation_queue WHERE invocation_id=$1`, queuedCancellation.ID).Scan(&queuedState, &queuedJob, &queuedCompleted, &queuedRequested); err != nil || queuedState != "canceled" || queuedJob != nil || queuedCompleted == nil || queuedRequested == nil {
		t.Fatalf("queued cancellation state=%q job=%v completed=%v requested=%v err=%v", queuedState, queuedJob, queuedCompleted, queuedRequested, err)
	}
	if _, err := controllerQueue.RequestCancellation(ctx, accountA, queuedCancellation.ID, now.Add(42*time.Second)); err == nil {
		t.Fatal("runner controller executed serving-side cancellation authority")
	}

	launchedCancellation := runnercontrol.Invocation{ID: "42000000-0000-4000-8000-000000000002", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(43 * time.Second)}
	if created, err := producerQueue.Enqueue(ctx, launchedCancellation); err != nil || !created {
		t.Fatalf("enqueue launched cancellation: created=%v err=%v", created, err)
	}
	launchedClaim, found, err := controllerQueue.ClaimFair(ctx, now.Add(43*time.Second), lease)
	if err != nil || !found || launchedClaim.ID != launchedCancellation.ID {
		t.Fatalf("claim launched cancellation=%+v found=%v err=%v", launchedClaim, found, err)
	}
	if err := controllerQueue.MarkLaunched(ctx, launchedClaim, "runner-cancel-launched", now.Add(43*time.Second)); err != nil {
		t.Fatal(err)
	}
	if state, err := producerQueue.RequestCancellation(ctx, accountA, launchedCancellation.ID, now.Add(44*time.Second)); err != nil || state != "canceling" {
		t.Fatalf("launched cancellation state=%q err=%v", state, err)
	}
	if stats, err := controllerQueue.Stats(ctx, now.Add(44*time.Second)); err != nil || stats.Canceling != 1 {
		t.Fatalf("canceling stats=%+v err=%v", stats, err)
	}
	var accountActive int
	if err := controller.QueryRow(ctx, `SELECT active_count FROM spyglass.runner_account_scheduling WHERE account_id=$1`, accountA).Scan(&accountActive); err != nil || accountActive != 1 {
		t.Fatalf("capacity released before Job absence: active=%d err=%v", accountActive, err)
	}
	candidates, err := controllerQueue.ClaimReconciliationCandidates(ctx, now.Add(44*time.Second), runnercontrol.InspectionInterval, 10)
	if err != nil || len(candidates) != 1 || candidates[0].ID != launchedCancellation.ID || candidates[0].State != "canceling" || candidates[0].CancelRequestedAt == nil {
		t.Fatalf("canceling reconciliation candidates=%+v err=%v", candidates, err)
	}
	if err := controllerQueue.Complete(ctx, launchedCancellation.ID, "runner-cancel-launched", "canceled", now.Add(45*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, launchedCancellation.ID, "runner-cancel-launched", "canceled", now.Add(46*time.Second)); err != nil {
		t.Fatalf("idempotent canceled completion: %v", err)
	}

	launchingCancellation := runnercontrol.Invocation{ID: "43000000-0000-4000-8000-000000000003", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(47 * time.Second)}
	if created, err := producerQueue.Enqueue(ctx, launchingCancellation); err != nil || !created {
		t.Fatalf("enqueue launching cancellation: created=%v err=%v", created, err)
	}
	launchingClaim, found, err := controllerQueue.ClaimFair(ctx, now.Add(47*time.Second), lease)
	if err != nil || !found || launchingClaim.ID != launchingCancellation.ID {
		t.Fatalf("claim launching cancellation=%+v found=%v err=%v", launchingClaim, found, err)
	}
	if state, err := producerQueue.RequestCancellation(ctx, accountA, launchingCancellation.ID, now.Add(48*time.Second)); err != nil || state != "launching" {
		t.Fatalf("launching cancellation state=%q err=%v", state, err)
	}
	if err := controllerQueue.MarkLaunched(ctx, launchingClaim, "runner-cancel-launching", now.Add(49*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, launchingCancellation.ID, "runner-cancel-launching", "canceled", now.Add(50*time.Second)); err != nil {
		t.Fatal(err)
	}

	failedCancellation := runnercontrol.Invocation{ID: "44000000-0000-4000-8000-000000000004", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(51 * time.Second)}
	if created, err := producerQueue.Enqueue(ctx, failedCancellation); err != nil || !created {
		t.Fatalf("enqueue launch-failure cancellation: created=%v err=%v", created, err)
	}
	failedClaim, found, err := controllerQueue.ClaimFair(ctx, now.Add(51*time.Second), lease)
	if err != nil || !found || failedClaim.ID != failedCancellation.ID {
		t.Fatalf("claim launch-failure cancellation=%+v found=%v err=%v", failedClaim, found, err)
	}
	if _, err := producerQueue.RequestCancellation(ctx, accountA, failedCancellation.ID, now.Add(52*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.FailLaunch(ctx, failedClaim, now.Add(53*time.Second), now.Add(54*time.Second), "cluster_unavailable", false); err != nil {
		t.Fatalf("cancel requested launch failure: %v", err)
	}
	if err := controller.QueryRow(ctx, `SELECT processing_state FROM spyglass.runner_invocation_queue WHERE invocation_id=$1`, failedCancellation.ID).Scan(&queuedState); err != nil || queuedState != "canceled" {
		t.Fatalf("launch-failure cancellation state=%q err=%v", queuedState, err)
	}

	staleCancellation := runnercontrol.Invocation{ID: "45000000-0000-4000-8000-000000000005", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(56 * time.Second)}
	if created, err := producerQueue.Enqueue(ctx, staleCancellation); err != nil || !created {
		t.Fatalf("enqueue stale-controller cancellation: created=%v err=%v", created, err)
	}
	staleClaim, found, err := controllerQueue.ClaimFair(ctx, now.Add(56*time.Second), lease)
	if err != nil || !found || staleClaim.ID != staleCancellation.ID {
		t.Fatalf("first stale-controller claim=%+v found=%v err=%v", staleClaim, found, err)
	}
	currentClaim, found, err := controllerQueue.ClaimFair(ctx, now.Add(87*time.Second), lease)
	if err != nil || !found || currentClaim.ID != staleCancellation.ID || currentClaim.LeaseID == staleClaim.LeaseID {
		t.Fatalf("reclaimed cancellation=%+v found=%v err=%v", currentClaim, found, err)
	}
	if _, err := producerQueue.RequestCancellation(ctx, accountA, staleCancellation.ID, now.Add(88*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.MarkLaunched(ctx, staleClaim, "runner-stale-cancel", now.Add(88*time.Second)); !errors.Is(err, runnercontrol.ErrLeaseLost) {
		t.Fatalf("stale controller overrode cancellation: %v", err)
	}
	if err := controllerQueue.MarkLaunched(ctx, currentClaim, "runner-current-cancel", now.Add(89*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, staleCancellation.ID, "runner-current-cancel", "canceled", now.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}

	uncertainLaunch := runnercontrol.Invocation{ID: "46000000-0000-4000-8000-000000000006", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(92 * time.Second)}
	if created, err := producerQueue.Enqueue(ctx, uncertainLaunch); err != nil || !created {
		t.Fatalf("enqueue uncertain launch: created=%v err=%v", created, err)
	}
	uncertainClaim, found, err := controllerQueue.ClaimFair(ctx, now.Add(92*time.Second), lease)
	if err != nil || !found || uncertainClaim.ID != uncertainLaunch.ID {
		t.Fatalf("claim uncertain launch=%+v found=%v err=%v", uncertainClaim, found, err)
	}
	if err := controllerQueue.MarkLaunchUncertain(ctx, uncertainClaim, "runner-uncertain", now.Add(92*time.Second), "launcher_ambiguous"); err != nil {
		t.Fatal(err)
	}
	if stats, err := controllerQueue.Stats(ctx, now.Add(92*time.Second)); err != nil || stats.LaunchUncertain != 1 {
		t.Fatalf("uncertain launch stats=%+v err=%v", stats, err)
	}
	if err := controller.QueryRow(ctx, `SELECT active_count FROM spyglass.runner_account_scheduling WHERE account_id=$1`, accountA).Scan(&accountActive); err != nil || accountActive != 1 {
		t.Fatalf("uncertain launch released capacity: active=%d err=%v", accountActive, err)
	}
	uncertainCandidates, err := controllerQueue.ClaimReconciliationCandidates(ctx, now.Add(92*time.Second), runnercontrol.InspectionInterval, 10)
	if err != nil || len(uncertainCandidates) != 1 || uncertainCandidates[0].ID != uncertainLaunch.ID || uncertainCandidates[0].State != "launch_uncertain" || uncertainCandidates[0].LaunchedAt != nil {
		t.Fatalf("uncertain candidates=%+v err=%v", uncertainCandidates, err)
	}
	if err := controllerQueue.ResolveLaunchAbsent(ctx, uncertainCandidates[0], now.Add(93*time.Second), now.Add(94*time.Second), "launcher_not_observed", false); err != nil {
		t.Fatal(err)
	}
	if err := controller.QueryRow(ctx, `SELECT active_count FROM spyglass.runner_account_scheduling WHERE account_id=$1`, accountA).Scan(&accountActive); err != nil || accountActive != 0 {
		t.Fatalf("observed absence did not release capacity: active=%d err=%v", accountActive, err)
	}
	retryClaim, found, err := controllerQueue.ClaimFair(ctx, now.Add(94*time.Second), lease)
	if err != nil || !found || retryClaim.ID != uncertainLaunch.ID || retryClaim.AttemptCount != 2 {
		t.Fatalf("retry absent uncertain launch=%+v found=%v err=%v", retryClaim, found, err)
	}
	if err := controllerQueue.MarkLaunched(ctx, retryClaim, "runner-uncertain", now.Add(94*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.Complete(ctx, uncertainLaunch.ID, "runner-uncertain", "completed", now.Add(95*time.Second)); err != nil {
		t.Fatal(err)
	}

	uncertainCancellation := runnercontrol.Invocation{ID: "47000000-0000-4000-8000-000000000007", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(96 * time.Second)}
	if created, err := producerQueue.Enqueue(ctx, uncertainCancellation); err != nil || !created {
		t.Fatalf("enqueue uncertain cancellation: created=%v err=%v", created, err)
	}
	uncertainCancelClaim, found, err := controllerQueue.ClaimFair(ctx, now.Add(96*time.Second), lease)
	if err != nil || !found || uncertainCancelClaim.ID != uncertainCancellation.ID {
		t.Fatalf("claim uncertain cancellation=%+v found=%v err=%v", uncertainCancelClaim, found, err)
	}
	if err := controllerQueue.MarkLaunchUncertain(ctx, uncertainCancelClaim, "runner-uncertain-cancel", now.Add(96*time.Second), "launcher_ambiguous"); err != nil {
		t.Fatal(err)
	}
	if state, err := producerQueue.RequestCancellation(ctx, accountA, uncertainCancellation.ID, now.Add(97*time.Second)); err != nil || state != "canceling" {
		t.Fatalf("uncertain cancellation state=%q err=%v", state, err)
	}
	if err := controller.QueryRow(ctx, `SELECT active_count FROM spyglass.runner_account_scheduling WHERE account_id=$1`, accountA).Scan(&accountActive); err != nil || accountActive != 1 {
		t.Fatalf("uncertain cancellation released capacity early: active=%d err=%v", accountActive, err)
	}
	if err := controllerQueue.Complete(ctx, uncertainCancellation.ID, "runner-uncertain-cancel", "canceled", now.Add(98*time.Second)); err != nil {
		t.Fatal(err)
	}
	uncertainObserved := runnercontrol.Invocation{ID: "48000000-0000-4000-8000-000000000008", AccountID: accountA, Profile: "agent-small", QueuedAt: now.Add(99 * time.Second)}
	if created, err := producerQueue.Enqueue(ctx, uncertainObserved); err != nil || !created {
		t.Fatalf("enqueue observed uncertain launch: created=%v err=%v", created, err)
	}
	observedClaim, found, err := controllerQueue.ClaimFair(ctx, now.Add(99*time.Second), lease)
	if err != nil || !found || observedClaim.ID != uncertainObserved.ID {
		t.Fatalf("claim observed uncertain launch=%+v found=%v err=%v", observedClaim, found, err)
	}
	if err := controllerQueue.MarkLaunchUncertain(ctx, observedClaim, "runner-uncertain-observed", now.Add(99*time.Second), "launcher_ambiguous"); err != nil {
		t.Fatal(err)
	}
	observedCandidates, err := controllerQueue.ClaimReconciliationCandidates(ctx, now.Add(99*time.Second), runnercontrol.InspectionInterval, 10)
	if err != nil || len(observedCandidates) != 1 || observedCandidates[0].ID != uncertainObserved.ID {
		t.Fatalf("observed uncertain candidates=%+v err=%v", observedCandidates, err)
	}
	if err := controllerQueue.ConfirmLaunch(ctx, observedCandidates[0], now.Add(100*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := controllerQueue.ConfirmLaunch(ctx, observedCandidates[0], now.Add(101*time.Second)); err != nil {
		t.Fatalf("idempotent uncertain confirmation: %v", err)
	}
	if err := controllerQueue.Complete(ctx, uncertainObserved.ID, "runner-uncertain-observed", "completed", now.Add(102*time.Second)); err != nil {
		t.Fatal(err)
	}

	stats, err := controllerQueue.Stats(ctx, now.Add(103*time.Second))
	if err != nil || stats.Ready != 0 || stats.Launching != 0 || stats.LaunchUncertain != 0 || stats.Launched != 0 || stats.Canceling != 0 || stats.DeadLetter != 0 {
		t.Fatalf("terminal runner stats=%+v err=%v", stats, err)
	}
	var active int
	if err := controller.QueryRow(ctx, `SELECT coalesce(sum(active_count),0) FROM spyglass.runner_account_scheduling`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("runner active capacity=%d err=%v", active, err)
	}
}
