package integrations

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const integrationTestNamespace = "81000000-0000-4000-8000-000000000001"

func integrationTestID(t *testing.T, label string) string {
	t.Helper()
	value, err := ids.Derive(integrationTestNamespace, label)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func integrationActor(t *testing.T) Actor {
	return Actor{UserID: ids.UserID(integrationTestID(t, "user"))}
}

func TestConnectionFreezesNormalizedScopeAndManagerAuthority(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	input := ConnectionInput{
		ID: ids.IntegrationConnectionID(integrationTestID(t, "connection")), RevisionID: ids.IntegrationConnectionRevisionID(integrationTestID(t, "revision-1")),
		AccountID: ids.AccountID(integrationTestID(t, "account")), Name: "Campaign email", Kind: ConnectorEmail,
		Capabilities: []Capability{CapabilityEmailSend, CapabilityEmailRead}, Scope: ConnectionScope{EmailAddress: "Launch@Example.com", AudienceReference: "audience:customers-v1"},
		CreatedBy: integrationActor(t), CreatedAt: now,
	}
	connection, revision, err := NewConnection(input, accounts.RoleAdministrator)
	if err != nil {
		t.Fatal(err)
	}
	if connection.State != ConnectionPending || connection.Version != 1 || revision.Revision != 1 || revision.Scope.EmailAddress != "Launch@example.com" ||
		len(revision.Capabilities) != 2 || revision.Capabilities[0] != CapabilityEmailRead || revision.Capabilities[1] != CapabilityEmailSend {
		t.Fatalf("connection=%+v revision=%+v", connection, revision)
	}
	if _, _, err := NewConnection(input, accounts.RoleMember); !errors.Is(err, ErrRole) {
		t.Fatalf("member create error=%v", err)
	}
	input.Capabilities = []Capability{CapabilityWebPublish}
	if _, _, err := NewConnection(input, accounts.RoleOwner); !errors.Is(err, ErrCapability) {
		t.Fatalf("cross-kind capability error=%v", err)
	}
	input.Kind, input.Capabilities = ConnectorWebPublish, []Capability{CapabilityWebPublish}
	input.Scope = ConnectionScope{HTTPSOrigin: "https://Publish.Example.com/", PathPrefix: "/campaigns"}
	_, webRevision, err := NewConnection(input, accounts.RoleOwner)
	if err != nil || webRevision.Scope.HTTPSOrigin != "https://publish.example.com" {
		t.Fatalf("web revision=%+v error=%v", webRevision, err)
	}
	input.Scope.PathPrefix = "/campaigns/../admin"
	if _, _, err := NewConnection(input, accounts.RoleOwner); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsafe path error=%v", err)
	}
	input.Kind, input.Capabilities = ConnectorGoogleDrive, []Capability{CapabilityDriveRead}
	input.Scope = ConnectionScope{DriveFolderIDs: []string{"folder_Z", "folder-a", "folder_0"}}
	_, driveRevision, err := NewConnection(input, accounts.RoleOwner)
	if err != nil || len(driveRevision.Scope.DriveFolderIDs) != 3 || driveRevision.Scope.DriveFolderIDs[0] != "folder-a" || driveRevision.Scope.DriveFolderIDs[2] != "folder_Z" {
		t.Fatalf("drive revision=%+v error=%v", driveRevision, err)
	}
	for name, folders := range map[string][]string{
		"missing":   nil,
		"duplicate": {"folder-a", "folder-a"},
		"trimmed":   {" folder-a"},
		"unsafe":    {"folder/a"},
	} {
		input.Scope = ConnectionScope{DriveFolderIDs: folders}
		if _, _, err := NewConnection(input, accounts.RoleOwner); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s folders error=%v", name, err)
		}
	}
	input.Scope = ConnectionScope{DriveFolderIDs: make([]string, MaximumDriveFolders+1)}
	for index := range input.Scope.DriveFolderIDs {
		input.Scope.DriveFolderIDs[index] = fmt.Sprintf("folder-%02d", index)
	}
	if _, _, err := NewConnection(input, accounts.RoleOwner); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized folder scope error=%v", err)
	}
}

