package cellapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type AgentService interface {
	CreateBoardroom(context.Context, agentapp.CreateBoardroomCommand) (agentdomain.Boardroom, bool, error)
	PublishPersona(context.Context, agentapp.PublishPersonaCommand) (agentapp.PersonaSummary, bool, error)
	StartRun(context.Context, agentapp.StartRunCommand) (agentapp.Run, bool, error)
	ListBoardrooms(context.Context, access.Actor, ids.AccountID, int) ([]agentdomain.Boardroom, error)
	ListPersonas(context.Context, access.Actor, ids.AccountID, ids.BoardroomID, int) ([]agentapp.PersonaSummary, error)
	GetRun(context.Context, access.Actor, ids.AccountID, ids.RunID) (agentapp.Run, error)
}

type createBoardroomRequest struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
}
type publishPersonaRequest struct {
	PersonaID             ids.PersonaID             `json:"persona_id"`
	ExpectedLatestVersion uint64                    `json:"expected_latest_version"`
	Name                  string                    `json:"name"`
	Role                  string                    `json:"role"`
	Description           string                    `json:"description"`
	SystemInstructions    string                    `json:"system_instructions"`
	Policy                agentdomain.PersonaPolicy `json:"policy"`
}
type startAgentRunRequest struct {
	ConversationID ids.ConversationID `json:"conversation_id,omitempty"`
	Subject        string             `json:"subject,omitempty"`
	Prompt         string             `json:"prompt"`
	PersonaIDs     []ids.PersonaID    `json:"persona_ids"`
}

