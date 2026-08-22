package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/baselinemaintenance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresBaselineMaintenanceQueueSchedulesLeasesAndRetries(t *testing.T) {
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
	if _, err := migrations.Apply(ctx, pool, migrations.Cell); err != nil {
		t.Fatal(err)
	}

	var now time.Time
	if err := pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	accountID := ids.AccountID("d1000000-0000-4000-8000-000000000001")
	assessmentID := ids.BaselineAssessmentID("d2000000-0000-4000-8000-000000000501")
	seedCellErasureAccount(t, ctx, pool, accountID, "d2000000-0000-4000-8000-000000000001", "d3000000-0000-4000-8000-000000000001", now, false)

	queue, err := postgresadapter.NewBaselineMaintenanceQueue(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := queue.Claim(ctx, "d4000000-0000-4000-8000-000000000001", now.Add(59*24*time.Hour), time.Minute); err != nil || found {
		t.Fatalf("early claim found=%t err=%v", found, err)
	}
	claimAt := now.Add(61 * 24 * time.Hour)
	claim, found, err := queue.Claim(ctx, "d4000000-0000-4000-8000-000000000002", claimAt, time.Minute)
	if err != nil || !found || claim.AccountID != accountID || claim.AssessmentID != assessmentID || claim.AssessmentVersion != 9 || claim.Attempt != 1 || !claim.ScheduledFor.Equal(now.Add(90*24*time.Hour)) {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	stale := claim
	stale.LeaseID = "d4000000-0000-4000-8000-000000000003"
	if _, err := queue.Fail(ctx, stale, true, claimAt.Add(time.Minute), "fixture_retry", claimAt, 10); !errors.Is(err, baselinemaintenance.ErrLease) {
		t.Fatalf("stale lease error=%v", err)
	}
	if err := queue.Complete(ctx, claim, claimAt); err != nil {
		t.Fatal(err)
	}
	if _, found, err := queue.Claim(ctx, "d4000000-0000-4000-8000-000000000004", claimAt.Add(23*time.Hour), time.Minute); err != nil || found {
		t.Fatalf("premature recurring claim found=%t err=%v", found, err)
	}
	retryAt := claimAt.Add(25 * time.Hour)
	retryClaim, found, err := queue.Claim(ctx, "d4000000-0000-4000-8000-000000000005", retryAt, time.Minute)
	if err != nil || !found || retryClaim.Attempt != 2 {
		t.Fatalf("retry claim=%+v found=%t err=%v", retryClaim, found, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE spyglass.baseline_maintenance_queue SET lease_expires_at=statement_timestamp()-interval '1 second' WHERE account_id=$1 AND assessment_id=$2`, accountID, assessmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Fail(ctx, retryClaim, true, retryAt.Add(time.Minute), "fixture_retry", retryAt, 10); !errors.Is(err, baselinemaintenance.ErrLease) {
		t.Fatalf("expired lease error=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE spyglass.baseline_maintenance_queue SET lease_expires_at=$3 WHERE account_id=$1 AND assessment_id=$2`, accountID, assessmentID, retryAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	state, err := queue.Fail(ctx, retryClaim, false, retryAt, "materialization_constraint", retryAt, 10)
	if err != nil || state != "dead_letter" {
		t.Fatalf("dead letter state=%q err=%v", state, err)
	}
	stats, err := queue.Stats(ctx, retryAt)
	if err != nil || stats.Pending != 0 || stats.Ready != 0 || stats.Leased != 0 || stats.Retrying != 0 || stats.DeadLetter != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}
