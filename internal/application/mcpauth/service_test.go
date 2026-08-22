package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	oauthUser     = "10000000-0000-4000-8000-000000000001"
	oauthSession  = "20000000-0000-4000-8000-000000000002"
	oauthPending  = "30000000-0000-4000-8000-000000000003"
	oauthGrant    = "40000000-0000-4000-8000-000000000004"
	oauthClient   = "https://client.example/oauth/metadata.json"
	oauthRedirect = "http://127.0.0.1:8765/callback"
	oauthIssuer   = "https://app.infiniteocean.net"
	oauthResource = "https://mcp.infiniteocean.net"
)

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

type sequenceIDs struct {
	values []string
	index  int
}

func (g *sequenceIDs) New() string {
	value := g.values[g.index]
	g.index++
	return value
}

type sequenceSecrets struct {
	values []string
	index  int
}

func (g *sequenceSecrets) New() (string, [32]byte, error) {
	value := g.values[g.index]
	g.index++
	return value, sha256.Sum256([]byte(value)), nil
}

type repositoryStub struct {
	pending       PendingAuthorization
	decision      AuthorizationDecision
	codeHash      [32]byte
	exchange      CodeExchange
	refresh       RefreshExchange
	requirement   TokenRequirement
	revokedHash   [32]byte
	revokedClient string
	revokedGrant  string
	error         error
}

func (r *repositoryStub) CreateAuthorization(_ context.Context, pending PendingAuthorization) error {
	r.pending = pending
	return r.error
}

func (r *repositoryStub) DecideAuthorization(_ context.Context, decision AuthorizationDecision, codeHash [32]byte, grantID string, now, expires time.Time) (PendingAuthorization, error) {
	r.decision, r.codeHash = decision, codeHash
	if r.error != nil {
		return PendingAuthorization{}, r.error
	}
	if grantID != oauthGrant || !now.Before(expires) || decision.PendingID != r.pending.ID || decision.UserID != r.pending.UserID || decision.SessionID != r.pending.SessionID {
		return PendingAuthorization{}, ErrInvalid
	}
	return r.pending, nil
}

func (r *repositoryStub) ExchangeCode(_ context.Context, exchange CodeExchange) (IssuedAuthority, error) {
	r.exchange = exchange
	if r.error != nil {
		return IssuedAuthority{}, r.error
	}
	if exchange.CodeHash != r.codeHash || exchange.VerifierChallenge != r.pending.CodeChallenge || exchange.ClientID != r.pending.ClientID || exchange.RedirectURI != r.pending.RedirectURI || exchange.Resource != r.pending.Resource {
		return IssuedAuthority{}, ErrInvalid
	}
	return IssuedAuthority{UserID: r.pending.UserID, Resource: r.pending.Resource, Scope: r.pending.Scope}, nil
}

func (r *repositoryStub) RotateRefresh(_ context.Context, refresh RefreshExchange) (IssuedAuthority, error) {
	r.refresh = refresh
	if r.error != nil {
		return IssuedAuthority{}, r.error
	}
	return IssuedAuthority{UserID: r.pending.UserID, Resource: r.pending.Resource, Scope: r.pending.Scope}, nil
}

func (r *repositoryStub) AuthenticateAccess(_ context.Context, _ [32]byte, requirement TokenRequirement, _ time.Time) (access.Actor, error) {
	r.requirement = requirement
	if r.error != nil {
		return access.Actor{}, r.error
	}
	return access.Actor{UserID: oauthUser}, nil
}

func (r *repositoryStub) Revoke(_ context.Context, hash [32]byte, clientID string, _ time.Time) error {
	r.revokedHash, r.revokedClient = hash, clientID
	return r.error
}

func (r *repositoryStub) ListGrants(_ context.Context, userID ids.UserID, _ time.Time) ([]GrantSummary, error) {
	if r.error != nil {
		return nil, r.error
	}
	return []GrantSummary{{ID: oauthGrant, ClientID: oauthClient, ClientName: "Trusted Client"}}, nil
}

func (r *repositoryStub) RevokeGrant(_ context.Context, userID ids.UserID, grantID string, _ time.Time) (bool, error) {
	r.revokedGrant = grantID
	return r.error == nil && userID == oauthUser && grantID == oauthGrant, r.error
}

