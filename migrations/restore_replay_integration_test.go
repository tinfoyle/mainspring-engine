package migrations_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/restoregate"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresRestoreReplayRebuildsPinnedErasureCheckpoints(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	originalURL, cleanupOriginal := createDatabase(t, ctx, adminURL)
	defer cleanupOriginal()
	restoredURL, cleanupRestored := createDatabase(t, ctx, adminURL)
	defer cleanupRestored()
	original := openPool(t, ctx, originalURL, nil)
	defer original.Close()
	restored := openPool(t, ctx, restoredURL, nil)
	defer restored.Close()
	for _, pool := range []*pgxpool.Pool{original, restored} {
		for _, target := range []migrations.Target{migrations.Global, migrations.Development, migrations.Cell} {
			if _, err := migrations.Apply(ctx, pool, target); err != nil {
				t.Fatalf("apply %s migrations: %v", target, err)
			}
		}
	}
	var now time.Time
	if err := original.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	accountA := ids.AccountID("ca100000-0000-4000-8000-000000000001")
	accountB := ids.AccountID("da100000-0000-4000-8000-000000000001")
	userA := ids.UserID("ca200000-0000-4000-8000-000000000001")
	userB := ids.UserID("cb200000-0000-4000-8000-000000000001")
	for _, pool := range []*pgxpool.Pool{original, restored} {
		seedGlobalErasureAccount(t, ctx, pool, accountA, userA, "ca300000-0000-4000-8000-000000000001", "c", now)
		seedGlobalErasureAccount(t, ctx, pool, accountB, userB, "cb300000-0000-4000-8000-000000000001", "d", now)
		seedCellErasureAccount(t, ctx, pool, accountA, "ca400000-0000-4000-8000-000000000001", "ca500000-0000-4000-8000-000000000001", now, true)
		seedCellErasureAccount(t, ctx, pool, accountB, "cb400000-0000-4000-8000-000000000001", "cb500000-0000-4000-8000-000000000001", now, true)
		if _, err := pool.Exec(ctx, `UPDATE cells SET assigned_accounts=2 WHERE id='cell-us-east-01'`); err != nil {
			t.Fatal(err)
		}
	}

	originalGlobal := postgresadapter.NewAccountErasureRepository(original)
	originalCell := postgresadapter.NewAccountErasureCellRepository(original, "cell-us-east-01")
	preparationIDs := &erasureIDs{values: []string{
		"ca600000-0000-4000-8000-000000000001", "ca700000-0000-4000-8000-000000000001",
		"ca800000-0000-4000-8000-000000000001", "ca900000-0000-4000-8000-000000000001",
	}}
	preparation, _ := accounterasure.NewService(originalGlobal, originalCell, preparationIDs, fixedClock{now: now})
	prepared, err := preparation.Prepare(ctx, accounterasure.PrepareCommand{
		AccountID: accountA, PolicyVersion: 1,
		Export:          accounterasure.ExportEvidence{Disposition: accounterasure.ExportNotApplicable, Reason: "restore replay integration fixture"},
		BackupExpiresAt: now.Add(35 * 24 * time.Hour), Actor: "requester@example.com",
		Reason: "prepare original Account erasure", Environment: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := preparation.Approve(ctx, prepared.ID, prepared.Version, "reviewer@example.com", "approve original Account erasure", "test")
	if err != nil {
		t.Fatal(err)
	}
	evidenceKey := bytes.Repeat([]byte{0x31}, 32)
	executionIDs := &erasureIDs{values: []string{
		"cc100000-0000-4000-8000-000000000001", "cc200000-0000-4000-8000-000000000001",
		"cc300000-0000-4000-8000-000000000001", "cc400000-0000-4000-8000-000000000001",
		"cc500000-0000-4000-8000-000000000001",
	}}
	execution, _ := accounterasure.NewExecutionService(originalGlobal, originalCell, executionIDs, fixedClock{now: now}, evidenceKey)
	originalGlobalTombstone, err := execution.Execute(ctx, accounterasure.ExecuteCommand{
		RequestID: approved.ID, AccountID: accountA, ExpectedVersion: approved.Version, LeaseDuration: 5 * time.Minute,
		Actor: "executor@example.com", Reason: "execute original Account erasure", Environment: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	originalCellTombstone, err := originalCell.AttestErasure(ctx, "cell-us-east-01", approved.ID, originalGlobalTombstone.AccountFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	directive := accounterasure.RestoreDirective{
		Version: accounterasure.RestoreDirectiveVersion, RequestID: approved.ID, AccountID: accountA, CellID: "cell-us-east-01",
		TombstonePlacementGeneration: originalCellTombstone.PlacementGeneration,
		AccountFingerprint:           originalGlobalTombstone.AccountFingerprint, PolicyVersion: originalGlobalTombstone.PolicyVersion,
		CellRequestVersion: originalCellTombstone.RequestVersion, FinalRequestVersion: originalGlobalTombstone.FinalRequestVersion,
		Environment: originalGlobalTombstone.Environment, PreparedAt: originalGlobalTombstone.PreparedAt,
		ApprovedAt: originalGlobalTombstone.ApprovedAt, CellErasedAt: originalGlobalTombstone.CellErasedAt,
		CompletedAt: originalGlobalTombstone.CompletedAt, ExportSHA256: originalGlobalTombstone.ExportSHA256,
		CellTombstoneSHA256:    originalGlobalTombstone.CellTombstoneSHA256,
		OperatorEvidenceSHA256: originalGlobalTombstone.OperatorEvidenceSHA256, BackupExpiresAt: originalGlobalTombstone.BackupExpiresAt,
		CellCheckpoint:   accounterasure.RestoreCheckpoint{PreviousRoot: make([]byte, 32), Sequence: originalCellTombstone.LedgerSequence, Root: originalCellTombstone.LedgerRoot},
		GlobalCheckpoint: accounterasure.RestoreCheckpoint{PreviousRoot: make([]byte, 32), Sequence: originalGlobalTombstone.LedgerSequence, Root: originalGlobalTombstone.LedgerRoot},
	}
	signingKey := bytes.Repeat([]byte{0x52}, 32)
	signed, err := accounterasure.SignRestoreDirective(directive, signingKey)
	if err != nil {
		t.Fatal(err)
	}

	// Model a backup taken before closure: restore replay is authorized by the
	// directive, not by the stale customer-facing state in the backup.
	if _, err := restored.Exec(ctx, `UPDATE accounts SET state='active' WHERE id=$1; UPDATE account_directory SET state='active' WHERE account_id=$1; UPDATE spyglass.account_namespaces SET state='active' WHERE account_id=$1`, pgx.QueryExecModeSimpleProtocol, accountA); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Exec(ctx, `INSERT INTO spyglass.account_audit_events(account_id,id,event_type,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at) VALUES ($1,'ca000000-0000-4000-8000-000000000099','restore-extra','system','restore-test','restore-extra','{}',$2)`, accountA, now); err != nil {
		t.Fatal(err)
	}

	globalFunctionRole := "spyglass_restore_global_function_" + randomSuffix(t)
	globalOperatorRole := "spyglass_restore_global_operator_" + randomSuffix(t)
	cellFunctionRole := "spyglass_restore_cell_function_" + randomSuffix(t)
	cellOperatorRole := "spyglass_restore_cell_operator_" + randomSuffix(t)
	if _, err := restored.Exec(ctx, `CREATE ROLE `+globalFunctionRole+` NOLOGIN NOBYPASSRLS; CREATE ROLE `+globalOperatorRole+` NOLOGIN NOBYPASSRLS;
		CREATE ROLE `+cellFunctionRole+` NOLOGIN NOBYPASSRLS; CREATE ROLE `+cellOperatorRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public,spyglass TO `+globalFunctionRole+`,`+globalOperatorRole+`,`+cellFunctionRole+`,`+cellOperatorRole+`;
		GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO `+globalFunctionRole+`;
		ALTER TABLE public.account_erasure_tombstones OWNER TO `+globalFunctionRole+`;
		ALTER FUNCTION public.spyglass_resolve_account_erasure_restore(uuid,uuid,bytea) OWNER TO `+globalFunctionRole+`;
		ALTER FUNCTION public.spyglass_replay_global_account_erasure(uuid,uuid,text,bigint,bytea,bigint,bigint,text,timestamptz,timestamptz,timestamptz,timestamptz,bytea,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) OWNER TO `+globalFunctionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_resolve_account_erasure_restore(uuid,uuid,bytea) TO `+globalOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_replay_global_account_erasure(uuid,uuid,text,bigint,bytea,bigint,bigint,text,timestamptz,timestamptz,timestamptz,timestamptz,bytea,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) TO `+globalOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_attest_global_account_erasure(uuid,bytea) TO `+globalOperatorRole+`;
		GRANT SELECT,DELETE,UPDATE ON spyglass.account_namespaces,spyglass.account_audit_events,spyglass.work_item_number_counters,
			spyglass.work_items,spyglass.work_item_events,spyglass.route_context_receipts,spyglass.work_capacity_release_queue,
			spyglass.route_context_receipt_cleanup_queue,spyglass.work_capacity_release_operator_events,
			spyglass.runner_account_scheduling,spyglass.runner_invocation_queue,spyglass.runner_invocation_exchanges,spyglass.runner_capability_events,
			spyglass.runner_action_authorizations,spyglass.runner_action_ledger,spyglass.runner_action_attempts,
			spyglass.agent_boardrooms,spyglass.agent_personas,spyglass.agent_persona_versions,spyglass.agent_conversations,
			spyglass.agent_runs,spyglass.agent_run_plan_turns,spyglass.agent_invocations,spyglass.agent_messages,
			spyglass.agent_result_projection_queue,spyglass.agent_user_messages,spyglass.agent_invocation_execution_plans,
			spyglass.agent_dispatch_queue TO `+cellFunctionRole+`;
		GRANT SELECT ON spyglass.account_erasure_restore_ledger TO `+cellFunctionRole+`;
		ALTER TABLE spyglass.account_erasure_tombstones OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_replay_account_cell_erasure_without_runner_control(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_replay_account_cell_erasure_without_runner_exchange(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_replay_account_cell_erasure_without_runner_capability_audit(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_replay_account_cell_erasure_without_runner_actions(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_replay_account_cell_erasure_without_agents(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_replay_account_cell_erasure_without_agent_projection(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_replay_account_cell_erasure_without_agent_dispatch(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) OWNER TO `+cellFunctionRole+`;
		ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) OWNER TO `+cellFunctionRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) TO `+cellOperatorRole); err != nil {
		t.Fatalf("create restore replay roles: %v", err)
	}
	globalOperator := openPool(t, ctx, restoredURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+globalOperatorRole)
		return err
	})
	cellOperator := openPool(t, ctx, restoredURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+cellOperatorRole)
		return err
	})
	defer func() {
		globalOperator.Close()
		cellOperator.Close()
		_, _ = restored.Exec(context.Background(), `REASSIGN OWNED BY `+globalFunctionRole+` TO postgres; REASSIGN OWNED BY `+cellFunctionRole+` TO spyglass;
			DROP OWNED BY `+globalOperatorRole+`; DROP OWNED BY `+cellOperatorRole+`; DROP OWNED BY `+globalFunctionRole+`; DROP OWNED BY `+cellFunctionRole+`;
			DROP ROLE IF EXISTS `+globalOperatorRole+`; DROP ROLE IF EXISTS `+cellOperatorRole+`; DROP ROLE IF EXISTS `+globalFunctionRole+`; DROP ROLE IF EXISTS `+cellFunctionRole)
	}()
	if _, err := globalOperator.Exec(ctx, `DELETE FROM public.accounts WHERE id=$1`, accountA); err == nil {
		t.Fatal("restore global operator received direct Account deletion authority")
	}
	if _, err := cellOperator.Exec(ctx, `DELETE FROM spyglass.account_namespaces WHERE account_id=$1`, accountA); err == nil {
		t.Fatal("restore cell operator received direct namespace deletion authority")
	}
	service, err := accounterasure.NewRestoreService(
		postgresadapter.NewAccountErasureRepository(globalOperator),
		postgresadapter.NewAccountErasureCellRepository(cellOperator, "cell-us-east-01"), signingKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	outOfOrder := directive
	outOfOrder.CellCheckpoint = accounterasure.RestoreCheckpoint{PreviousSequence: 1, PreviousRoot: bytes.Repeat([]byte{0x44}, 32), Sequence: 2, Root: bytes.Repeat([]byte{0x55}, 32)}
	outOfOrderSigned, _ := accounterasure.SignRestoreDirective(outOfOrder, signingKey)
	if _, err := service.Replay(ctx, outOfOrderSigned, "cell-us-east-01", "test"); !errors.Is(err, accounterasure.ErrStateConflict) {
		t.Fatalf("out-of-order restore directive=%v", err)
	}
	var accountBefore int
	_ = restored.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE id=$1`, accountA).Scan(&accountBefore)
	if accountBefore != 1 {
		t.Fatal("out-of-order directive mutated restored Account")
	}

	replayed, err := service.Replay(ctx, signed, "cell-us-east-01", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(replayed.LedgerRoot, originalGlobalTombstone.LedgerRoot) || replayed.LedgerSequence != originalGlobalTombstone.LedgerSequence {
		t.Fatalf("replayed global checkpoint=%x/%d original=%x/%d", replayed.LedgerRoot, replayed.LedgerSequence, originalGlobalTombstone.LedgerRoot, originalGlobalTombstone.LedgerSequence)
	}
	replayedCell, err := postgresadapter.NewAccountErasureCellRepository(cellOperator, "cell-us-east-01").ReplayCellRestore(ctx, accounterasure.CellRestoreReplay{Directive: directive, RestoredPlacementGeneration: 3})
	if err != nil || !bytes.Equal(replayedCell.LedgerRoot, originalCellTombstone.LedgerRoot) {
		t.Fatalf("idempotent cell replay=%+v err=%v", replayedCell, err)
	}
	globalCheckpoint, _ := restoregate.NewCheckpoint(replayed.LedgerSequence, replayed.LedgerRoot)
	cellCheckpoint, _ := restoregate.NewCheckpoint(replayedCell.LedgerSequence, replayedCell.LedgerRoot)
	globalGate, _ := restoregate.New(restored, restoregate.Global, globalCheckpoint)
	cellGate, _ := restoregate.New(restored, restoregate.Cell, cellCheckpoint)
	if err := globalGate.Ready(ctx); err != nil {
		t.Fatalf("restored global readiness=%v", err)
	}
	if err := cellGate.Ready(ctx); err != nil {
		t.Fatalf("restored cell readiness=%v", err)
	}
	assertGlobalAccountRows(t, ctx, restored, accountA, false)
	assertGlobalAccountRows(t, ctx, restored, accountB, true)
	assertCellAccountRows(t, ctx, restored, accountA, 0)
	assertCellAccountRows(t, ctx, restored, accountB, 1)
	repeated, err := service.Replay(ctx, signed, "cell-us-east-01", "test")
	if err != nil || !repeated.CompletedAt.Equal(replayed.CompletedAt) {
		t.Fatalf("idempotent restore replay=%+v err=%v", repeated, err)
	}
}
