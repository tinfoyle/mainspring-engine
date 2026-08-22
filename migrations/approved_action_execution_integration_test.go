package migrations_test

import (
	"context"
	"crypto/sha256"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestApprovedActionWorkerExecutesAfterRunnerTerminationAndBoundsUnknownReconciliation(t *testing.T) {
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
	now := time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)
	accountID := "11000000-0000-4000-8000-000000000001"
	boardroomID := "31000000-0000-4000-8000-000000000001"
	conversationID := "41000000-0000-4000-8000-000000000001"
	runID := "51000000-0000-4000-8000-000000000001"
	invocationID := "61000000-0000-4000-8000-000000000001"
	seedAgentProjectionFixture(t, ctx, owner, accountID, boardroomID, conversationID, runID, invocationID, sha256Bytes("runner"), "completed", now)

	approvalID := "71000000-0000-4000-8000-000000000001"
	operationID := "72000000-0000-4000-8000-000000000001"
	approvedBy := "73000000-0000-4000-8000-000000000001"
	payload := []byte(`{"entry_id":"74000000-0000-4000-8000-000000000001","expected_version":1}`)
	digest := sha256.Sum256(payload)
	// The durable approval intentionally outlives the fixture's one-hour runner
	// exchange. The post-approval worker must not depend on runner credentials.
	approvedAt, expiresAt := now.Add(time.Second), now.Add(2*time.Hour)
	if _, err := owner.Exec(ctx, `SELECT set_config('app.account_id',$1,false);
		INSERT INTO spyglass.attention_consequential_approvals
		(account_id,id,operation_id,invocation_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,
		 proposer_kind,proposer_id,policy_version,require_independent_review,expires_at,state,decision,decision_reason,
		 decided_by_user_id,decided_at,version,created_at,updated_at)
		VALUES ($1,$2,$3,$4,'finance.entry.post',$5,$6,1,$7,'workload','agent:test',3,true,$8,'approved','approve',
		 'approved exact Finance posting',$9,$10,2,$11,$10)`, pgx.QueryExecModeSimpleProtocol, accountID, approvalID, operationID,
		invocationID, payload, digest[:], sha256Bytes("evidence"), expiresAt, approvedBy, approvedAt, now); err != nil {
		t.Fatal(err)
	}
	var created bool
	if err := owner.QueryRow(ctx, `SELECT public.spyglass_record_runner_action_authorization($1,$2,$3,$4,'finance.entry.post',$5,1::smallint,$6,
		'workload','agent:test',$7,3::bigint,$8,$9)`, accountID, operationID, invocationID, approvalID, digest[:],
		sha256Bytes("evidence"), approvedBy, approvedAt, expiresAt).Scan(&created); err != nil || !created {
		t.Fatalf("authorization created=%v err=%v", created, err)
	}

	workerRole := "spyglass_approved_action_worker_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+workerRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_claim_approved_runner_action(uuid,timestamptz,timestamptz) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_complete_runner_action_v2(uuid,uuid,uuid,uuid,text,bytea,text,uuid,timestamptz,text,text,timestamptz) TO `+workerRole); err != nil {
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
	if err := worker.QueryRow(ctx, `SELECT count(*) FROM spyglass.attention_consequential_approvals`).Scan(new(int)); err == nil {
		t.Fatal("approved action worker directly read Attention payloads")
	}
	repository, _ := postgresadapter.NewApprovedActionRepository(worker)
	claim, found, err := repository.Claim(ctx, "81000000-0000-4000-8000-000000000001", now.Add(2*time.Second), now.Add(2*time.Minute))
	if err != nil || !found || claim.Lease.Mode != runnercapability.ActionExecute || string(claim.CanonicalPayload) != string(payload) || claim.ApprovedByUserID != ids.UserID(approvedBy) {
		t.Fatalf("initial claim=%+v found=%v err=%v", claim, found, err)
	}
	if err := repository.Complete(ctx, runnercapability.ActionCompletion{Lease: claim.Lease, Outcome: runnercapability.ActionUnknown,
		ErrorCode: "database_response_unknown", At: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repository.Claim(ctx, "81000000-0000-4000-8000-000000000002", now.Add(20*time.Second), now.Add(3*time.Minute)); err != nil || found {
		t.Fatalf("early reconciliation found=%v err=%v", found, err)
	}
	for attempt, at := range []time.Time{now.Add(40 * time.Second), now.Add(80 * time.Second)} {
		attemptID := []string{"81000000-0000-4000-8000-000000000003", "81000000-0000-4000-8000-000000000004"}[attempt]
		reconcile, found, err := repository.Claim(ctx, attemptID, at, at.Add(time.Minute))
		if err != nil || !found || reconcile.Lease.Mode != runnercapability.ActionReconcile {
			t.Fatalf("reconciliation %d claim=%+v found=%v err=%v", attempt+1, reconcile, found, err)
		}
		if err := repository.Complete(ctx, runnercapability.ActionCompletion{Lease: reconcile.Lease, Outcome: runnercapability.ActionUnknown,
			ErrorCode: "effect_not_visible", At: at.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, found, err := repository.Claim(ctx, "81000000-0000-4000-8000-000000000005", now.Add(2*time.Minute), now.Add(3*time.Minute)); err != nil || found {
		t.Fatalf("bounded unknown reconciliation found=%v err=%v", found, err)
	}
	var state string
	var attempts int
	if err := owner.QueryRow(ctx, `SELECT state,attempt_count FROM spyglass.runner_action_ledger WHERE account_id=$1 AND operation_id=$2`, accountID, operationID).Scan(&state, &attempts); err != nil || state != "unknown" || attempts != 3 {
		t.Fatalf("terminal unknown state=%q attempts=%d err=%v", state, attempts, err)
	}
}
