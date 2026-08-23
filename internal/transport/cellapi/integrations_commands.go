package cellapi

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	integrationsapp "github.com/tinfoyle/spyglass-engine/internal/application/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type integrationConnectionDefinitionRequest struct {
	Name         string                             `json:"name"`
	Kind         integrationsdomain.ConnectorKind   `json:"kind"`
	Capabilities []integrationsdomain.Capability    `json:"capabilities"`
	Scope        integrationsdomain.ConnectionScope `json:"scope"`
}

type integrationConnectionRevisionRequest struct {
	Name         string                             `json:"name"`
	Capabilities []integrationsdomain.Capability    `json:"capabilities"`
	Scope        integrationsdomain.ConnectionScope `json:"scope"`
}

type integrationCredentialRequest struct {
	ExpectedGeneration uint64     `json:"expected_generation,omitempty"`
	Provider           string     `json:"provider"`
	ReferenceSHA256    string     `json:"reference_sha256"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
}

type integrationExecutionPrepareRequest struct {
	ReleaseID      ids.MarketingReleaseID        `json:"release_id"`
	ReleaseVersion uint64                        `json:"release_version"`
	Capability     integrationsdomain.Capability `json:"capability"`
	ConnectionID   ids.IntegrationConnectionID   `json:"connection_id"`
}

func (s *Server) integrationsConnectionCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.integrationsCommandRequest(w, r)
	if !ok {
		return
	}
	var body integrationConnectionDefinitionRequest
	if !decodeIntegrationsJSON(w, r, &body) {
		return
	}
	value, created, err := s.integrationCommands.CreateConnection(routecontext.WithClaims(r.Context(), claims), integrationsapp.CreateConnectionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, Name: body.Name, Kind: body.Kind, Capabilities: body.Capabilities, Scope: body.Scope})
	if err != nil {
		s.writeIntegrationsError(w, "create connection", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/integrations/connections/%s", accountID, value.ID))
	writeMarketingVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) integrationsConnectionRevise(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, connectionID, version, ok := s.integrationsVersionCommand(w, r)
	if !ok {
		return
	}
	var body integrationConnectionRevisionRequest
	if !decodeIntegrationsJSON(w, r, &body) {
		return
	}
	value, err := s.integrationCommands.ReviseConnection(routecontext.WithClaims(r.Context(), claims), integrationsapp.ReviseConnectionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, ConnectionID: connectionID, ExpectedVersion: version,
		Name: body.Name, Capabilities: body.Capabilities, Scope: body.Scope})
	s.writeIntegrationConnection(w, "revise connection", value, err)
}

func (s *Server) integrationsCredentialActivate(w http.ResponseWriter, r *http.Request) {
	s.integrationsCredentialCommand(w, r, false)
}

func (s *Server) integrationsCredentialRotate(w http.ResponseWriter, r *http.Request) {
	s.integrationsCredentialCommand(w, r, true)
}

func (s *Server) integrationsCredentialCommand(w http.ResponseWriter, r *http.Request, rotate bool) {
	claims, actor, accountID, requestID, connectionID, version, ok := s.integrationsVersionCommand(w, r)
	if !ok {
		return
	}
	var body integrationCredentialRequest
	if !decodeIntegrationsJSON(w, r, &body) {
		return
	}
	digest, ok := decodeIntegrationDigest(body.ReferenceSHA256)
	if !ok {
		s.writeIntegrationsError(w, "bind credential", integrationsapp.ErrInvalid)
		return
	}
	command := integrationsapp.CredentialCommand{Actor: actor, AccountID: accountID, RequestID: requestID, ConnectionID: connectionID,
		ExpectedVersion: version, ExpectedGeneration: body.ExpectedGeneration, Provider: body.Provider, ReferenceSHA256: digest,
		CredentialExpiresAt: body.ExpiresAt}
	var value integrationsdomain.Connection
	var err error
	if rotate {
		value, err = s.integrationCommands.RotateCredential(routecontext.WithClaims(r.Context(), claims), command)
	} else {
		value, err = s.integrationCommands.ActivateConnection(routecontext.WithClaims(r.Context(), claims), command)
	}
	s.writeIntegrationConnection(w, "bind credential", value, err)
}

func (s *Server) integrationsConnectionDisable(w http.ResponseWriter, r *http.Request) {
	s.integrationsTransition(w, r, "disable")
}
func (s *Server) integrationsConnectionEnable(w http.ResponseWriter, r *http.Request) {
	s.integrationsTransition(w, r, "enable")
}
func (s *Server) integrationsConnectionRevoke(w http.ResponseWriter, r *http.Request) {
	s.integrationsTransition(w, r, "revoke")
}

func (s *Server) integrationsTransition(w http.ResponseWriter, r *http.Request, kind string) {
	claims, actor, accountID, requestID, connectionID, version, ok := s.integrationsVersionCommand(w, r)
	if !ok || !integrationsNoBody(w, r) {
		return
	}
	command := integrationsapp.TransitionCommand{Actor: actor, AccountID: accountID, RequestID: requestID, ConnectionID: connectionID, ExpectedVersion: version}
	var value integrationsdomain.Connection
	var err error
	switch kind {
	case "disable":
		value, err = s.integrationCommands.DisableConnection(routecontext.WithClaims(r.Context(), claims), command)
	case "enable":
		value, err = s.integrationCommands.EnableConnection(routecontext.WithClaims(r.Context(), claims), command)
	default:
		value, err = s.integrationCommands.RevokeConnection(routecontext.WithClaims(r.Context(), claims), command)
	}
	s.writeIntegrationConnection(w, kind+" connection", value, err)
}

func (s *Server) integrationsExecutionPrepare(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.integrationsCommandRequest(w, r)
	if !ok {
		return
	}
	var body integrationExecutionPrepareRequest
	if !decodeIntegrationsJSON(w, r, &body) {
		return
	}
	value, created, err := s.integrationCommands.PrepareExecution(routecontext.WithClaims(r.Context(), claims), integrationsapp.PrepareExecutionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, ReleaseID: body.ReleaseID, ReleaseVersion: body.ReleaseVersion,
		Capability: body.Capability, ConnectionID: body.ConnectionID})
	if err != nil {
		s.writeIntegrationsError(w, "prepare execution", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/integrations/executions/%s", accountID, value.ID))
	writeJSON(w, status, integrationsExecutionDTO(value))
}

func (s *Server) integrationsCommandRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if s.integrationCommands == nil {
		writeProblem(w, http.StatusServiceUnavailable, "integrations_unavailable", "Integrations commands are not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if claims.Authority.ActorKind != "user" || ids.Validate(claims.Authority.ActorID) != nil {
		writeProblem(w, http.StatusForbidden, "integration_management_denied", "Integration management requires a current human manager")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return claims, access.Actor{UserID: ids.UserID(claims.Authority.ActorID)}, accountID, claims.Authority.OperationID, true
}

func (s *Server) integrationsVersionCommand(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, ids.IntegrationConnectionID, uint64, bool) {
	claims, actor, accountID, requestID, ok := s.integrationsCommandRequest(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", "", 0, false
	}
	connectionID := ids.IntegrationConnectionID(r.PathValue("connectionID"))
	if ids.Validate(string(connectionID)) != nil || len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_integrations_command", "the Integrations command target is invalid")
		return routecontext.Claims{}, access.Actor{}, "", "", "", 0, false
	}
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		writeProblem(w, http.StatusPreconditionRequired, "integration_version_required", "If-Match with the current Integration version is required")
		return routecontext.Claims{}, access.Actor{}, "", "", "", 0, false
	}
	version, err := parseAttentionVersion(values)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_integration_version", "If-Match must contain exactly one weak Integration version ETag")
		return routecontext.Claims{}, access.Actor{}, "", "", "", 0, false
	}
	return claims, actor, accountID, requestID, connectionID, version, true
}

func decodeIntegrationsJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_integrations_command", "Integrations commands do not accept query parameters")
		return false
	}
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Integrations commands require application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_integrations_command", "the Integrations command body is invalid")
		return false
	}
	return true
}

func integrationsNoBody(w http.ResponseWriter, r *http.Request) bool {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_integrations_command", "Integrations commands do not accept query parameters")
		return false
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(strings.TrimSpace(string(raw))) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_integrations_command", "this Integrations command does not accept a body")
		return false
	}
	return true
}

func decodeIntegrationDigest(value string) ([32]byte, bool) {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 32 || value != strings.ToLower(value) {
		return [32]byte{}, false
	}
	var digest [32]byte
	copy(digest[:], raw)
	return digest, true
}

func (s *Server) writeIntegrationConnection(w http.ResponseWriter, operation string, value integrationsdomain.Connection, err error) {
	if err != nil {
		s.writeIntegrationsError(w, operation, err)
		return
	}
	writeMarketingVersion(w, value.Version)
	writeJSON(w, http.StatusOK, value)
}

var _ IntegrationsCommandService = (*integrationsapp.Service)(nil)