func TestCredentialRotationAndRevocationAreMonotonic(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	actor := integrationActor(t)
	accountID := ids.AccountID(integrationTestID(t, "account"))
	connectionID := ids.IntegrationConnectionID(integrationTestID(t, "connection"))
	firstInput := CredentialInput{ID: ids.IntegrationCredentialID(integrationTestID(t, "credential-1")), AccountID: accountID, ConnectionID: connectionID,
		Generation: 1, Provider: "mock_smtp", ReferenceSHA256: sha256.Sum256([]byte("secret-broker:binding-1")), CreatedBy: actor, CreatedAt: now}
	first, err := NewCredentialBinding(firstInput, accounts.RoleOwner)
	if err != nil || !first.Available(now) {
		t.Fatalf("credential=%+v error=%v", first, err)
	}
	secondInput := CredentialInput{ID: ids.IntegrationCredentialID(integrationTestID(t, "credential-2")), AccountID: accountID, ConnectionID: connectionID,
		Generation: 2, Provider: "mock_smtp", ReferenceSHA256: sha256.Sum256([]byte("secret-broker:binding-2")), CreatedBy: actor}
	rotated, second, err := first.Rotate(secondInput, 1, actor, accounts.RoleAdministrator, now.Add(time.Minute))
	if err != nil || rotated.State != CredentialRotated || second.State != CredentialActive || second.Generation != 2 || rotated.Available(now.Add(2*time.Minute)) {
		t.Fatalf("rotated=%+v second=%+v error=%v", rotated, second, err)
	}
	if _, _, err := first.Rotate(secondInput, 2, actor, accounts.RoleOwner, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale rotate error=%v", err)
	}
	revoked, err := second.Revoke(2, actor, accounts.RoleOwner, now.Add(2*time.Minute))
	if err != nil || revoked.State != CredentialRevoked || revoked.Available(now.Add(3*time.Minute)) {
		t.Fatalf("revoked=%+v error=%v", revoked, err)
	}
	if _, err := revoked.Revoke(2, actor, accounts.RoleOwner, now.Add(3*time.Minute)); !errors.Is(err, ErrState) {
		t.Fatalf("repeat revoke error=%v", err)
	}
}

