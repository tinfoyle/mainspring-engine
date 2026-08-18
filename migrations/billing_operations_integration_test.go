package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/billingadmin"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type billingAdminIDs struct{ values []string }

func (g *billingAdminIDs) New() string {
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

func TestPostgresBillingOperationsAreExactAuditedAndModeBound(t *testing.T) {
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
	for _, target := range []migrations.Target{migrations.Global, migrations.Development} {
		if _, err := migrations.Apply(ctx, pool, target); err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
	}
	now := time.Date(2026, 8, 18, 18, 0, 0, 0, time.UTC)
	userID, accountID := "ba100000-0000-4000-8000-000000000001", "ba200000-0000-4000-8000-000000000001"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,primary_email,display_name,state,email_verified_at,created_at) VALUES ($1,'billing-operator@example.com','Billing Owner','active',$2,$2)`, []any{userID, now}},
		{`INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,created_by_user_id,created_at) VALUES ($1,'billing-operator','Billing Operator','paid','active','cell-us-east-01',$2,$3)`, []any{accountID, userID, now}},
		{`INSERT INTO subscriptions(id,account_id,provider,provider_mode,provider_customer_id,provider_subscription_id,state,offer_code,offer_version,last_synced_at,created_at,updated_at) VALUES ('ba300000-0000-4000-8000-000000000001',$1,'stripe','test','cus_operator','sub_operator','active','team-monthly-v1',2,$2,$2,$2)`, []any{accountID, now}},
		{`INSERT INTO subscriptions(id,account_id,provider,provider_mode,provider_customer_id,provider_subscription_id,state,offer_code,offer_version,last_synced_at,created_at,updated_at) VALUES ('ba300000-0000-4000-8000-000000000002',$1,'stripe','test','cus_operator','sub_fresh','active','team-monthly-v1',2,$2,$2,$2)`, []any{accountID, now}},
		{`INSERT INTO billing_event_inbox(provider_event_id,account_id,event_type,provider_created_at,provider_object_id,mode,payload_hash,payload_reference,payload,signature_verified_at,processing_state,attempt_count,next_attempt_at,last_error_code,created_at) VALUES ('evt_operator',$1,'customer.subscription.updated',$2,'sub_operator','test',decode(repeat('01',32),'hex'),'postgres:inline','{}',$2,'failed',3,$2,'projection_failed',$2)`, []any{accountID, now}},
		{`INSERT INTO billing_reconciliation_queue(provider_subscription_id,reason,requested_at,next_attempt_at,attempt_count,last_error_code,processing_state) VALUES ('sub_operator','scheduled drift scan',$1,$1,2,'refresh_failed','failed')`, []any{now}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	service, err := billingadmin.NewService(postgresadapter.NewBillingAdminRepository(pool), &billingAdminIDs{values: []string{
		"ba400000-0000-4000-8000-000000000001", "ba400000-0000-4000-8000-000000000002", "ba400000-0000-4000-8000-000000000003", "ba400000-0000-4000-8000-000000000004",
		"ba400000-0000-4000-8000-000000000005", "ba400000-0000-4000-8000-000000000006",
	}})
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := service.Inspect(ctx, 10, "operator@example.com", "Inspect failed test billing work", "staging", "test")
	if err != nil || len(records) != 2 || records[0].Mode != "test" {
		t.Fatalf("inspection=%+v err=%v", records, err)
	}
	if _, _, err := service.ReplayEvent(ctx, "evt_operator", "operator@example.com", "Replay verified test event", "staging", "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.QueueRefresh(ctx, "sub_operator", "operator@example.com", "Refresh current test subscription", "staging", "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.QueueRefresh(ctx, "sub_fresh", "operator@example.com", "Queue first test subscription refresh", "staging", "test"); err != nil {
		t.Fatal(err)
	}
	var eventState, reconciliationState string
	if err := pool.QueryRow(ctx, `SELECT processing_state FROM billing_event_inbox WHERE provider_event_id='evt_operator'`).Scan(&eventState); err != nil || eventState != "accepted" {
		t.Fatalf("event state=%q err=%v", eventState, err)
	}
	if err := pool.QueryRow(ctx, `SELECT processing_state FROM billing_reconciliation_queue WHERE provider_subscription_id='sub_operator'`).Scan(&reconciliationState); err != nil || reconciliationState != "pending" {
		t.Fatalf("reconciliation state=%q err=%v", reconciliationState, err)
	}
	if _, _, err := service.ReplayEvent(ctx, "evt_operator", "operator@example.com", "Replay already queued event", "staging", "test"); !errors.Is(err, billingadmin.ErrStateConflict) {
		t.Fatalf("replayed active event=%v", err)
	}
	if _, _, err := service.QueueRefresh(ctx, "sub_operator", "operator@example.com", "Cross mode refresh attempt", "staging", "live"); !errors.Is(err, billingadmin.ErrNotFound) {
		t.Fatalf("cross-mode refresh=%v", err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_operator_events`).Scan(&auditCount); err != nil || auditCount != 5 {
		t.Fatalf("audit count=%d err=%v", auditCount, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing_operator_events SET reason='tampered'`); err == nil {
		t.Fatal("billing operator evidence was mutable")
	}
	if _, err := pool.Exec(ctx, `CREATE ROLE spyglass_billing_operator_contract NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	var inheritedExecute bool
	if err := pool.QueryRow(ctx, `SELECT has_function_privilege('spyglass_billing_operator_contract','public.spyglass_replay_billing_event(uuid,text,text,text,text,text)','EXECUTE')`).Scan(&inheritedExecute); err != nil || inheritedExecute {
		t.Fatalf("operator inherited function execution=%v err=%v", inheritedExecute, err)
	}
	if _, err := pool.Exec(ctx, `
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_billing_failures(uuid,text,text,text,text,integer) TO spyglass_billing_operator_contract;
		GRANT EXECUTE ON FUNCTION public.spyglass_replay_billing_event(uuid,text,text,text,text,text) TO spyglass_billing_operator_contract;
		GRANT EXECUTE ON FUNCTION public.spyglass_queue_billing_subscription_refresh(uuid,text,text,text,text,text) TO spyglass_billing_operator_contract`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE spyglass_billing_operator_contract`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT * FROM public.spyglass_inspect_billing_failures('ba500000-0000-4000-8000-000000000001','operator@example.com','Least privilege inspection','staging','test',10)`); err != nil {
		t.Fatalf("execute-only inspection=%v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE spyglass_billing_operator_contract`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT count(*) FROM public.billing_event_inbox`); err == nil {
		t.Fatal("execute-only operator could read billing inbox")
	}
	_ = tx.Rollback(ctx)
	if _, err := pool.Exec(ctx, `DROP OWNED BY spyglass_billing_operator_contract`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP ROLE spyglass_billing_operator_contract`); err != nil {
		t.Fatal(err)
	}
}