func (s *Server) agentBoardrooms(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, _, ok := s.agentRequest(w, r, false)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_query", "Agent Boardrooms do not accept query parameters")
		return
	}
	items, err := s.agents.ListBoardrooms(routecontext.WithClaims(r.Context(), claims), actor, accountID, 100)
	if err != nil {
		s.writeAgentError(w, "list_boardrooms", err)
		return
	}
	views := make([]agentBoardroomResponse, len(items))
	for index, item := range items {
		views[index] = agentBoardroomView(item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

func (s *Server) agentBoardroomCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.agentRequest(w, r, true)
	if !ok {
		return
	}
	var request createBoardroomRequest
	if len(r.URL.Query()) != 0 || !decodeAgentJSON(w, r, &request) {
		return
	}
	item, created, err := s.agents.CreateBoardroom(routecontext.WithClaims(r.Context(), claims), agentapp.CreateBoardroomCommand{Actor: actor, AccountID: accountID, RequestID: operationID, Name: request.Name, Purpose: request.Purpose})
	if err != nil {
		s.writeAgentError(w, "create_boardroom", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/agent-boardrooms/%s", accountID, item.ID))
	writeJSON(w, status, agentBoardroomView(item))
}

func (s *Server) agentPersonas(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, _, ok := s.agentRequest(w, r, false)
	if !ok {
		return
	}
	boardroomID := ids.BoardroomID(r.PathValue("boardroomID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(boardroomID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_query", "Agent Persona query is invalid")
		return
	}
	items, err := s.agents.ListPersonas(routecontext.WithClaims(r.Context(), claims), actor, accountID, boardroomID, 100)
	if err != nil {
		s.writeAgentError(w, "list_personas", err)
		return
	}
	views := make([]agentPersonaResponse, len(items))
	for index, item := range items {
		views[index] = agentPersonaView(item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

func (s *Server) agentPersonaPublish(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.agentRequest(w, r, true)
	if !ok {
		return
	}
	boardroomID := ids.BoardroomID(r.PathValue("boardroomID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(boardroomID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_command", "Agent Persona target is invalid")
		return
	}
	var request publishPersonaRequest
	if !decodeAgentJSON(w, r, &request) {
		return
	}
	item, created, err := s.agents.PublishPersona(routecontext.WithClaims(r.Context(), claims), agentapp.PublishPersonaCommand{
		Actor: actor, AccountID: accountID, BoardroomID: boardroomID, PersonaID: request.PersonaID,
		VersionID: ids.PersonaVersionID(operationID), ExpectedLatestVersion: request.ExpectedLatestVersion,
		Name: request.Name, Role: request.Role, Description: request.Description, SystemInstructions: request.SystemInstructions, Policy: request.Policy,
	})
	if err != nil {
		s.writeAgentError(w, "publish_persona", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, agentPersonaView(item))
}

func (s *Server) agentRunStart(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.agentRequest(w, r, true)
	if !ok {
		return
	}
	boardroomID := ids.BoardroomID(r.PathValue("boardroomID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(boardroomID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_command", "Agent Run target is invalid")
		return
	}
	var request startAgentRunRequest
	if !decodeAgentJSON(w, r, &request) {
		return
	}
	run, created, err := s.agents.StartRun(routecontext.WithClaims(r.Context(), claims), agentapp.StartRunCommand{Actor: actor,
		AccountID: accountID, RequestID: operationID, BoardroomID: boardroomID, ConversationID: request.ConversationID,
		Subject: request.Subject, Prompt: request.Prompt, PersonaIDs: request.PersonaIDs})
	if err != nil {
		s.writeAgentError(w, "start_run", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/agent-runs/%s", accountID, run.Plan.RunID))
	writeJSON(w, status, agentRunView(run))
}

func (s *Server) agentRun(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, _, ok := s.agentRequest(w, r, false)
	if !ok {
		return
	}
	runID := ids.RunID(r.PathValue("runID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(runID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_query", "Agent Run query is invalid")
		return
	}
	run, err := s.agents.GetRun(routecontext.WithClaims(r.Context(), claims), actor, accountID, runID)
	if err != nil {
		s.writeAgentError(w, "get_run", err)
		return
	}
	writeJSON(w, http.StatusOK, agentRunView(run))
}

func (s *Server) agentRequest(w http.ResponseWriter, r *http.Request, command bool) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if s.agents == nil {
		writeProblem(w, http.StatusServiceUnavailable, "agents_unavailable", "Agents are not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	actor := access.Actor{}
	if claims.Authority.ActorKind == "user" {
		actor.UserID = ids.UserID(claims.Authority.ActorID)
	} else {
		actor.WorkloadID = claims.Authority.ActorID
	}
	if !command {
		return claims, actor, accountID, "", true
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return claims, actor, accountID, claims.Authority.OperationID, true
}

func decodeAgentJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Agent commands require application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_command", "the Agent command body is invalid")
		return false
	}
	return true
}

func (s *Server) writeAgentError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, agentapp.ErrInvalidCommand):
		writeProblem(w, http.StatusBadRequest, "invalid_agent_command", "the Agent command is invalid")
	case errors.Is(err, agentapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "agent_resource_not_found", "the Agent resource was not found")
	case errors.Is(err, agentapp.ErrConflict):
		writeProblem(w, http.StatusConflict, "agent_operation_conflict", "the operation conflicts with durable Agent state")
	case errors.Is(err, agentapp.ErrConstraint):
		writeProblem(w, http.StatusUnprocessableEntity, "agent_command_rejected", "the Agent command violates configuration or lifecycle rules")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this Agent operation")
	default:
		s.logger.Error("Agent operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "agents_unavailable", "the Agent operation could not be completed")
	}
}

type agentBoardroomResponse struct {
	ID        ids.BoardroomID            `json:"id"`
	Name      string                     `json:"name"`
	Purpose   string                     `json:"purpose"`
	State     agentdomain.BoardroomState `json:"state"`
	Version   uint64                     `json:"version"`
	CreatedAt time.Time                  `json:"created_at"`
	UpdatedAt time.Time                  `json:"updated_at"`
}

func agentBoardroomView(item agentdomain.Boardroom) agentBoardroomResponse {
	return agentBoardroomResponse{ID: item.ID, Name: item.Name, Purpose: item.Purpose, State: item.State, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

type agentPersonaResponse struct {
	ID                 ids.PersonaID             `json:"id"`
	BoardroomID        ids.BoardroomID           `json:"boardroom_id"`
	State              string                    `json:"state"`
	LatestVersion      uint64                    `json:"latest_version"`
	PersonaVersionID   ids.PersonaVersionID      `json:"persona_version_id"`
	Name               string                    `json:"name"`
	Role               string                    `json:"role"`
	Description        string                    `json:"description"`
	SystemInstructions string                    `json:"system_instructions"`
	Policy             agentdomain.PersonaPolicy `json:"policy"`
	ContentDigest      string                    `json:"content_digest"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
}

func agentPersonaView(item agentapp.PersonaSummary) agentPersonaResponse {
	return agentPersonaResponse{ID: item.ID, BoardroomID: item.BoardroomID, State: item.State, LatestVersion: item.LatestVersion,
		PersonaVersionID: item.Published.ID, Name: item.Published.Name, Role: item.Published.Role, Description: item.Published.Description,
		SystemInstructions: item.Published.SystemInstructions, Policy: item.Published.Policy, ContentDigest: hex.EncodeToString(item.Published.ContentDigest[:]),
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

type agentRunTurnResponse struct {
	Turn             uint32               `json:"turn"`
	PersonaID        ids.PersonaID        `json:"persona_id"`
	PersonaVersionID ids.PersonaVersionID `json:"persona_version_id"`
}
type agentRunResponse struct {
	ID                 ids.RunID               `json:"id"`
	BoardroomID        ids.BoardroomID         `json:"boardroom_id"`
	ConversationID     ids.ConversationID      `json:"conversation_id"`
	State              string                  `json:"state"`
	Subject            string                  `json:"subject"`
	Prompt             string                  `json:"prompt"`
	UserMessageID      ids.MessageID           `json:"user_message_id"`
	EntitlementVersion uint64                  `json:"entitlement_version"`
	PolicyVersion      uint64                  `json:"policy_version"`
	PlanDigest         string                  `json:"plan_digest"`
	Turns              []agentRunTurnResponse  `json:"turns"`
	InvocationIDs      []ids.AgentInvocationID `json:"invocation_ids"`
	CreatedAt          time.Time               `json:"created_at"`
}

func agentRunView(run agentapp.Run) agentRunResponse {
	turns := make([]agentRunTurnResponse, len(run.Plan.Turns))
	for index, turn := range run.Plan.Turns {
		turns[index] = agentRunTurnResponse{Turn: turn.Turn, PersonaID: turn.PersonaID, PersonaVersionID: turn.PersonaVersionID}
	}
	return agentRunResponse{ID: run.Plan.RunID, BoardroomID: run.Plan.BoardroomID, ConversationID: run.Plan.ConversationID,
		State: run.State, Subject: run.Subject, Prompt: run.Prompt, UserMessageID: run.UserMessageID,
		EntitlementVersion: run.Plan.EntitlementVersion, PolicyVersion: run.Plan.PolicyVersion,
		PlanDigest: hex.EncodeToString(run.Plan.Digest[:]), Turns: turns, InvocationIDs: run.InvocationIDs, CreatedAt: run.Plan.CreatedAt}
}
