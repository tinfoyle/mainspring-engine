package cellapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	integrationauthorization "github.com/tinfoyle/spyglass-engine/internal/application/integrationauthorization"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type integrationAuthorizationBeginRequest struct {
	RedirectURI string `json:"redirect_uri"`
}

type integrationAuthorizationBeginResponse struct {
	Authorization    integrationAuthorizationSummary `json:"authorization"`
	AuthorizationURL string                          `json:"authorization_url"`
}

type integrationAuthorizationSummary struct {
	ID                   ids.IntegrationAuthorizationSessionID `json:"id"`
	AccountID            ids.AccountID                         `json:"account_id"`
	ConnectionID         ids.IntegrationConnectionID           `json:"connection_id"`
	Status               string                                `json:"status"`
	CredentialID         ids.IntegrationCredentialID           `json:"credential_id,omitempty"`
	CredentialGeneration uint64                                `json:"credential_generation,omitempty"`
	ErrorCode            string                                `json:"error_code,omitempty"`
	CreatedAt            time.Time                             `json:"created_at"`
	UpdatedAt            time.Time                             `json:"updated_at"`
	ExpiresAt            time.Time                             `json:"expires_at"`
}

type integrationCredentialRevocationSummary struct {
	ID                   string                                   `json:"id"`
	AccountID            ids.AccountID                            `json:"account_id"`
	ConnectionID         ids.IntegrationConnectionID              `json:"connection_id"`
	CredentialID         ids.IntegrationCredentialID              `json:"credential_id"`
	CredentialGeneration uint64                                   `json:"credential_generation"`
	State                integrationauthorization.RevocationState `json:"state"`
	CreatedAt            time.Time                                `json:"created_at"`
	UpdatedAt            time.Time                                `json:"updated_at"`
}

func (s *Server) integrationAuthorizationBegin(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.integrationsCommandRequest(w, r)
	if !ok {
		return
	}
	connectionID := ids.IntegrationConnectionID(r.PathValue("connectionID"))
	if ids.Validate(string(connectionID)) != nil || s.integrationAuthorization == nil {
		s.writeIntegrationAuthorizationError(w, "begin", integrationauthorization.ErrInvalid)
		return
	}
	var body integrationAuthorizationBeginRequest
	if !decodeIntegrationsJSON(w, r, &body) {
		return
	}
	result, err := s.integrationAuthorization.Begin(routecontext.WithClaims(r.Context(), claims), integrationauthorization.BeginCommand{
		Actor: actor, Session: authorizationSession(claims, actor), AccountID: accountID, RequestID: requestID,
		ConnectionID: connectionID, RedirectURI: body.RedirectURI,
	})
	if err != nil {
		s.writeIntegrationAuthorizationError(w, "begin", err)
		return
	}
	w.Header().Set("Location", "/api/v1/accounts/"+string(accountID)+"/integrations/authorizations/"+string(result.Session.ID))
	writeJSON(w, http.StatusCreated, integrationAuthorizationBeginResponse{Authorization: authorizationSummary(integrationauthorization.AuthorizationSummary{
		ID: result.Session.ID, AccountID: result.Session.AccountID, ConnectionID: result.Session.ConnectionID, Status: result.Session.Status,
		CredentialID: result.Session.CredentialID, CredentialGeneration: result.Session.CredentialGeneration, ErrorCode: result.Session.ErrorCode,
		CreatedAt: result.Session.CreatedAt, UpdatedAt: result.Session.UpdatedAt, ExpiresAt: result.Session.ExpiresAt,
	}), AuthorizationURL: result.AuthorizationURL})
}

func (s *Server) integrationAuthorizationStatus(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.integrationsReadRequest(w, r)
	if !ok {
		return
	}
	authorizationID := ids.IntegrationAuthorizationSessionID(r.PathValue("authorizationID"))
	if actor.UserID == "" || ids.Validate(string(authorizationID)) != nil || len(r.URL.Query()) != 0 || s.integrationAuthorization == nil {
		s.writeIntegrationAuthorizationError(w, "status", integrationauthorization.ErrInvalid)
		return
	}
	value, err := s.integrationAuthorization.Status(routecontext.WithClaims(r.Context(), claims), actor, accountID, authorizationID)
	if err != nil {
		s.writeIntegrationAuthorizationError(w, "status", err)
		return
	}
	writeJSON(w, http.StatusOK, authorizationSummary(value))
}

