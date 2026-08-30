package oidcauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/oidcauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestLoginRequiresExplicitConnectedSubjectAndIssuesOIDCSession(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := ids.UserID("11111111-1111-4111-8111-111111111111")
	repository := &repositoryStub{user: identity.User{ID: userID, State: identity.UserActive, SecurityVersion: 3}}
	sessionRepository := &sessionRepositoryStub{}
	sessionService, err := sessions.NewService(sessionRepository, staticIDs{}, staticClock{now}, time.Hour, time.Hour, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service, err := oidcauth.New(repository, sessionService, staticClock{now})
	if err != nil {
		t.Fatal(err)
	}
	assertion := oidcauth.Assertion{Issuer: "https://accounts.google.com", Subject: "google-subject", Email: "person@example.com", EmailVerified: true}
	if _, err := service.Login(context.Background(), assertion, "Test browser"); !errors.Is(err, oidcauth.ErrIdentityNotFound) {
		t.Fatalf("unconnected subject was accepted: %v", err)
	}
	repository.identifier = "https://accounts.google.com\x1fgoogle-subject"
	issued, err := service.Login(context.Background(), assertion, "Test browser")
	if err != nil {
		t.Fatal(err)
	}
	if issued.Session.UserID != userID || issued.Session.AuthenticationMethod != sessions.AuthenticationMethodOIDC || issued.Session.ReauthenticationMethod != sessions.AuthenticationMethodOIDC {
		t.Fatalf("unexpected OIDC session: %+v", issued.Session)
	}
	if issued.Session.AuthenticationMethod.Assurance() != sessions.AssuranceSingleFactor {
		t.Fatal("OIDC login was incorrectly treated as strong authentication")
	}
}

func TestConnectAndDisconnectRequireRecentPasskey(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := ids.UserID("11111111-1111-4111-8111-111111111111")
	repository := &repositoryStub{}
	sessionService, _ := sessions.NewService(&sessionRepositoryStub{}, staticIDs{}, staticClock{now}, time.Hour, time.Hour, time.Minute)
	service, _ := oidcauth.New(repository, sessionService, staticClock{now})
	assertion := oidcauth.Assertion{Issuer: "https://accounts.google.com", Subject: "google-subject", Email: "person@example.com", EmailVerified: true}
	password := sessions.Session{UserID: userID, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPassword}
	if err := service.Connect(context.Background(), password, assertion); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("password-only link was accepted: %v", err)
	}
	passkey := password
	passkey.ReauthenticationMethod = sessions.AuthenticationMethodPasskey
	if err := service.Connect(context.Background(), passkey, assertion); err != nil {
		t.Fatal(err)
	}
	if repository.identifier != "https://accounts.google.com\x1fgoogle-subject" {
		t.Fatalf("wrong stored identifier: %q", repository.identifier)
	}
	if err := service.Disconnect(context.Background(), password, "https://accounts.google.com"); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("password-only disconnect was accepted: %v", err)
	}
	if err := service.Disconnect(context.Background(), passkey, "https://accounts.google.com"); err != nil {
		t.Fatal(err)
	}
}

func TestAssertionRequiresVerifiedEmail(t *testing.T) {
	service, _ := oidcauth.New(&repositoryStub{}, mustSessions(t), staticClock{time.Now()})
	_, err := service.Login(context.Background(), oidcauth.Assertion{Issuer: "https://accounts.google.com", Subject: "subject", Email: "person@example.com"}, "browser")
	if !errors.Is(err, oidcauth.ErrInvalidAssertion) {
		t.Fatalf("unverified assertion was accepted: %v", err)
	}
}

type repositoryStub struct {
	identifier string
	user       identity.User
}

func (r *repositoryStub) UserForOIDC(_ context.Context, identifier string) (identity.User, error) {
	if identifier != r.identifier || r.identifier == "" {
		return identity.User{}, oidcauth.ErrIdentityNotFound
	}
	return r.user, nil
}
func (r *repositoryStub) Connected(_ context.Context, _ ids.UserID, prefix string) (bool, error) {
	return len(r.identifier) >= len(prefix) && r.identifier[:len(prefix)] == prefix, nil
}
func (r *repositoryStub) Connect(_ context.Context, _ ids.UserID, identifier string, _ time.Time) error {
	if r.identifier != "" {
		return oidcauth.ErrIdentityConflict
	}
	r.identifier = identifier
	return nil
}
func (r *repositoryStub) Disconnect(_ context.Context, _ ids.UserID, prefix string, _ time.Time) error {
	if len(r.identifier) < len(prefix) || r.identifier[:len(prefix)] != prefix {
		return oidcauth.ErrLastLoginMethod
	}
	r.identifier = ""
	return nil
}

type sessionRepositoryStub struct{ created sessions.Session }

func (r *sessionRepositoryStub) Create(_ context.Context, value sessions.Session) error {
	r.created = value
	return nil
}
func (*sessionRepositoryStub) Use(context.Context, [32]byte, time.Time, time.Duration) (sessions.Session, error) {
	return sessions.Session{}, sessions.ErrInvalidSession
}
func (*sessionRepositoryStub) Rotate(context.Context, ids.SessionID, [32]byte, [32]byte, time.Time) (bool, error) {
	return false, nil
}
func (*sessionRepositoryStub) Revoke(context.Context, ids.SessionID, time.Time) error { return nil }
func (*sessionRepositoryStub) RevokeAll(context.Context, ids.UserID, time.Time) error { return nil }
func (*sessionRepositoryStub) RevokeOwned(context.Context, ids.UserID, ids.SessionID, time.Time) (bool, error) {
	return false, nil
}
func (*sessionRepositoryStub) Active(context.Context, ids.UserID, time.Time, time.Duration) ([]sessions.Session, error) {
	return nil, nil
}
func (*sessionRepositoryStub) MarkReauthenticated(context.Context, ids.UserID, ids.SessionID, sessions.AuthenticationMethod, time.Time) (bool, error) {
	return false, nil
}
func (*sessionRepositoryStub) SecurityEvents(context.Context, ids.UserID, int) ([]sessions.SecurityEvent, error) {
	return nil, nil
}

type staticClock struct{ now time.Time }

func (c staticClock) Now() time.Time { return c.now }

type staticIDs struct{}

func (staticIDs) New() string { return "22222222-2222-4222-8222-222222222222" }

func mustSessions(t *testing.T) *sessions.Service {
	t.Helper()
	service, err := sessions.NewService(&sessionRepositoryStub{}, staticIDs{}, staticClock{time.Now()}, time.Hour, time.Hour, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
