package migrations_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	integrationsapp "github.com/tinfoyle/spyglass-engine/internal/application/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
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
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewIntegrationsRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := repository.GetExecution(ctx, ids.AccountID(fixture.accountID), ids.IntegrationExecutionID(fixture.executionID))
	if err != nil || detail.Execution.State != integrationsdomain.ExecutionManualResolution || len(detail.Attempts) != 3 || detail.Attempts[2].Outcome != integrationsdomain.AttemptUnknown {
		t.Fatalf("execution detail=%+v err=%v", detail, err)
	}
	page, err := repository.ListExecutions(ctx, ids.AccountID(fixture.accountID), integrationsapp.ExecutionListQuery{
		States: []integrationsdomain.ExecutionState{integrationsdomain.ExecutionManualResolution}, Capabilities: []integrationsdomain.Capability{integrationsdomain.CapabilityEmailSend}, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != ids.IntegrationExecutionID(fixture.executionID) {
		t.Fatalf("execution page=%+v err=%v", page, err)
	}
	if _, err := repository.GetExecution(ctx, ids.AccountID(fixture.otherAccountID), ids.IntegrationExecutionID(fixture.executionID)); !errors.Is(err, integrationsapp.ErrNotFound) {
		t.Fatalf("cross-Account execution=%v", err)
	}

	assertIntegrationRLS(t, ctx, owner, databaseURL, fixture)
	assertCredentialCannotEndWhileBound(t, ctx, owner, fixture)
}

func TestIntegrationsRepositoryReplaysRestoresAndIsolatesConnectionLifecycle(t *testing.T) {
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

	now := time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("96100000-0000-4000-8000-000000000001")
	otherAccountID := ids.AccountID("96200000-0000-4000-8000-000000000002")
	userID := ids.UserID("96300000-0000-4000-8000-000000000003")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',$3),($2,1,'active',$3)`, accountID, otherAccountID, now); err != nil {
		t.Fatal(err)
	}
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewIntegrationsRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	actor := integrationsdomain.Actor{UserID: userID}
	mutation := func(id, kind string, at time.Time) integrationsapp.Mutation {
		return integrationsapp.Mutation{EventID: id, Kind: kind, Actor: actor, CorrelationID: id, At: at}
	}

	connectionID := ids.IntegrationConnectionID("96400000-0000-4000-8000-000000000004")
	revisionID := ids.IntegrationConnectionRevisionID("96500000-0000-4000-8000-000000000005")
	input := integrationsdomain.ConnectionInput{ID: connectionID, RevisionID: revisionID, AccountID: accountID, Name: "Campaign email",
		Kind: integrationsdomain.ConnectorEmail, Capabilities: []integrationsdomain.Capability{integrationsdomain.CapabilityEmailSend},
		Scope: integrationsdomain.ConnectionScope{EmailAddress: "launch@Example.com", AudienceReference: "audience:customers-v1"}, CreatedBy: actor, CreatedAt: now}
	connection, created, err := repository.CreateConnection(ctx, input, accounts.RoleOwner, mutation(string(connectionID), "connection_created", now))
	if err != nil || !created || connection.State != integrationsdomain.ConnectionPending || connection.CurrentRevisionID != revisionID {
		t.Fatalf("connection=%+v created=%t err=%v", connection, created, err)
	}
	replayInput := input
	replayInput.CreatedAt = now.Add(time.Minute)
	replayed, created, err := repository.CreateConnection(ctx, replayInput, accounts.RoleOwner, mutation(string(connectionID), "connection_created", replayInput.CreatedAt))
	if err != nil || created || !reflect.DeepEqual(replayed, connection) {
		t.Fatalf("connection replay=%+v created=%t err=%v", replayed, created, err)
	}
	altered := replayInput
	altered.Scope.AudienceReference = "audience:changed"
	if _, _, err := repository.CreateConnection(ctx, altered, accounts.RoleOwner, mutation(string(connectionID), "connection_created", altered.CreatedAt)); !errors.Is(err, integrationsapp.ErrConflict) {
		t.Fatalf("altered replay=%v", err)
	}
	if _, err := repository.GetConnection(ctx, otherAccountID, connectionID); !errors.Is(err, integrationsapp.ErrNotFound) {
		t.Fatalf("cross-Account connection=%v", err)
	}

	digest1 := sha256.Sum256([]byte("broker-reference-one"))
	credentialID1 := ids.IntegrationCredentialID("96600000-0000-4000-8000-000000000006")
	activateAt := now.Add(2 * time.Minute)
	credential1 := integrationsdomain.CredentialInput{ID: credentialID1, AccountID: accountID, ConnectionID: connectionID, Generation: 1,
		Provider: "mock_smtp", ReferenceSHA256: digest1, CreatedBy: actor, CreatedAt: activateAt}
	activateEvent := "96700000-0000-4000-8000-000000000007"
	connection, err = repository.ActivateConnection(ctx, accountID, connectionID, 1, credential1, actor, accounts.RoleOwner,
		mutation(activateEvent, "connection_activated", activateAt))
	if err != nil || connection.State != integrationsdomain.ConnectionActive || connection.CredentialID != credentialID1 || connection.Version != 2 {
		t.Fatalf("activated=%+v err=%v", connection, err)
	}
	replayCredential := credential1
	replayCredential.CreatedAt = activateAt.Add(time.Minute)
	replayed, err = repository.ActivateConnection(ctx, accountID, connectionID, 1, replayCredential, actor, accounts.RoleOwner,
		mutation(activateEvent, "connection_activated", replayCredential.CreatedAt))
	if err != nil || !reflect.DeepEqual(replayed, connection) {
		t.Fatalf("activation replay=%+v err=%v", replayed, err)
	}

	reviseAt := now.Add(4 * time.Minute)
	revisionID2 := ids.IntegrationConnectionRevisionID("96800000-0000-4000-8000-000000000008")
	connection, err = repository.ReviseConnection(ctx, accountID, connectionID, 2, integrationsdomain.ConnectionRevisionInput{ID: revisionID2, Name: "Campaign email v2",
		Capabilities: []integrationsdomain.Capability{integrationsdomain.CapabilityEmailRead, integrationsdomain.CapabilityEmailSend},
		Scope:        integrationsdomain.ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:customers-v2"}}, actor, accounts.RoleOwner,
		mutation(string(revisionID2), "connection_revised", reviseAt))
	if err != nil || connection.Version != 3 || connection.CurrentRevision != 2 || connection.CurrentRevisionID != revisionID2 {
		t.Fatalf("revised=%+v err=%v", connection, err)
	}

	rotateAt := now.Add(5 * time.Minute)
	credentialID2 := ids.IntegrationCredentialID("96900000-0000-4000-8000-000000000009")
	credential2 := integrationsdomain.CredentialInput{ID: credentialID2, AccountID: accountID, ConnectionID: connectionID, Generation: 2,
		Provider: "mock_smtp", ReferenceSHA256: sha256.Sum256([]byte("broker-reference-two")), CreatedBy: actor, CreatedAt: rotateAt}
	rotateEvent := "96a00000-0000-4000-8000-00000000000a"
	connection, err = repository.RotateCredential(ctx, accountID, connectionID, 3, 1, credential2, actor, accounts.RoleOwner,
		mutation(rotateEvent, "credential_rotated", rotateAt))
	if err != nil || connection.Version != 4 || connection.CredentialID != credentialID2 || connection.CredentialGeneration != 2 {
		t.Fatalf("rotated=%+v err=%v", connection, err)
	}
	healthID1 := ids.IntegrationHealthObservationID("97000000-0000-4000-8000-000000000010")
	healthID2 := ids.IntegrationHealthObservationID("97100000-0000-4000-8000-000000000011")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.integration_health_observations
		(account_id,id,connection_id,connection_revision,credential_id,credential_generation,state,error_code,latency_milliseconds,checked_at)
		VALUES ($1,$2,$4,2,$5,2,'healthy',NULL,8,$6),($1,$3,$4,2,$5,2,'degraded','provider_slow',240,$7)`,
		accountID, healthID1, healthID2, connectionID, credentialID2, rotateAt.Add(time.Second), rotateAt.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	detail, err := repository.GetConnectionDetail(ctx, accountID, connectionID)
	if err != nil || detail.Connection.ID != connectionID || detail.Revision.ID != revisionID2 || detail.LatestHealth == nil || detail.LatestHealth.ID != healthID2 {
		t.Fatalf("connection detail=%+v err=%v", detail, err)
	}
	healthPage, err := repository.ListHealth(ctx, accountID, integrationsapp.HealthListQuery{ConnectionID: connectionID, Limit: 1})
	if err != nil || len(healthPage.Items) != 1 || healthPage.Items[0].ID != healthID2 || healthPage.NextCursor == nil {
		t.Fatalf("health page=%+v err=%v", healthPage, err)
	}
	healthRemainder, err := repository.ListHealth(ctx, accountID, integrationsapp.HealthListQuery{ConnectionID: connectionID, After: healthPage.NextCursor, Limit: 1})
	if err != nil || len(healthRemainder.Items) != 1 || healthRemainder.Items[0].ID != healthID1 || healthRemainder.NextCursor != nil {
		t.Fatalf("health remainder=%+v err=%v", healthRemainder, err)
	}

	for _, transition := range []struct {
		id   string
		kind string
		want integrationsdomain.ConnectionState
		call func(context.Context, ids.AccountID, ids.IntegrationConnectionID, uint64, integrationsdomain.Actor, accounts.MembershipRole, integrationsapp.Mutation) (integrationsdomain.Connection, error)
	}{{"96b00000-0000-4000-8000-00000000000b", "connection_disabled", integrationsdomain.ConnectionDisabled, repository.DisableConnection},
		{"96c00000-0000-4000-8000-00000000000c", "connection_activated", integrationsdomain.ConnectionActive, repository.EnableConnection},
		{"96d00000-0000-4000-8000-00000000000d", "connection_revoked", integrationsdomain.ConnectionRevoked, repository.RevokeConnection}} {
		now = now.Add(time.Minute)
		connection, err = transition.call(ctx, accountID, connectionID, connection.Version, actor, accounts.RoleOwner, mutation(transition.id, transition.kind, now.Add(5*time.Minute)))
		if err != nil || connection.State != transition.want {
			t.Fatalf("transition=%s value=%+v err=%v", transition.kind, connection, err)
		}
	}
	var credentialState string
	if err := owner.QueryRow(ctx, `SELECT state FROM spyglass.integration_credentials WHERE account_id=$1 AND id=$2`, accountID, credentialID2).Scan(&credentialState); err != nil || credentialState != "revoked" {
		t.Fatalf("credential state=%s err=%v", credentialState, err)
	}

	secondaryID := ids.IntegrationConnectionID("96e00000-0000-4000-8000-00000000000e")
	secondaryInput := integrationsdomain.ConnectionInput{ID: secondaryID, RevisionID: "96f00000-0000-4000-8000-00000000000f", AccountID: accountID,
		Name: "Website publisher", Kind: integrationsdomain.ConnectorWebPublish, Capabilities: []integrationsdomain.Capability{integrationsdomain.CapabilityWebPublish},
		Scope: integrationsdomain.ConnectionScope{HTTPSOrigin: "https://www.example.com", PathPrefix: "/news"}, CreatedBy: actor, CreatedAt: now.Add(20 * time.Minute)}
	if _, created, err := repository.CreateConnection(ctx, secondaryInput, accounts.RoleOwner, mutation(string(secondaryID), "connection_created", secondaryInput.CreatedAt)); err != nil || !created {
		t.Fatalf("secondary created=%t err=%v", created, err)
	}
	page, err := repository.ListConnections(ctx, accountID, integrationsapp.ConnectionListQuery{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != secondaryID || page.NextCursor == nil {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	remainder, err := repository.ListConnections(ctx, accountID, integrationsapp.ConnectionListQuery{After: page.NextCursor, Limit: 1})
	if err != nil || len(remainder.Items) != 1 || remainder.Items[0].ID != connectionID || remainder.NextCursor != nil {
		t.Fatalf("remainder=%+v err=%v", remainder, err)
	}
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
