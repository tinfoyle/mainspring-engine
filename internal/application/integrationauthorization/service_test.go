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

func TestStatusPersistsExpiryAndReturnsSafeSummary(t *testing.T) {
	repository := newAuthorizationRepository(t)
	secretStore := newAuthorizationSecretStore()
	clock := &authorizationClock{at: authorizationNow}
	service, err := NewService(authorizationAuthorizer{}, repository, secretStore, &authorizationProvider{},
		&authorizationSecrets{values: [][]byte{[]byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"), []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")}}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Begin(context.Background(), authorizationCommand()); err != nil {
		t.Fatal(err)
	}
	clock.at = authorizationNow.Add(AuthorizationTTL)
	summary, err := service.Status(context.Background(), access.Actor{UserID: authorizationUser}, authorizationAccount,
		ids.IntegrationAuthorizationSessionID(authorizationRequest))
	if err != nil || summary.Status != domain.AuthorizationExpired || summary.ErrorCode != "authorization_expired" ||
		summary.ID != ids.IntegrationAuthorizationSessionID(authorizationRequest) || !secretStore.authorizationGone {
		t.Fatalf("summary=%+v secret_deleted=%t err=%v", summary, secretStore.authorizationGone, err)
	}
	if repository.session == nil || repository.session.Status != domain.AuthorizationExpired ||
		repository.session.StateSHA256 == [sha256.Size]byte{} || repository.session.PKCEChallengeSHA256 == [sha256.Size]byte{} {
		t.Fatalf("durable expiry or internal digest bindings were lost: %+v", repository.session)
	}
}

func TestCallbackPersistsProviderDenialAndReplays(t *testing.T) {
	repository := newAuthorizationRepository(t)
	secretStore := newAuthorizationSecretStore()
	provider := &authorizationProvider{}
	service := newAuthorizationService(t, repository, secretStore, provider)
	if _, err := service.Begin(context.Background(), authorizationCommand()); err != nil {
		t.Fatal(err)
	}
	state := append([]byte(nil), secretStore.value.State...)
	command := CallbackCommand{AccountID: authorizationAccount, State: state, ProviderError: "access_denied"}
	result, err := service.Callback(context.Background(), command)
	if !errors.Is(err, ErrProviderRejected) || result.Session.Status != domain.AuthorizationFailed ||
		result.Session.ErrorCode != "provider_access_denied" || !secretStore.authorizationGone {
		t.Fatalf("denial=%+v deleted=%t err=%v", result, secretStore.authorizationGone, err)
	}
	providerCalls := provider.calls
	replayed, err := service.Callback(context.Background(), command)
	if !errors.Is(err, ErrProviderRejected) || replayed.Session != result.Session || provider.calls != providerCalls {
		t.Fatalf("denial replay=%+v err=%v provider_calls=%d", replayed, err, provider.calls)
	}
}

