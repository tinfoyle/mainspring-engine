package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsingest"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyconsent"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrights"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrightsadmin"
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

	rightsService, err := privacyrights.New(postgresadapter.NewPrivacyRightsRepository(pool), ids.RandomGenerator{}, fixedLifecycleClock{now})
	if err != nil {
		t.Fatal(err)
	}
	rightsSession := sessions.Session{UserID: affiliateUser, ReauthenticatedAt: now,
		ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
	rightsRequest, err := rightsService.Submit(ctx, privacyrights.SubmitCommand{UserID: affiliateUser, Session: rightsSession,
		Kind: privacy.RightsAccess, Scope: privacy.RightsAffiliate})
	if err != nil || rightsRequest.ResponseDueAt != now.AddDate(0, 1, 0) {
		t.Fatalf("privacy rights request=%+v err=%v", rightsRequest, err)
	}
	if _, err := rightsService.Submit(ctx, privacyrights.SubmitCommand{UserID: affiliateUser, Session: rightsSession,
		Kind: privacy.RightsAccess, Scope: privacy.RightsAffiliate}); !errors.Is(err, privacyrights.ErrAlreadyOpen) {
		t.Fatalf("duplicate privacy rights request error=%v", err)
	}
	canceledRights, err := rightsService.Cancel(ctx, privacyrights.CancelCommand{RequestID: rightsRequest.ID,
		UserID: affiliateUser, Session: rightsSession})
	var rightsEvents int
	if queryErr := pool.QueryRow(ctx, `SELECT count(*) FROM privacy_rights_request_events WHERE request_id=$1`, rightsRequest.ID).Scan(&rightsEvents); err != nil || queryErr != nil || canceledRights.State != privacy.RightsCanceled || rightsEvents != 2 {
		t.Fatalf("canceled rights=%+v events=%d err=%v query_err=%v", canceledRights, rightsEvents, err, queryErr)
	}
	fulfillmentRequest, err := rightsService.Submit(ctx, privacyrights.SubmitCommand{UserID: affiliateUser, Session: rightsSession,
		Kind: privacy.RightsErasure, Scope: privacy.RightsAnalytics})
	if err != nil {
		t.Fatal(err)
	}
	rightsAdmin, err := privacyrightsadmin.New(postgresadapter.NewPrivacyRightsAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	reviewedRights, err := rightsAdmin.StartReview(ctx, fulfillmentRequest.ID, fulfillmentRequest.Version,
		"privacy-operator@example.test", "Verify the submitted request", "local")
	if err != nil || reviewedRights.State != privacy.RightsInReview || reviewedRights.Version != 2 {
		t.Fatalf("reviewed rights=%+v err=%v", reviewedRights, err)
	}
	if _, err := rightsAdmin.Resolve(ctx, fulfillmentRequest.ID, fulfillmentRequest.Version, privacy.RightsCompleted,
		privacyrightsadmin.ResolutionEvidence{ID: "10000000-0000-4000-8000-000000000012", SHA256: [32]byte{1}},
		"privacy-operator@example.test", "Record reviewed fulfillment evidence", "local"); !errors.Is(err, privacyrightsadmin.ErrStateConflict) {
		t.Fatalf("stale privacy rights resolution error=%v", err)
	}
	evidence := privacyrightsadmin.ResolutionEvidence{ID: "10000000-0000-4000-8000-000000000012", SHA256: [32]byte{1, 2, 3}}
	completedRights, err := rightsAdmin.Resolve(ctx, fulfillmentRequest.ID, reviewedRights.Version, privacy.RightsCompleted, evidence,
		"privacy-operator@example.test", "Record reviewed fulfillment evidence", "local")
	var storedEvidenceID string
	var storedEvidenceSHA []byte
	if queryErr := pool.QueryRow(ctx, `SELECT evidence_id::text,evidence_sha256 FROM privacy_rights_request_events WHERE request_id=$1 AND action='resolved'`, fulfillmentRequest.ID).Scan(&storedEvidenceID, &storedEvidenceSHA); err != nil || queryErr != nil || completedRights.State != privacy.RightsCompleted || completedRights.Version != 3 || storedEvidenceID != evidence.ID || len(storedEvidenceSHA) != 32 {
		t.Fatalf("completed rights=%+v evidence_id=%q evidence_size=%d err=%v query_err=%v", completedRights, storedEvidenceID, len(storedEvidenceSHA), err, queryErr)
	}
	if _, err := pool.Exec(ctx, `UPDATE privacy_rights_request_events SET state='declined' WHERE request_id=$1 AND action='resolved'`, fulfillmentRequest.ID); err == nil {
		t.Fatal("privacy rights fulfillment evidence was mutable")
	}
	const legacyRequest = "10000000-0000-4000-8000-000000000013"
	const legacySubmittedEvent = "10000000-0000-4000-8000-000000000014"
	const legacyCanceledEvent = "10000000-0000-4000-8000-000000000015"
	legacyWrites := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO privacy_rights_requests
			(request_id,user_id,kind,scope,state,verified_at,requested_at,response_due_at,updated_at)
		 VALUES ($1,$2,'correction','identity','submitted',$3,$3,$3::timestamptz+interval '1 month',$3)`, []any{legacyRequest, affiliateUser, now}},
		{`INSERT INTO privacy_rights_request_events (event_id,request_id,state,occurred_at) VALUES ($1,$2,'submitted',$3)`, []any{legacySubmittedEvent, legacyRequest, now}},
		{`UPDATE privacy_rights_requests SET state='canceled',updated_at=$2::timestamptz+interval '1 second' WHERE request_id=$1`, []any{legacyRequest, now}},
		{`INSERT INTO privacy_rights_request_events (event_id,request_id,state,occurred_at) VALUES ($1,$2,'canceled',$3::timestamptz+interval '1 second')`, []any{legacyCanceledEvent, legacyRequest, now}},
	}
	for _, write := range legacyWrites {
		if _, err := pool.Exec(ctx, write.query, write.args...); err != nil {
			t.Fatalf("rolling compatibility write: %v", err)
		}
	}
	var legacyVersion uint64
	var legacyActions []string
	if err := pool.QueryRow(ctx, `SELECT version FROM privacy_rights_requests WHERE request_id=$1`, legacyRequest).Scan(&legacyVersion); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT action FROM privacy_rights_request_events WHERE request_id=$1 ORDER BY version`, legacyRequest)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		legacyActions = append(legacyActions, action)
	}
	rows.Close()
	if legacyVersion != 2 || len(legacyActions) != 2 || legacyActions[0] != "submitted" || legacyActions[1] != "canceled" {
		t.Fatalf("legacy version=%d actions=%v", legacyVersion, legacyActions)
	}
	const operatorRole = "spyglass_privacy_rights_operator_contract"
	if _, err := pool.Exec(ctx, `CREATE ROLE `+operatorRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA public TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_privacy_rights_request(uuid,uuid,text,text,text) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_transition_privacy_rights_request(uuid,uuid,bigint,text,text,uuid,bytea,text,text,text) TO `+operatorRole); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+operatorRole+`; DROP ROLE `+operatorRole)
	}()
	var directTableAccess, inspectFunctionAccess, transitionFunctionAccess bool
	if err := pool.QueryRow(ctx, `SELECT
		has_table_privilege($1,'public.privacy_rights_requests','SELECT'),
		has_function_privilege($1,'public.spyglass_inspect_privacy_rights_request(uuid,uuid,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_transition_privacy_rights_request(uuid,uuid,bigint,text,text,uuid,bytea,text,text,text)','EXECUTE')`, operatorRole).Scan(
		&directTableAccess, &inspectFunctionAccess, &transitionFunctionAccess); err != nil || directTableAccess || !inspectFunctionAccess || !transitionFunctionAccess {
		t.Fatalf("operator table=%v inspect=%v transition=%v err=%v", directTableAccess, inspectFunctionAccess, transitionFunctionAccess, err)
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
	privateDecision, err := consent.Set(ctx, privacyconsent.SetCommand{Surface: privacy.SurfacePrivate, Analytics: true})
	if err != nil || consent.Link(ctx, privateDecision.SubjectID, affiliateUser) != nil {
		t.Fatalf("private consent link decision=%+v err=%v", privateDecision, err)
	}
	var linkedUser string
	if err := pool.QueryRow(ctx, `SELECT user_id::text FROM privacy_consent_subjects WHERE id=$1`, privateDecision.SubjectID).Scan(&linkedUser); err != nil || linkedUser != affiliateUser {
		t.Fatalf("linked privacy user=%q err=%v", linkedUser, err)
	}
	if err := consent.Link(ctx, privateDecision.SubjectID, customerUser); !errors.Is(err, privacyconsent.ErrSubjectOwned) {
		t.Fatalf("cross-user privacy subject link error=%v", err)
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
