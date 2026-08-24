package cellapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	integrationauthorization "github.com/tinfoyle/spyglass-engine/internal/application/integrationauthorization"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const integrationAuthorizationOperationID = "bb000000-0000-4000-8000-00000000000b"

type integrationAuthorizationTransportService struct {
	begin    integrationauthorization.BeginCommand
	callback integrationauthorization.CallbackCommand
	revoke   integrationauthorization.RevokeCommand
	statusID ids.IntegrationAuthorizationSessionID
}

func authorizationTransportSession(status integrationsdomain.AuthorizationStatus) integrationsdomain.AuthorizationSession {
	now := time.Date(2026, 8, 23, 23, 0, 0, 0, time.UTC)
	return integrationsdomain.AuthorizationSession{ID: ids.IntegrationAuthorizationSessionID(integrationAuthorizationOperationID),
		AccountID: integrationsAccountID, ConnectionID: integrationsConnectionID, Status: status, CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(15 * time.Minute)}
}

func (service *integrationAuthorizationTransportService) Begin(_ context.Context, command integrationauthorization.BeginCommand) (integrationauthorization.BeginResult, error) {
	service.begin = command
	return integrationauthorization.BeginResult{Session: authorizationTransportSession(integrationsdomain.AuthorizationPending),
		AuthorizationURL: "https://accounts.google.test/authorize?redacted=true"}, nil
}
func (service *integrationAuthorizationTransportService) Callback(_ context.Context, command integrationauthorization.CallbackCommand) (integrationauthorization.CallbackResult, error) {
	service.callback = command
	return integrationauthorization.CallbackResult{Session: authorizationTransportSession(integrationsdomain.AuthorizationCompleted)}, nil
}
func (service *integrationAuthorizationTransportService) Status(_ context.Context, _ access.Actor, _ ids.AccountID,
	authorizationID ids.IntegrationAuthorizationSessionID) (integrationauthorization.AuthorizationSummary, error) {
	service.statusID = authorizationID
	value := authorizationTransportSession(integrationsdomain.AuthorizationPending)
	return integrationauthorization.AuthorizationSummary{ID: authorizationID, AccountID: value.AccountID, ConnectionID: value.ConnectionID,
		Status: value.Status, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ExpiresAt: value.ExpiresAt}, nil
}
func (service *integrationAuthorizationTransportService) Revoke(_ context.Context, command integrationauthorization.RevokeCommand) (integrationauthorization.RevocationWorkflow, error) {
	service.revoke = command
	now := time.Date(2026, 8, 23, 23, 0, 0, 0, time.UTC)
	return integrationauthorization.RevocationWorkflow{ID: command.RequestID, AccountID: command.AccountID, ConnectionID: command.ConnectionID,
		CredentialID: integrationsCredentialID, CredentialGeneration: 2, State: integrationauthorization.RevocationCompleted,
		CreatedAt: now, UpdatedAt: now}, nil
}

func TestIntegrationAuthorizationHTTPRoutesKeepProtocolSecretsOutOfResults(t *testing.T) {
	service := &integrationAuthorizationTransportService{}
	strong := time.Date(2026, 8, 23, 22, 59, 0, 0, time.UTC)
	claims := integrationsMutationClaims(integrationAuthorizationOperationID)
	claims.Authority.StrongAuthenticatedAt = &strong
	server, err := New(claimAcceptor{claims: claims}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody,
		WithIntegrations(&integrationsQueryTransportService{}), WithIntegrationCommands(&integrationsCommandTransportService{}),
		WithIntegrationAuthorization(service))
	if err != nil {
		t.Fatal(err)
	}
	begin := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+string(integrationsAccountID)+"/integrations/connections/"+
		string(integrationsConnectionID)+"/authorizations", strings.NewReader(`{"redirect_uri":"https://app.infiniteocean.net/api/v1/accounts/callback"}`))
	begin.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	begin.Header.Set("Idempotency-Key", integrationAuthorizationOperationID)
	begin.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, begin)
	if response.Code != http.StatusCreated || service.begin.Session.ReauthenticationMethod != sessions.AuthenticationMethodPasskey ||
		!service.begin.Session.ReauthenticatedAt.Equal(strong) || strings.Contains(response.Body.String(), "state_sha256") ||
		strings.Contains(response.Body.String(), "pkce") || strings.Contains(response.Body.String(), "scope_revision") {
		t.Fatalf("begin status=%d command=%+v body=%s", response.Code, service.begin, response.Body.String())
	}

	state := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	code := "4/0code-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn"
	callback := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+string(integrationsAccountID)+
		"/integrations/google/authorization-callback?state="+state+"&code="+code, nil)
	callback.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, callback)
	if response.Code != http.StatusOK || string(service.callback.State) != state || string(service.callback.Code) != code ||
		strings.Contains(response.Body.String(), "redirect_uri") {
		t.Fatalf("callback status=%d command=%+v body=%s", response.Code, service.callback, response.Body.String())
	}

	authorizationID := ids.IntegrationAuthorizationSessionID(integrationAuthorizationOperationID)
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+string(integrationsAccountID)+
		"/integrations/authorizations/"+string(authorizationID), nil)
	statusRequest.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, statusRequest)
	if response.Code != http.StatusOK || service.statusID != authorizationID || strings.Contains(response.Body.String(), "state") &&
		!strings.Contains(response.Body.String(), `"status"`) {
		t.Fatalf("status=%d id=%s body=%s", response.Code, service.statusID, response.Body.String())
	}

	revoke := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+string(integrationsAccountID)+"/integrations/connections/"+
		string(integrationsConnectionID)+"/credential-revocations", nil)
	revoke.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	revoke.Header.Set("Idempotency-Key", integrationAuthorizationOperationID)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, revoke)
	if response.Code != http.StatusOK || service.revoke.Session.ReauthenticationMethod != sessions.AuthenticationMethodPasskey ||
		strings.Contains(response.Body.String(), "reference") || strings.Contains(response.Body.String(), "provider") {
		t.Fatalf("revoke status=%d command=%+v body=%s", response.Code, service.revoke, response.Body.String())
	}
}

var _ IntegrationAuthorizationService = (*integrationAuthorizationTransportService)(nil)
