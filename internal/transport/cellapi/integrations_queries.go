package cellapi

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	integrationsapp "github.com/tinfoyle/spyglass-engine/internal/application/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type integrationsCursorEnvelope struct {
	Version int       `json:"v"`
	Kind    string    `json:"kind"`
	Date    time.Time `json:"date"`
	ID      string    `json:"id"`
}

type integrationsConnectionDetailResponse struct {
	Connection   integrationsdomain.Connection         `json:"connection"`
	Revision     integrationsdomain.ConnectionRevision `json:"revision"`
	LatestHealth *integrationsdomain.HealthObservation `json:"latest_health,omitempty"`
}

type integrationsExecutionResponse struct {
	ID                   ids.IntegrationExecutionID          `json:"id"`
	AccountID            ids.AccountID                       `json:"account_id"`
	ReleaseID            ids.MarketingReleaseID              `json:"release_id"`
	ReleaseVersion       uint64                              `json:"release_version"`
	ApprovalID           ids.ConsequentialApprovalID         `json:"approval_id"`
	Capability           integrationsdomain.Capability       `json:"capability"`
	ConnectionID         ids.IntegrationConnectionID         `json:"connection_id"`
	ConnectionRevisionID ids.IntegrationConnectionRevisionID `json:"connection_revision_id"`
	ConnectionRevision   uint64                              `json:"connection_revision"`
	CredentialID         ids.IntegrationCredentialID         `json:"credential_id"`
	CredentialGeneration uint64                              `json:"credential_generation"`
	PayloadSHA256        string                              `json:"payload_sha256"`
	State                integrationsdomain.ExecutionState   `json:"state"`
	AttemptCount         uint16                              `json:"attempt_count"`
	CurrentAttemptID     ids.IntegrationAttemptID            `json:"current_attempt_id,omitempty"`
	LastErrorCode        string                              `json:"last_error_code,omitempty"`
	LeaseExpiresAt       *time.Time                          `json:"lease_expires_at,omitempty"`
	NextAttemptAt        *time.Time                          `json:"next_attempt_at,omitempty"`
	CreatedAt            time.Time                           `json:"created_at"`
	UpdatedAt            time.Time                           `json:"updated_at"`
	CompletedAt          *time.Time                          `json:"completed_at,omitempty"`
}

type integrationsExecutionDetailResponse struct {
	Execution integrationsExecutionResponse `json:"execution"`
	Attempts  []integrationsdomain.Attempt  `json:"attempts"`
}

