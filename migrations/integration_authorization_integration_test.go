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

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationauthorization"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestIntegrationAuthorizationSessionsAreOneUseRLSAndCredentialBound(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 23, 16, 0, 0, 0, time.UTC)
	accountID := "72100000-0000-4000-8000-000000000001"
	otherAccountID := "72200000-0000-4000-8000-000000000002"
	userID := "72300000-0000-4000-8000-000000000003"
	connectionID := "72400000-0000-4000-8000-000000000004"
	revisionID := "72500000-0000-4000-8000-000000000005"
	sessionID := "72600000-0000-4000-8000-000000000006"
	stateDigest := sha256.Sum256([]byte("one-use-state"))
	codeDigest := sha256.Sum256([]byte("one-use-code"))
	scopeDigest := sha256.Sum256([]byte("drive-scope-revision"))
	pkceDigest := sha256.Sum256([]byte("pkce-verifier"))
	if _, err := owner.Exec(ctx, `
		INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',$3),($2,1,'active',$3);
		INSERT INTO spyglass.integration_connections(account_id,id,name,connector_kind,state,current_revision,credential_generation,version,created_by_user_id,created_at,updated_at)
		VALUES ($1,$4,'Authorized Drive','google_drive','pending',1,0,1,$5,$3,$3);
		INSERT INTO spyglass.integration_connection_revisions(account_id,id,connection_id,revision,capabilities,drive_folder_ids,created_by_user_id,created_at)
		VALUES ($1,$6,$4,1,ARRAY['google_drive.read'],ARRAY['folder-a'],$5,$3)`,
		pgx.QueryExecModeSimpleProtocol, accountID, otherAccountID, now, connectionID, userID, revisionID); err != nil {
		t.Fatal(err)
	}
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewIntegrationAuthorizationRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := repository.Authority(ctx, ids.AccountID(accountID), ids.IntegrationConnectionID(connectionID))
	if err != nil || authority.Connection.ID != ids.IntegrationConnectionID(connectionID) || authority.Revision.ID != ids.IntegrationConnectionRevisionID(revisionID) {
		t.Fatalf("authorization authority=%+v err=%v", authority, err)
	}
	scopeDigest = integrationauthorization.ScopeRevisionDigest(authority.Revision)
	pending, err := domain.NewAuthorizationSession(domain.AuthorizationSessionInput{ID: ids.IntegrationAuthorizationSessionID(sessionID),
		AccountID: ids.AccountID(accountID), ConnectionID: ids.IntegrationConnectionID(connectionID),
		ConnectionRevision: ids.IntegrationConnectionRevisionID(revisionID), Provider: domain.GoogleOAuthProvider, Scope: domain.GoogleDriveReadScope,
		ScopeRevisionSHA256: scopeDigest, RedirectURI: "https://app.infiniteocean.net/api/v1/accounts/oauth/callback",
		StateSHA256: stateDigest, PKCEChallengeSHA256: pkceDigest, CreatedBy: domain.Actor{UserID: ids.UserID(userID)},
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	startEvent := integrationauthorization.Event{ID: "72800000-0000-4000-8000-000000000008", Type: "authorization_started", ActorKind: "user",
		ActorID: userID, CorrelationID: sessionID, At: now}
	stored, created, err := repository.Create(ctx, pending, accounts.RoleOwner, startEvent)
	if err != nil || !created || stored != pending {
		t.Fatalf("create authorization stored=%+v created=%t err=%v", stored, created, err)
	}
	stored, created, err = repository.Create(ctx, pending, accounts.RoleOwner, startEvent)
	if err != nil || created || stored != pending {
		t.Fatalf("replay authorization stored=%+v created=%t err=%v", stored, created, err)
	}
	loaded, err := repository.Get(ctx, ids.AccountID(accountID), ids.IntegrationAuthorizationSessionID(sessionID))
	if err != nil || loaded != pending {
		t.Fatalf("load authorization=%+v err=%v", loaded, err)
	}
	progress, err := repository.ClaimCallback(ctx, ids.AccountID(accountID), stateDigest, codeDigest, now.Add(time.Minute))
	if err != nil || progress.Session.Status != domain.AuthorizationExchanging || progress.Workflow.State != integrationauthorization.WorkflowClaimed ||
		progress.Workflow.TargetGeneration != 1 || progress.Workflow.PreviousCredentialID != "" {
		t.Fatalf("claim callback=%+v err=%v", progress, err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.integration_authorization_sessions SET status='pending',version=3,
		claimed_at=NULL,updated_at=$3 WHERE account_id=$1 AND id=$2`, accountID, sessionID, now.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "transition is invalid") {
		t.Fatalf("one-use state rewind=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.integration_connections SET state='revoked',revoked_by_user_id=$3,revoked_at=$4,updated_at=$4,version=version+1
		WHERE account_id=$1 AND id=$2`, accountID, connectionID, userID, now.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "active authorization workflow") {
		t.Fatalf("connection workflow fence=%v", err)
	}
	progress, changed, err := repository.StartProviderExchange(ctx, ids.AccountID(accountID), ids.IntegrationAuthorizationSessionID(sessionID), now.Add(2*time.Minute))
	if err != nil || !changed || progress.Workflow.State != integrationauthorization.WorkflowProviderExchanging {
		t.Fatalf("start provider exchange=%+v changed=%t err=%v", progress, changed, err)
	}
	progress, err = repository.MarkCredentialStored(ctx, ids.AccountID(accountID), ids.IntegrationAuthorizationSessionID(sessionID), now.Add(2*time.Minute))
	if err != nil || progress.Workflow.State != integrationauthorization.WorkflowCredentialStored {
		t.Fatalf("mark credential stored=%+v err=%v", progress, err)
	}
	progress, err = repository.CompleteCallback(ctx, ids.AccountID(accountID), ids.IntegrationAuthorizationSessionID(sessionID), now.Add(2*time.Minute))
	if err != nil || progress.Session.Status != domain.AuthorizationCompleted || progress.Workflow.State != integrationauthorization.WorkflowCompleted {
		t.Fatalf("complete credential-bound authorization=%+v err=%v", progress, err)
	}
	var status string
	var generation int
	if err := owner.QueryRow(ctx, `SELECT status,credential_generation FROM spyglass.integration_authorization_sessions WHERE account_id=$1 AND id=$2`, accountID, sessionID).Scan(&status, &generation); err != nil || status != "completed" || generation != 1 {
		t.Fatalf("authorization status=%s generation=%d err=%v", status, generation, err)
	}
	rotationSessionID := ids.IntegrationAuthorizationSessionID("72a00000-0000-4000-8000-00000000000a")
	rotationState := sha256.Sum256([]byte("rotation-state"))
	rotationCode := sha256.Sum256([]byte("rotation-code"))
	rotationPending, err := domain.NewAuthorizationSession(domain.AuthorizationSessionInput{ID: rotationSessionID,
		AccountID: ids.AccountID(accountID), ConnectionID: ids.IntegrationConnectionID(connectionID),
		ConnectionRevision: ids.IntegrationConnectionRevisionID(revisionID), Provider: domain.GoogleOAuthProvider, Scope: domain.GoogleDriveReadScope,
		ScopeRevisionSHA256: scopeDigest, RedirectURI: "https://app.infiniteocean.net/api/v1/accounts/oauth/callback",
		StateSHA256: rotationState, PKCEChallengeSHA256: pkceDigest, CreatedBy: domain.Actor{UserID: ids.UserID(userID)},
		CreatedAt: now.Add(3 * time.Minute), ExpiresAt: now.Add(13 * time.Minute)}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	rotationEvent := integrationauthorization.Event{ID: "72b00000-0000-4000-8000-00000000000b", Type: "authorization_started", ActorKind: "user",
		ActorID: userID, CorrelationID: string(rotationSessionID), At: now.Add(3 * time.Minute)}
	if _, created, err := repository.Create(ctx, rotationPending, accounts.RoleOwner, rotationEvent); err != nil || !created {
		t.Fatalf("create rotation authorization created=%t err=%v", created, err)
	}
	rotation, err := repository.ClaimCallback(ctx, ids.AccountID(accountID), rotationState, rotationCode, now.Add(4*time.Minute))
	if err != nil || rotation.Workflow.PreviousCredentialID != progress.Workflow.TargetCredentialID || rotation.Workflow.PreviousGeneration != 1 || rotation.Workflow.TargetGeneration != 2 {
		t.Fatalf("rotation claim=%+v err=%v", rotation, err)
	}
	rotation, changed, err = repository.StartProviderExchange(ctx, ids.AccountID(accountID), rotationSessionID, now.Add(5*time.Minute))
	if err != nil || !changed {
		t.Fatalf("rotation exchange=%+v changed=%t err=%v", rotation, changed, err)
	}
	rotation, err = repository.MarkCredentialStored(ctx, ids.AccountID(accountID), rotationSessionID, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CompleteCallback(ctx, ids.AccountID(accountID), rotationSessionID, now.Add(5*time.Minute)); !errors.Is(err, integrationauthorization.ErrConflict) {
		t.Fatalf("unfenced rotation completion=%v", err)
	}
	rotation, err = repository.MarkPreviousFenced(ctx, ids.AccountID(accountID), rotationSessionID, now.Add(5*time.Minute))
	if err != nil || rotation.Workflow.State != integrationauthorization.WorkflowPreviousFenced {
		t.Fatalf("rotation fence=%+v err=%v", rotation, err)
	}
	rotation, err = repository.CompleteCallback(ctx, ids.AccountID(accountID), rotationSessionID, now.Add(5*time.Minute))
	if err != nil || rotation.Session.Status != domain.AuthorizationCompleted || rotation.Session.CredentialGeneration != 2 {
		t.Fatalf("rotation complete=%+v err=%v", rotation, err)
	}
	var activeGeneration int
	var rotatedCount int
	if err := owner.QueryRow(ctx, `SELECT credential_generation FROM spyglass.integration_connections WHERE account_id=$1 AND id=$2`, accountID, connectionID).Scan(&activeGeneration); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.integration_credentials WHERE account_id=$1 AND connection_id=$2 AND state='rotated'`, accountID, connectionID).Scan(&rotatedCount); err != nil || activeGeneration != 2 || rotatedCount != 1 {
		t.Fatalf("rotation generations active=%d rotated=%d err=%v", activeGeneration, rotatedCount, err)
	}
	role := "authorization_reader_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN; GRANT USAGE ON SCHEMA spyglass TO `+role+`;
		GRANT SELECT ON spyglass.integration_authorization_sessions,spyglass.integration_authorization_workflows TO `+role); err != nil {
		t.Fatal(err)
	}
	defer owner.Exec(context.Background(), `DROP ROLE IF EXISTS `+role)
	reader := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+role+`; SELECT set_config('app.account_id',$1,false)`, pgx.QueryExecModeSimpleProtocol, otherAccountID)
		return err
	})
	defer reader.Close()
	var count int
	if err := reader.QueryRow(ctx, `SELECT count(*) FROM spyglass.integration_authorization_sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-Account authorization count=%d err=%v", count, err)
	}
	if err := reader.QueryRow(ctx, `SELECT count(*) FROM spyglass.integration_authorization_workflows`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-Account authorization workflow count=%d err=%v", count, err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.integration_authorization_events
		(account_id,id,session_id,event_type,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,'72900000-0000-4000-8000-000000000009',$2,'authorization_completed','provider_callback','google_oauth',$2,'{"generation":1}',$3)`,
		accountID, sessionID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.integration_authorization_events SET event_type='authorization_failed' WHERE account_id=$1 AND session_id=$2`, accountID, sessionID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("authorization event mutation=%v", err)
	}
}
