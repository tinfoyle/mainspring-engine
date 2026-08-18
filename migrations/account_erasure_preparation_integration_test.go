package migrations_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type erasureIDs struct{ values []string }

func (g *erasureIDs) New() string {
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

func TestPostgresAccountErasurePreparationIsReviewedAndNonDestructive(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	for _, target := range []migrations.Target{migrations.Global, migrations.Development, migrations.Cell} {
		if _, err := migrations.Apply(ctx, pool, target); err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
	}

	var now time.Time
	if err := pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	accountID := ids.AccountID("e1000000-0000-4000-8000-000000000001")
	userID := ids.UserID("e1000000-0000-4000-8000-000000000002")
	closureID := "e1000000-0000-4000-8000-000000000003"
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES ($1,'erasure-owner@example.com','Erasure Owner','active',$2,1,$2)`, userID, now); err != nil {
		t.Fatalf("seed erasure owner: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,created_by_user_id,created_at,version) VALUES ($1,'erasure-account','Erasure Account','free','closed','cell-us-east-01',3,1,$2,$3,7)`, accountID, userID, now); err != nil {
		t.Fatalf("seed closed Account: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO account_directory(account_id,cell_id,placement_generation,state,data_region,updated_at) VALUES ($1,'cell-us-east-01',3,'frozen','us-east',$2)`, accountID, now); err != nil {
		t.Fatalf("seed Account directory: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO account_closure_requests(id,account_id,state,requested_by_user_id,reason,account_version,requested_at,execute_after,next_attempt_at,attempt_count,closed_at,delete_after) VALUES ($1,$2,'closed',$3,'customer requested closure',7,$4::timestamptz-interval '50 days',$4::timestamptz-interval '49 days',$4::timestamptz-interval '49 days',1,$4::timestamptz-interval '40 days',$4::timestamptz-interval '10 days')`, closureID, accountID, userID, now); err != nil {
		t.Fatalf("seed closed request: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,3,'frozen',$2::timestamptz-interval '1 day')`, accountID, now); err != nil {
		t.Fatalf("seed cell namespace: %v", err)
	}
	ownerGlobalRepository := postgresadapter.NewAccountErasureRepository(pool)
	ownerCellRepository := postgresadapter.NewAccountErasureCellRepository(pool, "cell-us-east-01")
	if _, err := pool.Exec(ctx, `UPDATE account_directory SET state='moving' WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerGlobalRepository.Target(ctx, accountID); !errors.Is(err, accounterasure.ErrNotEligible) {
		t.Fatalf("moving placement eligibility=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE account_directory SET state='frozen' WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entitlement_usage_counters(account_id,package_code,limit_code,current_value,version,updated_at) VALUES ($1,'work','active_items',1,1,$2)`, accountID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerGlobalRepository.Target(ctx, accountID); !errors.Is(err, accounterasure.ErrNotEligible) {
		t.Fatalf("active usage eligibility=%v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM entitlement_usage_counters WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	eligibleTarget, err := ownerGlobalRepository.Target(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE spyglass.account_namespaces SET state='active' WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerCellRepository.Attest(ctx, eligibleTarget); !errors.Is(err, accounterasure.ErrNotEligible) {
		t.Fatalf("active cell namespace eligibility=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE spyglass.account_namespaces SET state='frozen' WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	operatorRole := "spyglass_erasure_operator_" + randomSuffix(t)
	if _, err := pool.Exec(ctx, `CREATE ROLE `+operatorRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA public,spyglass TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_assert_account_erasure_eligible(uuid) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_prepare_account_erasure(uuid,uuid,uuid,bigint,text,text,bytea,timestamptz,text,timestamptz,bigint,text,bigint,timestamptz,text,text,text) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_account_erasure(uuid,uuid,text,text,text) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_approve_account_erasure(uuid,uuid,bigint,bigint,text,bigint,timestamptz,text,text,text) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_cancel_account_erasure(uuid,uuid,bigint,text,text,text) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_attest_account_erasure_readiness(uuid,bigint) TO `+operatorRole); err != nil {
		t.Fatalf("create Account erasure operator role: %v", err)
	}
	operatorPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+operatorRole)
		return err
	})
	defer func() {
		operatorPool.Close()
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+operatorRole+`; DROP ROLE IF EXISTS `+operatorRole)
	}()
	var unauthorizedCount int
	if err := operatorPool.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&unauthorizedCount); err == nil {
		t.Fatal("Account erasure operator directly read Accounts")
	}
	if err := operatorPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_items`).Scan(&unauthorizedCount); err == nil {
		t.Fatal("Account erasure operator directly read customer Work")
	}
	globalRepository := postgresadapter.NewAccountErasureRepository(operatorPool)
	cellRepository := postgresadapter.NewAccountErasureCellRepository(operatorPool, "cell-us-east-01")

	generator := &erasureIDs{values: []string{
		"e2000000-0000-4000-8000-000000000001", "e2000000-0000-4000-8000-000000000002",
		"e2000000-0000-4000-8000-000000000003", "e2000000-0000-4000-8000-000000000004",
		"e2000000-0000-4000-8000-000000000005", "e2000000-0000-4000-8000-000000000006",
		"e2000000-0000-4000-8000-000000000007",
	}}
	service, err := accounterasure.NewService(
		globalRepository,
		cellRepository,
		generator, fixedClock{now: now},
	)
	if err != nil {
		t.Fatal(err)
	}
	exportExpires := now.Add(7 * 24 * time.Hour)
	prepared, err := service.Prepare(ctx, accounterasure.PrepareCommand{
		AccountID: accountID, PolicyVersion: 1,
		Export:          accounterasure.ExportEvidence{Disposition: accounterasure.ExportArtifact, Reference: "object://restricted/erasure-export", SHA256: bytes.Repeat([]byte{0x45}, 32), ExpiresAt: &exportExpires},
		BackupExpiresAt: now.Add(35 * 24 * time.Hour), Actor: "requester@example.com", Reason: "prepare retained Account for reviewed erasure", Environment: "test",
	})
	if err != nil || prepared.State != accounterasure.StatePrepared || prepared.AccountID != accountID || prepared.ClosureRequestID != closureID || prepared.CellID != "cell-us-east-01" || prepared.PlacementGeneration != 3 || prepared.AccountVersion != 7 || prepared.Version != 1 {
		t.Fatalf("prepared request=%+v err=%v", prepared, err)
	}
	databaseAttestation, err := cellRepository.Attest(ctx, accounterasure.Target{AccountID: prepared.AccountID, CellID: prepared.CellID, PlacementGeneration: prepared.PlacementGeneration, AccountVersion: prepared.AccountVersion})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := globalRepository.Approve(ctx, prepared.ID, prepared.Version, databaseAttestation, accounterasure.Change{EventID: "e3000000-0000-4000-8000-000000000001", Actor: prepared.RequestedBy, Reason: "attempt direct self approval through function", Environment: "test"}); !errors.Is(err, accounterasure.ErrStateConflict) {
		t.Fatalf("database self approval=%v", err)
	}
	if _, err := service.Approve(ctx, prepared.ID, prepared.Version, "requester@example.com", "requester cannot approve own erasure", "test"); !errors.Is(err, accounterasure.ErrReviewRequired) {
		t.Fatalf("self approval=%v", err)
	}
	approved, err := service.Approve(ctx, prepared.ID, prepared.Version, "reviewer@example.com", "independently approve Account erasure", "test")
	if err != nil || approved.State != accounterasure.StateApproved || approved.Version != 2 || approved.ApprovedBy != "reviewer@example.com" {
		t.Fatalf("approved request=%+v err=%v", approved, err)
	}
	if _, err := service.Approve(ctx, prepared.ID, prepared.Version, "second-reviewer@example.com", "attempt stale Account erasure approval", "test"); !errors.Is(err, accounterasure.ErrStateConflict) {
		t.Fatalf("stale approval=%v", err)
	}
	canceled, err := service.Cancel(ctx, approved.ID, approved.Version, "reviewer@example.com", "cancel before destructive execution exists", "test")
	if err != nil || canceled.State != accounterasure.StateCanceled || canceled.Version != 3 {
		t.Fatalf("canceled request=%+v err=%v", canceled, err)
	}

	var accountCount, namespaceCount, eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE id=$1`, accountID).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.account_namespaces WHERE account_id=$1`, accountID).Scan(&namespaceCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_erasure_operator_events WHERE request_id=$1`, prepared.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 || namespaceCount != 1 || eventCount != 6 {
		t.Fatalf("non-destructive boundary: accounts=%d namespaces=%d events=%d", accountCount, namespaceCount, eventCount)
	}
	if _, err := pool.Exec(ctx, `UPDATE account_erasure_operator_events SET reason='tampered' WHERE request_id=$1`, prepared.ID); err == nil {
		t.Fatal("Account erasure operator history was mutable")
	}
}