func TestConnectionRevisionAndCredentialBindingDoNotRewritePriorAuthority(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	input := integrationExecutionInput(t, now)
	prior := input.ConnectionRevision
	revisedConnection, revision, err := input.Connection.Revise(input.Connection.Version,
		ConnectionRevisionInput{ID: ids.IntegrationConnectionRevisionID(integrationTestID(t, "revision-2")), Name: "Campaign email v2", Capabilities: []Capability{CapabilityEmailSend},
			Scope: ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:customers-v2"}}, integrationActor(t), accounts.RoleOwner, now.Add(time.Minute))
	if err != nil || revisedConnection.CurrentRevision != 2 || revision.Scope.AudienceReference != "audience:customers-v2" || prior.Scope.AudienceReference != "audience:customers-v1" {
		t.Fatalf("connection=%+v prior=%+v revision=%+v error=%v", revisedConnection, prior, revision, err)
	}
	nextCredential, err := NewCredentialBinding(CredentialInput{ID: ids.IntegrationCredentialID(integrationTestID(t, "credential-2")), AccountID: input.AccountID, ConnectionID: input.Connection.ID,
		Generation: 2, Provider: "mock_smtp", ReferenceSHA256: sha256.Sum256([]byte("secret-broker:binding-2")), CreatedBy: integrationActor(t), CreatedAt: now.Add(2 * time.Minute)}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := revisedConnection.BindCredential(revisedConnection.Version, nextCredential, integrationActor(t), accounts.RoleAdministrator, now.Add(3*time.Minute))
	if err != nil || rebound.CredentialID != nextCredential.ID || rebound.CredentialGeneration != 2 || input.Connection.CredentialID != input.Credential.ID {
		t.Fatalf("rebound=%+v original=%+v error=%v", rebound, input.Connection, err)
	}
	health, err := NewHealthObservation(ids.IntegrationHealthObservationID(integrationTestID(t, "health")), rebound, HealthDegraded, "provider_rate_limited", 1250, now.Add(4*time.Minute))
	if err != nil || health.ConnectionRevisionID != revision.ID || health.CredentialID != nextCredential.ID {
		t.Fatalf("health=%+v error=%v", health, err)
	}
}

func TestExecutionRetriesOnlyAfterReconciliationProvesNotApplied(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	execution := integrationExecutionFixture(t, now)
	executing, first, err := execution.Claim(ids.IntegrationAttemptID(integrationTestID(t, "attempt-1")), now.Add(time.Minute), now)
	if err != nil || executing.State != ExecutionExecuting || first.Mode != AttemptExecute {
		t.Fatalf("executing=%+v attempt=%+v error=%v", executing, first, err)
	}
	unknown, first, err := executing.Complete(first, AttemptUnknown, "provider_timeout", nil, now.Add(10*time.Second))
	if err != nil || unknown.State != ExecutionUnknown || first.Outcome != AttemptUnknown {
		t.Fatalf("unknown=%+v attempt=%+v error=%v", unknown, first, err)
	}
	reconciling, second, err := unknown.Claim(ids.IntegrationAttemptID(integrationTestID(t, "attempt-2")), now.Add(2*time.Minute), now.Add(time.Minute))
	if err != nil || reconciling.State != ExecutionReconciling || second.Mode != AttemptReconcile {
		t.Fatalf("reconciling=%+v attempt=%+v error=%v", reconciling, second, err)
	}
	retryAt := now.Add(3 * time.Minute)
	retrying, _, err := reconciling.Complete(second, AttemptNotApplied, "provider_confirmed_absent", &retryAt, now.Add(90*time.Second))
	if err != nil || retrying.State != ExecutionRetryWait {
		t.Fatalf("retry=%+v error=%v", retrying, err)
	}
	if _, _, err := retrying.Claim(ids.IntegrationAttemptID(integrationTestID(t, "attempt-early")), now.Add(4*time.Minute), now.Add(2*time.Minute)); !errors.Is(err, ErrState) {
		t.Fatalf("early retry error=%v", err)
	}
	executing, third, err := retrying.Claim(ids.IntegrationAttemptID(integrationTestID(t, "attempt-3")), now.Add(4*time.Minute), retryAt)
	if err != nil || third.Mode != AttemptExecute {
		t.Fatalf("retry execute=%+v attempt=%+v error=%v", executing, third, err)
	}
	succeeded, _, err := executing.Complete(third, AttemptSucceeded, "", nil, now.Add(3*time.Minute+10*time.Second))
	if err != nil || succeeded.State != ExecutionSucceeded || succeeded.CompletedAt == nil {
		t.Fatalf("succeeded=%+v error=%v", succeeded, err)
	}
}

func TestExecutionUnknownIsBoundedAndNeverReexecutes(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	value := integrationExecutionFixture(t, now)
	for number := 1; number <= int(MaximumAttempts); number++ {
		attemptID := ids.IntegrationAttemptID(integrationTestID(t, "unknown-attempt-"+string(rune('0'+number))))
		claimed, attempt, err := value.Claim(attemptID, now.Add(time.Duration(number)*time.Minute), now.Add(time.Duration(number-1)*time.Minute))
		if err != nil {
			t.Fatalf("claim %d: %v", number, err)
		}
		if number == 1 && attempt.Mode != AttemptExecute || number > 1 && attempt.Mode != AttemptReconcile {
			t.Fatalf("attempt %d mode=%s", number, attempt.Mode)
		}
		value, _, err = claimed.Complete(attempt, AttemptUnknown, "provider_ambiguous", nil, now.Add(time.Duration(number-1)*time.Minute+10*time.Second))
		if err != nil {
			t.Fatalf("complete %d: %v", number, err)
		}
	}
	if value.State != ExecutionManualResolution || value.AttemptCount != MaximumAttempts {
		t.Fatalf("execution=%+v", value)
	}
	if _, _, err := value.Claim(ids.IntegrationAttemptID(integrationTestID(t, "attempt-4")), now.Add(5*time.Minute), now.Add(4*time.Minute)); !errors.Is(err, ErrState) {
		t.Fatalf("manual-resolution claim error=%v", err)
	}
}

func TestLostLeaseBecomesUnknownAndRevokedCredentialCannotPrepare(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	execution := integrationExecutionFixture(t, now)
	claimed, attempt, err := execution.Claim(ids.IntegrationAttemptID(integrationTestID(t, "lease-attempt")), now.Add(time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	unknown, expired, err := claimed.ExpireLease(attempt, now.Add(time.Minute))
	if err != nil || unknown.State != ExecutionUnknown || expired.ErrorCode != "lease_expired" {
		t.Fatalf("unknown=%+v attempt=%+v error=%v", unknown, expired, err)
	}

	input := integrationExecutionInput(t, now.Add(2*time.Minute))
	revoked, err := input.Credential.Revoke(input.Credential.Generation, integrationActor(t), accounts.RoleOwner, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	input.Credential = revoked
	if _, err := NewExecution(input); !errors.Is(err, ErrCredential) {
		t.Fatalf("revoked credential execution error=%v", err)
	}
}

func TestRestoreObservabilityRejectsMalformedHistory(t *testing.T) {
	now := time.Date(2026, 8, 23, 13, 0, 0, 0, time.UTC)
	connection := integrationExecutionInput(t, now).Connection
	health, err := NewHealthObservation(ids.IntegrationHealthObservationID(integrationTestID(t, "health")), connection, HealthHealthy, "", 12, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	malformedHealth := health
	malformedHealth.ErrorCode = "secret_leaked"
	if _, err := RestoreHealthObservation(malformedHealth); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed health error=%v", err)
	}
	execution := integrationExecutionFixture(t, now)
	_, attempt, err := execution.Claim(ids.IntegrationAttemptID(integrationTestID(t, "restore-attempt")), now.Add(time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	malformedAttempt := attempt
	malformedAttempt.Outcome = AttemptFailed
	if _, err := RestoreAttempt(malformedAttempt); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed attempt error=%v", err)
	}
}

func integrationExecutionFixture(t *testing.T, now time.Time) Execution {
	t.Helper()
	value, err := NewExecution(integrationExecutionInput(t, now))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func integrationExecutionInput(t *testing.T, now time.Time) ExecutionInput {
	t.Helper()
	actor := integrationActor(t)
	accountID := ids.AccountID(integrationTestID(t, "account"))
	connection, revision, err := NewConnection(ConnectionInput{ID: ids.IntegrationConnectionID(integrationTestID(t, "connection")), RevisionID: ids.IntegrationConnectionRevisionID(integrationTestID(t, "revision-1")),
		AccountID: accountID, Name: "Campaign email", Kind: ConnectorEmail, Capabilities: []Capability{CapabilityEmailSend},
		Scope: ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:customers-v1"}, CreatedBy: actor, CreatedAt: now.Add(-2 * time.Minute)}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := NewCredentialBinding(CredentialInput{ID: ids.IntegrationCredentialID(integrationTestID(t, "credential-1")), AccountID: accountID, ConnectionID: connection.ID,
		Generation: 1, Provider: "mock_smtp", ReferenceSHA256: sha256.Sum256([]byte("secret-broker:binding-1")), CreatedBy: actor, CreatedAt: now.Add(-time.Minute)}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	connection, err = connection.Activate(1, credential, actor, accounts.RoleOwner, now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return ExecutionInput{ID: ids.IntegrationExecutionID(integrationTestID(t, "execution")), AccountID: accountID,
		ReleaseID: ids.MarketingReleaseID(integrationTestID(t, "release")), ReleaseVersion: 3, ApprovalID: ids.ConsequentialApprovalID(integrationTestID(t, "approval")),
		Capability: CapabilityEmailSend, Connection: connection, ConnectionRevision: revision, Credential: credential,
		PayloadSHA256: sha256.Sum256([]byte("canonical-release-and-connector-envelope")), CreatedAt: now}
}
