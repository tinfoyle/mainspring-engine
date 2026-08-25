package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresAnalyticsReportIsAggregatedSuppressedAndLeastPrivilege(t *testing.T) {
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

	now := time.Now().UTC()
	from := now.Add(-2 * time.Hour)
	statements := []string{
		`INSERT INTO privacy_consent_subjects(id,created_at) VALUES
		 ('20000000-0000-4000-8000-000000000001',$1),
		 ('20000000-0000-4000-8000-000000000002',$1),
		 ('20000000-0000-4000-8000-000000000003',$1),
		 ('20000000-0000-4000-8000-000000000004',$1),
		 ('20000000-0000-4000-8000-000000000005',$1)`,
		`INSERT INTO privacy_consent_decisions(decision_id,subject_id,policy_version,surface,analytics,marketing,effective_at) VALUES
		 ('20000000-0000-4000-8000-000000000011','20000000-0000-4000-8000-000000000001',1,'public',true,false,$1),
		 ('20000000-0000-4000-8000-000000000012','20000000-0000-4000-8000-000000000002',1,'public',true,false,$1),
		 ('20000000-0000-4000-8000-000000000013','20000000-0000-4000-8000-000000000003',1,'public',true,false,$1),
		 ('20000000-0000-4000-8000-000000000014','20000000-0000-4000-8000-000000000004',1,'public',true,false,$1),
		 ('20000000-0000-4000-8000-000000000015','20000000-0000-4000-8000-000000000005',1,'public',true,false,$1)`,
		`INSERT INTO analytics_events(event_id,subject_id,consent_decision_id,event_name,surface,occurred_at,fields,ingested_at) VALUES
		 ('20000000-0000-4000-8000-000000000021','20000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000011','landing_viewed','public',$1,'{"device_class":"phone"}',$1),
		 ('20000000-0000-4000-8000-000000000022','20000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000011','landing_viewed','public',$1::timestamptz+interval '1 second','{"device_class":"phone"}',$1),
		 ('20000000-0000-4000-8000-000000000023','20000000-0000-4000-8000-000000000002','20000000-0000-4000-8000-000000000012','landing_viewed','public',$1::timestamptz+interval '2 seconds','{"device_class":"phone"}',$1),
		 ('20000000-0000-4000-8000-000000000024','20000000-0000-4000-8000-000000000003','20000000-0000-4000-8000-000000000013','landing_viewed','public',$1::timestamptz+interval '3 seconds','{"device_class":"phone"}',$1),
		 ('20000000-0000-4000-8000-000000000025','20000000-0000-4000-8000-000000000004','20000000-0000-4000-8000-000000000014','landing_viewed','public',$1::timestamptz+interval '4 seconds','{"device_class":"phone"}',$1),
		 ('20000000-0000-4000-8000-000000000026','20000000-0000-4000-8000-000000000005','20000000-0000-4000-8000-000000000015','landing_viewed','public',$1::timestamptz+interval '5 seconds','{"device_class":"phone"}',$1)`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement, from.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	var eventCount, uniqueSubjects uint64
	var dimension string
	if err := pool.QueryRow(ctx, `SELECT dimension_value,event_count,unique_subjects
		FROM spyglass_analytics_funnel_report($1,$2,'day','device_class',5)`, from, now).Scan(
		&dimension, &eventCount, &uniqueSubjects); err != nil {
		t.Fatal(err)
	}
	if dimension != "phone" || eventCount != 6 || uniqueSubjects != 5 {
		t.Fatalf("dimension=%q event_count=%d unique_subjects=%d", dimension, eventCount, uniqueSubjects)
	}

	var suppressed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass_analytics_funnel_report($1,$2,'day','none',6)`, from, now).Scan(&suppressed); err != nil || suppressed != 0 {
		t.Fatalf("suppressed rows=%d err=%v", suppressed, err)
	}
	if _, err := pool.Exec(ctx, `SELECT * FROM spyglass_analytics_funnel_report($1,$2,'day','email',5)`, from, now); err == nil {
		t.Fatal("unreviewed analytics dimension was accepted")
	}

	const reporterRole = "spyglass_analytics_reporter_contract"
	if _, err := pool.Exec(ctx, `CREATE ROLE `+reporterRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA public TO `+reporterRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_analytics_funnel_report(timestamptz,timestamptz,text,text,integer) TO `+reporterRole); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+reporterRole+`; DROP ROLE `+reporterRole)
	}()
	var publicExecute, directTableAccess, reportExecute bool
	if err := pool.QueryRow(ctx, `SELECT
		has_function_privilege('public','public.spyglass_analytics_funnel_report(timestamptz,timestamptz,text,text,integer)','EXECUTE'),
		has_table_privilege($1,'public.analytics_events','SELECT'),
		has_function_privilege($1,'public.spyglass_analytics_funnel_report(timestamptz,timestamptz,text,text,integer)','EXECUTE')`, reporterRole).Scan(
		&publicExecute, &directTableAccess, &reportExecute); err != nil || publicExecute || directTableAccess || !reportExecute {
		t.Fatalf("public_execute=%v table=%v report_execute=%v err=%v", publicExecute, directTableAccess, reportExecute, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+reporterRole); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT event_count FROM spyglass_analytics_funnel_report($1,$2,'day','none',5)`, from, now).Scan(&eventCount); err != nil || eventCount != 6 {
		t.Fatalf("least-privilege report event_count=%d err=%v", eventCount, err)
	}
	if _, err := tx.Exec(ctx, `SELECT event_name FROM analytics_events LIMIT 1`); err == nil {
		t.Fatal("reporter read raw analytics events")
	}
}
