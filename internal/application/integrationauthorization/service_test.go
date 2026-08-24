package integrationauthorization

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	authorizationAccount    = ids.AccountID("b1000000-0000-4000-8000-000000000001")
	authorizationUser       = ids.UserID("b2000000-0000-4000-8000-000000000002")
	authorizationConnection = ids.IntegrationConnectionID("b3000000-0000-4000-8000-000000000003")
	authorizationRevision   = ids.IntegrationConnectionRevisionID("b4000000-0000-4000-8000-000000000004")
	authorizationRequest    = "b5000000-0000-4000-8000-000000000005"
	authorizationRedirect   = "https://app.infiniteocean.net/api/v1/integrations/google/callback"
)

var authorizationNow = time.Date(2026, 8, 23, 18, 0, 0, 0, time.UTC)

func TestBeginFreezesAuthorityAndReplaysExactConsent(t *testing.T) {
	repository := newAuthorizationRepository(t)
	secretStore := newAuthorizationSecretStore()
	provider := &authorizationProvider{}
	service := newAuthorizationService(t, repository, secretStore, provider)
	command := authorizationCommand()
	first, err := service.Begin(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Session.ID != ids.IntegrationAuthorizationSessionID(authorizationRequest) || first.Session.ConnectionRevision != authorizationRevision ||
		first.Session.ScopeRevisionSHA256 != ScopeRevisionDigest(repository.authority.Revision) || first.Session.Status != domain.AuthorizationPending ||
		first.Session.ExpiresAt != authorizationNow.Add(AuthorizationTTL) || first.AuthorizationURL == "" {
		t.Fatalf("first=%+v", first)
	}
	if first.Session.StateSHA256 != sha256.Sum256(secretStore.value.State) || first.Session.PKCEChallengeSHA256 != sha256.Sum256(pkceChallenge(secretStore.value.Verifier)) {
		t.Fatalf("session digests do not bind encrypted material")
	}
	second, err := service.Begin(context.Background(), command)
	if err != nil || second.AuthorizationURL != first.AuthorizationURL || second.Session != first.Session || repository.createCalls != 1 {
		t.Fatalf("replay=%+v err=%v creates=%d", second, err, repository.createCalls)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls=%d", provider.calls)
	}
}

func TestBeginRequiresRecentPasskeyAndExactDriveAuthority(t *testing.T) {
	repository := newAuthorizationRepository(t)
	service := newAuthorizationService(t, repository, newAuthorizationSecretStore(), &authorizationProvider{})
	command := authorizationCommand()
	command.Session.ReauthenticationMethod = sessions.AuthenticationMethodPassword
	if _, err := service.Begin(context.Background(), command); err == nil {
		t.Fatal("password-only session authorized provider consent")
	}
	command = authorizationCommand()
	repository.authority.Connection.State = domain.ConnectionRevoked
	repository.authority.Connection.RevokedBy = &domain.Actor{UserID: authorizationUser}
	repository.authority.Connection.RevokedAt = &authorizationNow
	repository.authority.Connection.UpdatedAt = authorizationNow
	if _, err := service.Begin(context.Background(), command); !errors.Is(err, ErrInvalid) {
		t.Fatalf("revoked authority error=%v", err)
	}
}

func TestBeginRecoversStoredSecretAndRejectsChangedReplay(t *testing.T) {
	repository := newAuthorizationRepository(t)
	secretStore := newAuthorizationSecretStore()
	secretStore.failPutAfterStore = true
	service := newAuthorizationService(t, repository, secretStore, &authorizationProvider{})
	command := authorizationCommand()
	first, err := service.Begin(context.Background(), command)
	if err != nil || first.AuthorizationURL == "" {
		t.Fatalf("stored-secret recovery=%+v err=%v", first, err)
	}
	command.RedirectURI = "https://app.infiniteocean.net/api/v1/integrations/google/other-callback"
	if _, err := service.Begin(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed replay error=%v", err)
	}
}

func TestCallbackSealsCredentialCompletesAndReplaysWithoutSecondExchange(t *testing.T) {
	repository := newAuthorizationRepository(t)
	secretStore := newAuthorizationSecretStore()
	provider := &authorizationProvider{}
	service := newAuthorizationService(t, repository, secretStore, provider)
	begin, err := service.Begin(context.Background(), authorizationCommand())
	if err != nil {
		t.Fatal(err)
	}
	state := append([]byte(nil), secretStore.value.State...)
	code := []byte("4/0code-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn")
	result, err := service.Callback(context.Background(), CallbackCommand{AccountID: authorizationAccount, State: state, Code: code})
	if err != nil || result.Session.Status != domain.AuthorizationCompleted || result.Session.CredentialGeneration != 1 ||
		secretStore.credential == nil || string(secretStore.credential.Material) != `{"refresh_token":"refresh-token"}` || !secretStore.authorizationGone {
		t.Fatalf("callback=%+v credential=%+v err=%v", result, secretStore.credential, err)
	}
	exchangeCalls := provider.calls
	replayed, err := service.Callback(context.Background(), CallbackCommand{AccountID: authorizationAccount, State: state, Code: code})
	if err != nil || replayed.Session != result.Session || provider.calls != exchangeCalls {
		t.Fatalf("callback replay=%+v err=%v provider_calls=%d begin=%+v", replayed, err, provider.calls, begin)
	}
}

func TestCallbackRotatesAndFencesPreviousGeneration(t *testing.T) {
	repository := newAuthorizationRepository(t)
	previousID := ids.IntegrationCredentialID("ba000000-0000-4000-8000-000000000010")
	previous, err := domain.NewCredentialBinding(domain.CredentialInput{ID: previousID, AccountID: authorizationAccount,
		ConnectionID: authorizationConnection, Generation: 1, Provider: domain.GoogleOAuthProvider,
		ReferenceSHA256: sha256.Sum256([]byte("previous-reference")), CreatedBy: domain.Actor{UserID: authorizationUser}, CreatedAt: authorizationNow.Add(-30 * time.Minute)}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	active, err := repository.authority.Connection.Activate(repository.authority.Connection.Version, previous, domain.Actor{UserID: authorizationUser}, accounts.RoleOwner, authorizationNow.Add(-20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repository.authority.Connection = active
	secretStore := newAuthorizationSecretStore()
	provider := &authorizationProvider{}
	service := newAuthorizationService(t, repository, secretStore, provider)
	if _, err := service.Begin(context.Background(), authorizationCommand()); err != nil {
		t.Fatal(err)
	}
	result, err := service.Callback(context.Background(), CallbackCommand{AccountID: authorizationAccount, State: append([]byte(nil), secretStore.value.State...),
		Code: []byte("4/0rotation-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijk")})
	if err != nil || result.Session.CredentialGeneration != 2 || secretStore.fencedID != previousID || secretStore.fencedGeneration != 1 ||
		repository.workflow.State != WorkflowCompleted {
		t.Fatalf("rotation=%+v workflow=%+v fenced=%s/%d err=%v", result, repository.workflow, secretStore.fencedID, secretStore.fencedGeneration, err)
	}
}

func TestCallbackRecoversAmbiguousCredentialWriteAndFailsStaleUnknownExchange(t *testing.T) {
	repository := newAuthorizationRepository(t)
	secretStore := newAuthorizationSecretStore()
	secretStore.failCredentialPutAfterStore = true
	service := newAuthorizationService(t, repository, secretStore, &authorizationProvider{})
	if _, err := service.Begin(context.Background(), authorizationCommand()); err != nil {
		t.Fatal(err)
	}
	state := append([]byte(nil), secretStore.value.State...)
	code := []byte("4/0ambiguous-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijk")
	if result, err := service.Callback(context.Background(), CallbackCommand{AccountID: authorizationAccount, State: state, Code: code}); err != nil || result.Session.Status != domain.AuthorizationCompleted {
		t.Fatalf("ambiguous credential write=%+v err=%v", result, err)
	}

	repository = newAuthorizationRepository(t)
	secretStore = newAuthorizationSecretStore()
	provider := &authorizationProvider{}
	clock := &authorizationClock{at: authorizationNow}
	service, err := NewService(authorizationAuthorizer{}, repository, secretStore, provider,
		&authorizationSecrets{values: [][]byte{[]byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"), []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")}}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Begin(context.Background(), authorizationCommand()); err != nil {
		t.Fatal(err)
	}
	state = append([]byte(nil), secretStore.value.State...)
	code = []byte("4/0unknown-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn")
	progress, err := repository.ClaimCallback(context.Background(), authorizationAccount, sha256.Sum256(state), sha256.Sum256(code), authorizationNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.StartProviderExchange(context.Background(), authorizationAccount, progress.Session.ID, authorizationNow); err != nil {
		t.Fatal(err)
	}
	clock.at = authorizationNow.Add(ProviderExchangeLease + time.Second)
	result, err := service.Callback(context.Background(), CallbackCommand{AccountID: authorizationAccount, State: state, Code: code})
	if !errors.Is(err, ErrProviderUnavailable) || result.Session.Status != domain.AuthorizationFailed || result.Session.ErrorCode != "provider_exchange_outcome_unknown" || provider.calls != 1 {
		t.Fatalf("unknown exchange=%+v err=%v provider_calls=%d", result, err, provider.calls)
	}
}

func TestCallbackPersistsProviderScopeFailure(t *testing.T) {
	repository := newAuthorizationRepository(t)
	secretStore := newAuthorizationSecretStore()
	provider := &authorizationProvider{exchangeErr: ErrScopeMismatch}
	service := newAuthorizationService(t, repository, secretStore, provider)
	if _, err := service.Begin(context.Background(), authorizationCommand()); err != nil {
		t.Fatal(err)
	}
	result, err := service.Callback(context.Background(), CallbackCommand{AccountID: authorizationAccount, State: append([]byte(nil), secretStore.value.State...),
		Code: []byte("4/0scope-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnop")})
	if !errors.Is(err, ErrScopeMismatch) || result.Session.Status != domain.AuthorizationFailed || result.Session.ErrorCode != "provider_scope_mismatch" || !secretStore.authorizationGone {
		t.Fatalf("scope failure=%+v err=%v", result, err)
	}
}

func authorizationCommand() BeginCommand {
	return BeginCommand{Actor: access.Actor{UserID: authorizationUser}, Session: sessions.Session{ID: ids.SessionID("b6000000-0000-4000-8000-000000000006"),
		UserID: authorizationUser, ReauthenticatedAt: authorizationNow.Add(-time.Minute), ReauthenticationMethod: sessions.AuthenticationMethodPasskey},
		AccountID: authorizationAccount, RequestID: authorizationRequest, ConnectionID: authorizationConnection, RedirectURI: authorizationRedirect}
}

func newAuthorizationService(t *testing.T, repository *authorizationRepository, secrets integrationcredentials.Store, provider Provider) *Service {
	t.Helper()
	service, err := NewService(authorizationAuthorizer{}, repository, secrets, provider,
		&authorizationSecrets{values: [][]byte{[]byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"), []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")}}, &authorizationClock{at: authorizationNow})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type authorizationAuthorizer struct{}

func (authorizationAuthorizer) Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error) {
	return access.AccountContext{AccountID: authorizationAccount, Role: accounts.RoleAdministrator}, nil
}

type authorizationClock struct{ at time.Time }

func (clock *authorizationClock) Now() time.Time { return clock.at }

type authorizationSecrets struct {
	values [][]byte
	index  int
}

func (generator *authorizationSecrets) New() ([]byte, error) {
	if generator.index >= len(generator.values) {
		return nil, errors.New("unexpected secret generation")
	}
	value := append([]byte(nil), generator.values[generator.index]...)
	generator.index++
	return value, nil
}

type authorizationProvider struct {
	calls       int
	exchangeErr error
}

func (provider *authorizationProvider) AuthorizationURL(state, challenge []byte, redirect string) (string, error) {
	provider.calls++
	return "https://provider.invalid/authorize?state=" + string(state) + "&challenge=" + string(challenge) + "&redirect=" + redirect, nil
}
func (provider *authorizationProvider) Exchange(context.Context, ExchangeRequest) (RefreshCredential, error) {
	provider.calls++
	if provider.exchangeErr != nil {
		return RefreshCredential{}, provider.exchangeErr
	}
	return RefreshCredential{RefreshToken: []byte("refresh-token")}, nil
}
func (*authorizationProvider) Revoke(context.Context, []byte) error { return errors.New("unused") }

type authorizationRepository struct {
	authority   Authority
	session     *domain.AuthorizationSession
	workflow    *CredentialWorkflow
	createCalls int
}

func newAuthorizationRepository(t *testing.T) *authorizationRepository {
	t.Helper()
	connection, revision, err := domain.NewConnection(domain.ConnectionInput{ID: authorizationConnection, RevisionID: authorizationRevision,
		AccountID: authorizationAccount, Name: "Drive", Kind: domain.ConnectorGoogleDrive, Capabilities: []domain.Capability{domain.CapabilityDriveRead},
		Scope: domain.ConnectionScope{DriveFolderIDs: []string{"folder-a"}}, CreatedBy: domain.Actor{UserID: authorizationUser}, CreatedAt: authorizationNow.Add(-time.Hour)}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	return &authorizationRepository{authority: Authority{Connection: connection, Revision: revision}}
}

func (repository *authorizationRepository) Authority(context.Context, ids.AccountID, ids.IntegrationConnectionID) (Authority, error) {
	return repository.authority, nil
}
func (repository *authorizationRepository) Create(_ context.Context, session domain.AuthorizationSession, _ accounts.MembershipRole, _ Event) (domain.AuthorizationSession, bool, error) {
	repository.createCalls++
	if repository.session != nil {
		return *repository.session, false, ErrConflict
	}
	repository.session = &session
	return session, true, nil
}
func (repository *authorizationRepository) Get(_ context.Context, _ ids.AccountID, _ ids.IntegrationAuthorizationSessionID) (domain.AuthorizationSession, error) {
	if repository.session == nil {
		return domain.AuthorizationSession{}, ErrNotFound
	}
	return *repository.session, nil
}

func (repository *authorizationRepository) ClaimCallback(_ context.Context, accountID ids.AccountID, stateDigest, codeDigest [sha256.Size]byte, at time.Time) (CallbackProgress, error) {
	if repository.session == nil || repository.session.AccountID != accountID || repository.session.StateSHA256 != stateDigest {
		return CallbackProgress{}, ErrNotFound
	}
	if repository.workflow != nil {
		if repository.workflow.CodeSHA256 != codeDigest {
			return CallbackProgress{}, ErrConflict
		}
		return CallbackProgress{Session: *repository.session, Workflow: *repository.workflow}, nil
	}
	next, err := repository.session.BeginExchange(stateDigest, repository.session.Version, at)
	if err != nil {
		return CallbackProgress{}, err
	}
	targetID, _ := TargetCredentialID(next.ID)
	workflow := CredentialWorkflow{AccountID: accountID, SessionID: next.ID, State: WorkflowClaimed, ConnectionID: next.ConnectionID,
		ConnectionVersion: repository.authority.Connection.Version, TargetCredentialID: targetID,
		TargetGeneration: repository.authority.Connection.CredentialGeneration + 1, PreviousCredentialID: repository.authority.Connection.CredentialID,
		PreviousGeneration: repository.authority.Connection.CredentialGeneration, CodeSHA256: codeDigest, CreatedAt: at, UpdatedAt: at}
	workflow.ReferenceSHA256 = sha256.Sum256(CredentialReference(workflow))
	repository.session, repository.workflow = &next, &workflow
	return CallbackProgress{Session: next, Workflow: workflow}, nil
}

func (repository *authorizationRepository) StartProviderExchange(_ context.Context, _ ids.AccountID, _ ids.IntegrationAuthorizationSessionID, at time.Time) (CallbackProgress, bool, error) {
	if repository.workflow.State != WorkflowClaimed {
		return CallbackProgress{Session: *repository.session, Workflow: *repository.workflow}, false, nil
	}
	repository.workflow.State, repository.workflow.ExchangeStartedAt, repository.workflow.UpdatedAt = WorkflowProviderExchanging, timePointerForTest(at), at
	return CallbackProgress{Session: *repository.session, Workflow: *repository.workflow}, true, nil
}

func (repository *authorizationRepository) MarkCredentialStored(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID, time.Time) (CallbackProgress, error) {
	if repository.workflow.State == WorkflowProviderExchanging {
		repository.workflow.State = WorkflowCredentialStored
	}
	return CallbackProgress{Session: *repository.session, Workflow: *repository.workflow}, nil
}

func (repository *authorizationRepository) MarkPreviousFenced(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID, time.Time) (CallbackProgress, error) {
	if repository.workflow.State == WorkflowCredentialStored {
		repository.workflow.State = WorkflowPreviousFenced
	}
	return CallbackProgress{Session: *repository.session, Workflow: *repository.workflow}, nil
}

func (repository *authorizationRepository) CompleteCallback(_ context.Context, _ ids.AccountID, _ ids.IntegrationAuthorizationSessionID, at time.Time) (CallbackProgress, error) {
	if repository.workflow.State == WorkflowCompleted {
		return CallbackProgress{Session: *repository.session, Workflow: *repository.workflow}, nil
	}
	next, err := repository.session.Complete(repository.workflow.TargetCredentialID, repository.workflow.TargetGeneration, repository.session.Version, at)
	if err != nil {
		return CallbackProgress{}, err
	}
	repository.session, repository.workflow.State = &next, WorkflowCompleted
	return CallbackProgress{Session: next, Workflow: *repository.workflow}, nil
}

func (repository *authorizationRepository) FailCallback(_ context.Context, _ ids.AccountID, _ ids.IntegrationAuthorizationSessionID, code string, at time.Time) (CallbackProgress, error) {
	next, err := repository.session.Fail(code, repository.session.Version, at)
	if err != nil {
		return CallbackProgress{}, err
	}
	repository.session, repository.workflow.State, repository.workflow.FailureCode = &next, WorkflowFailed, code
	return CallbackProgress{Session: next, Workflow: *repository.workflow}, nil
}

func timePointerForTest(value time.Time) *time.Time { return &value }

type authorizationSecretStore struct {
	value                       integrationcredentials.AuthorizationSecret
	credential                  *integrationcredentials.CredentialSecret
	credentialFenced            bool
	fencedID                    ids.IntegrationCredentialID
	fencedGeneration            uint64
	authorizationGone           bool
	failPutAfterStore           bool
	failCredentialPutAfterStore bool
}

func newAuthorizationSecretStore() *authorizationSecretStore { return &authorizationSecretStore{} }

func (store *authorizationSecretStore) PutAuthorization(_ context.Context, value integrationcredentials.AuthorizationSecret) error {
	if len(store.value.State) == 0 {
		store.value = value
		store.value.State = append([]byte(nil), value.State...)
		store.value.Verifier = append([]byte(nil), value.Verifier...)
	}
	if store.failPutAfterStore {
		return errors.New("ambiguous write")
	}
	return nil
}
func (store *authorizationSecretStore) Authorization(_ context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, now time.Time) (integrationcredentials.AuthorizationMaterial, error) {
	if store.authorizationGone || store.value.AccountID != accountID || store.value.SessionID != sessionID || !store.value.ExpiresAt.After(now) {
		return integrationcredentials.AuthorizationMaterial{}, errors.New("not found")
	}
	return integrationcredentials.AuthorizationMaterial{State: append([]byte(nil), store.value.State...), Verifier: append([]byte(nil), store.value.Verifier...), ExpiresAt: store.value.ExpiresAt}, nil
}

func (store *authorizationSecretStore) DeleteAuthorization(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID) error {
	store.authorizationGone = true
	return nil
}
func (store *authorizationSecretStore) PutCredential(_ context.Context, value integrationcredentials.CredentialSecret) error {
	copyValue := value
	copyValue.Reference, copyValue.Material = append([]byte(nil), value.Reference...), append([]byte(nil), value.Material...)
	store.credential = &copyValue
	if store.failCredentialPutAfterStore {
		return errors.New("ambiguous credential write")
	}
	return nil
}
func (store *authorizationSecretStore) CredentialExists(_ context.Context, accountID ids.AccountID, credentialID ids.IntegrationCredentialID, generation uint64, provider string, referenceDigest [32]byte) (bool, error) {
	return store.credential != nil && !store.credentialFenced && store.credential.AccountID == accountID && store.credential.CredentialID == credentialID &&
		store.credential.Generation == generation && store.credential.Provider == provider && sha256.Sum256(store.credential.Reference) == referenceDigest, nil
}
func (store *authorizationSecretStore) FenceCredential(_ context.Context, _ ids.AccountID, credentialID ids.IntegrationCredentialID, generation uint64, _ integrationcredentials.CredentialEndState) error {
	store.credentialFenced = true
	store.fencedID, store.fencedGeneration = credentialID, generation
	return nil
}
func (*authorizationSecretStore) PurgeCredential(context.Context, ids.AccountID, ids.IntegrationCredentialID, uint64) error {
	return errors.New("unused")
}
