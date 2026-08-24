package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsingest"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyconsent"
	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresPrivacyAnalyticsAndAffiliateLifecycle(t *testing.T) {
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
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	const affiliateUser = "10000000-0000-4000-8000-000000000001"
	const customerUser = "10000000-0000-4000-8000-000000000002"
	const affiliateAccount = "10000000-0000-4000-8000-000000000003"
	const customerAccount = "10000000-0000-4000-8000-000000000004"
	const checkoutRequest = "10000000-0000-4000-8000-000000000005"
	statements := []string{
		`INSERT INTO cells(id,region,state,soft_account_limit,created_at) VALUES ('cell-test','us-east','active',100,$1)`,
		`INSERT INTO users(id,primary_email,display_name,state,created_at) VALUES
		 ('` + affiliateUser + `','affiliate@example.test','Affiliate','active',$1),
		 ('` + customerUser + `','customer@example.test','Customer','active',$1)`,
		`INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,created_by_user_id,created_at) VALUES
		 ('` + affiliateAccount + `','affiliate','Affiliate','free','active','cell-test','` + affiliateUser + `',$1),
		 ('` + customerAccount + `','customer','Customer','free','active','cell-test','` + customerUser + `',$1)`,
		`INSERT INTO memberships(id,account_id,user_id,role,state,created_at) VALUES
		 ('10000000-0000-4000-8000-000000000006','` + affiliateAccount + `','` + affiliateUser + `','owner','active',$1),
		 ('10000000-0000-4000-8000-000000000007','` + customerAccount + `','` + customerUser + `','owner','active',$1)`,
		`INSERT INTO billing_checkout_attempts(request_id,account_id,provider,mode,offer_code,state,expires_at,created_at,updated_at)
		 VALUES ('` + checkoutRequest + `','` + customerAccount + `','stripe','test','team-monthly-v1','active',$1::timestamptz+interval '30 minutes',$1,$1)`,
		`INSERT INTO affiliate_commission_rules(rule_id,version,offer_code,currency,eligible_invoice_minor,commission_minor,initial_invoice_qualifies,maximum_cycles,hold_days,effective_from)
		 VALUES ('10000000-0000-4000-8000-000000000008',1,'team-monthly-v1','USD',5000,1000,false,0,30,$1)`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement, now); err != nil {
			t.Fatal(err)
		}
	}

	affiliateRepository := postgresadapter.NewAffiliateProgramRepository(pool)
	affiliateService, _ := affiliateprogram.New(affiliateRepository, ids.RandomGenerator{}, fixedAffiliateCode{"IO-PARTNER1"}, fixedLifecycleClock{now}, 1, 1)
	enrollment, err := affiliateService.Enroll(ctx, affiliateprogram.EnrollCommand{UserID: affiliateUser, Session: sessions.Session{UserID: affiliateUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}, SettlementAccountID: affiliateAccount, AcceptedTermsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	attribution, err := affiliateService.Reserve(ctx, affiliateprogram.ReserveCommand{PublicCode: enrollment.PublicCode,
		ReferredAccountID: customerAccount, CheckoutRequestID: checkoutRequest, OfferCode: "team-monthly-v1", OfferVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := affiliateService.Lock(ctx, attribution.ID, "sub_affiliate_test"); err != nil {
		t.Fatal(err)
	}
	paid := affiliateprogram.PaidInvoice{SubscriptionID: "sub_affiliate_test", InvoiceID: "in_affiliate_renewal", AmountPaidMinor: 5000, Currency: "USD"}
	first, err := affiliateService.RecordPaidInvoice(ctx, paid)
	if err != nil {
		t.Fatal(err)
	}
	second, err := affiliateService.RecordPaidInvoice(ctx, paid)
	if err != nil || first.ID != second.ID || first.Cycle != 2 {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}

	privacyRepository := postgresadapter.NewPrivacyConsentRepository(pool)
	consent, _ := privacyconsent.New(privacyRepository, ids.RandomGenerator{}, fixedLifecycleClock{now}, 1)
	decision, err := consent.Set(ctx, privacyconsent.SetCommand{Surface: privacy.SurfacePublic, Analytics: true})
	if err != nil {
		t.Fatal(err)
	}
	ingestion, _ := analyticsingest.New(privacyRepository, postgresadapter.NewAnalyticsEventSink(pool), analytics.LaunchRegistry(), fixedLifecycleClock{now}, 1)
	event := analytics.Envelope{ID: ids.AnalyticsEventID(ids.RandomGenerator{}.New()), SubjectID: decision.SubjectID,
		Name: analytics.LandingViewed, Surface: privacy.SurfacePublic, OccurredAt: now, Fields: map[string]string{"device_class": "phone"}}
	if err := ingestion.Ingest(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := ingestion.Ingest(ctx, event); err != nil {
		t.Fatalf("exact analytics replay: %v", err)
	}
	history, err := consent.History(ctx, decision.SubjectID)
	if err != nil || len(history) != 1 || history[0].ID != decision.ID {
		t.Fatalf("privacy history=%+v err=%v", history, err)
	}
	if err := consent.Erase(ctx, decision.SubjectID); err != nil {
		t.Fatal(err)
	}
	var privacyRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics_events WHERE subject_id=$1`, decision.SubjectID).Scan(&privacyRows); err != nil || privacyRows != 0 {
		t.Fatalf("privacy rows=%d err=%v", privacyRows, err)
	}

	const retainedSubject = "10000000-0000-4000-8000-000000000009"
	const retainedDecision = "10000000-0000-4000-8000-000000000010"
	const retainedEvent = "10000000-0000-4000-8000-000000000011"
	old := now.Add(-400 * 24 * time.Hour)
	retentionStatements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO privacy_consent_subjects(id,created_at) VALUES ($1,$2)`, []any{retainedSubject, old}},
		{`INSERT INTO privacy_consent_decisions(decision_id,subject_id,policy_version,surface,analytics,marketing,effective_at)
		 VALUES ($1,$2,1,'public',true,false,$3)`, []any{retainedDecision, retainedSubject, old}},
		{`INSERT INTO analytics_events(event_id,subject_id,consent_decision_id,event_name,surface,occurred_at,fields,ingested_at)
		 VALUES ($1,$2,$3,'landing_viewed','public',$4,'{}'::jsonb,$4)`, []any{retainedEvent, retainedSubject, retainedDecision, old}},
	}
	for _, statement := range retentionStatements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	var total, eligible, age int64
	if err := pool.QueryRow(ctx, `SELECT total,eligible,oldest_eligible_age_seconds FROM spyglass_analytics_retention_stats($1,$2)`, now, int64((395*24*time.Hour)/time.Second)).Scan(&total, &eligible, &age); err != nil || total != 1 || eligible != 1 || age < int64((400*24*time.Hour)/time.Second) {
		t.Fatalf("retention stats total=%d eligible=%d age=%d err=%v", total, eligible, age, err)
	}
	var pruned int64
	if err := pool.QueryRow(ctx, `SELECT spyglass_prune_analytics_events($1,$2,$3)`, now, int64((395*24*time.Hour)/time.Second), 100).Scan(&pruned); err != nil || pruned != 1 {
		t.Fatalf("retention prune=%d err=%v", pruned, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM memberships WHERE account_id=$1`, customerAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM billing_checkout_attempts WHERE account_id=$1`, customerAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, customerAccount); err != nil {
		t.Fatal(err)
	}
	var detachedAccount, detachedCheckout *string
	var commissions int
	if err := pool.QueryRow(ctx, `SELECT referred_account_id::text,checkout_request_id::text FROM affiliate_attributions WHERE attribution_id=$1`, attribution.ID).Scan(&detachedAccount, &detachedCheckout); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM affiliate_commission_entries WHERE attribution_id=$1`, attribution.ID).Scan(&commissions); err != nil || detachedAccount != nil || detachedCheckout != nil || commissions != 1 {
		t.Fatalf("account=%v checkout=%v commissions=%d err=%v", detachedAccount, detachedCheckout, commissions, err)
	}
}

type fixedAffiliateCode struct{ value string }

func (g fixedAffiliateCode) NewCode() (string, error) { return g.value, nil }

type fixedLifecycleClock struct{ now time.Time }

func (c fixedLifecycleClock) Now() time.Time { return c.now }