func TestRevokeFencesCompletesPurgesAndReplaysWithoutProviderCall(t *testing.T) {
	repository := newAuthorizationRepository(t)
	credentialID := ids.IntegrationCredentialID("bc000000-0000-4000-8000-00000000000c")
	reference := []byte("spyglass-encrypted://test/revocation-reference")
	credential, err := domain.NewCredentialBinding(domain.CredentialInput{ID: credentialID, AccountID: authorizationAccount,
		ConnectionID: authorizationConnection, Generation: 1, Provider: domain.GoogleOAuthProvider, ReferenceSHA256: sha256.Sum256(reference),
		CreatedBy: domain.Actor{UserID: authorizationUser}, CreatedAt: authorizationNow.Add(-30 * time.Minute)}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	active, err := repository.authority.Connection.Activate(repository.authority.Connection.Version, credential,
		domain.Actor{UserID: authorizationUser}, accounts.RoleOwner, authorizationNow.Add(-20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repository.authority.Connection = active
	secretStore := newAuthorizationSecretStore()
	secretStore.credential = &integrationcredentials.CredentialSecret{AccountID: authorizationAccount, CredentialID: credentialID, Generation: 1,
		Provider: domain.GoogleOAuthProvider, Reference: reference, Material: []byte(`{"refresh_token":"revocation-refresh"}`)}
	provider := &authorizationProvider{}
	service := newAuthorizationService(t, repository, secretStore, provider)
	command := RevokeCommand{Actor: access.Actor{UserID: authorizationUser}, Session: authorizationCommand().Session, AccountID: authorizationAccount,
		RequestID: "bd000000-0000-4000-8000-00000000000d", ConnectionID: authorizationConnection}
	result, err := service.Revoke(context.Background(), command)
	if err != nil || result.State != RevocationCompleted || provider.revokeCalls != 1 || provider.revokedToken != "revocation-refresh" ||
		secretStore.fencedID != credentialID || !secretStore.purged {
		t.Fatalf("revocation=%+v err=%v provider=%d/%q fenced=%s purged=%t", result, err, provider.revokeCalls, provider.revokedToken, secretStore.fencedID, secretStore.purged)
	}
	replayed, err := service.Revoke(context.Background(), command)
	if err != nil || replayed.State != RevocationCompleted || provider.revokeCalls != 1 {
		t.Fatalf("revocation replay=%+v err=%v provider_calls=%d", replayed, err, provider.revokeCalls)
	}
}

func TestRevokeRetriesProviderBeforeFencing(t *testing.T) {
	repository := newAuthorizationRepository(t)
	credentialID := ids.IntegrationCredentialID("be000000-0000-4000-8000-00000000000e")
	reference := []byte("spyglass-encrypted://test/revocation-reference")
	credential, err := domain.NewCredentialBinding(domain.CredentialInput{ID: credentialID, AccountID: authorizationAccount,
		ConnectionID: authorizationConnection, Generation: 1, Provider: domain.GoogleOAuthProvider, ReferenceSHA256: sha256.Sum256(reference),
		CreatedBy: domain.Actor{UserID: authorizationUser}, CreatedAt: authorizationNow.Add(-30 * time.Minute)}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	active, err := repository.authority.Connection.Activate(repository.authority.Connection.Version, credential,
		domain.Actor{UserID: authorizationUser}, accounts.RoleOwner, authorizationNow.Add(-20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repository.authority.Connection = active
	secretStore := newAuthorizationSecretStore()
	secretStore.credential = &integrationcredentials.CredentialSecret{AccountID: authorizationAccount, CredentialID: credentialID, Generation: 1,
		Provider: domain.GoogleOAuthProvider, Reference: reference, Material: []byte(`{"refresh_token":"revocation-refresh"}`)}
	provider := &authorizationProvider{revokeErr: ErrProviderUnavailable}
	service := newAuthorizationService(t, repository, secretStore, provider)
	command := RevokeCommand{Actor: access.Actor{UserID: authorizationUser}, Session: authorizationCommand().Session, AccountID: authorizationAccount,
		RequestID: "bf000000-0000-4000-8000-00000000000f", ConnectionID: authorizationConnection}
	first, err := service.Revoke(context.Background(), command)
	if !errors.Is(err, ErrProviderUnavailable) || first.State != RevocationProviderRevoking || secretStore.credentialFenced || secretStore.purged {
		t.Fatalf("first=%+v err=%v fenced=%t purged=%t", first, err, secretStore.credentialFenced, secretStore.purged)
	}
	provider.revokeErr = nil
	second, err := service.Revoke(context.Background(), command)
	if err != nil || second.State != RevocationCompleted || provider.revokeCalls != 2 || !secretStore.credentialFenced || !secretStore.purged {
		t.Fatalf("retry=%+v err=%v provider_calls=%d fenced=%t purged=%t", second, err, provider.revokeCalls,
			secretStore.credentialFenced, secretStore.purged)
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
	calls        int
	exchangeErr  error
	revokeCalls  int
	revokeErr    error
	revokedToken string
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
func (provider *authorizationProvider) Revoke(_ context.Context, token []byte) error {
	provider.revokeCalls++
	provider.revokedToken = string(token)
	return provider.revokeErr
}

type authorizationRepository struct {
	authority   Authority
	session     *domain.AuthorizationSession
	workflow    *CredentialWorkflow
	revocation  *RevocationWorkflow
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

func (repository *authorizationRepository) ExpireAuthorization(_ context.Context, _ ids.AccountID,
	_ ids.IntegrationAuthorizationSessionID, at time.Time) (domain.AuthorizationSession, error) {
	if repository.session == nil {
		return domain.AuthorizationSession{}, ErrNotFound
	}
	if repository.session.Status != domain.AuthorizationPending {
		return *repository.session, nil
	}
	next, err := repository.session.Expire(repository.session.Version, at)
	if err != nil {
		return domain.AuthorizationSession{}, err
	}
	repository.session = &next
	return next, nil
}

func (repository *authorizationRepository) RejectCallback(_ context.Context, accountID ids.AccountID, stateDigest [sha256.Size]byte,
	code string, at time.Time) (domain.AuthorizationSession, error) {
	if repository.session == nil || repository.session.AccountID != accountID || repository.session.StateSHA256 != stateDigest {
		return domain.AuthorizationSession{}, ErrNotFound
	}
	if repository.session.Status == domain.AuthorizationFailed {
		if repository.session.ErrorCode != code {
			return domain.AuthorizationSession{}, ErrConflict
		}
		return *repository.session, nil
	}
	if repository.session.Status == domain.AuthorizationExpired {
		return *repository.session, nil
	}
	claimed, err := repository.session.BeginExchange(stateDigest, repository.session.Version, at)
	if err != nil {
		return domain.AuthorizationSession{}, err
	}
	if claimed.Status == domain.AuthorizationExpired {
		repository.session = &claimed
		return claimed, nil
	}
	failed, err := claimed.Fail(code, claimed.Version, at)
	if err != nil {
		return domain.AuthorizationSession{}, err
	}
	repository.session = &failed
	return failed, nil
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

func (repository *authorizationRepository) PrepareRevocation(_ context.Context, accountID ids.AccountID, workflowID string,
	connectionID ids.IntegrationConnectionID, actor domain.Actor, at time.Time) (RevocationWorkflow, error) {
	if repository.revocation != nil {
		if repository.revocation.ID != workflowID || repository.revocation.ConnectionID != connectionID || repository.revocation.CreatedBy != actor {
			return RevocationWorkflow{}, ErrConflict
		}
		return *repository.revocation, nil
	}
	connection := repository.authority.Connection
	workflow := RevocationWorkflow{AccountID: accountID, ID: workflowID, ConnectionID: connectionID, ConnectionVersion: connection.Version,
		CredentialID: connection.CredentialID, CredentialGeneration: connection.CredentialGeneration, Provider: domain.GoogleOAuthProvider,
		ReferenceSHA256: sha256.Sum256([]byte("spyglass-encrypted://test/revocation-reference")), State: RevocationPrepared,
		CreatedBy: actor, CreatedAt: at, UpdatedAt: at}
	repository.revocation = &workflow
	return workflow, nil
}

func (repository *authorizationRepository) StartProviderRevocation(_ context.Context, _ ids.AccountID, _ string, at time.Time) (RevocationWorkflow, bool, error) {
	if repository.revocation.State != RevocationPrepared {
		return *repository.revocation, false, nil
	}
	repository.revocation.State, repository.revocation.ProviderStartedAt, repository.revocation.UpdatedAt = RevocationProviderRevoking, timePointerForTest(at), at
	return *repository.revocation, true, nil
}

func (repository *authorizationRepository) MarkProviderRevoked(context.Context, ids.AccountID, string, time.Time) (RevocationWorkflow, error) {
	if repository.revocation.State == RevocationProviderRevoking {
		repository.revocation.State = RevocationProviderRevoked
	}
	return *repository.revocation, nil
}

func (repository *authorizationRepository) MarkRevocationVaultFenced(context.Context, ids.AccountID, string, time.Time) (RevocationWorkflow, error) {
	if repository.revocation.State == RevocationProviderRevoked {
		repository.revocation.State = RevocationVaultFenced
	}
	return *repository.revocation, nil
}

func (repository *authorizationRepository) CompleteRevocation(context.Context, ids.AccountID, string, time.Time) (RevocationWorkflow, error) {
	if repository.revocation.State == RevocationVaultFenced {
		repository.revocation.State = RevocationCompleted
	}
	return *repository.revocation, nil
}

func timePointerForTest(value time.Time) *time.Time { return &value }

type authorizationSecretStore struct {
	value                       integrationcredentials.AuthorizationSecret
	credential                  *integrationcredentials.CredentialSecret
	credentialFenced            bool
	fencedID                    ids.IntegrationCredentialID
	fencedGeneration            uint64
	authorizationGone           bool
	purged                      bool
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
func (store *authorizationSecretStore) CredentialMaterial(_ context.Context, accountID ids.AccountID, credentialID ids.IntegrationCredentialID,
	generation uint64, provider string, reference [32]byte) (integrationcredentials.Lease, error) {
	if store.credential == nil || store.credential.AccountID != accountID || store.credential.CredentialID != credentialID ||
		store.credential.Generation != generation || store.credential.Provider != provider || sha256.Sum256(store.credential.Reference) != reference || store.credentialFenced {
		return nil, errors.New("unavailable")
	}
	return &authorizationLease{material: append([]byte(nil), store.credential.Material...)}, nil
}
func (store *authorizationSecretStore) FenceCredential(_ context.Context, _ ids.AccountID, credentialID ids.IntegrationCredentialID, generation uint64, _ integrationcredentials.CredentialEndState) error {
	store.credentialFenced = true
	store.fencedID, store.fencedGeneration = credentialID, generation
	return nil
}
func (store *authorizationSecretStore) PurgeCredential(context.Context, ids.AccountID, ids.IntegrationCredentialID, uint64) error {
	store.purged = true
	return nil
}

type authorizationLease struct{ material []byte }

func (lease *authorizationLease) Material() []byte { return lease.material }
func (lease *authorizationLease) Close() error {
	wipe(lease.material)
	lease.material = nil
	return nil
}