func TestAuthorizationCodeAndRotatingTokenFlow(t *testing.T) {
	now := time.Date(2026, 8, 22, 22, 0, 0, 0, time.UTC)
	verifier := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	challenge := pkceChallenge(verifier)
	code := secretByte(1)
	accessToken := secretByte(2)
	refreshToken := secretByte(3)
	rotatedAccess := secretByte(4)
	rotatedRefresh := secretByte(5)
	repository := &repositoryStub{}
	service, err := New(repository, &sequenceIDs{values: []string{oauthPending, oauthGrant}}, &sequenceSecrets{values: []string{code, accessToken, refreshToken, rotatedAccess, rotatedRefresh}}, testClock{now}, oauthIssuer, oauthResource)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := service.Begin(context.Background(), AuthorizationCommand{
		UserID: oauthUser, SessionID: oauthSession, Client: Client{ID: oauthClient, Name: "Trusted Client", RedirectURIs: []string{oauthRedirect}},
		RedirectURI: oauthRedirect, Resource: oauthResource, Scope: ScopeMCP, State: "client-state", CodeChallenge: challenge,
	})
	if err != nil || pending.ID != oauthPending || pending.ExpiresAt.Sub(now) != AuthorizationRequestTTL {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	decision, err := service.Decide(context.Background(), AuthorizationDecision{PendingID: oauthPending, UserID: oauthUser, SessionID: oauthSession, Approve: true})
	if err != nil || decision.Code != code || decision.RedirectURI != oauthRedirect || decision.Issuer != oauthIssuer || !decision.Approved {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if repository.codeHash != sha256.Sum256([]byte(code)) {
		t.Fatal("authorization code was not persisted as a hash")
	}
	tokens, err := service.ExchangeCode(context.Background(), code, oauthClient, oauthRedirect, oauthResource, verifier)
	if err != nil || tokens.AccessToken != accessToken || tokens.RefreshToken != refreshToken || tokens.ExpiresIn != 900 || tokens.Scope != ScopeMCP {
		t.Fatalf("tokens=%+v err=%v", tokens, err)
	}
	if repository.exchange.Access.Hash != sha256.Sum256([]byte(accessToken)) || repository.exchange.Refresh.Hash != sha256.Sum256([]byte(refreshToken)) || repository.exchange.Access.ExpiresAt.Sub(now) != AccessTokenTTL || repository.exchange.Refresh.ExpiresAt.Sub(now) != RefreshTokenTTL {
		t.Fatalf("exchange=%+v", repository.exchange)
	}
	rotated, err := service.Refresh(context.Background(), refreshToken, oauthClient, oauthResource)
	if err != nil || rotated.AccessToken != rotatedAccess || rotated.RefreshToken != rotatedRefresh {
		t.Fatalf("rotated=%+v err=%v", rotated, err)
	}
	if repository.refresh.RefreshHash != sha256.Sum256([]byte(refreshToken)) || repository.refresh.Refresh.Hash != sha256.Sum256([]byte(rotatedRefresh)) {
		t.Fatalf("refresh=%+v", repository.refresh)
	}
	actor, err := service.Authenticate(context.Background(), accessToken, TokenRequirement{Audience: oauthResource, Scope: ScopeMCP})
	if err != nil || actor.UserID != oauthUser || repository.requirement.Audience != oauthResource {
		t.Fatalf("actor=%+v requirement=%+v err=%v", actor, repository.requirement, err)
	}
	if err := service.Revoke(context.Background(), refreshToken, oauthClient); err != nil || repository.revokedHash != sha256.Sum256([]byte(refreshToken)) || repository.revokedClient != oauthClient {
		t.Fatalf("revoke hash=%x client=%s err=%v", repository.revokedHash, repository.revokedClient, err)
	}
	grants, err := service.Grants(context.Background(), oauthUser)
	if err != nil || len(grants) != 1 || grants[0].ID != oauthGrant {
		t.Fatalf("grants=%+v err=%v", grants, err)
	}
	revoked, err := service.RevokeGrant(context.Background(), oauthUser, oauthGrant)
	if err != nil || !revoked || repository.revokedGrant != oauthGrant {
		t.Fatalf("grant revoked=%t selected=%q err=%v", revoked, repository.revokedGrant, err)
	}
}

func TestAuthorizationRejectsRedirectPKCEAndAudienceDrift(t *testing.T) {
	now := time.Date(2026, 8, 22, 22, 0, 0, 0, time.UTC)
	repository := &repositoryStub{}
	service, err := New(repository, &sequenceIDs{values: []string{oauthPending}}, &sequenceSecrets{}, testClock{now}, oauthIssuer, oauthResource)
	if err != nil {
		t.Fatal(err)
	}
	base := AuthorizationCommand{UserID: oauthUser, SessionID: oauthSession, Client: Client{ID: oauthClient, Name: "Client", RedirectURIs: []string{oauthRedirect}}, RedirectURI: oauthRedirect, Resource: oauthResource, Scope: ScopeMCP, CodeChallenge: pkceChallenge(secretByte(7))}
	for name, mutate := range map[string]func(*AuthorizationCommand){
		"redirect":  func(command *AuthorizationCommand) { command.RedirectURI = "https://attacker.example/callback" },
		"challenge": func(command *AuthorizationCommand) { command.CodeChallenge = "plain-not-s256" },
		"resource":  func(command *AuthorizationCommand) { command.Resource = "https://api.infiniteocean.net" },
		"scope":     func(command *AuthorizationCommand) { command.Scope = "admin" },
	} {
		t.Run(name, func(t *testing.T) {
			command := base
			mutate(&command)
			if _, err := service.Begin(context.Background(), command); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if _, err := service.Authenticate(context.Background(), secretByte(8), TokenRequirement{Audience: "https://other.example", Scope: ScopeMCP}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("err=%v", err)
	}
}

func TestRedirectPolicyAllowsOnlyHTTPSOrLoopbackHTTP(t *testing.T) {
	for _, value := range []string{"https://client.example/callback", "http://localhost:3000/callback", "http://127.0.0.1:8765/callback", "http://[::1]:4567/callback"} {
		if !validRedirectURI(value) {
			t.Fatalf("valid redirect rejected: %s", value)
		}
	}
	for _, value := range []string{"http://client.example/callback", "javascript:alert(1)", "https://user@example.com/callback", "https://client.example/callback#fragment"} {
		if validRedirectURI(value) {
			t.Fatalf("unsafe redirect accepted: %s", value)
		}
	}
}

func secretByte(value byte) string {
	raw := make([]byte, 32)
	for index := range raw {
		raw[index] = value
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

var _ Repository = (*repositoryStub)(nil)
var _ ids.Generator = (*sequenceIDs)(nil)
