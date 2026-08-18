package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresAccountClosureIsRecoverableBillingAwareAndAudited(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	for _, target := range []migrations.Target{migrations.Global, migrations.Development} {
		if _, err := migrations.Apply(ctx, pool, target); err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
	}

	accountID := ids.AccountID("c1000000-0000-4000-8000-000000000001")
	ownerID := ids.UserID("c2000000-0000-4000-8000-000000000002")
	now := time.Date(2026, 8, 18, 17, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES ($1,'closure-owner@example.com','Closure Owner','active',$2,1,$2)`, ownerID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,version,created_by_user_id,created_at)
		SELECT $1,'closure-contract','Closure Contract','free','active',id,1,1,1,$2,$3 FROM cells ORDER BY id LIMIT 1`, accountID, ownerID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,account_id,user_id,role,state,version,created_at) VALUES ('c3000000-0000-4000-8000-000000000003',$1,$2,'owner','active',1,$3)`, accountID, ownerID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO entitlement_snapshots(account_id,version,catalog_version,evaluated_at,source_hash,effective_packages)
		SELECT $1,1,version,$2,decode(repeat('00',32),'hex'),'[]'::jsonb FROM catalog_publications WHERE state='published'`, accountID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO subscriptions(id,account_id,provider,provider_mode,provider_customer_id,provider_subscription_id,state,offer_code,offer_version,last_synced_at,created_at,updated_at)
		VALUES ('c4000000-0000-4000-8000-000000000004',$1,'stripe','test','cus_closure','sub_closure','active','team-monthly-v1',2,$2,$2,$2)`, accountID, now); err != nil {
		t.Fatal(err)
	}

	repository := postgresadapter.NewAccountLifecycleRepository(pool)
	first := accountlifecycle.RequestMutation{RequestID: "c5000000-0000-4000-8000-000000000005", EventID: "c6000000-0000-4000-8000-000000000006", ActorUserID: ownerID, AccountID: accountID, ExpectedAccountVersion: 1, Reason: "Business operation concluded", At: now, ExecuteAfter: now.Add(7 * 24 * time.Hour)}
	if _, err := repository.Request(ctx, first); !errors.Is(err, accountlifecycle.ErrBillingActive) {
		t.Fatalf("active billing request error=%v", err)
	}
	var accountState accounts.AccountState
	var version uint64
	if err := pool.QueryRow(ctx, `SELECT state,version FROM accounts WHERE id=$1`, accountID).Scan(&accountState, &version); err != nil || accountState != accounts.AccountActive || version != 1 {
		t.Fatalf("billing rejection changed Account: state=%s version=%d err=%v", accountState, version, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subscriptions SET state='canceled' WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	requested, err := repository.Request(ctx, first)
	if err != nil || requested.State != accountlifecycle.StateCoolingOff || requested.AccountState != accounts.AccountClosing || requested.AccountVersion != 2 {
		t.Fatalf("requested=%+v err=%v", requested, err)
	}
	authorizer, _ := access.NewAuthorizer(postgresadapter.NewAccessRepository(pool))
	if _, err := authorizer.Authorize(ctx, access.Actor{UserID: ownerID}, accountID, access.Requirement{}); !access.IsDenied(err, access.DenialAccountUnavailable) {
		t.Fatalf("closing Account authorization error=%v", err)
	}
	choices, err := postgresadapter.NewAccountAccessRepository(pool).Choices(ctx, ownerID)
	if err != nil || len(choices) != 0 {
		t.Fatalf("closing Account choices=%+v err=%v", choices, err)
	}

	if _, err := repository.Cancel(ctx, accountlifecycle.CancelMutation{EventID: "c7000000-0000-4000-8000-000000000007", ActorUserID: ownerID, AccountID: accountID, ExpectedAccountVersion: 1, Reason: "Stale restore attempt", At: now.Add(time.Hour)}); !errors.Is(err, accountlifecycle.ErrVersionConflict) {
		t.Fatalf("stale cancellation error=%v", err)
	}
	canceled, err := repository.Cancel(ctx, accountlifecycle.CancelMutation{EventID: "c8000000-0000-4000-8000-000000000008", ActorUserID: ownerID, AccountID: accountID, ExpectedAccountVersion: 2, Reason: "Operations will continue", At: now.Add(time.Hour)})
	if err != nil || canceled.State != accountlifecycle.StateCanceled || canceled.AccountState != accounts.AccountActive || canceled.AccountVersion != 3 {
		t.Fatalf("canceled=%+v err=%v", canceled, err)
	}

	second := accountlifecycle.RequestMutation{RequestID: "c9000000-0000-4000-8000-000000000009", EventID: "ca000000-0000-4000-8000-000000000010", ActorUserID: ownerID, AccountID: accountID, ExpectedAccountVersion: 3, Reason: "Approved final closure", At: now.Add(2 * time.Hour), ExecuteAfter: now.Add(7*24*time.Hour + 2*time.Hour)}
	if _, err := repository.Request(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repository.Claim(ctx, second.ExecuteAfter.Add(-time.Second), 2*time.Minute); err != nil || ok {
		t.Fatalf("premature claim ok=%v err=%v", ok, err)
	}
	work, ok, err := repository.Claim(ctx, second.ExecuteAfter, 2*time.Minute)
	if err != nil || !ok || work.Attempt != 1 || work.RequestID != second.RequestID {
		t.Fatalf("work=%+v ok=%v err=%v", work, ok, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subscriptions SET state='active' WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	blocked, err := repository.Evaluate(ctx, work, "cb000000-0000-4000-8000-000000000011", second.ExecuteAfter, 30*24*time.Hour, 24*time.Hour)
	if err != nil || blocked.State != accountlifecycle.StateBlocked || blocked.BlockerCode != "billing_active" || blocked.AccountVersion != 4 {
		t.Fatalf("blocked=%+v err=%v", blocked, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subscriptions SET state='canceled' WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	retryAt := second.ExecuteAfter.Add(24 * time.Hour)
	work, ok, err = repository.Claim(ctx, retryAt, 2*time.Minute)
	if err != nil || !ok || work.Attempt != 2 {
		t.Fatalf("retry work=%+v ok=%v err=%v", work, ok, err)
	}
	closed, err := repository.Evaluate(ctx, work, "cc000000-0000-4000-8000-000000000012", retryAt, 30*24*time.Hour, 24*time.Hour)
	if err != nil || closed.State != accountlifecycle.StateClosed || closed.AccountState != accounts.AccountClosed || closed.AccountVersion != 5 || closed.DeleteAfter == nil || !closed.DeleteAfter.Equal(retryAt.Add(30*24*time.Hour)) {
		t.Fatalf("closed=%+v err=%v", closed, err)
	}
	history, err := repository.ListOwned(ctx, ownerID)
	if err != nil || len(history) != 2 || history[0].State != accountlifecycle.StateClosed || history[1].State != accountlifecycle.StateCanceled {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_lifecycle_events WHERE account_id=$1`, accountID).Scan(&eventCount); err != nil || eventCount != 5 {
		t.Fatalf("event count=%d err=%v", eventCount, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE account_lifecycle_events SET reason='tampered' WHERE account_id=$1`, accountID); err == nil {
		t.Fatal("immutable Account lifecycle audit accepted an update")
	}
	if _, err := repository.Cancel(ctx, accountlifecycle.CancelMutation{EventID: "cd000000-0000-4000-8000-000000000013", ActorUserID: ownerID, AccountID: accountID, ExpectedAccountVersion: 5, Reason: "Closed cannot restore", At: retryAt.Add(time.Hour)}); !errors.Is(err, accountlifecycle.ErrStateConflict) {
		t.Fatalf("closed Account cancellation error=%v", err)
	}
}
