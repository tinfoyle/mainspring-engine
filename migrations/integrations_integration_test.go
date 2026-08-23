package migrations_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestIntegrationsSchemaBindsAuthorityAndReconcilesUnknownDelivery(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatal(err)
	}

	fixture := seedIntegrationAuthority(t, ctx, owner)
	seedIntegrationConnection(t, ctx, owner, fixture)

	if _, err := owner.Exec(ctx, `UPDATE spyglass.integration_connection_revisions SET audience_reference='audience:changed'
		WHERE account_id=$1 AND id=$2`, fixture.accountID, fixture.revisionID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("revision mutation=%v", err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.integration_executions
		(account_id,id,release_id,release_version,approval_id,capability,connection_id,connection_revision_id,connection_revision,
		 credential_id,credential_generation,payload_sha256,state,created_at,updated_at)
		VALUES ($1,$2,$3,3,$4,'email.send',$5,$6,1,$7,1,decode(repeat('81',32),'hex'),'prepared',$8,$8)`,
		fixture.accountID, fixture.executionID, fixture.releaseID, fixture.approvalID, fixture.connectionID, fixture.revisionID, fixture.credentialID, fixture.executionAt); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.integration_execution_queue WHERE account_id=$1 AND execution_id=$2`, fixture.accountID, fixture.executionID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued=%d err=%v", queued, err)
	}

	first := claimIntegrationExecution(t, ctx, owner, fixture.attempt1, fixture.executionAt, fixture.executionAt.Add(time.Minute))
	if first.mode != "execute" || first.executionID != fixture.executionID || first.credentialID != fixture.credentialID {
		t.Fatalf("first claim=%+v", first)
	}
	if _, err := owner.Exec(ctx, `SELECT public.spyglass_complete_integration_execution($1,$2,$3,'unknown','provider_timeout',NULL,$4)`,
		fixture.accountID, fixture.executionID, fixture.attempt1, fixture.executionAt.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := tryClaimIntegrationExecution(ctx, owner, fixture.attempt2, fixture.executionAt.Add(20*time.Second), fixture.executionAt.Add(80*time.Second)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("early reconciliation error=%v", err)
	}
	second := claimIntegrationExecution(t, ctx, owner, fixture.attempt2, fixture.executionAt.Add(40*time.Second), fixture.executionAt.Add(100*time.Second))
	if second.mode != "reconcile" {
		t.Fatalf("second claim=%+v", second)
	}
	retryAt := fixture.executionAt.Add(2 * time.Minute)
	if _, err := owner.Exec(ctx, `SELECT public.spyglass_complete_integration_execution($1,$2,$3,'not_applied','provider_confirmed_absent',$4,$5)`,
		fixture.accountID, fixture.executionID, fixture.attempt2, retryAt, fixture.executionAt.Add(50*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := tryClaimIntegrationExecution(ctx, owner, fixture.attempt3, fixture.executionAt.Add(time.Minute), fixture.executionAt.Add(3*time.Minute)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("early execute retry error=%v", err)
	}
	third := claimIntegrationExecution(t, ctx, owner, fixture.attempt3, retryAt, retryAt.Add(time.Minute))
	if third.mode != "execute" {
		t.Fatalf("third claim=%+v", third)
	}
	if _, err := tryClaimIntegrationExecution(ctx, owner, fixture.attempt4, retryAt.Add(time.Minute), retryAt.Add(2*time.Minute)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired final lease claim error=%v", err)
	}
	var state string
	var attemptCount int
	var lastError string
	if err := owner.QueryRow(ctx, `SELECT state,attempt_count,last_error_code FROM spyglass.integration_executions WHERE account_id=$1 AND id=$2`, fixture.accountID, fixture.executionID).Scan(&state, &attemptCount, &lastError); err != nil || state != "manual_resolution" || attemptCount != 3 || lastError != "lease_expired" {
		t.Fatalf("execution state=%s attempts=%d err=%v", state, attemptCount, err)
	}
	var modes, outcomes []string
	if err := owner.QueryRow(ctx, `SELECT array_agg(mode ORDER BY attempt_number),array_agg(outcome ORDER BY attempt_number)
		FROM spyglass.integration_execution_attempts WHERE account_id=$1 AND execution_id=$2`, fixture.accountID, fixture.executionID).Scan(&modes, &outcomes); err != nil ||
		strings.Join(modes, ",") != "execute,reconcile,execute" || strings.Join(outcomes, ",") != "unknown,not_applied,unknown" {
		t.Fatalf("modes=%v outcomes=%v err=%v", modes, outcomes, err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.integration_execution_queue WHERE account_id=$1 AND execution_id=$2`, fixture.accountID, fixture.executionID).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("terminal queue=%d err=%v", queued, err)
	}

	assertIntegrationRLS(t, ctx, owner, databaseURL, fixture)
	assertCredentialCannotEndWhileBound(t, ctx, owner, fixture)
}

type integrationFixture struct {
	accountID, otherAccountID, userID, approvalID, campaignID, releaseID string
	connectionID, revisionID, credentialID, executionID                  string
	attempt1, attempt2, attempt3, attempt4                               string
	now, executionAt                                                     time.Time
}

func seedIntegrationAuthority(t *testing.T, ctx context.Context, owner *pgxpool.Pool) integrationFixture {
	t.Helper()
	value := integrationFixture{
		accountID: "82100000-0000-4000-8000-000000000001", otherAccountID: "82200000-0000-4000-8000-000000000002", userID: "82300000-0000-4000-8000-000000000003",
		approvalID: "82400000-0000-4000-8000-000000000004", campaignID: "82500000-0000-4000-8000-000000000005", releaseID: "82600000-0000-4000-8000-000000000006",
		connectionID: "82700000-0000-4000-8000-000000000007", revisionID: "82800000-0000-4000-8000-000000000008", credentialID: "82900000-0000-4000-8000-000000000009",
		executionID: "82a00000-0000-4000-8000-00000000000a", attempt1: "82b00000-0000-4000-8000-00000000000b", attempt2: "82c00000-0000-4000-8000-00000000000c", attempt3: "82d00000-0000-4000-8000-00000000000d", attempt4: "82f00000-0000-4000-8000-00000000000f",
		now: time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC),
	}
	value.executionAt = value.now.Add(10 * time.Minute)
	boardroomID, personaID := "83100000-0000-4000-8000-000000000001", "83200000-0000-4000-8000-000000000002"
	personaVersionID, conversationID := "83300000-0000-4000-8000-000000000003", "83400000-0000-4000-8000-000000000004"
	runID, invocationID, operationID := "83500000-0000-4000-8000-000000000005", "83600000-0000-4000-8000-000000000006", "83700000-0000-4000-8000-000000000007"
	actionPayload := []byte(`{"campaign_id":"82500000-0000-4000-8000-000000000005","campaign_version":2,"release_id":"82600000-0000-4000-8000-000000000006","release_version":2}`)
	actionDigest := sha256.Sum256(actionPayload)
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$4),($2,1,'active',$4);
		INSERT INTO spyglass.agent_boardrooms(account_id,id,name,purpose,state,version,created_at,updated_at) VALUES ($1,$5,'Integration room','Verify delivery authority','active',1,$4,$4);
		INSERT INTO spyglass.agent_personas(account_id,id,boardroom_id,state,latest_version,created_at,updated_at) VALUES ($1,$6,$5,'active',1,$4,$4);
		INSERT INTO spyglass.agent_persona_versions(account_id,id,persona_id,version,name,role,description,system_instructions,policy,content_digest,created_by,created_at)
		VALUES ($1,$7,$6,1,'Marketing Agent','Marketing','Integration fixture','Propose an exact Marketing release.','{}',decode(repeat('61',32),'hex'),$3,$4);
		INSERT INTO spyglass.agent_conversations(account_id,id,boardroom_id,subject,state,next_message_sequence,created_by,created_at,updated_at)
		VALUES ($1,$8,$5,'Integration delivery','open',1,$3,$4,$4);
		INSERT INTO spyglass.agent_runs(account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at,started_at,completed_at)
		VALUES ($1,$9,$5,$8,'succeeded',1,1,decode(repeat('62',32),'hex'),1,$3,$4,$4,$4);
		INSERT INTO spyglass.agent_run_plan_turns(account_id,run_id,turn,persona_id,persona_version_id,persona_digest) VALUES ($1,$9,1,$6,$7,decode(repeat('61',32),'hex'));
		INSERT INTO spyglass.agent_invocations(account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,response_model,provider_response_id,runner_result_digest,result_digest,result_payload,input_tokens,output_tokens,total_tokens,queued_at,started_at,completed_at)
		VALUES ($1,$10,$9,1,$7,'succeeded','openai','gpt-test','gpt-test','resp-integration',decode(repeat('63',32),'hex'),decode(repeat('64',32),'hex'),'{}',1,1,2,$4,$4,$4);
		INSERT INTO spyglass.attention_consequential_approvals
		(account_id,id,operation_id,invocation_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,proposer_kind,proposer_id,policy_version,require_independent_review,expires_at,state,decision,decision_reason,decided_by_user_id,decided_at,version,created_at,updated_at)
		VALUES ($1,$11,$12,$10,'marketing.release.activate',$13,$14,1,decode(repeat('65',32),'hex'),'workload','runner-invocation:'||$10::text,1,true,$4::timestamptz+interval '2 hours','approved','approve','approved exact release',$3,$4::timestamptz+interval '7 minutes',2,$4,$4::timestamptz+interval '7 minutes');
		INSERT INTO spyglass.marketing_campaigns(account_id,id,name,objective,audience,state,active_release_id,version,created_by_kind,created_by_id,origin,created_at,updated_at)
		VALUES ($1,$15,'Launch','Announce the release','Current customers','draft',NULL,1,'user',$3,'human',$4,$4);
		INSERT INTO spyglass.marketing_campaign_channels(account_id,campaign_id,channel) VALUES ($1,$15,'email');
		INSERT INTO spyglass.marketing_release_plans(account_id,id,campaign_id,campaign_version,name,state,approval_id,version,created_by_kind,created_by_id,origin,submitted_by_user_id,approved_by_user_id,created_at,updated_at)
		VALUES ($1,$16,$15,1,'Initial release','approved',$11,3,'user',$3,'human',$3,$3,$4,$4::timestamptz+interval '7 minutes');
		INSERT INTO spyglass.marketing_release_channels(account_id,campaign_id,release_id,channel) VALUES ($1,$15,$16,'email');
		UPDATE spyglass.marketing_campaigns SET state='active',active_release_id=$16,version=2,updated_at=$4::timestamptz+interval '8 minutes'
		WHERE account_id=$1 AND id=$15`,
		pgx.QueryExecModeSimpleProtocol, value.accountID, value.otherAccountID, value.userID, value.now, boardroomID, personaID, personaVersionID, conversationID,
		runID, invocationID, value.approvalID, operationID, actionPayload, actionDigest[:], value.campaignID, value.releaseID); err != nil {
		t.Fatal(err)
	}
	return value
}

func seedIntegrationConnection(t *testing.T, ctx context.Context, owner *pgxpool.Pool, fixture integrationFixture) {
	t.Helper()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `INSERT INTO spyglass.integration_connections
		(account_id,id,name,connector_kind,state,current_revision,credential_generation,version,created_by_user_id,created_at,updated_at)
		VALUES ($1,$2,'Campaign email','email','pending',1,0,1,$3,$4,$4)`, fixture.accountID, fixture.connectionID, fixture.userID, fixture.now.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO spyglass.integration_connection_revisions
		(account_id,id,connection_id,revision,capabilities,email_address,audience_reference,created_by_user_id,created_at)
		VALUES ($1,$2,$3,1,ARRAY['email.send'],'launch@example.com','audience:customers-v1',$4,$5)`, fixture.accountID, fixture.revisionID, fixture.connectionID, fixture.userID, fixture.now.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO spyglass.integration_credentials
		(account_id,id,connection_id,generation,provider,reference_sha256,state,created_by_user_id,created_at,updated_at)
		VALUES ($1,$2,$3,1,'mock_smtp',decode(repeat('71',32),'hex'),'active',$4,$5,$5)`, fixture.accountID, fixture.credentialID, fixture.connectionID, fixture.userID, fixture.now.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE spyglass.integration_connections SET state='active',credential_id=$2,credential_generation=1,version=2,updated_at=$4
		WHERE account_id=$1 AND id=$3`, fixture.accountID, fixture.credentialID, fixture.connectionID, fixture.now.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO spyglass.integration_health_observations
		(account_id,id,connection_id,connection_revision,credential_id,credential_generation,state,latency_milliseconds,checked_at)
		VALUES ($1,'82e00000-0000-4000-8000-00000000000e',$2,1,$3,1,'healthy',5,$4)`, fixture.accountID, fixture.connectionID, fixture.credentialID, fixture.now.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

type integrationClaim struct {
	accountID, executionID, attemptID, mode, capability, releaseID, approvalID, connectionID, revisionID, credentialID string
	releaseVersion, revision, credentialGeneration                                                                     int64
	payload                                                                                                            []byte
	idempotencyKey                                                                                                     string
	lease                                                                                                              time.Time
}

func claimIntegrationExecution(t *testing.T, ctx context.Context, owner *pgxpool.Pool, attemptID string, at, lease time.Time) integrationClaim {
	t.Helper()
	result, err := tryClaimIntegrationExecution(ctx, owner, attemptID, at, lease)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func tryClaimIntegrationExecution(ctx context.Context, owner *pgxpool.Pool, attemptID string, at, lease time.Time) (integrationClaim, error) {
	var value integrationClaim
	err := owner.QueryRow(ctx, `SELECT * FROM public.spyglass_claim_integration_execution($1,$2,$3)`, attemptID, at, lease).Scan(
		&value.accountID, &value.executionID, &value.attemptID, &value.mode, &value.capability, &value.releaseID, &value.releaseVersion, &value.approvalID,
		&value.connectionID, &value.revisionID, &value.revision, &value.credentialID, &value.credentialGeneration, &value.payload, &value.idempotencyKey, &value.lease)
	return value, err
}

func assertIntegrationRLS(t *testing.T, ctx context.Context, owner *pgxpool.Pool, databaseURL string, fixture integrationFixture) {
	t.Helper()
	role := "spyglass_integrations_reader_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA spyglass TO `+role+`;
		GRANT SELECT,INSERT ON spyglass.integration_connections TO `+role); err != nil {
		t.Fatal(err)
	}
	reader := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+role)
		return err
	})
	defer func() {
		reader.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE IF EXISTS `+role)
	}()
	if _, err := reader.Exec(ctx, `SELECT set_config('app.account_id',$1::text,false)`, fixture.otherAccountID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := reader.QueryRow(ctx, `SELECT count(*) FROM spyglass.integration_connections WHERE account_id=$1`, fixture.accountID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-Account count=%d err=%v", count, err)
	}
	if _, err := reader.Exec(ctx, `INSERT INTO spyglass.integration_connections
		(account_id,id,name,connector_kind,state,current_revision,credential_generation,version,created_by_user_id,created_at,updated_at)
		VALUES ($1,'82f00000-0000-4000-8000-00000000000f','Cross Account','email','pending',1,0,1,$2,$3,$3)`, fixture.accountID, fixture.userID, fixture.now); err == nil {
		t.Fatal("cross-Account Integration insert bypassed RLS")
	}
}

func assertCredentialCannotEndWhileBound(t *testing.T, ctx context.Context, owner *pgxpool.Pool, fixture integrationFixture) {
	t.Helper()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE spyglass.integration_credentials SET state='revoked',ended_by_user_id=$3,ended_at=$4,updated_at=$4
		WHERE account_id=$1 AND id=$2`, fixture.accountID, fixture.credentialID, fixture.userID, fixture.executionAt.Add(3*time.Minute)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "remains bound") {
		t.Fatalf("bound credential revocation=%v", err)
	}
}
