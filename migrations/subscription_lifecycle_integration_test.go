package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/subscriptionlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresSubscriptionRemediationLifecycle(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	if _, err := migrations.Apply(ctx, pool, migrations.Global); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	const userID = "10000000-0000-4000-8000-000000000001"
	const accountID = "20000000-0000-4000-8000-000000000002"
	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,primary_email,display_name,state,email_verified_at,created_at) VALUES ($1,'owner@example.com','Owner','active',$2,$2)`, []any{userID, now}},
		{`INSERT INTO cells(id,region,state,soft_account_limit,created_at) VALUES ('test-cell','local','active',100,$1)`, []any{now}},
		{`INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,created_by_user_id,created_at) VALUES ($1,'lifecycle-test','Lifecycle Test','inactive','active','test-cell',$2,$3)`, []any{accountID, userID, now}},
		{`INSERT INTO memberships(id,account_id,user_id,role,state,created_at) VALUES ('30000000-0000-4000-8000-000000000003',$1,$2,'owner','active',$3)`, []any{accountID, userID, now}},
		{`INSERT INTO subscriptions(id,account_id,provider,provider_mode,provider_customer_id,provider_subscription_id,state,offer_code,offer_version,current_period_start,current_period_end,last_synced_at,created_at,updated_at) VALUES ('40000000-0000-4000-8000-000000000004',$1,'stripe','test','cus_lifecycle','sub_lifecycle','past_due','team-monthly-v2',2,$2::timestamptz,$2::timestamptz+interval '30 days',$2::timestamptz,$2::timestamptz,$2::timestamptz)`, []any{accountID, now}},
	}
	for _, statement := range seed {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed %q: %v", statement.query, err)
		}
	}
	if _, err := pool.Exec(ctx, `SELECT spyglass_project_subscription_lifecycle($1,'sub_lifecycle','past_due',$2::timestamptz+interval '30 days',NULL,$2::timestamptz)`, accountID, now); err != nil {
		t.Fatal(err)
	}
	var state string
	var effectiveAt time.Time
	var notices int
	if err := pool.QueryRow(ctx, `SELECT state,effective_at FROM account_subscription_lifecycles WHERE account_id=$1 AND state NOT IN ('recovered','closed')`, accountID).Scan(&state, &effectiveAt); err != nil || state != "grace_read_only" || !effectiveAt.Equal(now) {
		t.Fatalf("initial lifecycle state=%q effective=%s err=%v", state, effectiveAt, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_subscription_lifecycle_notices`).Scan(&notices); err != nil || notices != 4 {
		t.Fatalf("notice count=%d err=%v", notices, err)
	}
	lifecycleRepository := postgresadapter.NewSubscriptionLifecycleRepository(pool)
	notice, ok, err := lifecycleRepository.ClaimNotice(ctx, now, 2*time.Minute)
	if err != nil || !ok || notice.Kind != "payment_failed" || len(notice.Recipients) != 1 || notice.Recipients[0].Email != "owner@example.com" {
		t.Fatalf("claimed notice=%+v ok=%v err=%v", notice, ok, err)
	}
	prepared := subscriptionlifecycle.PreparedNotification{ID: "50000000-0000-4000-8000-000000000005", AccountID: ids.AccountID(accountID), Ciphertext: []byte("sealed"), Nonce: []byte("nonce"), KeyVersion: 1, CreatedAt: now}
	if err := lifecycleRepository.EmitNotice(ctx, notice, []subscriptionlifecycle.PreparedNotification{prepared}, now); err != nil {
		t.Fatal(err)
	}
	var notificationKind string
	if err := pool.QueryRow(ctx, `SELECT kind FROM identity_notification_outbox WHERE id=$1`, prepared.ID).Scan(&notificationKind); err != nil || notificationKind != "subscription_lifecycle" {
		t.Fatalf("notification kind=%q err=%v", notificationKind, err)
	}
	// Repeated provider invalidations preserve the first-failure clock.
	if _, err := pool.Exec(ctx, `SELECT spyglass_project_subscription_lifecycle($1,'sub_lifecycle','past_due',$2::timestamptz+interval '30 days',NULL,$2::timestamptz+interval '2 days')`, accountID, now); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT effective_at FROM account_subscription_lifecycles WHERE account_id=$1 AND state='grace_read_only'`, accountID).Scan(&effectiveAt); err != nil || !effectiveAt.Equal(now) {
		t.Fatalf("first failure moved to %s: %v", effectiveAt, err)
	}
	var lifecycleID, leaseID string
	if err := pool.QueryRow(ctx, `SELECT lifecycle_id,lease_id FROM spyglass_claim_subscription_lifecycle($1,$2)`, now.Add(7*24*time.Hour), 120).Scan(&lifecycleID, &leaseID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT spyglass_advance_subscription_lifecycle($1,$2,$3,$4)`, lifecycleID, leaseID, now.Add(7*24*time.Hour), 3600).Scan(&state); err != nil || state != "restricted" {
		t.Fatalf("restriction state=%q err=%v", state, err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM accounts WHERE id=$1`, accountID).Scan(&state); err != nil || state != "restricted" {
		t.Fatalf("Account state=%q err=%v", state, err)
	}
	if err := pool.QueryRow(ctx, `SELECT lifecycle_id,lease_id FROM spyglass_claim_subscription_lifecycle($1,$2)`, now.Add(30*24*time.Hour), 120).Scan(&lifecycleID, &leaseID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT spyglass_advance_subscription_lifecycle($1,$2,$3,$4)`, lifecycleID, leaseID, now.Add(30*24*time.Hour), 3600).Scan(&state); err != nil || state != "termination_pending" {
		t.Fatalf("termination state=%q err=%v", state, err)
	}
	var terminationState string
	if err := pool.QueryRow(ctx, `SELECT state FROM account_subscription_termination_jobs WHERE lifecycle_id=$1`, lifecycleID).Scan(&terminationState); err != nil || terminationState != "pending" {
		t.Fatalf("termination job state=%q err=%v", terminationState, err)
	}
	termination, ok, err := lifecycleRepository.ClaimTermination(ctx, now.Add(30*24*time.Hour), 2*time.Minute)
	if err != nil || !ok || termination.ProviderSubscriptionID != "sub_lifecycle" {
		t.Fatalf("claimed termination=%+v ok=%v err=%v", termination, ok, err)
	}
	if err := lifecycleRepository.CompleteTermination(ctx, termination, now.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var reconciliationState string
	if err := pool.QueryRow(ctx, `SELECT processing_state FROM billing_reconciliation_queue WHERE provider_subscription_id='sub_lifecycle'`).Scan(&reconciliationState); err != nil || reconciliationState != "pending" {
		t.Fatalf("termination reconciliation state=%q err=%v", reconciliationState, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subscriptions SET state='active' WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT spyglass_project_subscription_lifecycle($1,'sub_lifecycle','active',$2::timestamptz+interval '60 days',NULL,$2::timestamptz+interval '31 days')`, accountID, now); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM accounts WHERE id=$1`, accountID).Scan(&state); err != nil || state != "active" {
		t.Fatalf("recovered Account state=%q err=%v", state, err)
	}

	termEnd := now.Add(40 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `SELECT spyglass_project_subscription_lifecycle($1,'sub_lifecycle','active',$2,$2,$3)`, accountID, termEnd, now.Add(32*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Moving the provider's cancellation date recovers the old schedule and creates a new one.
	movedEnd := termEnd.Add(24 * time.Hour)
	if _, err := pool.Exec(ctx, `SELECT spyglass_project_subscription_lifecycle($1,'sub_lifecycle','active',$2,$2,$3)`, accountID, movedEnd, now.Add(33*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var recovered int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_subscription_lifecycles WHERE account_id=$1 AND state='recovered'`, accountID).Scan(&recovered); err != nil || recovered != 2 {
		t.Fatalf("recovered lifecycle count=%d err=%v", recovered, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subscriptions SET state='canceled',current_period_end=$2,cancel_at=$2 WHERE account_id=$1`, accountID, movedEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT spyglass_project_subscription_lifecycle($1,'sub_lifecycle','canceled',$2,$2,$2)`, accountID, movedEnd); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT lifecycle_id FROM account_subscription_lifecycles WHERE account_id=$1 AND state='restricted'`, accountID).Scan(&lifecycleID); err != nil {
		t.Fatal(err)
	}
	deleteAt := movedEnd.Add(30 * 24 * time.Hour)
	if err := pool.QueryRow(ctx, `SELECT lifecycle_id,lease_id FROM spyglass_claim_subscription_lifecycle($1,$2)`, deleteAt, 120).Scan(&lifecycleID, &leaseID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT spyglass_advance_subscription_lifecycle($1,$2,$3,$4)`, lifecycleID, leaseID, deleteAt, 3600).Scan(&state); err != nil || state != "closed" {
		t.Fatalf("deadline state=%q err=%v", state, err)
	}
	var closureState string
	var deleteAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT state,delete_after FROM account_closure_requests WHERE account_id=$1`, accountID).Scan(&closureState, &deleteAfter); err != nil || closureState != "closed" || !deleteAfter.Equal(deleteAt) {
		t.Fatalf("closure state=%q delete_after=%s err=%v", closureState, deleteAfter, err)
	}
	// The existing reviewed erasure finalizer sets this transaction-local fence
	// before deleting the Account. Verify the new graph honors that fence and
	// leaves no provider identifiers or lifecycle evidence behind.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, query := range []string{
		`DELETE FROM identity_notification_outbox WHERE account_id=$1`,
		`DELETE FROM billing_reconciliation_queue WHERE provider_subscription_id IN (SELECT provider_subscription_id FROM subscriptions WHERE account_id=$1)`,
		`DELETE FROM subscriptions WHERE account_id=$1`,
		`DELETE FROM account_closure_requests WHERE account_id=$1`,
		`DELETE FROM memberships WHERE account_id=$1`,
	} {
		if _, err := tx.Exec(ctx, query, accountID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('spyglass.erasure_account_id',$1,true)`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	var lifecycleRows int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM account_subscription_lifecycles WHERE account_id=$1`, accountID).Scan(&lifecycleRows); err != nil || lifecycleRows != 0 {
		t.Fatalf("lifecycle rows after erasure=%d err=%v", lifecycleRows, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
