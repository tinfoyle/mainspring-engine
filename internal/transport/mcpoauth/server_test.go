package mcpoauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testUser     = ids.UserID("10000000-0000-4000-8000-000000000001")
	testSession  = ids.SessionID("20000000-0000-4000-8000-000000000002")
	testPending  = "30000000-0000-4000-8000-000000000003"
	testGrant    = "40000000-0000-4000-8000-000000000004"
	testIssuer   = "https://app.example"
	testResource = "https://mcp.example"
	testClient   = "https://client.example/oauth/metadata.json"
	testRedirect = "http://127.0.0.1:8765/callback"
	testVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
)

type transportClock struct{ now time.Time }

func (c transportClock) Now() time.Time { return c.now }

type transportLimiter struct{ allowed bool }

func (l transportLimiter) Allow(context.Context, abuse.Scope, [32]byte, time.Time, abuse.Policy) (bool, error) {
	return l.allowed, nil
}

type transportIDs struct{ values []string }

func (g *transportIDs) New() string {
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

type transportRepository struct {
	pending  mcpauth.PendingAuthorization
	codeHash [32]byte
}

func (r *transportRepository) CreateAuthorization(_ context.Context, pending mcpauth.PendingAuthorization) error {
	r.pending = pending
	return nil
}
func (r *transportRepository) DecideAuthorization(_ context.Context, _ mcpauth.AuthorizationDecision, codeHash [32]byte, _ string, _, _ time.Time) (mcpauth.PendingAuthorization, error) {
	r.codeHash = codeHash
	return r.pending, nil
}
func (r *transportRepository) ExchangeCode(_ context.Context, exchange mcpauth.CodeExchange) (mcpauth.IssuedAuthority, error) {
	if exchange.CodeHash != r.codeHash {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrNotFound
	}
	return mcpauth.IssuedAuthority{UserID: testUser, Resource: testResource, Scope: mcpauth.ScopeMCP}, nil
}
func (*transportRepository) RotateRefresh(context.Context, mcpauth.RefreshExchange) (mcpauth.IssuedAuthority, error) {
	return mcpauth.IssuedAuthority{UserID: testUser, Resource: testResource, Scope: mcpauth.ScopeMCP}, nil
}
func (*transportRepository) AuthenticateAccess(context.Context, [32]byte, mcpauth.TokenRequirement, time.Time) (access.Actor, error) {
	return access.Actor{UserID: testUser}, nil
}
func (*transportRepository) Revoke(context.Context, [32]byte, string, time.Time) error { return nil }
func (*transportRepository) ListGrants(context.Context, ids.UserID, time.Time) ([]mcpauth.GrantSummary, error) {
	return nil, nil
}
func (*transportRepository) RevokeGrant(context.Context, ids.UserID, string, time.Time) (bool, error) {
	return true, nil
}

type transportSessions struct{ authenticated bool }

func (s transportSessions) Authenticate(context.Context, string) (sessions.Authenticated, error) {
	if !s.authenticated {
		return sessions.Authenticated{}, sessions.ErrInvalidSession
	}
	return sessions.Authenticated{Session: sessions.Session{ID: testSession, UserID: testUser, ExpiresAt: time.Now().Add(time.Hour)}}, nil
}

type transportClients struct{}

func (transportClients) Load(_ context.Context, clientID string) (mcpauth.Client, error) {
	if clientID != testClient {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	return mcpauth.Client{ID: testClient, Name: "Test MCP Client", RedirectURIs: []string{testRedirect}}, nil
}

func TestAuthorizationMetadataConsentAndTokenExchange(t *testing.T) {
	now := time.Date(2026, 8, 22, 23, 0, 0, 0, time.UTC)
	repository := &transportRepository{}
	service, err := mcpauth.New(repository, &transportIDs{values: []string{testPending, testGrant}}, mcpauth.RandomSecrets{}, transportClock{now}, testIssuer, testResource)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service, transportSessions{authenticated: true}, transportClients{}, transportLimiter{allowed: true}, transportClock{now}, Config{Issuer: testIssuer, Resource: testResource, TrustedOrigin: testIssuer}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler(nil)

	metadataRequest := httptest.NewRequest(http.MethodGet, testIssuer+"/.well-known/oauth-authorization-server", nil)
	metadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(metadataResponse, metadataRequest)
	var metadata map[string]any
	if metadataResponse.Code != http.StatusOK || json.Unmarshal(metadataResponse.Body.Bytes(), &metadata) != nil || metadata["issuer"] != testIssuer || metadata["client_id_metadata_document_supported"] != true {
		t.Fatalf("metadata response = %d %s", metadataResponse.Code, metadataResponse.Body.String())
	}

	verifierDigest := sha256.Sum256([]byte(testVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(verifierDigest[:])
	authorizationURL := testIssuer + "/oauth/authorize?" + url.Values{
		"response_type": {"code"}, "client_id": {testClient}, "redirect_uri": {testRedirect},
		"resource": {testResource}, "scope": {mcpauth.ScopeMCP}, "state": {"opaque-state"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}.Encode()
	authorizationRequest := httptest.NewRequest(http.MethodGet, authorizationURL, nil)
	authorizationRequest.AddCookie(&http.Cookie{Name: defaultSessionCookie, Value: "session"})
	authorizationResponse := httptest.NewRecorder()
	handler.ServeHTTP(authorizationResponse, authorizationRequest)
	if authorizationResponse.Code != http.StatusOK || !strings.Contains(authorizationResponse.Body.String(), "Test MCP Client") || !strings.Contains(authorizationResponse.Body.String(), testPending) {
		t.Fatalf("authorization response = %d %s", authorizationResponse.Code, authorizationResponse.Body.String())
	}

	decisionForm := url.Values{"pending_id": {testPending}, "decision": {"approve"}}
	decisionRequest := httptest.NewRequest(http.MethodPost, testIssuer+"/oauth/authorize", strings.NewReader(decisionForm.Encode()))
	decisionRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	decisionRequest.Header.Set("Origin", testIssuer)
	decisionRequest.AddCookie(&http.Cookie{Name: defaultSessionCookie, Value: "session"})
	decisionResponse := httptest.NewRecorder()
	handler.ServeHTTP(decisionResponse, decisionRequest)
	location, err := url.Parse(decisionResponse.Header().Get("Location"))
	if err != nil || decisionResponse.Code != http.StatusSeeOther || location.Query().Get("code") == "" || location.Query().Get("state") != "opaque-state" || location.Query().Get("iss") != testIssuer {
		t.Fatalf("decision response = %d location=%q error=%v", decisionResponse.Code, decisionResponse.Header().Get("Location"), err)
	}

	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "code": {location.Query().Get("code")}, "client_id": {testClient},
		"redirect_uri": {testRedirect}, "resource": {testResource}, "code_verifier": {testVerifier},
	}
	tokenRequest := httptest.NewRequest(http.MethodPost, testIssuer+"/oauth/token", strings.NewReader(tokenForm.Encode()))
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResponse := httptest.NewRecorder()
	handler.ServeHTTP(tokenResponse, tokenRequest)
	var tokenSet map[string]any
	if tokenResponse.Code != http.StatusOK || json.Unmarshal(tokenResponse.Body.Bytes(), &tokenSet) != nil || tokenSet["token_type"] != "Bearer" || tokenSet["scope"] != mcpauth.ScopeMCP || tokenSet["access_token"] == "" || tokenSet["refresh_token"] == "" {
		t.Fatalf("token response = %d %s", tokenResponse.Code, tokenResponse.Body.String())
	}
}

func TestAuthorizationRequiresSessionAndSameOriginDecision(t *testing.T) {
	now := time.Date(2026, 8, 22, 23, 0, 0, 0, time.UTC)
	service, err := mcpauth.New(&transportRepository{}, &transportIDs{values: []string{testPending, testGrant}}, mcpauth.RandomSecrets{}, transportClock{now}, testIssuer, testResource)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	withoutSession, _ := New(service, transportSessions{}, transportClients{}, transportLimiter{allowed: true}, transportClock{now}, Config{Issuer: testIssuer, Resource: testResource, TrustedOrigin: testIssuer}, logger)
	request := httptest.NewRequest(http.MethodGet, testIssuer+"/oauth/authorize?response_type=code", nil)
	response := httptest.NewRecorder()
	withoutSession.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || !strings.HasPrefix(response.Header().Get("Location"), "/login?return_to=") {
		t.Fatalf("missing-session response = %d %q", response.Code, response.Header().Get("Location"))
	}

	withSession, _ := New(service, transportSessions{authenticated: true}, transportClients{}, transportLimiter{allowed: true}, transportClock{now}, Config{Issuer: testIssuer, Resource: testResource, TrustedOrigin: testIssuer}, logger)
	form := url.Values{"pending_id": {testPending}, "decision": {"approve"}}
	request = httptest.NewRequest(http.MethodPost, testIssuer+"/oauth/authorize", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://attacker.example")
	request.AddCookie(&http.Cookie{Name: defaultSessionCookie, Value: "session"})
	response = httptest.NewRecorder()
	withSession.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin decision response = %d", response.Code)
	}
}

func TestTokenEndpointEnforcesDistributedRequestBudget(t *testing.T) {
	now := time.Date(2026, 8, 22, 23, 0, 0, 0, time.UTC)
	service, err := mcpauth.New(&transportRepository{}, &transportIDs{values: []string{testPending}}, mcpauth.RandomSecrets{}, transportClock{now}, testIssuer, testResource)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service, transportSessions{}, transportClients{}, transportLimiter{}, transportClock{now}, Config{Issuer: testIssuer, Resource: testResource, TrustedOrigin: testIssuer}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, testIssuer+"/oauth/token", strings.NewReader("grant_type=authorization_code"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "900" || !strings.Contains(response.Body.String(), "temporarily_unavailable") {
		t.Fatalf("limited response = %d retry=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
}

func TestClientMetadataAddressesMustBePublic(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "::1", "fc00::1", "ff02::1", "0.0.0.0"} {
		if publicMetadataAddress(net.ParseIP(raw)) {
			t.Fatalf("address %s was accepted", raw)
		}
	}
	if !publicMetadataAddress(net.ParseIP("203.0.113.10")) {
		t.Fatal("public documentation address was rejected")
	}
}
