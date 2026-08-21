package migrations_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/runneraction"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestRunnerActionLedgerExecutesOnceAndReconcilesUncertainRetries(t *testing.T) {
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
	now := time.Date(2026, 8, 18, 18, 0, 0, 0, time.UTC)
	accountA := ids.AccountID("10000000-0000-4000-8000-000000000001")
	accountB := ids.AccountID("10000000-0000-4000-8000-000000000002")
	invocation := "20000000-0000-4000-8000-000000000001"
	podUID := "30000000-0000-4000-8000-000000000001"
	seedRunnerActionInvocation(t, ctx, owner, accountA, invocation, podUID, now)
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2)`, accountB, now); err != nil {
		t.Fatal(err)
	}

	actionRole := "spyglass_runner_action_" + randomSuffix(t)
	approvalRole := "spyglass_runner_approval_" + randomSuffix(t)
	resolutionRole := "spyglass_runner_resolution_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+actionRole+` NOLOGIN NOBYPASSRLS; CREATE ROLE `+approvalRole+` NOLOGIN NOBYPASSRLS;
		CREATE ROLE `+resolutionRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public TO `+actionRole+`,`+approvalRole+`,`+resolutionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_begin_runner_action_v2(uuid,uuid,uuid,uuid,text,bytea,uuid,timestamptz,timestamptz) TO `+actionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_complete_runner_action_v2(uuid,uuid,uuid,uuid,text,bytea,text,uuid,timestamptz,text,text,timestamptz) TO `+actionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_record_runner_action_authorization(uuid,uuid,uuid,uuid,text,bytea,smallint,bytea,text,text,uuid,bigint,timestamptz,timestamptz) TO `+approvalRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_cancel_runner_action_authorization(uuid,uuid,uuid,timestamptz) TO `+approvalRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_request_runner_action_resolution(uuid,uuid,uuid,text,bytea,uuid,timestamptz) TO `+resolutionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_confirm_runner_action_resolution(uuid,uuid,uuid,uuid,timestamptz) TO `+resolutionRole); err != nil {
		t.Fatal(err)
	}
	actionPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+actionRole)
		return err
	})
	approvalPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+approvalRole)
		return err
	})
	resolutionPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+resolutionRole)
		return err
	})
	defer func() {
		actionPool.Close()
		approvalPool.Close()
		resolutionPool.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+actionRole+`; DROP OWNED BY `+approvalRole+`; DROP OWNED BY `+resolutionRole+`;
			DROP ROLE IF EXISTS `+actionRole+`; DROP ROLE IF EXISTS `+approvalRole+`; DROP ROLE IF EXISTS `+resolutionRole)
	}()
	if err := actionPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.runner_action_ledger`).Scan(new(int)); err == nil {
		t.Fatal("action role directly read the ledger")
	}
	if err := approvalPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.runner_action_authorizations`).Scan(new(int)); err == nil {
		t.Fatal("approval projection role directly read authorizations")
	}
	if err := resolutionPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.runner_action_ledger`).Scan(new(int)); err == nil {
		t.Fatal("resolution role directly read the action ledger")
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.runner_action_executors
		(capability,executor_id,executor_version,policy_version,enabled,max_definite_attempts,retry_base_delay,retryable_error_codes,updated_at)
		VALUES ('email.send','test.email',1,1,true,3,interval '5 seconds',ARRAY['provider_rate_limited'],$1)`, now); err != nil {
		t.Fatal(err)
	}

	operation := "40000000-0000-4000-8000-000000000001"
	approval := "50000000-0000-4000-8000-000000000001"
	digest := sha256.Sum256([]byte(`{"to":"owner@example.com"}`))
	recordActionAuthorization(t, ctx, approvalPool, accountA, invocation, operation, approval, digest, now, now.Add(30*time.Minute))
	var duplicate bool
	if err := approvalPool.QueryRow(ctx, `SELECT public.spyglass_record_runner_action_authorization($1,$2,$3,$4,'email.send',$5,1::smallint,$6,'workload','agent-proposer',$7,3::bigint,$8,$9)`,
		accountA, operation, invocation, approval, digest[:], sha256Bytes("evidence"), "60000000-0000-4000-8000-000000000001", now, now.Add(30*time.Minute)).Scan(&duplicate); err != nil || duplicate {
		t.Fatalf("exact authorization retry created=%v err=%v", duplicate, err)
	}
	if err := approvalPool.QueryRow(ctx, `SELECT public.spyglass_record_runner_action_authorization($1,$2,$3,$4,'email.send',$5,1::smallint,$6,'workload','agent-proposer',$7,3::bigint,$8,$9)`,
		accountA, operation, invocation, approval, sha256Bytes("changed"), sha256Bytes("evidence"), "60000000-0000-4000-8000-000000000001", now, now.Add(30*time.Minute)).Scan(&duplicate); err == nil {
		t.Fatal("changed authorization bytes were accepted")
	}

	clock := &fixedClock{now: now}
	generator := &erasureIDs{values: []string{
		"70000000-0000-4000-8000-000000000001", "70000000-0000-4000-8000-000000000002",
		"70000000-0000-4000-8000-000000000003", "70000000-0000-4000-8000-000000000004",
		"70000000-0000-4000-8000-000000000005", "70000000-0000-4000-8000-000000000006",
	}}
	repository, _ := postgresadapter.NewRunnerActionRepository(actionPool)
	service, _ := runneraction.New(repository, generator, clock, 2*time.Minute)
	request := runnercapability.ActionRequest{AccountID: accountA, InvocationID: invocation, PodUID: podUID, OperationID: operation, Capability: "email.send", InputDigest: digest, ExpiresAt: now.Add(time.Hour)}

	first, err := service.BeginAction(ctx, request)
	if err != nil || first.Mode != runnercapability.ActionExecute || first.IdempotencyKey != operation {
		t.Fatalf("first lease=%#v err=%v", first, err)
	}
	if _, err := service.BeginAction(ctx, request); !errors.Is(err, runneraction.ErrActionBusy) {
		t.Fatalf("concurrent duplicate=%v", err)
	}
	unknown := runnercapability.ActionCompletion{Lease: first, Outcome: runnercapability.ActionUnknown, ErrorCode: "provider_timeout", At: now.Add(time.Second)}
	if err := service.CompleteAction(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteAction(ctx, unknown); err != nil {
		t.Fatalf("idempotent completion=%v", err)
	}
	conflict := unknown
	conflict.Outcome, conflict.ErrorCode = runnercapability.ActionFailed, "provider_rejected"
	if err := service.CompleteAction(ctx, conflict); !errors.Is(err, runneraction.ErrStateConflict) {
		t.Fatalf("conflicting completion=%v", err)
	}

	clock.now = now.Add(3 * time.Second)
	reconcile, err := service.BeginAction(ctx, request)
	if err != nil || reconcile.Mode != runnercapability.ActionReconcile || reconcile.IdempotencyKey != first.IdempotencyKey {
		t.Fatalf("reconcile lease=%#v err=%v cause=%v", reconcile, err, errors.Unwrap(err))
	}
	if err := service.CompleteAction(ctx, runnercapability.ActionCompletion{Lease: reconcile, Outcome: runnercapability.ActionSucceeded, At: clock.now}); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Second)
	afterSuccess, err := service.BeginAction(ctx, request)
	if err != nil || afterSuccess.Mode != runnercapability.ActionReconcile {
		t.Fatalf("successful retry was not reconciliation-only: %#v %v", afterSuccess, err)
	}

	crossPod := request
	crossPod.PodUID = "30000000-0000-4000-8000-000000000099"
	if _, err := service.BeginAction(ctx, crossPod); !errors.Is(err, runneraction.ErrStateConflict) {
		t.Fatalf("cross-Pod action=%v", err)
	}
	crossAccount := request
	crossAccount.AccountID = accountB
	if _, err := service.BeginAction(ctx, crossAccount); !errors.Is(err, runneraction.ErrStateConflict) {
		t.Fatalf("cross-Account action=%v", err)
	}

	canceledOperation := "40000000-0000-4000-8000-000000000002"
	canceledApproval := "50000000-0000-4000-8000-000000000002"
	recordActionAuthorization(t, ctx, approvalPool, accountA, invocation, canceledOperation, canceledApproval, digest, now, now.Add(30*time.Minute))
	var canceled bool
	if err := approvalPool.QueryRow(ctx, `SELECT public.spyglass_cancel_runner_action_authorization($1,$2,$3,$4)`, accountA, canceledOperation, canceledApproval, now.Add(time.Second)).Scan(&canceled); err != nil || !canceled {
		t.Fatalf("cancel authorization changed=%v err=%v", canceled, err)
	}
	if err := approvalPool.QueryRow(ctx, `SELECT public.spyglass_cancel_runner_action_authorization($1,$2,$3,$4)`, accountA, canceledOperation, canceledApproval, now.Add(time.Second)).Scan(&canceled); err != nil || canceled {
		t.Fatalf("exact cancellation retry changed=%v err=%v", canceled, err)
	}
	if err := approvalPool.QueryRow(ctx, `SELECT public.spyglass_cancel_runner_action_authorization($1,$2,$3,$4)`, accountA, canceledOperation, canceledApproval, now.Add(2*time.Second)).Scan(&canceled); err == nil {
		t.Fatal("changed cancellation timestamp was accepted")
	}
	canceledService, _ := runneraction.New(repository, &erasureIDs{values: []string{"71000000-0000-4000-8000-000000000001"}}, clock, time.Minute)
	canceledRequest := request
	canceledRequest.OperationID = canceledOperation
	if _, err := canceledService.BeginAction(ctx, canceledRequest); !errors.Is(err, runneraction.ErrActionDenied) {
		t.Fatalf("canceled authorization action=%v", err)
	}

	expiredOperation := "40000000-0000-4000-8000-000000000003"
	recordActionAuthorization(t, ctx, approvalPool, accountA, invocation, expiredOperation, "50000000-0000-4000-8000-000000000003", digest, now.Add(-10*time.Minute), now.Add(-time.Minute))
	expiredService, _ := runneraction.New(repository, &erasureIDs{values: []string{"71000000-0000-4000-8000-000000000002"}}, clock, time.Minute)
	expiredRequest := request
	expiredRequest.OperationID = expiredOperation
	if _, err := expiredService.BeginAction(ctx, expiredRequest); !errors.Is(err, runneraction.ErrApprovalExpired) {
		t.Fatalf("expired authorization action=%v", err)
	}
	shortOperation := "40000000-0000-4000-8000-000000000005"
	recordActionAuthorization(t, ctx, approvalPool, accountA, invocation, shortOperation, "50000000-0000-4000-8000-000000000005", digest, now, now.Add(30*time.Second))
	shortService, _ := runneraction.New(repository, &erasureIDs{values: []string{"71000000-0000-4000-8000-000000000005"}}, clock, 2*time.Minute)
	shortRequest := request
	shortRequest.OperationID = shortOperation
	shortLease, err := shortService.BeginAction(ctx, shortRequest)
	if err != nil || !shortLease.LeaseExpiresAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("approval-capped lease=%#v err=%v", shortLease, err)
	}

	leaseOperation := "40000000-0000-4000-8000-000000000004"
	recordActionAuthorization(t, ctx, approvalPool, accountA, invocation, leaseOperation, "50000000-0000-4000-8000-000000000004", digest, now, now.Add(30*time.Minute))
	leaseClock := &fixedClock{now: now}
	leaseService, _ := runneraction.New(repository, &erasureIDs{values: []string{"71000000-0000-4000-8000-000000000003", "71000000-0000-4000-8000-000000000004"}}, leaseClock, time.Minute)
	leaseRequest := request
	leaseRequest.OperationID = leaseOperation
	abandoned, err := leaseService.BeginAction(ctx, leaseRequest)
	if err != nil || abandoned.Mode != runnercapability.ActionExecute {
		t.Fatalf("abandoned execute lease=%#v err=%v", abandoned, err)
	}
	leaseClock.now = now.Add(2 * time.Minute)
	recovered, err := leaseService.BeginAction(ctx, leaseRequest)
	if err != nil || recovered.Mode != runnercapability.ActionReconcile {
		t.Fatalf("expired lease retry=%#v err=%v", recovered, err)
	}
	var priorOutcome, priorError string
	if err := owner.QueryRow(ctx, `SELECT outcome,error_code FROM spyglass.runner_action_attempts WHERE account_id=$1 AND attempt_id=$2`, accountA, abandoned.AttemptID).Scan(&priorOutcome, &priorError); err != nil || priorOutcome != "unknown" || priorError != "lease_expired" {
		t.Fatalf("abandoned attempt outcome=%q code=%q err=%v", priorOutcome, priorError, err)
	}

	retryOperation := "40000000-0000-4000-8000-000000000006"
	recordActionAuthorization(t, ctx, approvalPool, accountA, invocation, retryOperation, "50000000-0000-4000-8000-000000000006", digest, now, now.Add(30*time.Minute))
	retryClock := &fixedClock{now: now}
	retryService, _ := runneraction.New(repository, &erasureIDs{values: []string{
		"72000000-0000-4000-8000-000000000001", "72000000-0000-4000-8000-000000000002",
		"72000000-0000-4000-8000-000000000003", "72000000-0000-4000-8000-000000000004",
	}}, retryClock, time.Minute)
	retryRequest := request
	retryRequest.OperationID = retryOperation
	retryLease, err := retryService.BeginAction(ctx, retryRequest)
	if err != nil || retryLease.Mode != runnercapability.ActionExecute {
		t.Fatalf("retry first lease=%#v err=%v", retryLease, err)
	}
	if err := retryService.CompleteAction(ctx, runnercapability.ActionCompletion{Lease: retryLease, Outcome: runnercapability.ActionFailed, ErrorCode: "provider_rate_limited", At: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	retryClock.now = now.Add(4 * time.Second)
	if _, err := retryService.BeginAction(ctx, retryRequest); !errors.Is(err, runneraction.ErrActionBusy) {
		t.Fatalf("early definite retry=%v", err)
	}
	retryClock.now = now.Add(7 * time.Second)
	secondRetry, err := retryService.BeginAction(ctx, retryRequest)
	if err != nil || secondRetry.Mode != runnercapability.ActionExecute {
		t.Fatalf("due definite retry=%#v err=%v", secondRetry, err)
	}
	if err := retryService.CompleteAction(ctx, runnercapability.ActionCompletion{Lease: secondRetry, Outcome: runnercapability.ActionFailed, ErrorCode: "provider_rejected", At: now.Add(8 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := retryService.BeginAction(ctx, retryRequest); !errors.Is(err, runneraction.ErrActionDenied) {
		t.Fatalf("non-retryable definite failure=%v", err)
	}

	manualOperation := "40000000-0000-4000-8000-000000000007"
	recordActionAuthorization(t, ctx, approvalPool, accountA, invocation, manualOperation, "50000000-0000-4000-8000-000000000007", digest, now, now.Add(30*time.Minute))
	manualClock := &fixedClock{now: now.Add(10 * time.Second)}
	manualService, _ := runneraction.New(repository, &erasureIDs{values: []string{"73000000-0000-4000-8000-000000000001"}}, manualClock, time.Minute)
	manualRequest := request
	manualRequest.OperationID = manualOperation
	manualLease, err := manualService.BeginAction(ctx, manualRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := manualService.CompleteAction(ctx, runnercapability.ActionCompletion{Lease: manualLease, Outcome: runnercapability.ActionUnknown, ErrorCode: "provider_timeout", At: now.Add(11 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	resolutionID := "74000000-0000-4000-8000-000000000001"
	requesterID := "75000000-0000-4000-8000-000000000001"
	confirmerID := "75000000-0000-4000-8000-000000000002"
	reason := sha256.Sum256([]byte("operator inspected provider record and evidence"))
	var changed bool
	requestedAt := now.Add(12 * time.Second)
	if err := resolutionPool.QueryRow(ctx, `SELECT public.spyglass_request_runner_action_resolution($1,$2,$3,'succeeded',$4,$5,$6)`,
		accountA, manualOperation, resolutionID, reason[:], requesterID, requestedAt).Scan(&changed); err != nil || !changed {
		t.Fatalf("manual resolution request changed=%v err=%v", changed, err)
	}
	if err := resolutionPool.QueryRow(ctx, `SELECT public.spyglass_confirm_runner_action_resolution($1,$2,$3,$4,$5)`,
		accountA, manualOperation, resolutionID, requesterID, now.Add(13*time.Second)).Scan(&changed); err == nil {
		t.Fatal("manual resolution accepted the requesting operator as confirmer")
	}
	confirmedAt := now.Add(14 * time.Second)
	if err := resolutionPool.QueryRow(ctx, `SELECT public.spyglass_confirm_runner_action_resolution($1,$2,$3,$4,$5)`,
		accountA, manualOperation, resolutionID, confirmerID, confirmedAt).Scan(&changed); err != nil || !changed {
		t.Fatalf("manual resolution confirmation changed=%v err=%v", changed, err)
	}
	if err := resolutionPool.QueryRow(ctx, `SELECT public.spyglass_confirm_runner_action_resolution($1,$2,$3,$4,$5)`,
		accountA, manualOperation, resolutionID, confirmerID, confirmedAt).Scan(&changed); err != nil || changed {
		t.Fatalf("manual resolution replay changed=%v err=%v", changed, err)
	}
	var manualState, resolutionState string
	if err := owner.QueryRow(ctx, `SELECT l.state,r.state FROM spyglass.runner_action_ledger l JOIN spyglass.runner_action_manual_resolutions r
		ON r.account_id=l.account_id AND r.operation_id=l.operation_id WHERE l.account_id=$1 AND l.operation_id=$2`, accountA, manualOperation).Scan(&manualState, &resolutionState); err != nil || manualState != "succeeded" || resolutionState != "applied" {
		t.Fatalf("manual outcome ledger=%q resolution=%q err=%v", manualState, resolutionState, err)
	}
}

func seedRunnerActionInvocation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID ids.AccountID, invocationID, podUID string, now time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$4);
		INSERT INTO spyglass.runner_invocation_queue(invocation_id,account_id,profile,processing_state,attempt_count,job_name,queued_at,launched_at,next_inspection_at)
		VALUES ($2,$1,'agent-small','launched',1,'runner-action-test',$4,$4,$4);
		SELECT set_config('app.account_id',$1::text,true);
		INSERT INTO spyglass.runner_invocation_exchanges(account_id,invocation_id,request_ciphertext,request_nonce,request_key_version,request_digest,request_expires_at,bound_pod_uid,bound_at,last_fetched_at,fetch_count,created_at)
		VALUES ($1,$2,decode(repeat('aa',17),'hex'),decode(repeat('bb',12),'hex'),1,decode(repeat('cc',32),'hex'),$4::timestamptz+interval '1 hour',$3,$4,$4,1,$4)`, pgx.QueryExecModeSimpleProtocol, accountID, invocationID, podUID, now); err != nil {
		t.Fatal(err)
	}
}

func recordActionAuthorization(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID ids.AccountID, invocationID, operationID, approvalID string, digest [sha256.Size]byte, approvedAt, expiresAt time.Time) {
	t.Helper()
	var created bool
	if err := pool.QueryRow(ctx, `SELECT public.spyglass_record_runner_action_authorization($1,$2,$3,$4,'email.send',$5,1::smallint,$6,'workload','agent-proposer',$7,3::bigint,$8,$9)`,
		accountID, operationID, invocationID, approvalID, digest[:], sha256Bytes("evidence"), "60000000-0000-4000-8000-000000000001", approvedAt, expiresAt).Scan(&created); err != nil || !created {
		t.Fatalf("record authorization created=%v err=%v", created, err)
	}
}

func sha256Bytes(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}
