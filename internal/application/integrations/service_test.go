package integrations

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type integrationTestClock struct{ at time.Time }

func (clock *integrationTestClock) Now() time.Time { return clock.at }

type integrationTestAuthorizer struct {
	role         accounts.MembershipRole
	requirements []access.Requirement
}

func (authorizer *integrationTestAuthorizer) Authorize(_ context.Context, _ access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	authorizer.requirements = append(authorizer.requirements, requirement)
	if len(requirement.Roles) > 0 && !slices.Contains(requirement.Roles, authorizer.role) {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialRole, Package: requirement.Package}
	}
	return access.AccountContext{AccountID: accountID, Role: authorizer.role}, nil
}

type integrationTestStore struct {
	connection         domain.Connection
	revision           domain.ConnectionRevision
	credential         domain.CredentialBinding
	mutations          []Mutation
	listQuery          ConnectionListQuery
	healthQuery        HealthListQuery
	executionQuery     ExecutionListQuery
	executionDetail    ExecutionDetail
	resolutionOutcome  domain.ExecutionState
	resolutionEvidence [32]byte
}

func (store *integrationTestStore) CreateConnection(_ context.Context, input domain.ConnectionInput, role accounts.MembershipRole, mutation Mutation) (domain.Connection, bool, error) {
	connection, revision, err := domain.NewConnection(input, role)
	if err != nil || !mutation.Valid() {
		return domain.Connection{}, false, errors.Join(ErrInvalid, err)
	}
	store.connection, store.revision = connection, revision
	store.mutations = append(store.mutations, mutation)
	return connection, true, nil
}

func (store *integrationTestStore) GetConnection(_ context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID) (domain.Connection, error) {
	if store.connection.AccountID != accountID || store.connection.ID != connectionID {
		return domain.Connection{}, ErrNotFound
	}
	return store.connection, nil
}

func (store *integrationTestStore) GetConnectionDetail(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID) (ConnectionDetail, error) {
	connection, err := store.GetConnection(ctx, accountID, connectionID)
	return ConnectionDetail{Connection: connection, Revision: store.revision}, err
}

func (store *integrationTestStore) ListConnections(_ context.Context, _ ids.AccountID, query ConnectionListQuery) (ConnectionPage, error) {
	store.listQuery = query
	return ConnectionPage{Items: []domain.Connection{store.connection}}, nil
}

func (store *integrationTestStore) ListHealth(_ context.Context, _ ids.AccountID, query HealthListQuery) (HealthPage, error) {
	store.healthQuery = query
	return HealthPage{}, nil
}

func (store *integrationTestStore) GetExecution(_ context.Context, accountID ids.AccountID, executionID ids.IntegrationExecutionID) (ExecutionDetail, error) {
	if store.executionDetail.Execution.AccountID != accountID || store.executionDetail.Execution.ID != executionID {
		return ExecutionDetail{}, ErrNotFound
	}
	return store.executionDetail, nil
}

func (store *integrationTestStore) ListExecutions(_ context.Context, _ ids.AccountID, query ExecutionListQuery) (ExecutionPage, error) {
	store.executionQuery = query
	return ExecutionPage{}, nil
}

func (store *integrationTestStore) PrepareExecution(_ context.Context, request PrepareExecutionRequest, _ accounts.MembershipRole, mutation Mutation) (domain.Execution, bool, error) {
	value, err := domain.NewExecution(domain.ExecutionInput{ID: request.ID, AccountID: request.AccountID, ReleaseID: request.ReleaseID,
		ReleaseVersion: request.ReleaseVersion, ApprovalID: "95600000-0000-4000-8000-000000000006", Capability: request.Capability,
		Connection: store.connection, ConnectionRevision: store.revision, Credential: store.credential,
		PayloadSHA256: sha256.Sum256([]byte("test delivery manifest")), CreatedAt: request.PreparedAt})
	if err != nil || !mutation.Valid() {
		return domain.Execution{}, false, errors.Join(ErrInvalid, err)
	}
	store.mutations = append(store.mutations, mutation)
	return value, true, nil
}

