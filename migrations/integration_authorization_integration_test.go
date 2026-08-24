package migrations_test

import (
	"context"
	"crypto/sha256"
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
	credentialID := "72700000-0000-4000-8000-000000000007"
	stateDigest := sha256.Sum256([]byte("one-use-state"))
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
	if _, err := owner.Exec(ctx, `UPDATE spyglass.integration_authorization_sessions SET status='exchanging',version=2,
		claimed_at=$3,updated_at=$3 WHERE account_id=$1 AND id=$2`, accountID, sessionID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.integration_authorization_sessions SET status='pending',version=3,
		claimed_at=NULL,updated_at=$3 WHERE account_id=$1 AND id=$2`, accountID, sessionID, now.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "transition is invalid") {
		t.Fatalf("one-use state rewind=%v", err)
	}
	tx, err := owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO spyglass.integration_credentials
		(account_id,id,connection_id,generation,provider,reference_sha256,state,created_by_user_id,created_at,updated_at)
		VALUES ($1,$2,$3,1,'google_oauth',decode(repeat('ab',32),'hex'),'active',$4,$5,$5)`,
		accountID, credentialID, connectionID, userID, now.Add(2*time.Minute)); err == nil {
		_, err = tx.Exec(ctx, `UPDATE spyglass.integration_connections SET state='active',credential_id=$3,credential_generation=1,
			version=2,updated_at=$4 WHERE account_id=$1 AND id=$2`, accountID, connectionID, credentialID, now.Add(2*time.Minute))
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE spyglass.integration_authorization_sessions SET status='completed',version=3,credential_id=$3,
			credential_generation=1,completed_at=$4,updated_at=$4 WHERE account_id=$1 AND id=$2`, accountID, sessionID, credentialID, now.Add(2*time.Minute))
	}
	if err == nil {
		err = tx.Commit(ctx)
	} else {
		_ = tx.Rollback(ctx)
	}
	if err != nil {
		t.Fatalf("complete credential-bound authorization: %v", err)
	}
	var status string
	var generation int
	if err := owner.QueryRow(ctx, `SELECT status,credential_generation FROM spyglass.integration_authorization_sessions WHERE account_id=$1 AND id=$2`, accountID, sessionID).Scan(&status, &generation); err != nil || status != "completed" || generation != 1 {
		t.Fatalf("authorization status=%s generation=%d err=%v", status, generation, err)
	}
	role := "authorization_reader_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN; GRANT USAGE ON SCHEMA spyglass TO `+role+`;
		GRANT SELECT ON spyglass.integration_authorization_sessions TO `+role); err != nil {
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
