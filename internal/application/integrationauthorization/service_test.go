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

func authorizationCommand() BeginCommand {
	return BeginCommand{Actor: access.Actor{UserID: authorizationUser}, Session: sessions.Session{ID: ids.SessionID("b6000000-0000-4000-8000-000000000006"),
		UserID: authorizationUser, ReauthenticatedAt: authorizationNow.Add(-time.Minute), ReauthenticationMethod: sessions.AuthenticationMethodPasskey},
		AccountID: authorizationAccount, RequestID: authorizationRequest, ConnectionID: authorizationConnection, RedirectURI: authorizationRedirect}
}

func newAuthorizationService(t *testing.T, repository *authorizationRepository, secrets integrationcredentials.Store, provider Provider) *Service {
	t.Helper()
	service, err := NewService(authorizationAuthorizer{}, repository, secrets, provider,
		&authorizationSecrets{values: [][]byte{[]byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"), []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")}}, authorizationClock{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type authorizationAuthorizer struct{}

func (authorizationAuthorizer) Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error) {
	return access.AccountContext{AccountID: authorizationAccount, Role: accounts.RoleAdministrator}, nil
}

type authorizationClock struct{}

func (authorizationClock) Now() time.Time { return authorizationNow }

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

type authorizationProvider struct{ calls int }

func (provider *authorizationProvider) AuthorizationURL(state, challenge []byte, redirect string) (string, error) {
	provider.calls++
	return "https://provider.invalid/authorize?state=" + string(state) + "&challenge=" + string(challenge) + "&redirect=" + redirect, nil
}
func (*authorizationProvider) Exchange(context.Context, ExchangeRequest) (RefreshCredential, error) {
	return RefreshCredential{}, errors.New("unused")
}
func (*authorizationProvider) Revoke(context.Context, []byte) error { return errors.New("unused") }

type authorizationRepository struct {
	authority   Authority
	session     *domain.AuthorizationSession
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

type authorizationSecretStore struct {
	value             integrationcredentials.AuthorizationSecret
	failPutAfterStore bool
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
	if store.value.AccountID != accountID || store.value.SessionID != sessionID || !store.value.ExpiresAt.After(now) {
		return integrationcredentials.AuthorizationMaterial{}, errors.New("not found")
	}
	return integrationcredentials.AuthorizationMaterial{State: append([]byte(nil), store.value.State...), Verifier: append([]byte(nil), store.value.Verifier...), ExpiresAt: store.value.ExpiresAt}, nil
}
func (*authorizationSecretStore) DeleteAuthorization(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID) error {
	return nil
}
func (*authorizationSecretStore) PutCredential(context.Context, integrationcredentials.CredentialSecret) error {
	return errors.New("unused")
}
func (*authorizationSecretStore) FenceCredential(context.Context, ids.AccountID, ids.IntegrationCredentialID, uint64, integrationcredentials.CredentialEndState) error {
	return errors.New("unused")
}
func (*authorizationSecretStore) PurgeCredential(context.Context, ids.AccountID, ids.IntegrationCredentialID, uint64) error {
	return errors.New("unused")
}