func (store *integrationTestStore) RequestExecutionResolution(_ context.Context, _ ids.AccountID, _ ids.IntegrationExecutionID,
	resolutionID ids.IntegrationResolutionID, outcome domain.ExecutionState, evidence [32]byte, _ accounts.MembershipRole, mutation Mutation) error {
	store.mutations = append(store.mutations, mutation)
	store.resolutionOutcome, store.resolutionEvidence = outcome, evidence
	store.executionDetail.Resolution = &domain.ExecutionResolution{ID: resolutionID, AccountID: store.executionDetail.Execution.AccountID,
		ExecutionID: store.executionDetail.Execution.ID, RequestedOutcome: outcome, EvidenceSHA256: evidence,
		RequestedByUserID: mutation.Actor.UserID, RequestedAt: mutation.At, State: domain.ResolutionPending}
	return nil
}

func (store *integrationTestStore) ConfirmExecutionResolution(_ context.Context, _ ids.AccountID, _ ids.IntegrationExecutionID,
	_ ids.IntegrationResolutionID, _ accounts.MembershipRole, mutation Mutation) error {
	store.mutations = append(store.mutations, mutation)
	return nil
}

func TestExecutionResolutionCommandsRequireHumanManagerAndHashEvidence(t *testing.T) {
	accountID := ids.AccountID("99100000-0000-4000-8000-000000000001")
	userID := ids.UserID("99200000-0000-4000-8000-000000000002")
	executionID := ids.IntegrationExecutionID("99300000-0000-4000-8000-000000000003")
	requestID := "99400000-0000-4000-8000-000000000004"
	now := time.Date(2026, 8, 23, 23, 30, 0, 0, time.UTC)
	store := &integrationTestStore{executionDetail: ExecutionDetail{Execution: domain.Execution{ID: executionID, AccountID: accountID}}}
	authorizer := &integrationTestAuthorizer{role: accounts.RoleOwner}
	service, err := New(authorizer, store, &integrationTestClock{at: now})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := service.RequestExecutionResolution(context.Background(), RequestExecutionResolutionCommand{Actor: access.Actor{UserID: userID},
		AccountID: accountID, RequestID: requestID, ExecutionID: executionID, RequestedOutcome: domain.ExecutionSucceeded,
		Evidence: "  provider receipt 123  "})
	wantDigest := sha256.Sum256([]byte("provider receipt 123"))
	if err != nil || detail.Resolution == nil || store.resolutionEvidence != wantDigest || store.resolutionOutcome != domain.ExecutionSucceeded ||
		len(authorizer.requirements) != 1 || !authorizer.requirements[0].Mutation {
		t.Fatalf("detail=%+v digest=%x requirements=%+v err=%v", detail, store.resolutionEvidence, authorizer.requirements, err)
	}
	if _, err := service.RequestExecutionResolution(context.Background(), RequestExecutionResolutionCommand{Actor: access.Actor{WorkloadID: "runner"},
		AccountID: accountID, RequestID: requestID, ExecutionID: executionID, RequestedOutcome: domain.ExecutionSucceeded, Evidence: "receipt"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("workload resolution=%v", err)
	}
}

func (store *integrationTestStore) ReviseConnection(_ context.Context, _ ids.AccountID, _ ids.IntegrationConnectionID, expected uint64, input domain.ConnectionRevisionInput, actor domain.Actor, role accounts.MembershipRole, mutation Mutation) (domain.Connection, error) {
	connection, revision, err := store.connection.Revise(expected, input, actor, role, mutation.At)
	if err != nil || !mutation.Valid() {
		return domain.Connection{}, errors.Join(ErrInvalid, err)
	}
	store.connection, store.revision = connection, revision
	store.mutations = append(store.mutations, mutation)
	return connection, nil
}

func (store *integrationTestStore) ActivateConnection(_ context.Context, _ ids.AccountID, _ ids.IntegrationConnectionID, expected uint64, input domain.CredentialInput, actor domain.Actor, role accounts.MembershipRole, mutation Mutation) (domain.Connection, error) {
	credential, err := domain.NewCredentialBinding(input, role)
	if err != nil {
		return domain.Connection{}, err
	}
	connection, err := store.connection.Activate(expected, credential, actor, role, mutation.At)
	if err != nil || !mutation.Valid() {
		return domain.Connection{}, errors.Join(ErrInvalid, err)
	}
	store.connection, store.credential = connection, credential
	store.mutations = append(store.mutations, mutation)
	return connection, nil
}

func (store *integrationTestStore) RotateCredential(_ context.Context, _ ids.AccountID, _ ids.IntegrationConnectionID, expectedVersion, expectedGeneration uint64, input domain.CredentialInput, actor domain.Actor, role accounts.MembershipRole, mutation Mutation) (domain.Connection, error) {
	previous, replacement, err := store.credential.Rotate(input, expectedGeneration, actor, role, mutation.At)
	if err != nil {
		return domain.Connection{}, err
	}
	connection, err := store.connection.BindCredential(expectedVersion, replacement, actor, role, mutation.At)
	if err != nil || !mutation.Valid() {
		return domain.Connection{}, errors.Join(ErrInvalid, err)
	}
	_ = previous
	store.connection, store.credential = connection, replacement
	store.mutations = append(store.mutations, mutation)
	return connection, nil
}

func (store *integrationTestStore) DisableConnection(_ context.Context, _ ids.AccountID, _ ids.IntegrationConnectionID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation Mutation) (domain.Connection, error) {
	connection, err := store.connection.Disable(expected, actor, role, mutation.At)
	if err != nil || !mutation.Valid() {
		return domain.Connection{}, errors.Join(ErrInvalid, err)
	}
	store.connection = connection
	store.mutations = append(store.mutations, mutation)
	return connection, nil
}

func (store *integrationTestStore) EnableConnection(_ context.Context, _ ids.AccountID, _ ids.IntegrationConnectionID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation Mutation) (domain.Connection, error) {
	connection, err := store.connection.Activate(expected, store.credential, actor, role, mutation.At)
	if err != nil || !mutation.Valid() {
		return domain.Connection{}, errors.Join(ErrInvalid, err)
	}
	store.connection = connection
	store.mutations = append(store.mutations, mutation)
	return connection, nil
}

func (store *integrationTestStore) RevokeConnection(_ context.Context, _ ids.AccountID, _ ids.IntegrationConnectionID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation Mutation) (domain.Connection, error) {
	connection, err := store.connection.Revoke(expected, actor, role, mutation.At)
	if err != nil || !mutation.Valid() {
		return domain.Connection{}, errors.Join(ErrInvalid, err)
	}
	store.connection = connection
	store.mutations = append(store.mutations, mutation)
	return connection, nil
}

func TestManagementLifecycleUsesIntegrationsAuthorityAndOpaqueCredentialAttestation(t *testing.T) {
	accountID := ids.AccountID("94100000-0000-4000-8000-000000000001")
	userID := ids.UserID("94200000-0000-4000-8000-000000000002")
	connectionID := "94300000-0000-4000-8000-000000000003"
	authorizer := &integrationTestAuthorizer{role: accounts.RoleOwner}
	store := &integrationTestStore{}
	clock := &integrationTestClock{at: time.Date(2026, 8, 23, 18, 0, 0, 0, time.UTC)}
	service, err := New(authorizer, store, clock)
	if err != nil {
		t.Fatal(err)
	}
	actor := access.Actor{UserID: userID}
	connection, created, err := service.CreateConnection(context.Background(), CreateConnectionCommand{Actor: actor, AccountID: accountID, RequestID: connectionID,
		Name: "Campaign email", Kind: domain.ConnectorEmail, Capabilities: []domain.Capability{domain.CapabilityEmailSend},
		Scope: domain.ConnectionScope{EmailAddress: "launch@Example.com", AudienceReference: "audience:customers-v1"}})
	if err != nil || !created || connection.State != domain.ConnectionPending || connection.CurrentRevision != 1 {
		t.Fatalf("create=%+v created=%v err=%v", connection, created, err)
	}
	derivedRevision, _ := ids.Derive(connectionID, "integration-connection-revision-1")
	if string(connection.CurrentRevisionID) != derivedRevision || store.revision.Scope.EmailAddress != "launch@example.com" {
		t.Fatalf("revision=%+v", store.revision)
	}
	digest := sha256.Sum256([]byte("opaque-broker-reference"))
	activateID := "94400000-0000-4000-8000-000000000004"
	clock.at = clock.at.Add(time.Minute)
	connection, err = service.ActivateConnection(context.Background(), CredentialCommand{Actor: actor, AccountID: accountID, RequestID: activateID,
		ConnectionID: connection.ID, ExpectedVersion: 1, Provider: "mock_smtp", ReferenceSHA256: digest})
	if err != nil || connection.State != domain.ConnectionActive || connection.CredentialGeneration != 1 || store.credential.ReferenceSHA256 != digest {
		t.Fatalf("activate=%+v credential=%+v err=%v", connection, store.credential, err)
	}
	clock.at = clock.at.Add(time.Minute)
	execution, created, err := service.PrepareExecution(context.Background(), PrepareExecutionCommand{Actor: actor, AccountID: accountID,
		RequestID: "94410000-0000-4000-8000-000000000014", ReleaseID: "94420000-0000-4000-8000-000000000024", ReleaseVersion: 3,
		Capability: domain.CapabilityEmailSend, ConnectionID: connection.ID})
	if err != nil || !created || execution.State != domain.ExecutionPrepared || execution.ConnectionRevision != 1 || execution.CredentialGeneration != 1 {
		t.Fatalf("execution=%+v created=%t err=%v", execution, created, err)
	}
	reviseID := "94500000-0000-4000-8000-000000000005"
	clock.at = clock.at.Add(time.Minute)
	connection, err = service.ReviseConnection(context.Background(), ReviseConnectionCommand{Actor: actor, AccountID: accountID, RequestID: reviseID,
		ConnectionID: connection.ID, ExpectedVersion: 2, Name: "Campaign email v2", Capabilities: []domain.Capability{domain.CapabilityEmailRead, domain.CapabilityEmailSend},
		Scope: domain.ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:customers-v2"}})
	if err != nil || connection.Version != 3 || connection.CurrentRevision != 2 || string(connection.CurrentRevisionID) != reviseID {
		t.Fatalf("revise=%+v err=%v", connection, err)
	}
	rotateID := "94600000-0000-4000-8000-000000000006"
	clock.at = clock.at.Add(time.Minute)
	connection, err = service.RotateCredential(context.Background(), CredentialCommand{Actor: actor, AccountID: accountID, RequestID: rotateID,
		ConnectionID: connection.ID, ExpectedVersion: 3, ExpectedGeneration: 1, Provider: "mock_smtp", ReferenceSHA256: sha256.Sum256([]byte("next-reference"))})
	if err != nil || connection.Version != 4 || connection.CredentialGeneration != 2 {
		t.Fatalf("rotate=%+v err=%v", connection, err)
	}
	for _, transition := range []struct {
		id   string
		call func(context.Context, TransitionCommand) (domain.Connection, error)
		want domain.ConnectionState
	}{{"94700000-0000-4000-8000-000000000007", service.DisableConnection, domain.ConnectionDisabled},
		{"94800000-0000-4000-8000-000000000008", service.EnableConnection, domain.ConnectionActive},
		{"94900000-0000-4000-8000-000000000009", service.RevokeConnection, domain.ConnectionRevoked}} {
		clock.at = clock.at.Add(time.Minute)
		connection, err = transition.call(context.Background(), TransitionCommand{Actor: actor, AccountID: accountID, RequestID: transition.id,
			ConnectionID: connection.ID, ExpectedVersion: connection.Version})
		if err != nil || connection.State != transition.want {
			t.Fatalf("transition=%s value=%+v err=%v", transition.id, connection, err)
		}
	}
	wantKinds := []string{"connection_created", "connection_activated", "execution_prepared", "connection_revised", "credential_rotated", "connection_disabled", "connection_activated", "connection_revoked"}
	for index, want := range wantKinds {
		if store.mutations[index].Kind != want || !store.mutations[index].Valid() {
			t.Fatalf("mutation[%d]=%+v", index, store.mutations[index])
		}
	}
	marketingRequirements := 0
	for _, requirement := range authorizer.requirements {
		if requirement.Package == PackageCode {
			if !requirement.Mutation || !slices.Equal(requirement.Roles, []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}) {
				t.Fatalf("management requirement=%+v", requirement)
			}
			continue
		}
		if requirement.Package != "marketing" || !requirement.Mutation || !slices.Equal(requirement.Roles, []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}) {
			t.Fatalf("management requirement=%+v", requirement)
		}
		marketingRequirements++
	}
	if marketingRequirements != 1 {
		t.Fatalf("marketing requirements=%d", marketingRequirements)
	}
}

func TestReadsNormalizeBoundedQueriesAndManagementRejectsMemberOrWorkload(t *testing.T) {
	accountID := ids.AccountID("95100000-0000-4000-8000-000000000001")
	userID := ids.UserID("95200000-0000-4000-8000-000000000002")
	connectionID := ids.IntegrationConnectionID("95300000-0000-4000-8000-000000000003")
	authorizer := &integrationTestAuthorizer{role: accounts.RoleMember}
	store := &integrationTestStore{connection: domain.Connection{ID: connectionID, AccountID: accountID}}
	service, _ := New(authorizer, store, &integrationTestClock{at: time.Now()})
	actor := access.Actor{UserID: userID}
	if _, err := service.GetConnection(context.Background(), actor, accountID, connectionID); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListConnections(context.Background(), actor, accountID, ConnectionListQuery{States: []domain.ConnectionState{domain.ConnectionRevoked, domain.ConnectionActive}, Kinds: []domain.ConnectorKind{domain.ConnectorWebPublish, domain.ConnectorWebResearch, domain.ConnectorGoogleDrive, domain.ConnectorEmail}})
	if err != nil || len(page.Items) != 1 || store.listQuery.Limit != DefaultConnectionPageSize ||
		!slices.Equal(store.listQuery.States, []domain.ConnectionState{domain.ConnectionActive, domain.ConnectionRevoked}) ||
		!slices.Equal(store.listQuery.Kinds, []domain.ConnectorKind{domain.ConnectorEmail, domain.ConnectorGoogleDrive, domain.ConnectorWebPublish, domain.ConnectorWebResearch}) {
		t.Fatalf("page=%+v query=%+v err=%v", page, store.listQuery, err)
	}
	_, _, err = service.CreateConnection(context.Background(), CreateConnectionCommand{Actor: actor, AccountID: accountID,
		RequestID: "95400000-0000-4000-8000-000000000004", Name: "Denied", Kind: domain.ConnectorEmail,
		Capabilities: []domain.Capability{domain.CapabilityEmailRead}, Scope: domain.ConnectionScope{EmailAddress: "read@example.com"}})
	if !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("member management error=%v", err)
	}
	_, _, err = service.CreateConnection(context.Background(), CreateConnectionCommand{Actor: access.Actor{WorkloadID: "integration-worker"}, AccountID: accountID,
		RequestID: "95500000-0000-4000-8000-000000000005"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("workload management error=%v", err)
	}
	if _, err := service.ListHealth(context.Background(), actor, accountID, HealthListQuery{ConnectionID: connectionID}); err != nil || store.healthQuery.Limit != DefaultConnectionPageSize {
		t.Fatalf("health query=%+v err=%v", store.healthQuery, err)
	}
	if _, err := service.ListExecutions(context.Background(), actor, accountID, ExecutionListQuery{
		States:       []domain.ExecutionState{domain.ExecutionSucceeded, domain.ExecutionManualResolution},
		Capabilities: []domain.Capability{domain.CapabilityWebPublish, domain.CapabilityEmailSend}}); err != nil ||
		store.executionQuery.Limit != DefaultConnectionPageSize || !slices.Equal(store.executionQuery.States, []domain.ExecutionState{domain.ExecutionManualResolution, domain.ExecutionSucceeded}) ||
		!slices.Equal(store.executionQuery.Capabilities, []domain.Capability{domain.CapabilityEmailSend, domain.CapabilityWebPublish}) {
		t.Fatalf("execution query=%+v err=%v", store.executionQuery, err)
	}
	if _, err := service.ListExecutions(context.Background(), actor, accountID, ExecutionListQuery{States: []domain.ExecutionState{domain.ExecutionUnknown, domain.ExecutionUnknown}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate execution state error=%v", err)
	}
}

func TestConstructionAndErrorClassification(t *testing.T) {
	authorizer, store, clock := &integrationTestAuthorizer{}, &integrationTestStore{}, &integrationTestClock{}
	for name, build := range map[string]func() error{
		"authorizer": func() error { _, err := New(nil, store, clock); return err },
		"store":      func() error { _, err := New(authorizer, nil, clock); return err },
		"clock":      func() error { _, err := New(authorizer, store, nil); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := build(); err == nil {
				t.Fatal("missing dependency accepted")
			}
		})
	}
	if !errors.Is(ClassifyForAdapter(domain.ErrConflict), ErrConflict) || !errors.Is(ClassifyForAdapter(errors.New("database unavailable")), ErrRepository) ||
		!errors.Is(ClassifyForAdapter(domain.ErrCredential), ErrInvalid) {
		t.Fatal("unexpected error classification")
	}
}