func (s *Server) integrationsConnectionList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.integrationsReadRequest(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "state", "kind", "cursor", "limit") {
		s.writeIntegrationsError(w, "list connections", integrationsapp.ErrInvalid)
		return
	}
	limit, err := parseLimit(r, integrationsapp.DefaultConnectionPageSize)
	if err != nil {
		s.writeIntegrationsError(w, "list connections", integrationsapp.ErrInvalid)
		return
	}
	query := integrationsapp.ConnectionListQuery{Limit: limit}
	for _, value := range r.URL.Query()["state"] {
		query.States = append(query.States, integrationsdomain.ConnectionState(value))
	}
	for _, value := range r.URL.Query()["kind"] {
		query.Kinds = append(query.Kinds, integrationsdomain.ConnectorKind(value))
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeIntegrationsCursor(raw, "connection")
		if err != nil {
			s.writeIntegrationsError(w, "decode connection cursor", err)
			return
		}
		query.After = &integrationsapp.ConnectionCursor{UpdatedAt: cursor.Date, ID: ids.IntegrationConnectionID(cursor.ID)}
	}
	page, err := s.integrations.ListConnections(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeIntegrationsError(w, "list connections", err)
		return
	}
	response := map[string]any{"items": page.Items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeIntegrationsCursor("connection", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) integrationsConnectionGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.integrationsReadRequest(w, r)
	if !ok {
		return
	}
	connectionID := ids.IntegrationConnectionID(r.PathValue("connectionID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(connectionID)) != nil {
		s.writeIntegrationsError(w, "get connection", integrationsapp.ErrInvalid)
		return
	}
	detail, err := s.integrations.GetConnectionDetail(routecontext.WithClaims(r.Context(), claims), actor, accountID, connectionID)
	if err != nil {
		s.writeIntegrationsError(w, "get connection", err)
		return
	}
	writeMarketingVersion(w, detail.Connection.Version)
	writeJSON(w, http.StatusOK, integrationsConnectionDetailResponse{Connection: detail.Connection, Revision: detail.Revision, LatestHealth: detail.LatestHealth})
}

func (s *Server) integrationsHealthList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.integrationsReadRequest(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "cursor", "limit") {
		s.writeIntegrationsError(w, "list health", integrationsapp.ErrInvalid)
		return
	}
	connectionID := ids.IntegrationConnectionID(r.PathValue("connectionID"))
	limit, err := parseLimit(r, integrationsapp.DefaultConnectionPageSize)
	if err != nil || ids.Validate(string(connectionID)) != nil {
		s.writeIntegrationsError(w, "list health", integrationsapp.ErrInvalid)
		return
	}
	query := integrationsapp.HealthListQuery{ConnectionID: connectionID, Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeIntegrationsCursor(raw, "health")
		if err != nil {
			s.writeIntegrationsError(w, "decode health cursor", err)
			return
		}
		query.After = &integrationsapp.HealthCursor{CheckedAt: cursor.Date, ID: ids.IntegrationHealthObservationID(cursor.ID)}
	}
	page, err := s.integrations.ListHealth(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeIntegrationsError(w, "list health", err)
		return
	}
	response := map[string]any{"items": page.Items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeIntegrationsCursor("health", page.NextCursor.CheckedAt, string(page.NextCursor.ID))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) integrationsExecutionList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.integrationsReadRequest(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "connection_id", "state", "capability", "cursor", "limit") {
		s.writeIntegrationsError(w, "list executions", integrationsapp.ErrInvalid)
		return
	}
	limit, err := parseLimit(r, integrationsapp.DefaultConnectionPageSize)
	if err != nil {
		s.writeIntegrationsError(w, "list executions", integrationsapp.ErrInvalid)
		return
	}
	query := integrationsapp.ExecutionListQuery{ConnectionID: ids.IntegrationConnectionID(r.URL.Query().Get("connection_id")), Limit: limit}
	for _, value := range r.URL.Query()["state"] {
		query.States = append(query.States, integrationsdomain.ExecutionState(value))
	}
	for _, value := range r.URL.Query()["capability"] {
		query.Capabilities = append(query.Capabilities, integrationsdomain.Capability(value))
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeIntegrationsCursor(raw, "execution")
		if err != nil {
			s.writeIntegrationsError(w, "decode execution cursor", err)
			return
		}
		query.After = &integrationsapp.ExecutionCursor{UpdatedAt: cursor.Date, ID: ids.IntegrationExecutionID(cursor.ID)}
	}
	page, err := s.integrations.ListExecutions(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeIntegrationsError(w, "list executions", err)
		return
	}
	items := make([]integrationsExecutionResponse, len(page.Items))
	for index, item := range page.Items {
		items[index] = integrationsExecutionDTO(item)
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeIntegrationsCursor("execution", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) integrationsExecutionGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.integrationsReadRequest(w, r)
	if !ok {
		return
	}
	executionID := ids.IntegrationExecutionID(r.PathValue("executionID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(executionID)) != nil {
		s.writeIntegrationsError(w, "get execution", integrationsapp.ErrInvalid)
		return
	}
	detail, err := s.integrations.GetExecution(routecontext.WithClaims(r.Context(), claims), actor, accountID, executionID)
	if err != nil {
		s.writeIntegrationsError(w, "get execution", err)
		return
	}
	writeJSON(w, http.StatusOK, integrationsExecutionDetailResponse{Execution: integrationsExecutionDTO(detail.Execution), Attempts: detail.Attempts})
}

func (s *Server) integrationsReadRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if s.integrations == nil {
		writeProblem(w, http.StatusServiceUnavailable, "integrations_unavailable", "Integrations is not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	actor := access.Actor{}
	if claims.Authority.ActorKind == "user" {
		actor.UserID = ids.UserID(claims.Authority.ActorID)
	} else {
		actor.WorkloadID = claims.Authority.ActorID
	}
	return claims, actor, accountID, true
}

func encodeIntegrationsCursor(kind string, date time.Time, id string) string {
	raw, _ := json.Marshal(integrationsCursorEnvelope{Version: 1, Kind: kind, Date: date.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeIntegrationsCursor(raw, kind string) (integrationsCursorEnvelope, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 1024 {
		return integrationsCursorEnvelope{}, integrationsapp.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var value integrationsCursorEnvelope
	if decoder.Decode(&value) != nil || value.Version != 1 || value.Kind != kind || value.Date.IsZero() || ids.Validate(value.ID) != nil {
		return integrationsCursorEnvelope{}, integrationsapp.ErrInvalid
	}
	if decoder.Decode(&struct{}{}) == nil {
		return integrationsCursorEnvelope{}, integrationsapp.ErrInvalid
	}
	value.Date = value.Date.UTC()
	return value, nil
}

func integrationsExecutionDTO(value integrationsdomain.Execution) integrationsExecutionResponse {
	return integrationsExecutionResponse{ID: value.ID, AccountID: value.AccountID, ReleaseID: value.ReleaseID, ReleaseVersion: value.ReleaseVersion,
		ApprovalID: value.ApprovalID, Capability: value.Capability, ConnectionID: value.ConnectionID, ConnectionRevisionID: value.ConnectionRevisionID,
		ConnectionRevision: value.ConnectionRevision, CredentialID: value.CredentialID, CredentialGeneration: value.CredentialGeneration,
		PayloadSHA256: hex.EncodeToString(value.PayloadSHA256[:]), State: value.State, AttemptCount: value.AttemptCount, CurrentAttemptID: value.CurrentAttemptID,
		LastErrorCode: value.LastErrorCode, LeaseExpiresAt: value.LeaseExpiresAt, NextAttemptAt: value.NextAttemptAt, CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt, CompletedAt: value.CompletedAt}
}

func (s *Server) writeIntegrationsError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, integrationsapp.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_integrations_query", "the Integrations query is invalid")
	case errors.Is(err, integrationsapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "integration_record_not_found", "the Integration record was not found")
	case errors.Is(err, integrationsapp.ErrConflict):
		writeProblem(w, http.StatusConflict, "integration_operation_conflict", "the operation conflicts with durable Integration state")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this Integration operation")
	default:
		s.logger.Error("Integrations operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "integrations_unavailable", "the Integrations operation could not be completed")
	}
}

var _ IntegrationsQueryService = (*integrationsapp.Service)(nil)
