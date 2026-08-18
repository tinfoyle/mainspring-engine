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
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type brokerIdentityVerifier struct{ identity runnerbroker.Identity }

func (v brokerIdentityVerifier) Verify(_ context.Context, token, invocationID string) (runnerbroker.Identity, error) {
	if token != "reviewed-pod-token" || invocationID != v.identity.InvocationID {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	return v.identity, nil
}

func TestRunnerBrokerExchangeIsEncryptedPodBoundAndLeastPrivilege(t *testing.T) {
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

	now := time.Date(2026, 8, 18, 20, 0, 0, 0, time.UTC)
	accountA := ids.AccountID("51000000-0000-4000-8000-000000000001")
	accountB := ids.AccountID("52000000-0000-4000-8000-000000000002")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',$3),($2,1,'active',$3)`, accountA, accountB, now); err != nil {
		t.Fatal(err)
	}

	producerRole := "spyglass_broker_producer_" + randomSuffix(t)
	brokerRole := "spyglass_broker_exchange_" + randomSuffix(t)
	controllerRole := "spyglass_broker_controller_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+producerRole+` NOLOGIN NOBYPASSRLS;
		CREATE ROLE `+brokerRole+` NOLOGIN NOBYPASSRLS;
		CREATE ROLE `+controllerRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public TO `+producerRole+`,`+brokerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_provision_runner_invocation(uuid,uuid,text,timestamptz,bytea,bytea,integer,bytea,timestamptz) TO `+producerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_claim_runner_exchange(uuid,uuid,text,text,timestamptz) TO `+brokerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_submit_runner_result(uuid,uuid,text,text,text,bytea,bytea,integer,bytea,timestamptz) TO `+brokerRole+`;
		GRANT USAGE ON SCHEMA spyglass TO `+controllerRole+`;
		GRANT SELECT,UPDATE ON spyglass.runner_invocation_queue TO `+controllerRole); err != nil {
		t.Fatal(err)
	}
	producer := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+producerRole)
		return err
	})
	broker := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+brokerRole)
		return err
	})
	controller := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+controllerRole)
		return err
	})
	defer func() {
		producer.Close()
		broker.Close()
		controller.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+producerRole+`; DROP OWNED BY `+brokerRole+`; DROP OWNED BY `+controllerRole+`;
			DROP ROLE IF EXISTS `+producerRole+`; DROP ROLE IF EXISTS `+brokerRole+`; DROP ROLE IF EXISTS `+controllerRole)
	}()

	for role, pool := range map[string]interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}{"producer": producer, "broker": broker} {
		var forbidden int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.runner_invocation_queue`).Scan(&forbidden); err == nil {
			t.Fatalf("%s directly read identifier queue", role)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.runner_invocation_exchanges`).Scan(&forbidden); err == nil {
			t.Fatalf("%s directly read encrypted exchanges", role)
		}
	}

	cipher, err := runnerbroker.NewCipher(map[int][]byte{1: bytes.Repeat([]byte{0x41}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	producerRepository, _ := postgresadapter.NewRunnerBrokerRepository(producer)
	invocation := runnercontrol.Invocation{ID: "61000000-0000-4000-8000-000000000001", AccountID: accountA, Profile: "agent-small", QueuedAt: now}
	identity := runnerbroker.Identity{InvocationID: invocation.ID, Profile: invocation.Profile, JobName: "spyglass-runner-61000000", JobUID: "71000000-0000-4000-8000-000000000001", PodName: "runner-a", PodUID: "81000000-0000-4000-8000-000000000001"}
	producerService, _ := runnerbroker.NewService(producerRepository, brokerIdentityVerifier{identity}, cipher, fixedClock{now: now})
	command := runnerbroker.ProvisionCommand{Invocation: invocation, Request: runnerbroker.Request{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{"prompt":"private reef plan"}`), Capabilities: []string{"work:read"}, ExpiresAt: now.Add(time.Hour)}}
	if created, err := producerService.Provision(ctx, command); err != nil || !created {
		t.Fatalf("provision created=%v err=%v", created, err)
	}
	if created, err := producerService.Provision(ctx, command); err != nil || created {
		t.Fatalf("idempotent provision created=%v err=%v", created, err)
	}
	conflict := command
	conflict.Request.Input = json.RawMessage(`{"prompt":"different"}`)
	if _, err := producerService.Provision(ctx, conflict); !errors.Is(err, runnerbroker.ErrExchangeConflict) {
		t.Fatalf("conflicting provision=%v", err)
	}
	legacyInvocation := runnercontrol.Invocation{ID: "65000000-0000-4000-8000-000000000005", AccountID: accountB, Profile: "agent-small", QueuedAt: now}
	var queued bool
	if err := owner.QueryRow(ctx, `SELECT public.spyglass_enqueue_runner_invocation($1,$2,$3,$4)`, legacyInvocation.ID, legacyInvocation.AccountID, legacyInvocation.Profile, now).Scan(&queued); err != nil || !queued {
		t.Fatalf("seed queue-only invocation queued=%v err=%v", queued, err)
	}
	legacyCommand := command
	legacyCommand.Invocation = legacyInvocation
	if _, err := producerService.Provision(ctx, legacyCommand); !errors.Is(err, runnerbroker.ErrExchangeConflict) {
		t.Fatalf("queue-only identity accepted an attached exchange: %v", err)
	}
	var ciphertext []byte
	if _, err := owner.Exec(ctx, `SELECT set_config('app.account_id',$1::text,false)`, accountA); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT request_ciphertext FROM spyglass.runner_invocation_exchanges WHERE invocation_id=$1`, invocation.ID).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte("private reef plan")) {
		t.Fatalf("stored request is not safely encrypted err=%v", err)
	}
	if _, err := controller.Exec(ctx, `UPDATE spyglass.runner_invocation_queue SET processing_state='launched',job_name=$2,
		launched_at=$3,next_attempt_at=NULL,next_inspection_at=$3 WHERE invocation_id=$1`, invocation.ID, identity.JobName, now); err != nil {
		t.Fatal(err)
	}

	brokerRepository, _ := postgresadapter.NewRunnerBrokerRepository(broker)
	brokerService, _ := runnerbroker.NewService(brokerRepository, brokerIdentityVerifier{identity}, cipher, fixedClock{now: now.Add(time.Second)})
	for attempt := 0; attempt < 2; attempt++ {
		request, err := brokerService.Fetch(ctx, "reviewed-pod-token", invocation.ID)
		if err != nil || !bytes.Contains(request.Input, []byte("private reef plan")) {
			t.Fatalf("fetch attempt %d request=%+v err=%v", attempt, request, err)
		}
	}
	otherPod := identity
	otherPod.PodUID = "82000000-0000-4000-8000-000000000002"
	otherPodService, _ := runnerbroker.NewService(brokerRepository, brokerIdentityVerifier{otherPod}, cipher, fixedClock{now: now.Add(2 * time.Second)})
	if _, err := otherPodService.Fetch(ctx, "reviewed-pod-token", invocation.ID); !errors.Is(err, runnerbroker.ErrIdentityDenied) {
		t.Fatalf("second Pod claim=%v", err)
	}
	result := runnerbroker.Result{SchemaVersion: 1, Outcome: "completed", Output: json.RawMessage(`{"summary":"done privately"}`)}
	if created, err := brokerService.Submit(ctx, "reviewed-pod-token", invocation.ID, result); err != nil || !created {
		t.Fatalf("submit created=%v err=%v", created, err)
	}
	if created, err := brokerService.Submit(ctx, "reviewed-pod-token", invocation.ID, result); err != nil || created {
		t.Fatalf("idempotent submit created=%v err=%v", created, err)
	}
	result.Output = json.RawMessage(`{"summary":"conflict"}`)
	if _, err := brokerService.Submit(ctx, "reviewed-pod-token", invocation.ID, result); !errors.Is(err, runnerbroker.ErrExchangeConflict) {
		t.Fatalf("conflicting result=%v", err)
	}
	lateService, _ := runnerbroker.NewService(brokerRepository, brokerIdentityVerifier{identity}, cipher, fixedClock{now: now.Add(2 * time.Hour)})
	if _, err := lateService.Submit(ctx, "reviewed-pod-token", invocation.ID, runnerbroker.Result{SchemaVersion: 1, Outcome: "completed", Output: json.RawMessage(`{}`)}); !errors.Is(err, runnerbroker.ErrExchangeExpired) {
		t.Fatalf("expired result submission=%v", err)
	}

	assertDeniedState := func(id, state string, expires time.Time, expected error) {
		t.Helper()
		item := invocation
		item.ID = id
		item.QueuedAt = now
		idn := identity
		idn.InvocationID = id
		idn.JobName = "spyglass-runner-" + id[:8]
		cmd := command
		cmd.Invocation = item
		cmd.Request.ExpiresAt = expires
		if _, err := producerService.Provision(ctx, cmd); err != nil {
			t.Fatal(err)
		}
		if state != "queued" {
			if _, err := controller.Exec(ctx, `UPDATE spyglass.runner_invocation_queue SET processing_state=$2::text,job_name=$3,launched_at=$4::timestamptz,
				next_attempt_at=NULL,next_inspection_at=CASE WHEN $2::text IN ('launched','canceling') THEN $4::timestamptz ELSE NULL END,
				cancel_requested_at=CASE WHEN $2::text IN ('canceling','canceled') THEN $4::timestamptz ELSE NULL END,
				completed_at=CASE WHEN $2::text='canceled' THEN $4::timestamptz ELSE NULL END WHERE invocation_id=$1`, id, state, idn.JobName, now); err != nil {
				t.Fatal(err)
			}
		}
		svc, _ := runnerbroker.NewService(brokerRepository, brokerIdentityVerifier{idn}, cipher, fixedClock{now: now.Add(2 * time.Minute)})
		if _, err := svc.Fetch(ctx, "reviewed-pod-token", id); !errors.Is(err, expected) {
			t.Fatalf("state %s fetch=%v want=%v", state, err, expected)
		}
	}
	assertDeniedState("62000000-0000-4000-8000-000000000002", "queued", now.Add(time.Hour), runnerbroker.ErrExchangeNotReady)
	assertDeniedState("63000000-0000-4000-8000-000000000003", "canceled", now.Add(time.Hour), runnerbroker.ErrExchangeCanceled)
	assertDeniedState("64000000-0000-4000-8000-000000000004", "launched", now.Add(time.Minute), runnerbroker.ErrExchangeExpired)
}
