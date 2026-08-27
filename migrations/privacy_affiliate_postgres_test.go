package migrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesupport"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesupportadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsingest"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyconsent"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrights"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrightsadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
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
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
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
		`INSERT INTO billing_profiles(account_id,stripe_customer_id,billing_email,version,created_at,updated_at)
		 VALUES ('` + affiliateAccount + `','cus_affiliate_settlement','affiliate@example.test',1,$1,$1)`,
		`INSERT INTO billing_checkout_attempts(request_id,account_id,provider,mode,offer_code,state,expires_at,created_at,updated_at)
		 VALUES ('` + checkoutRequest + `','` + customerAccount + `','stripe','test','team-monthly-v1','active',$1::timestamptz+interval '30 minutes',$1,$1)`,
		`INSERT INTO affiliate_commission_rules(rule_id,version,offer_code,currency,eligible_invoice_minor,commission_minor,initial_invoice_qualifies,maximum_cycles,hold_days,effective_from)
		 VALUES ('10000000-0000-4000-8000-000000000008',1,'team-monthly-v1','USD',5000,1000,false,0,30,$1)`,
		`INSERT INTO catalog_publications(version,state,content,content_hash,created_at,created_by,change_reason)
		 SELECT 999,'draft',content,decode(repeat('99',32),'hex'),$1,'migration-test','exercise Affiliate line matching' FROM catalog_publications WHERE version=1`,
		`INSERT INTO offer_provider_prices(catalog_version,offer_code,provider,mode,provider_price_id,active,created_at)
		 VALUES (999,'team-monthly-v1','stripe','test','price_affiliate_team',true,$1)`,
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
	secondOpenRequest, err := rightsService.Submit(ctx, privacyrights.SubmitCommand{UserID: affiliateUser, Session: rightsSession,
		Kind: privacy.RightsRestriction, Scope: privacy.RightsIdentity})
	if err != nil {
		t.Fatal(err)
	}
	rightsAdmin, err := privacyrightsadmin.New(postgresadapter.NewPrivacyRightsAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	beforeDue, err := rightsAdmin.ListOpen(ctx, fulfillmentRequest.ResponseDueAt.Add(-time.Nanosecond), 25,
		"privacy-operator@example.test", "Prioritize requests approaching deadline", "local")
	if err != nil || len(beforeDue) != 0 {
		t.Fatalf("privacy rights queue before due=%+v err=%v", beforeDue, err)
	}
	due, err := rightsAdmin.ListOpen(ctx, fulfillmentRequest.ResponseDueAt, 25,
		"privacy-operator@example.test", "Prioritize requests approaching deadline", "local")
	if err != nil || len(due) != 2 || string(due[0].RequestID) >= string(due[1].RequestID) {
		t.Fatalf("privacy rights queue=%+v err=%v", due, err)
	}
	queueByID := map[ids.PrivacyRightsRequestID]privacyrightsadmin.QueueItem{due[0].RequestID: due[0], due[1].RequestID: due[1]}
	if item := queueByID[fulfillmentRequest.ID]; item.Version != fulfillmentRequest.Version || item.Kind != fulfillmentRequest.Kind ||
		item.Scope != fulfillmentRequest.Scope || item.State != fulfillmentRequest.State || queueByID[secondOpenRequest.ID].Kind != secondOpenRequest.Kind {
		t.Fatalf("privacy rights queue projection=%+v", queueByID)
	}
	var queueAccesses, lastResultCount int
	if queryErr := pool.QueryRow(ctx, `SELECT count(*),max(result_count) FROM privacy_rights_queue_access_events`).Scan(&queueAccesses, &lastResultCount); queryErr != nil || queueAccesses != 2 || lastResultCount != 2 {
		t.Fatalf("privacy rights queue accesses=%d result_count=%d err=%v", queueAccesses, lastResultCount, queryErr)
	}
	if _, err := pool.Exec(ctx, `UPDATE privacy_rights_queue_access_events SET result_count=0`); err == nil {
		t.Fatal("privacy rights queue access evidence was mutable")
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
	if _, err := pool.Exec(ctx, `INSERT INTO privacy_rights_requests
		(request_id,user_id,kind,scope,state,verified_at,requested_at,response_due_at,updated_at)
		VALUES ('10000000-0000-4000-8000-000000000016',$1,'access','identity','submitted',$2,$2,$2::timestamptz+interval '2 months',$2)`, affiliateUser, now); err == nil {
		t.Fatal("database accepted an overstated privacy-rights response deadline")
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
		GRANT EXECUTE ON FUNCTION public.spyglass_list_open_privacy_rights_requests(uuid,timestamptz,integer,text,text,text) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_privacy_rights_request(uuid,uuid,text,text,text) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_transition_privacy_rights_request(uuid,uuid,bigint,text,text,uuid,bytea,text,text,text) TO `+operatorRole); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+operatorRole+`; DROP ROLE `+operatorRole)
	}()
	var directTableAccess, directQueueAuditAccess, listFunctionAccess, inspectFunctionAccess, transitionFunctionAccess bool
	if err := pool.QueryRow(ctx, `SELECT
		has_table_privilege($1,'public.privacy_rights_requests','SELECT'),
		has_table_privilege($1,'public.privacy_rights_queue_access_events','SELECT'),
		has_function_privilege($1,'public.spyglass_list_open_privacy_rights_requests(uuid,timestamptz,integer,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_inspect_privacy_rights_request(uuid,uuid,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_transition_privacy_rights_request(uuid,uuid,bigint,text,text,uuid,bytea,text,text,text)','EXECUTE')`, operatorRole).Scan(
		&directTableAccess, &directQueueAuditAccess, &listFunctionAccess, &inspectFunctionAccess, &transitionFunctionAccess); err != nil || directTableAccess || directQueueAuditAccess || !listFunctionAccess || !inspectFunctionAccess || !transitionFunctionAccess {
		t.Fatalf("operator table=%v queue_audit=%v list=%v inspect=%v transition=%v err=%v", directTableAccess, directQueueAuditAccess, listFunctionAccess, inspectFunctionAccess, transitionFunctionAccess, err)
	}

	affiliateRepository := postgresadapter.NewAffiliateProgramRepository(pool)
	affiliateService, _ := affiliateprogram.New(affiliateRepository, ids.RandomGenerator{}, fixedAffiliateCode{"IO-PARTNER1"}, fixedLifecycleClock{now}, 1, 1)
	affiliateSession := sessions.Session{UserID: affiliateUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
	enrollment, err := affiliateService.Enroll(ctx, affiliateprogram.EnrollCommand{UserID: affiliateUser, Session: affiliateSession, SettlementAccountID: affiliateAccount, AcceptedTermsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	affiliateService, _ = affiliateprogram.New(affiliateRepository, ids.RandomGenerator{}, fixedAffiliateCode{"IO-PARTNER2"}, fixedLifecycleClock{now}, 1, 1)
	enrollment, err = affiliateService.ReplaceCode(ctx, affiliateprogram.ReplaceCodeCommand{UserID: affiliateUser, Session: affiliateSession, ExpectedVersion: enrollment.Version})
	if err != nil || enrollment.PublicCode != "IO-PARTNER2" || enrollment.Version != 2 {
		t.Fatalf("first code replacement=%+v err=%v", enrollment, err)
	}
	if _, err := affiliateRepository.EnrollmentByCode(ctx, "IO-PARTNER1"); !errors.Is(err, affiliateprogram.ErrCodeUnavailable) {
		t.Fatalf("retired public code lookup error=%v", err)
	}
	attribution, err := affiliateService.Reserve(ctx, affiliateprogram.ReserveCommand{PublicCode: enrollment.PublicCode,
		ReferredAccountID: customerAccount, CheckoutRequestID: checkoutRequest, OfferCode: "team-monthly-v1", OfferVersion: 999})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := affiliateService.Lock(ctx, attribution.ID, "sub_affiliate_test"); err != nil {
		t.Fatal(err)
	}
	affiliateService, _ = affiliateprogram.New(affiliateRepository, ids.RandomGenerator{}, fixedAffiliateCode{"IO-PARTNER3"}, fixedLifecycleClock{now}, 1, 1)
	enrollment, err = affiliateService.ReplaceCode(ctx, affiliateprogram.ReplaceCodeCommand{UserID: affiliateUser, Session: affiliateSession, ExpectedVersion: enrollment.Version})
	if err != nil || enrollment.PublicCode != "IO-PARTNER3" || enrollment.Version != 3 {
		t.Fatalf("locked-attribution code replacement=%+v err=%v", enrollment, err)
	}
	reuseService, _ := affiliateprogram.New(affiliateRepository, ids.RandomGenerator{}, fixedAffiliateCode{"IO-PARTNER1"}, fixedLifecycleClock{now}, 1, 1)
	if _, err := reuseService.ReplaceCode(ctx, affiliateprogram.ReplaceCodeCommand{UserID: affiliateUser, Session: affiliateSession, ExpectedVersion: enrollment.Version}); !errors.Is(err, affiliateprogram.ErrCodeUnavailable) {
		t.Fatalf("retired code reuse error=%v", err)
	}
	var codeHistory, retiredCodes int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE replaced_at IS NOT NULL) FROM affiliate_public_code_history WHERE affiliate_id=$1`, enrollment.ID).Scan(&codeHistory, &retiredCodes); err != nil || codeHistory != 3 || retiredCodes != 2 {
		t.Fatalf("Affiliate code history=%d retired=%d err=%v", codeHistory, retiredCodes, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE affiliate_public_code_history SET replaced_at=statement_timestamp() WHERE public_code='IO-PARTNER1'`); err == nil {
		t.Fatal("retired Affiliate public code history was mutable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM affiliate_public_code_history WHERE public_code='IO-PARTNER1'`); err == nil {
		t.Fatal("retired Affiliate public code history was deletable")
	}
	paid := affiliateprogram.PaidInvoice{SubscriptionID: "sub_affiliate_test", InvoiceID: "in_affiliate_renewal", PaymentIntentID: "pi_affiliate_renewal", AmountPaidMinor: 5000, Currency: "USD", Mode: "test",
		Lines: []affiliateprogram.InvoiceLine{{ID: "il_affiliate_renewal", ProviderPriceID: "price_affiliate_team", AmountMinor: 5000, Currency: "USD"}}}
	first, err := affiliateService.RecordPaidInvoice(ctx, paid)
	if err != nil {
		t.Fatal(err)
	}
	second, err := affiliateService.RecordPaidInvoice(ctx, paid)
	if err != nil || first.ID != second.ID || first.Cycle != 2 {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
	partial := affiliates.AdverseBillingEvidence{EventID: "evt_affiliate_partial_refund", Kind: affiliates.AdverseRefund,
		ProviderObjectID: "re_affiliate_partial", PaymentIntentID: paid.PaymentIntentID, AmountMinor: 2000, Currency: "USD", OccurredAt: now}
	partialReversal, reversed, err := affiliateService.RecordAdverseBilling(ctx, partial)
	if err != nil || !reversed || partialReversal.ReversesID == nil || *partialReversal.ReversesID != first.ID {
		t.Fatalf("partial reversal=%+v reversed=%v err=%v", partialReversal, reversed, err)
	}
	remaining := affiliates.AdverseBillingEvidence{EventID: "evt_affiliate_remaining_refund", Kind: affiliates.AdverseRefund,
		ProviderObjectID: "re_affiliate_remaining", PaymentIntentID: paid.PaymentIntentID, AmountMinor: 3000, Currency: "USD", OccurredAt: now}
	reversal, reversed, err := affiliateService.RecordAdverseBilling(ctx, remaining)
	if err != nil || !reversed || reversal.ID != partialReversal.ID || reversal.ReversesID == nil || *reversal.ReversesID != first.ID {
		t.Fatalf("full reversal=%+v reversed=%v err=%v", reversal, reversed, err)
	}
	replayed, replayedReversal, err := affiliateService.RecordAdverseBilling(ctx, remaining)
	if err != nil || !replayedReversal || replayed.ID != reversal.ID {
		t.Fatalf("replayed reversal=%+v reversed=%v err=%v", replayed, replayedReversal, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE affiliate_provider_adverse_events SET amount_minor=5000 WHERE provider_event_id=$1`, remaining.EventID); err == nil {
		t.Fatal("Affiliate adverse provider evidence was mutable")
	}
	futureEvidence := affiliates.AdverseBillingEvidence{EventID: "evt_affiliate_early_dispute", Kind: affiliates.AdverseDispute,
		ProviderObjectID: "dp_affiliate_early", PaymentIntentID: "pi_affiliate_next", AmountMinor: 5000, Currency: "USD", OccurredAt: now}
	if early, reversed, err := affiliateService.RecordAdverseBilling(ctx, futureEvidence); err != nil || reversed {
		t.Fatalf("early adverse evidence reversal=%+v reversed=%v err=%v", early, reversed, err)
	}
	next, err := affiliateService.RecordPaidInvoice(ctx, affiliateprogram.PaidInvoice{SubscriptionID: "sub_affiliate_test",
		InvoiceID: "in_affiliate_next", PaymentIntentID: futureEvidence.PaymentIntentID, AmountPaidMinor: 5000, Currency: "USD", Mode: "test",
		Lines: []affiliateprogram.InvoiceLine{{ID: "il_affiliate_next", ProviderPriceID: "price_affiliate_team", AmountMinor: 5000, Currency: "USD"}}})
	if err != nil || next.Cycle != 3 {
		t.Fatalf("next earning=%+v err=%v", next, err)
	}
	statement, err := affiliateService.Statement(ctx, affiliateUser)
	if err != nil || statement.ReferredSubscriptions != 1 || statement.PendingMinor != 0 || statement.AvailableMinor != 0 || statement.ReversedMinor != 2000 || len(statement.Entries) != 4 {
		t.Fatalf("Affiliate statement=%+v err=%v", statement, err)
	}
	third, err := affiliateService.RecordPaidInvoice(ctx, affiliateprogram.PaidInvoice{SubscriptionID: "sub_affiliate_test",
		InvoiceID: "in_affiliate_third", PaymentIntentID: "pi_affiliate_third", AmountPaidMinor: 4500, Currency: "USD", Mode: "test",
		Lines: []affiliateprogram.InvoiceLine{{ID: "il_affiliate_third", ProviderPriceID: "price_affiliate_team", AmountMinor: 4500, Currency: "USD"}}})
	if err != nil || third.AmountMinor != 900 || third.Cycle != 4 {
		t.Fatalf("third earning=%+v err=%v", third, err)
	}
	fourth, err := affiliateService.RecordPaidInvoice(ctx, affiliateprogram.PaidInvoice{SubscriptionID: "sub_affiliate_test",
		InvoiceID: "in_affiliate_fourth", PaymentIntentID: "pi_affiliate_fourth", AmountPaidMinor: 5000, Currency: "USD", Mode: "test",
		Lines: []affiliateprogram.InvoiceLine{{ID: "il_affiliate_fourth", ProviderPriceID: "price_affiliate_team", AmountMinor: 5000, Currency: "USD"}}})
	if err != nil || fourth.Cycle != 5 {
		t.Fatalf("fourth earning=%+v err=%v", fourth, err)
	}
	affiliateAdmin, err := affiliateadmin.New(postgresadapter.NewAffiliateAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := affiliateAdmin.PublishSettlementPolicy(ctx, 1, 2, 500, "affiliate-operator@example.test",
		"Exercise the reviewed Support check threshold", "local")
	if err != nil || policy.Version != 2 || policy.CheckThresholdMinor != 500 {
		t.Fatalf("Affiliate settlement policy=%+v err=%v", policy, err)
	}
	var settlementNow time.Time
	if err := pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&settlementNow); err != nil {
		t.Fatal(err)
	}
	const affiliatePasskeySession = "10000000-0000-4000-8000-000000000089"
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions
			(id,user_id,token_hash,security_version,authenticated_at,reauthenticated_at,last_seen_at,rotated_at,expires_at,
			 client_label,authentication_method,reauthentication_method)
		SELECT $1,$2,decode(repeat('89',32),'hex'),security_version,statement_timestamp()-interval '1 minute',
		       statement_timestamp(),statement_timestamp(),statement_timestamp(),statement_timestamp()+interval '1 hour',
		       'Affiliate Support confirmation','password','passkey'
		FROM users WHERE id=$2`, affiliatePasskeySession, affiliateUser); err != nil {
		t.Fatal(err)
	}
	checkReservation, err := affiliateAdmin.ReserveSupportCheck(ctx, enrollment.ID, affiliatePasskeySession, 500,
		"affiliate-operator@example.test", "Reserve the customer-confirmed Support check amount", "local")
	if err != nil || checkReservation.State != "reserved" || checkReservation.AmountMinor != 500 || checkReservation.PolicyVersion != 2 {
		t.Fatalf("Affiliate Support check reservation=%+v err=%v", checkReservation, err)
	}
	policy, err = affiliateAdmin.PublishSettlementPolicy(ctx, 2, 3, 500, "affiliate-operator@example.test",
		"Prove reservations retain their original settlement policy", "local")
	if err != nil || policy.Version != 3 || policy.CheckThresholdMinor != 500 {
		t.Fatalf("replacement Affiliate settlement policy=%+v err=%v", policy, err)
	}
	releasedCheck, err := affiliateAdmin.TransitionSupportCheck(ctx, checkReservation.ID, checkReservation.Version, "released",
		"affiliate-operator@example.test", "Release the canceled Support check review", "local")
	if err != nil || releasedCheck.State != "released" || releasedCheck.Version != 2 || releasedCheck.PolicyVersion != 2 {
		t.Fatalf("released Affiliate Support check=%+v err=%v", releasedCheck, err)
	}
	if err := pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&settlementNow); err != nil {
		t.Fatal(err)
	}
	settlementRepository := postgresadapter.NewAffiliateSettlementRepository(pool)
	reservations, err := settlementRepository.PrepareInvoiceCredits(ctx, "10000000-0000-4000-8000-000000000090", affiliateAccount,
		"in_affiliate_account_renewal", 5350, "USD", settlementNow)
	if err != nil || len(reservations) != 1 || reservations[0].AmountMinor != 900 || reservations[0].PolicyVersion != 3 || reservations[0].ProviderCustomerID != "cus_affiliate_settlement" {
		t.Fatalf("Affiliate credit reservations=%+v err=%v", reservations, err)
	}
	credited, err := settlementRepository.CompleteInvoiceCredit(ctx, reservations[0].ID, "cus_affiliate_settlement", "cbtxn_affiliate_credit", settlementNow)
	if err != nil || credited.State != "credited" {
		t.Fatalf("credited reservation=%+v err=%v", credited, err)
	}
	settledMinor, err := settlementRepository.SettleInvoiceCredits(ctx, "in_affiliate_account_renewal", settlementNow)
	if err != nil || settledMinor != 900 {
		t.Fatalf("settled credit=%d err=%v", settledMinor, err)
	}
	settlementSnapshot, err := affiliateRepository.SettlementSnapshot(ctx, enrollment.ID)
	if err != nil || settlementSnapshot.AvailableMinor != 0 || settlementSnapshot.ReservedMinor != 0 || settlementSnapshot.SettledMinor != 900 || settlementSnapshot.CheckThresholdMinor != 500 {
		t.Fatalf("settlement snapshot=%+v err=%v", settlementSnapshot, err)
	}
	settledAdverse := affiliates.AdverseBillingEvidence{EventID: "evt_affiliate_settled_credit_note", Kind: affiliates.AdverseCreditNote,
		ProviderObjectID: "cn_affiliate_settled", InvoiceID: "in_affiliate_third", InvoiceLineIDs: []string{"il_affiliate_third"},
		AmountMinor: 1, Currency: "USD", OccurredAt: now}
	settledReversal, reversed, err := affiliateService.RecordAdverseBilling(ctx, settledAdverse)
	if err != nil || !reversed || settledReversal.ReversesID == nil || *settledReversal.ReversesID != third.ID {
		t.Fatalf("settled Affiliate reversal=%+v reversed=%v err=%v", settledReversal, reversed, err)
	}
	adjustments, err := settlementRepository.PrepareReversalAdjustments(ctx, "10000000-0000-4000-8000-000000000091", settledAdverse.ProviderObjectID, settlementNow)
	if err != nil || len(adjustments) != 1 || adjustments[0].Kind != "customer_balance_debit" || adjustments[0].State != "pending" || adjustments[0].AmountMinor != 900 {
		t.Fatalf("Affiliate reversal adjustments=%+v err=%v", adjustments, err)
	}
	adjusted, err := settlementRepository.CompleteReversalAdjustment(ctx, adjustments[0].ID, "cus_affiliate_settlement", "cbtxn_affiliate_reversal", settlementNow)
	if err != nil || adjusted.State != "applied" {
		t.Fatalf("Affiliate reversal adjustment=%+v err=%v", adjusted, err)
	}
	commissioningCredit := affiliates.AdverseBillingEvidence{EventID: "evt_affiliate_commissioning_credit_note", Kind: affiliates.AdverseCreditNote,
		ProviderObjectID: "cn_affiliate_commissioning", InvoiceID: "in_affiliate_fourth", InvoiceLineIDs: []string{"il_commissioning_only"},
		AmountMinor: 25_000, Currency: "USD", OccurredAt: now}
	if entry, reversed, err := affiliateService.RecordAdverseBilling(ctx, commissioningCredit); err != nil || reversed || entry.ID != "" {
		t.Fatalf("commissioning-only credit note reversal=%+v reversed=%v err=%v", entry, reversed, err)
	}
	fifth, err := affiliateService.RecordPaidInvoice(ctx, affiliateprogram.PaidInvoice{SubscriptionID: "sub_affiliate_test",
		InvoiceID: "in_affiliate_fifth", PaymentIntentID: "pi_affiliate_fifth", AmountPaidMinor: 5000, Currency: "USD", Mode: "test",
		Lines: []affiliateprogram.InvoiceLine{{ID: "il_affiliate_fifth", ProviderPriceID: "price_affiliate_team", AmountMinor: 5000, Currency: "USD"}}})
	if err != nil || fifth.Cycle != 6 {
		t.Fatalf("fifth earning=%+v err=%v", fifth, err)
	}
	settledCheck, err := affiliateAdmin.ReserveSupportCheck(ctx, enrollment.ID, affiliatePasskeySession, 500,
		"affiliate-operator@example.test", "Reserve the reviewed Support check accounting amount", "local")
	if err != nil || settledCheck.State != "reserved" || settledCheck.AmountMinor != 500 || settledCheck.PolicyVersion != 3 {
		t.Fatalf("settled Support check reservation=%+v err=%v", settledCheck, err)
	}
	settledCheck, err = affiliateAdmin.TransitionSupportCheck(ctx, settledCheck.ID, settledCheck.Version, "settled",
		"affiliate-operator@example.test", "Record the externally completed Support check", "local")
	if err != nil || settledCheck.State != "settled" {
		t.Fatalf("settled Support check accounting=%+v err=%v", settledCheck, err)
	}
	checkAdverse := affiliates.AdverseBillingEvidence{EventID: "evt_affiliate_check_refund", Kind: affiliates.AdverseRefund,
		ProviderObjectID: "re_affiliate_check", PaymentIntentID: "pi_affiliate_fourth", AmountMinor: 1, Currency: "USD", OccurredAt: now}
	checkReversal, reversed, err := affiliateService.RecordAdverseBilling(ctx, checkAdverse)
	if err != nil || !reversed || checkReversal.ReversesID == nil || *checkReversal.ReversesID != fourth.ID {
		t.Fatalf("Support-check earning reversal=%+v reversed=%v err=%v", checkReversal, reversed, err)
	}
	checkAdjustments, err := settlementRepository.PrepareReversalAdjustments(ctx, "10000000-0000-4000-8000-000000000092", checkAdverse.ProviderObjectID, settlementNow)
	if err != nil || len(checkAdjustments) != 1 || checkAdjustments[0].Kind != "support_check_recovery" ||
		checkAdjustments[0].State != "applied" || checkAdjustments[0].AmountMinor != 500 {
		t.Fatalf("Support-check recovery adjustments=%+v err=%v", checkAdjustments, err)
	}
	sixth, err := affiliateService.RecordPaidInvoice(ctx, affiliateprogram.PaidInvoice{SubscriptionID: "sub_affiliate_test",
		InvoiceID: "in_affiliate_sixth", PaymentIntentID: "pi_affiliate_sixth", AmountPaidMinor: 5000, Currency: "USD", Mode: "test",
		Lines: []affiliateprogram.InvoiceLine{{ID: "il_affiliate_sixth", ProviderPriceID: "price_affiliate_team", AmountMinor: 5000, Currency: "USD"}}})
	if err != nil || sixth.Cycle != 7 {
		t.Fatalf("sixth earning=%+v err=%v", sixth, err)
	}
	recoveredSnapshot, err := affiliateRepository.SettlementSnapshot(ctx, enrollment.ID)
	if err != nil || recoveredSnapshot.AvailableMinor != 500 || recoveredSnapshot.ReservedMinor != 0 || recoveredSnapshot.CheckThresholdMinor != 500 {
		t.Fatalf("Support-check recovery snapshot=%+v err=%v", recoveredSnapshot, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE affiliate_attributions
		SET created_at=statement_timestamp()-interval '1 hour',locked_at=statement_timestamp()-interval '30 minutes'
		WHERE attribution_id=$1`, attribution.ID); err != nil {
		t.Fatal(err)
	}
	risk, err := affiliateAdmin.InspectRisk(ctx, enrollment.ID, "affiliate-operator@example.test",
		"Review aggregate referral risk evidence", "local")
	if err != nil || risk.ValidReservations != 1 || risk.DistinctReferredAccounts != 1 ||
		risk.LockedAttributions != 1 || risk.MaximumReservationsPerAccount != 1 ||
		risk.LargestAccountShareBasisPoints != 10000 || len(risk.Flags()) != 0 {
		t.Fatalf("Affiliate risk=%+v flags=%v err=%v", risk, risk.Flags(), err)
	}
	var riskInspectionEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM affiliate_enrollment_events WHERE affiliate_id=$1 AND action='risk_inspected'`, enrollment.ID).Scan(&riskInspectionEvents); err != nil || riskInspectionEvents != 1 {
		t.Fatalf("Affiliate risk inspection events=%d err=%v", riskInspectionEvents, err)
	}
	inspected, err := affiliateAdmin.Inspect(ctx, enrollment.ID, "affiliate-operator@example.test",
		"Review the enrollment before a state change", "local")
	if err != nil || inspected.Version != enrollment.Version || inspected.State != affiliates.EnrollmentActive {
		t.Fatalf("inspected enrollment=%+v err=%v", inspected, err)
	}
	suspended, err := affiliateAdmin.Transition(ctx, enrollment.ID, inspected.Version, affiliates.EnrollmentSuspended,
		"affiliate-operator@example.test", "Suspend during a documented referral review", "local")
	if err != nil || suspended.Version != 4 || suspended.State != affiliates.EnrollmentSuspended {
		t.Fatalf("suspended enrollment=%+v err=%v", suspended, err)
	}
	if _, err := affiliateRepository.EnrollmentByCode(ctx, enrollment.PublicCode); !errors.Is(err, affiliateprogram.ErrCodeUnavailable) {
		t.Fatalf("suspended public code lookup error=%v", err)
	}
	if _, err := affiliateAdmin.Transition(ctx, enrollment.ID, inspected.Version, affiliates.EnrollmentActive,
		"affiliate-operator@example.test", "Attempt a stale enrollment transition", "local"); !errors.Is(err, affiliateadmin.ErrStateConflict) {
		t.Fatalf("stale Affiliate transition error=%v", err)
	}
	activated, err := affiliateAdmin.Transition(ctx, enrollment.ID, suspended.Version, affiliates.EnrollmentActive,
		"affiliate-operator@example.test", "Reactivate after the referral review completed", "local")
	if err != nil || activated.Version != 5 || activated.State != affiliates.EnrollmentActive {
		t.Fatalf("activated enrollment=%+v err=%v", activated, err)
	}
	closed, err := affiliateAdmin.Transition(ctx, enrollment.ID, activated.Version, affiliates.EnrollmentClosed,
		"affiliate-operator@example.test", "Close the Affiliate enrollment after final review", "local")
	if err != nil || closed.Version != 6 || closed.State != affiliates.EnrollmentClosed {
		t.Fatalf("closed enrollment=%+v err=%v", closed, err)
	}
	var closureVoids int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM affiliate_commission_entries WHERE kind='void' AND source_entry_id=$1`, sixth.ID).Scan(&closureVoids); err != nil || closureVoids != 1 {
		t.Fatalf("closure voids=%d err=%v", closureVoids, err)
	}
	if _, err := affiliateAdmin.Transition(ctx, enrollment.ID, closed.Version, affiliates.EnrollmentActive,
		"affiliate-operator@example.test", "Attempt to reopen a terminal enrollment", "local"); !errors.Is(err, affiliateadmin.ErrStateConflict) {
		t.Fatalf("closed Affiliate transition error=%v", err)
	}
	var affiliateEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM affiliate_enrollment_events WHERE affiliate_id=$1`, enrollment.ID).Scan(&affiliateEvents); err != nil || affiliateEvents != 6 {
		t.Fatalf("Affiliate enrollment events=%d err=%v", affiliateEvents, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE affiliate_enrollment_events SET state='active' WHERE affiliate_id=$1 AND action='closed'`, enrollment.ID); err == nil {
		t.Fatal("Affiliate enrollment lifecycle evidence was mutable")
	}
	const affiliateOperatorRole = "spyglass_affiliate_operator_contract"
	if _, err := pool.Exec(ctx, `CREATE ROLE `+affiliateOperatorRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA public TO `+affiliateOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_affiliate_enrollment(uuid,uuid,text,text,text) TO `+affiliateOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_affiliate_risk(uuid,uuid,text,text,text) TO `+affiliateOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_transition_affiliate_enrollment(uuid,uuid,bigint,text,text,text,text) TO `+affiliateOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_publish_affiliate_settlement_policy(uuid,bigint,bigint,bigint,text,text,text) TO `+affiliateOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_reserve_affiliate_support_check(uuid,uuid,uuid,uuid,bigint,text,text,text) TO `+affiliateOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_transition_affiliate_support_check(uuid,uuid,bigint,text,text,text,text) TO `+affiliateOperatorRole); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+affiliateOperatorRole+`; DROP ROLE `+affiliateOperatorRole)
	}()
	var affiliateDirectAccess, affiliateAttributionAccess, affiliateCodeHistoryAccess, affiliateInspectAccess, affiliateRiskAccess, affiliateTransitionAccess, affiliatePolicyAccess, affiliateCheckReserveAccess, affiliateCheckTransitionAccess bool
	if err := pool.QueryRow(ctx, `SELECT
		has_table_privilege($1,'public.affiliate_enrollments','SELECT'),
		has_table_privilege($1,'public.affiliate_attributions','SELECT'),
		has_table_privilege($1,'public.affiliate_public_code_history','SELECT'),
		has_function_privilege($1,'public.spyglass_inspect_affiliate_enrollment(uuid,uuid,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_inspect_affiliate_risk(uuid,uuid,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_transition_affiliate_enrollment(uuid,uuid,bigint,text,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_publish_affiliate_settlement_policy(uuid,bigint,bigint,bigint,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_reserve_affiliate_support_check(uuid,uuid,uuid,uuid,bigint,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_transition_affiliate_support_check(uuid,uuid,bigint,text,text,text,text)','EXECUTE')`, affiliateOperatorRole).Scan(
		&affiliateDirectAccess, &affiliateAttributionAccess, &affiliateCodeHistoryAccess, &affiliateInspectAccess, &affiliateRiskAccess,
		&affiliateTransitionAccess, &affiliatePolicyAccess, &affiliateCheckReserveAccess, &affiliateCheckTransitionAccess); err != nil ||
		affiliateDirectAccess || affiliateAttributionAccess || affiliateCodeHistoryAccess || !affiliateInspectAccess || !affiliateRiskAccess ||
		!affiliateTransitionAccess || !affiliatePolicyAccess || !affiliateCheckReserveAccess || !affiliateCheckTransitionAccess {
		t.Fatalf("Affiliate operator enrollment=%v attribution=%v code_history=%v inspect=%v risk=%v transition=%v policy=%v reserve_check=%v transition_check=%v err=%v",
			affiliateDirectAccess, affiliateAttributionAccess, affiliateCodeHistoryAccess, affiliateInspectAccess, affiliateRiskAccess,
			affiliateTransitionAccess, affiliatePolicyAccess, affiliateCheckReserveAccess, affiliateCheckTransitionAccess, err)
	}
	finalCheck, err := affiliateAdmin.ReserveSupportCheck(ctx, enrollment.ID, affiliatePasskeySession, 500,
		"affiliate-operator@example.test", "Reserve the remaining closed-enrollment credit before retention", "local")
	if err != nil || finalCheck.State != "reserved" || finalCheck.AmountMinor != 500 || finalCheck.PolicyVersion != 3 {
		t.Fatalf("final Affiliate Support check reservation=%+v err=%v", finalCheck, err)
	}
	finalCheck, err = affiliateAdmin.TransitionSupportCheck(ctx, finalCheck.ID, finalCheck.Version, "settled",
		"affiliate-operator@example.test", "Record the final closed-enrollment Support check settlement", "local")
	if err != nil || finalCheck.State != "settled" {
		t.Fatalf("final Affiliate Support check settlement=%+v err=%v", finalCheck, err)
	}

	supportService, err := affiliatesupport.New(postgresadapter.NewAffiliateSupportRepository(pool), ids.RandomGenerator{}, fixedLifecycleClock{now})
	if err != nil {
		t.Fatal(err)
	}
	appeal, err := supportService.Submit(ctx, affiliatesupport.SubmitCommand{UserID: affiliateUser, Kind: affiliates.SupportEnrollmentAppeal})
	if err != nil || appeal.State != affiliates.SupportSubmitted || appeal.UserID != affiliateUser {
		t.Fatalf("Affiliate appeal=%+v err=%v", appeal, err)
	}
	if _, err := supportService.Submit(ctx, affiliatesupport.SubmitCommand{UserID: affiliateUser, Kind: affiliates.SupportEnrollmentAppeal}); !errors.Is(err, affiliatesupport.ErrAlreadyOpen) {
		t.Fatalf("duplicate Affiliate appeal error=%v", err)
	}
	commissionReview, err := supportService.Submit(ctx, affiliatesupport.SubmitCommand{UserID: affiliateUser,
		Kind: affiliates.SupportCommissionReview, CommissionEntryID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supportService.Submit(ctx, affiliatesupport.SubmitCommand{UserID: customerUser,
		Kind: affiliates.SupportCommissionReview, CommissionEntryID: first.ID}); err == nil {
		t.Fatal("another user submitted an Affiliate commission review")
	}
	listedSupport, err := supportService.List(ctx, affiliateUser)
	if err != nil || len(listedSupport) != 2 {
		t.Fatalf("listed Affiliate support=%+v err=%v", listedSupport, err)
	}
	canceledReview, err := supportService.Cancel(ctx, commissionReview.ID, affiliateUser)
	if err != nil || canceledReview.State != affiliates.SupportCanceled || canceledReview.Version != 2 {
		t.Fatalf("canceled Affiliate review=%+v err=%v", canceledReview, err)
	}
	if _, err := supportService.Cancel(ctx, commissionReview.ID, customerUser); !errors.Is(err, affiliatesupport.ErrNotFound) {
		t.Fatalf("cross-user support cancellation error=%v", err)
	}
	supportAdmin, err := affiliatesupportadmin.New(postgresadapter.NewAffiliateSupportAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	inspectedAppeal, err := supportAdmin.Inspect(ctx, appeal.ID, "affiliate-support@example.test", "Inspect the submitted enrollment appeal", "local")
	if err != nil || inspectedAppeal.Version != 1 {
		t.Fatalf("inspected Affiliate appeal=%+v err=%v", inspectedAppeal, err)
	}
	reviewedAppeal, err := supportAdmin.StartReview(ctx, appeal.ID, inspectedAppeal.Version,
		"affiliate-support@example.test", "Begin review of the enrollment appeal", "local")
	if err != nil || reviewedAppeal.State != affiliates.SupportInReview || reviewedAppeal.Version != 2 {
		t.Fatalf("reviewed Affiliate appeal=%+v err=%v", reviewedAppeal, err)
	}
	if _, err := supportAdmin.Resolve(ctx, appeal.ID, inspectedAppeal.Version, affiliates.SupportApproved,
		"affiliate-support@example.test", "Attempt a stale support decision", "local"); !errors.Is(err, affiliatesupportadmin.ErrStateConflict) {
		t.Fatalf("stale Affiliate support resolution error=%v", err)
	}
	resolvedAppeal, err := supportAdmin.Resolve(ctx, appeal.ID, reviewedAppeal.Version, affiliates.SupportApproved,
		"affiliate-support@example.test", "Approve after the documented enrollment review", "local")
	if err != nil || resolvedAppeal.State != affiliates.SupportResolved || resolvedAppeal.Outcome != affiliates.SupportApproved || resolvedAppeal.Version != 3 {
		t.Fatalf("resolved Affiliate appeal=%+v err=%v", resolvedAppeal, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE affiliate_support_request_events SET state='declined' WHERE request_id=$1 AND action='resolved'`, appeal.ID); err == nil {
		t.Fatal("Affiliate support decision evidence was mutable")
	}
	exported, err := affiliateService.Export(ctx, affiliateprogram.ExportCommand{UserID: affiliateUser, Session: affiliateSession})
	if err != nil || exported.SchemaVersion != affiliateprogram.AffiliateDataExportSchemaVersion || exported.Enrollment == nil ||
		exported.Enrollment.UserID != affiliateUser || exported.Enrollment.PublicCode != "IO-PARTNER3" ||
		len(exported.PublicCodes) != 3 || len(exported.EnrollmentEvents) != 6 ||
		exported.AttributionSummary.Total != 1 || exported.AttributionSummary.Locked != 1 ||
		len(exported.CommissionEntries) != 14 || len(exported.CreditSettlements) != 4 || len(exported.CreditReversals) != 2 || len(exported.SupportRequests) != 2 || len(exported.SupportEvents) != 6 {
		t.Fatalf("Affiliate data export=%+v err=%v", exported, err)
	}
	rawExport, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{customerAccount, "sub_affiliate_test", "in_affiliate_renewal", "pi_affiliate_renewal",
		"cus_affiliate_settlement", "cbtxn_affiliate_credit", "cbtxn_affiliate_reversal", "affiliate-operator@example.test", "affiliate-support@example.test", "documented enrollment review"} {
		if strings.Contains(string(rawExport), forbidden) {
			t.Fatalf("Affiliate data export leaked prohibited value %q: %s", forbidden, rawExport)
		}
	}
	emptyExport, err := affiliateService.Export(ctx, affiliateprogram.ExportCommand{UserID: customerUser, Session: sessions.Session{
		UserID: customerUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey,
	}})
	if err != nil || emptyExport.Enrollment != nil || len(emptyExport.PublicCodes) != 0 || emptyExport.AttributionSummary.Total != 0 {
		t.Fatalf("empty Affiliate data export=%+v err=%v", emptyExport, err)
	}
	const affiliateExportRole = "spyglass_affiliate_export_contract"
	if _, err := pool.Exec(ctx, `CREATE ROLE `+affiliateExportRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA public TO `+affiliateExportRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_export_affiliate_data(uuid) TO `+affiliateExportRole); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+affiliateExportRole+`; DROP ROLE `+affiliateExportRole)
	}()
	var exportDirectAccess, exportFunctionAccess bool
	if err := pool.QueryRow(ctx, `SELECT
		has_table_privilege($1,'public.affiliate_enrollments','SELECT'),
		has_function_privilege($1,'public.spyglass_export_affiliate_data(uuid)','EXECUTE')`, affiliateExportRole).Scan(
		&exportDirectAccess, &exportFunctionAccess); err != nil || exportDirectAccess || !exportFunctionAccess {
		t.Fatalf("Affiliate export role table=%v function=%v err=%v", exportDirectAccess, exportFunctionAccess, err)
	}
	const affiliateSupportOperatorRole = "spyglass_affiliate_support_operator_contract"
	if _, err := pool.Exec(ctx, `CREATE ROLE `+affiliateSupportOperatorRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA public TO `+affiliateSupportOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_affiliate_support_request(uuid,uuid,text,text,text) TO `+affiliateSupportOperatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_transition_affiliate_support_request(uuid,uuid,bigint,text,text,text,text,text,text) TO `+affiliateSupportOperatorRole); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+affiliateSupportOperatorRole+`; DROP ROLE `+affiliateSupportOperatorRole)
	}()
	var supportDirectAccess, supportInspectAccess, supportTransitionAccess bool
	if err := pool.QueryRow(ctx, `SELECT
		has_table_privilege($1,'public.affiliate_support_requests','SELECT'),
		has_function_privilege($1,'public.spyglass_inspect_affiliate_support_request(uuid,uuid,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_transition_affiliate_support_request(uuid,uuid,bigint,text,text,text,text,text,text)','EXECUTE')`, affiliateSupportOperatorRole).Scan(
		&supportDirectAccess, &supportInspectAccess, &supportTransitionAccess); err != nil || supportDirectAccess || !supportInspectAccess || !supportTransitionAccess {
		t.Fatalf("Affiliate support operator table=%v inspect=%v transition=%v err=%v", supportDirectAccess, supportInspectAccess, supportTransitionAccess, err)
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
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM affiliate_commission_entries WHERE attribution_id=$1`, attribution.ID).Scan(&commissions); err != nil || detachedAccount != nil || detachedCheckout != nil || commissions != 14 {
		t.Fatalf("account=%v checkout=%v commissions=%d err=%v", detachedAccount, detachedCheckout, commissions, err)
	}

	retentionNow := now.AddDate(8, 0, 0)
	var retainedAffiliates, eligibleAffiliates int64
	if err := pool.QueryRow(ctx, `SELECT total,eligible FROM spyglass_affiliate_minimization_stats($1)`, retentionNow).Scan(
		&retainedAffiliates, &eligibleAffiliates); err != nil || retainedAffiliates != 1 || eligibleAffiliates != 1 {
		var ledgerState []byte
		_ = pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'cycle',earned.cycle,'amount_minor',earned.amount_minor,
			'allocated_minor',COALESCE((SELECT sum(allocation.amount_minor) FROM affiliate_credit_allocations allocation
				JOIN affiliate_credit_reservations reservation ON reservation.reservation_id=allocation.reservation_id
				WHERE allocation.earning_entry_id=earned.entry_id AND reservation.state='settled'),0),
			'reversed',EXISTS(SELECT 1 FROM affiliate_commission_entries terminal WHERE terminal.reverses_entry_id=earned.entry_id),
			'voided',EXISTS(SELECT 1 FROM affiliate_commission_entries terminal WHERE terminal.source_entry_id=earned.entry_id AND terminal.kind='void'))
			ORDER BY earned.cycle),'[]'::jsonb)
			FROM affiliate_commission_entries earned WHERE earned.affiliate_id=$1 AND earned.kind='earned'`, enrollment.ID).Scan(&ledgerState)
		t.Fatalf("populated Affiliate retention stats total=%d eligible=%d ledger=%s err=%v", retainedAffiliates, eligibleAffiliates, ledgerState, err)
	}
	var minimizedAffiliates int64
	if err := pool.QueryRow(ctx, `SELECT spyglass_minimize_due_affiliates($1,100)`, retentionNow).Scan(&minimizedAffiliates); err != nil || minimizedAffiliates != 1 {
		t.Fatalf("populated Affiliate minimization=%d err=%v", minimizedAffiliates, err)
	}
	var remainingAffiliateRows, tombstoneAttributions, tombstoneCommissions, tombstoneReservations, tombstoneSupportRequests, tombstoneCodes int64
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM affiliate_enrollments),
		attribution_count,commission_entry_count,credit_reservation_count,support_request_count,code_count
		FROM affiliate_minimization_tombstones`).Scan(
		&remainingAffiliateRows, &tombstoneAttributions, &tombstoneCommissions, &tombstoneReservations,
		&tombstoneSupportRequests, &tombstoneCodes); err != nil || remainingAffiliateRows != 0 || tombstoneAttributions != 1 ||
		tombstoneCommissions != 14 || tombstoneReservations != 4 || tombstoneSupportRequests != 2 || tombstoneCodes != 3 {
		t.Fatalf("populated Affiliate tombstone remaining=%d attributions=%d commissions=%d reservations=%d support=%d codes=%d err=%v",
			remainingAffiliateRows, tombstoneAttributions, tombstoneCommissions, tombstoneReservations,
			tombstoneSupportRequests, tombstoneCodes, err)
	}
	var affiliateGraphRows int64
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM affiliate_attributions)+
		(SELECT count(*) FROM affiliate_commission_entries)+
		(SELECT count(*) FROM affiliate_credit_reservations)+
		(SELECT count(*) FROM affiliate_support_requests)+
		(SELECT count(*) FROM affiliate_public_code_history)+
		(SELECT count(*) FROM affiliate_retention_controls)`).Scan(&affiliateGraphRows); err != nil || affiliateGraphRows != 0 {
		t.Fatalf("populated Affiliate graph rows=%d err=%v", affiliateGraphRows, err)
	}
}

type fixedAffiliateCode struct{ value string }

func (g fixedAffiliateCode) NewCode() (string, error) { return g.value, nil }

type fixedLifecycleClock struct{ now time.Time }

func (c fixedLifecycleClock) Now() time.Time { return c.now }