func (s *Server) integrationAuthorizationCallback(w http.ResponseWriter, r *http.Request) {
	claims, _, accountID, ok := s.integrationsReadRequest(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	if len(query) != 2 || len(query["state"]) != 1 || s.integrationAuthorization == nil {
		s.writeIntegrationAuthorizationError(w, "callback", integrationauthorization.ErrInvalid)
		return
	}
	command := integrationauthorization.CallbackCommand{AccountID: accountID, State: []byte(query.Get("state"))}
	if len(query["code"]) == 1 && len(query["error"]) == 0 {
		command.Code = []byte(query.Get("code"))
	} else if len(query["error"]) == 1 && len(query["code"]) == 0 {
		command.ProviderError = query.Get("error")
	} else {
		s.writeIntegrationAuthorizationError(w, "callback", integrationauthorization.ErrInvalid)
		return
	}
	result, callbackErr := s.integrationAuthorization.Callback(routecontext.WithClaims(r.Context(), claims), command)
	if callbackErr != nil && result.Session.ID == "" {
		s.writeIntegrationAuthorizationError(w, "callback", callbackErr)
		return
	}
	status := integrationauthorization.AuthorizationSummary{ID: result.Session.ID, AccountID: result.Session.AccountID,
		ConnectionID: result.Session.ConnectionID, Status: result.Session.Status, CredentialID: result.Session.CredentialID,
		CredentialGeneration: result.Session.CredentialGeneration, ErrorCode: result.Session.ErrorCode, CreatedAt: result.Session.CreatedAt,
		UpdatedAt: result.Session.UpdatedAt, ExpiresAt: result.Session.ExpiresAt}
	if callbackErr != nil {
		w.Header().Set("X-Spyglass-Authorization-Outcome", authorizationErrorCode(callbackErr))
	}
	writeJSON(w, http.StatusOK, authorizationSummary(status))
}

func (s *Server) integrationAuthorizationRevoke(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.integrationsCommandRequest(w, r)
	if !ok || !integrationsNoBody(w, r) {
		return
	}
	connectionID := ids.IntegrationConnectionID(r.PathValue("connectionID"))
	if ids.Validate(string(connectionID)) != nil || s.integrationAuthorization == nil {
		s.writeIntegrationAuthorizationError(w, "revoke", integrationauthorization.ErrInvalid)
		return
	}
	value, err := s.integrationAuthorization.Revoke(routecontext.WithClaims(r.Context(), claims), integrationauthorization.RevokeCommand{
		Actor: actor, Session: authorizationSession(claims, actor), AccountID: accountID, RequestID: requestID, ConnectionID: connectionID,
	})
	if err != nil {
		s.writeIntegrationAuthorizationError(w, "revoke", err)
		return
	}
	writeJSON(w, http.StatusOK, integrationCredentialRevocationSummary{ID: value.ID, AccountID: value.AccountID, ConnectionID: value.ConnectionID,
		CredentialID: value.CredentialID, CredentialGeneration: value.CredentialGeneration, State: value.State,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt})
}

func authorizationSession(claims routecontext.Claims, actor access.Actor) sessions.Session {
	value := sessions.Session{UserID: actor.UserID}
	if claims.Authority.StrongAuthenticatedAt != nil {
		value.ReauthenticatedAt = claims.Authority.StrongAuthenticatedAt.UTC()
		value.ReauthenticationMethod = sessions.AuthenticationMethodPasskey
	}
	return value
}

func authorizationSummary(value integrationauthorization.AuthorizationSummary) integrationAuthorizationSummary {
	return integrationAuthorizationSummary{ID: value.ID, AccountID: value.AccountID, ConnectionID: value.ConnectionID, Status: string(value.Status),
		CredentialID: value.CredentialID, CredentialGeneration: value.CredentialGeneration, ErrorCode: value.ErrorCode,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ExpiresAt: value.ExpiresAt}
}

func authorizationErrorCode(err error) string {
	switch {
	case errors.Is(err, integrationauthorization.ErrExpired):
		return "authorization_expired"
	case errors.Is(err, integrationauthorization.ErrProviderRejected):
		return "provider_rejected"
	case errors.Is(err, integrationauthorization.ErrProviderUnavailable):
		return "provider_unavailable"
	case errors.Is(err, integrationauthorization.ErrScopeMismatch):
		return "provider_scope_mismatch"
	default:
		return "authorization_failed"
	}
}

func (s *Server) writeIntegrationAuthorizationError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, integrationauthorization.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_integration_authorization", "the Integration authorization request is invalid")
	case errors.Is(err, integrationauthorization.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "integration_authorization_not_found", "the Integration authorization was not found")
	case errors.Is(err, integrationauthorization.ErrConflict):
		writeProblem(w, http.StatusConflict, "integration_authorization_conflict", "the request conflicts with durable authorization state")
	case errors.Is(err, integrationauthorization.ErrExpired):
		writeProblem(w, http.StatusGone, "integration_authorization_expired", "the Integration authorization has expired")
	case errors.Is(err, integrationauthorization.ErrProviderRejected), errors.Is(err, integrationauthorization.ErrScopeMismatch):
		writeProblem(w, http.StatusUnprocessableEntity, authorizationErrorCode(err), "the provider did not grant the exact requested authorization")
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "recent_passkey_required", "recent passkey verification is required")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this authorization operation")
	default:
		s.logger.Error("Integration authorization operation failed", "operation", strings.TrimSpace(operation), "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "integration_authorization_unavailable", "the Integration authorization operation could not be completed")
	}
}

var _ IntegrationAuthorizationService = (*integrationauthorization.Service)(nil)
