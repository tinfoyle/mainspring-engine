package cellapi

import (
	"context"
	"encoding/base64"
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
	ResolveRun(context.Context, agentapp.ResolveRunCommand) (agentapp.RunResolution, bool, error)
	ListBoardrooms(context.Context, access.Actor, ids.AccountID, int) ([]agentdomain.Boardroom, error)
	ListPersonas(context.Context, access.Actor, ids.AccountID, ids.BoardroomID, int) ([]agentapp.PersonaSummary, error)
	ListConversations(context.Context, access.Actor, ids.AccountID, ids.BoardroomID, agentapp.ConversationListQuery) (agentapp.ConversationPage, error)
	GetConversation(context.Context, access.Actor, ids.AccountID, ids.ConversationID) (agentapp.Conversation, error)
	ListMessages(context.Context, access.Actor, ids.AccountID, ids.ConversationID, agentapp.MessageListQuery) (agentapp.MessagePage, error)
	GetRun(context.Context, access.Actor, ids.AccountID, ids.RunID) (agentapp.Run, error)
}

type createBoardroomRequest struct {
	Name    string  `json:"name"`
	Purpose *string `json:"purpose"`
}
type publishPersonaRequest struct {
	PersonaID             ids.PersonaID        `json:"persona_id"`
	ExpectedLatestVersion *uint64              `json:"expected_latest_version"`
	Name                  string               `json:"name"`
	Role                  string               `json:"role"`
	Description           *string              `json:"description"`
	SystemInstructions    string               `json:"system_instructions"`
	Policy                personaPolicyRequest `json:"policy"`
}
type personaPolicyRequest struct {
	Provider            string              `json:"provider"`
	Model               string              `json:"model"`
	ReasoningEffort     string              `json:"reasoning_effort,omitempty"`
	MaximumInputTokens  int64               `json:"maximum_input_tokens"`
	MaximumOutputTokens int64               `json:"maximum_output_tokens"`
	MaximumCostMicros   int64               `json:"maximum_cost_micros"`
	MaximumToolSteps    int                 `json:"maximum_tool_steps"`
	CitationPolicy      string              `json:"citation_policy"`
	ActionPolicy        string              `json:"action_policy"`
	Tools               *[]toolGrantRequest `json:"tools"`
}
type toolGrantRequest struct {
	Name        string          `json:"name"`
	Capability  string          `json:"capability"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}
type startAgentRunRequest struct {
	ConversationID ids.ConversationID `json:"conversation_id,omitempty"`
	Subject        *string            `json:"subject"`
	Prompt         string             `json:"prompt"`
	PersonaIDs     []ids.PersonaID    `json:"persona_ids"`
}
type resolveAgentRunRequest struct {
	Action agentapp.RunResolutionAction `json:"action"`
	Note   string                       `json:"note"`
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
	if request.Purpose == nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_command", "purpose is required")
		return
	}
	item, created, err := s.agents.CreateBoardroom(routecontext.WithClaims(r.Context(), claims), agentapp.CreateBoardroomCommand{Actor: actor, AccountID: accountID, RequestID: operationID, Name: request.Name, Purpose: *request.Purpose})
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
	if request.ExpectedLatestVersion == nil || request.Description == nil || request.Policy.Tools == nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_command", "expected_latest_version, description, and policy tools are required")
		return
	}
	item, created, err := s.agents.PublishPersona(routecontext.WithClaims(r.Context(), claims), agentapp.PublishPersonaCommand{
		Actor: actor, AccountID: accountID, BoardroomID: boardroomID, PersonaID: request.PersonaID,
		VersionID: ids.PersonaVersionID(operationID), ExpectedLatestVersion: *request.ExpectedLatestVersion,
		Name: request.Name, Role: request.Role, Description: *request.Description, SystemInstructions: request.SystemInstructions, Policy: request.Policy.domainPolicy(),
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

func (request personaPolicyRequest) domainPolicy() agentdomain.PersonaPolicy {
	tools := make([]agentdomain.ToolGrant, len(*request.Tools))
	for index, tool := range *request.Tools {
		tools[index] = agentdomain.ToolGrant{Name: tool.Name, Capability: tool.Capability, Description: tool.Description, InputSchema: tool.InputSchema}
	}
	return agentdomain.PersonaPolicy{
		Provider: request.Provider, Model: request.Model, ReasoningEffort: request.ReasoningEffort,
		MaximumInputTokens: request.MaximumInputTokens, MaximumOutputTokens: request.MaximumOutputTokens,
		MaximumCostMicros: request.MaximumCostMicros, MaximumToolSteps: request.MaximumToolSteps,
		CitationPolicy: request.CitationPolicy, ActionPolicy: request.ActionPolicy, Tools: tools,
	}
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
	if (request.ConversationID == "" && request.Subject == nil) || (request.ConversationID != "" && request.Subject != nil) {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_command", "subject is required only when creating a conversation")
		return
	}
	subject := ""
	if request.Subject != nil {
		subject = *request.Subject
	}
	run, created, err := s.agents.StartRun(routecontext.WithClaims(r.Context(), claims), agentapp.StartRunCommand{Actor: actor,
		AccountID: accountID, RequestID: operationID, BoardroomID: boardroomID, ConversationID: request.ConversationID,
		Subject: subject, Prompt: request.Prompt, PersonaIDs: request.PersonaIDs})
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

func (s *Server) agentRunResolve(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.agentRequest(w, r, true)
	if !ok {
		return
	}
	runID := ids.RunID(r.PathValue("runID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(runID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_command", "Agent Run resolution target is invalid")
		return
	}
	var request resolveAgentRunRequest
	if !decodeAgentJSON(w, r, &request) {
		return
	}
	resolution, created, err := s.agents.ResolveRun(routecontext.WithClaims(r.Context(), claims), agentapp.ResolveRunCommand{
		Actor: actor, AccountID: accountID, RequestID: operationID, RunID: runID, Action: request.Action, Note: request.Note,
	})
	if err != nil {
		s.writeAgentError(w, "resolve_run", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	targetRunID := resolution.RunID
	if resolution.RetryRunID != "" {
		targetRunID = resolution.RetryRunID
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/agent-runs/%s", accountID, targetRunID))
	writeJSON(w, status, agentRunResolutionView(resolution))
}

func (s *Server) agentConversations(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, _, ok := s.agentRequest(w, r, false)
	if !ok {
		return
	}
	boardroomID := ids.BoardroomID(r.PathValue("boardroomID"))
	query, err := parseAgentConversationQuery(r)
	if ids.Validate(string(boardroomID)) != nil || err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_query", "Agent Conversation query is invalid")
		return
	}
	page, err := s.agents.ListConversations(routecontext.WithClaims(r.Context(), claims), actor, accountID, boardroomID, query)
	if err != nil {
		s.writeAgentError(w, "list_conversations", err)
		return
	}
	items := make([]agentConversationResponse, len(page.Items))
	for index, item := range page.Items {
		items[index] = agentConversationView(item)
	}
	response := agentConversationPageResponse{Items: items}
	if page.NextCursor != nil {
		response.NextCursor, err = encodeAgentConversationCursor(*page.NextCursor)
		if err != nil {
			s.logger.Error("encode Agent Conversation cursor", "error", err)
			writeProblem(w, http.StatusInternalServerError, "internal_error", "the Agent Conversation page could not be encoded")
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) agentConversation(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, _, ok := s.agentRequest(w, r, false)
	if !ok {
		return
	}
	conversationID := ids.ConversationID(r.PathValue("conversationID"))
	if ids.Validate(string(conversationID)) != nil || len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_query", "Agent Conversation query is invalid")
		return
	}
	item, err := s.agents.GetConversation(routecontext.WithClaims(r.Context(), claims), actor, accountID, conversationID)
	if err != nil {
		s.writeAgentError(w, "get_conversation", err)
		return
	}
	writeJSON(w, http.StatusOK, agentConversationView(item))
}

func (s *Server) agentMessages(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, _, ok := s.agentRequest(w, r, false)
	if !ok {
		return
	}
	conversationID := ids.ConversationID(r.PathValue("conversationID"))
	query, err := parseAgentMessageQuery(r)
	if ids.Validate(string(conversationID)) != nil || err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_query", "Agent Message query is invalid")
		return
	}
	page, err := s.agents.ListMessages(routecontext.WithClaims(r.Context(), claims), actor, accountID, conversationID, query)
	if err != nil {
		s.writeAgentError(w, "list_messages", err)
		return
	}
	items := make([]agentMessageResponse, len(page.Items))
	for index, item := range page.Items {
		items[index] = agentMessageView(item)
	}
	response := agentMessagePageResponse{Items: items}
	if page.NextAfterSequence != nil {
		response.NextCursor, err = encodeAgentMessageCursor(*page.NextAfterSequence)
		if err != nil {
			s.logger.Error("encode Agent Message cursor", "error", err)
			writeProblem(w, http.StatusInternalServerError, "internal_error", "the Agent Message page could not be encoded")
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

type agentConversationCursorEnvelope struct {
	Version   int                `json:"v"`
	UpdatedAt time.Time          `json:"updated_at"`
	ID        ids.ConversationID `json:"id"`
}

type agentMessageCursorEnvelope struct {
	Version       int    `json:"v"`
	AfterSequence uint64 `json:"after_sequence"`
}

func parseAgentConversationQuery(r *http.Request) (agentapp.ConversationListQuery, error) {
	values := r.URL.Query()
	for key := range values {
		if key != "cursor" && key != "limit" {
			return agentapp.ConversationListQuery{}, agentapp.ErrInvalidCommand
		}
	}
	if len(values["cursor"]) > 1 || len(values["limit"]) > 1 {
		return agentapp.ConversationListQuery{}, agentapp.ErrInvalidCommand
	}
	limit, err := parseLimit(r, 50)
	if err != nil {
		return agentapp.ConversationListQuery{}, err
	}
	query := agentapp.ConversationListQuery{Limit: limit}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAgentConversationCursor(raw)
		if err != nil {
			return agentapp.ConversationListQuery{}, err
		}
		query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, cursor.ID
	}
	return query, nil
}

func parseAgentMessageQuery(r *http.Request) (agentapp.MessageListQuery, error) {
	values := r.URL.Query()
	for key := range values {
		if key != "cursor" && key != "limit" {
			return agentapp.MessageListQuery{}, agentapp.ErrInvalidCommand
		}
	}
	if len(values["cursor"]) > 1 || len(values["limit"]) > 1 {
		return agentapp.MessageListQuery{}, agentapp.ErrInvalidCommand
	}
	limit, err := parseLimit(r, 50)
	if err != nil {
		return agentapp.MessageListQuery{}, err
	}
	query := agentapp.MessageListQuery{Limit: limit}
	if raw := values.Get("cursor"); raw != "" {
		query.AfterSequence, err = decodeAgentMessageCursor(raw)
		if err != nil {
			return agentapp.MessageListQuery{}, err
		}
	}
	return query, nil
}

func encodeAgentConversationCursor(cursor agentapp.ConversationCursor) (string, error) {
	if cursor.UpdatedAt.IsZero() || ids.Validate(string(cursor.ID)) != nil {
		return "", agentapp.ErrInvalidCommand
	}
	raw, err := json.Marshal(agentConversationCursorEnvelope{Version: 1, UpdatedAt: cursor.UpdatedAt.UTC(), ID: cursor.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeAgentConversationCursor(raw string) (agentapp.ConversationCursor, error) {
	if len(raw) > 1024 {
		return agentapp.ConversationCursor{}, agentapp.ErrInvalidCommand
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return agentapp.ConversationCursor{}, agentapp.ErrInvalidCommand
	}
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	var value agentConversationCursorEnvelope
	if err := decoder.Decode(&value); err != nil || value.Version != 1 || value.UpdatedAt.IsZero() || ids.Validate(string(value.ID)) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return agentapp.ConversationCursor{}, agentapp.ErrInvalidCommand
	}
	return agentapp.ConversationCursor{UpdatedAt: value.UpdatedAt.UTC(), ID: value.ID}, nil
}

func encodeAgentMessageCursor(after uint64) (string, error) {
	if after == 0 {
		return "", agentapp.ErrInvalidCommand
	}
	raw, err := json.Marshal(agentMessageCursorEnvelope{Version: 1, AfterSequence: after})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeAgentMessageCursor(raw string) (uint64, error) {
	if len(raw) > 256 {
		return 0, agentapp.ErrInvalidCommand
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, agentapp.ErrInvalidCommand
	}
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	var value agentMessageCursorEnvelope
	if err := decoder.Decode(&value); err != nil || value.Version != 1 || value.AfterSequence == 0 || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return 0, agentapp.ErrInvalidCommand
	}
	return value.AfterSequence, nil
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
type agentRunInvocationResponse struct {
	ID               ids.AgentInvocationID `json:"id"`
	Turn             uint32                `json:"turn"`
	PersonaVersionID ids.PersonaVersionID  `json:"persona_version_id"`
	Status           string                `json:"status"`
	StartedAt        *time.Time            `json:"started_at,omitempty"`
	CompletedAt      *time.Time            `json:"completed_at,omitempty"`
}
type agentRunResolutionResponse struct {
	ID         ids.RunResolutionID          `json:"id"`
	RunID      ids.RunID                    `json:"run_id"`
	Action     agentapp.RunResolutionAction `json:"action"`
	Note       string                       `json:"note"`
	ActorID    ids.UserID                   `json:"actor_id"`
	RetryRunID ids.RunID                    `json:"retry_run_id,omitempty"`
	CreatedAt  time.Time                    `json:"created_at"`
}
type agentRunResponse struct {
	ID                 ids.RunID                    `json:"id"`
	BoardroomID        ids.BoardroomID              `json:"boardroom_id"`
	ConversationID     ids.ConversationID           `json:"conversation_id"`
	State              string                       `json:"state"`
	Subject            string                       `json:"subject"`
	Prompt             string                       `json:"prompt"`
	UserMessageID      ids.MessageID                `json:"user_message_id"`
	EntitlementVersion uint64                       `json:"entitlement_version"`
	PolicyVersion      uint64                       `json:"policy_version"`
	PlanDigest         string                       `json:"plan_digest"`
	Turns              []agentRunTurnResponse       `json:"turns"`
	InvocationIDs      []ids.AgentInvocationID      `json:"invocation_ids"`
	Invocations        []agentRunInvocationResponse `json:"invocations"`
	Resolutions        []agentRunResolutionResponse `json:"resolutions"`
	CreatedAt          time.Time                    `json:"created_at"`
}

func agentRunView(run agentapp.Run) agentRunResponse {
	turns := make([]agentRunTurnResponse, len(run.Plan.Turns))
	for index, turn := range run.Plan.Turns {
		turns[index] = agentRunTurnResponse{Turn: turn.Turn, PersonaID: turn.PersonaID, PersonaVersionID: turn.PersonaVersionID}
	}
	invocations := make([]agentRunInvocationResponse, len(run.Invocations))
	for index, invocation := range run.Invocations {
		invocations[index] = agentRunInvocationResponse{ID: invocation.ID, Turn: invocation.Turn, PersonaVersionID: invocation.PersonaVersionID,
			Status: invocation.Status, StartedAt: invocation.StartedAt, CompletedAt: invocation.CompletedAt}
	}
	resolutions := make([]agentRunResolutionResponse, len(run.Resolutions))
	for index, resolution := range run.Resolutions {
		resolutions[index] = agentRunResolutionView(resolution)
	}
	return agentRunResponse{ID: run.Plan.RunID, BoardroomID: run.Plan.BoardroomID, ConversationID: run.Plan.ConversationID,
		State: run.State, Subject: run.Subject, Prompt: run.Prompt, UserMessageID: run.UserMessageID,
		EntitlementVersion: run.Plan.EntitlementVersion, PolicyVersion: run.Plan.PolicyVersion,
		PlanDigest: hex.EncodeToString(run.Plan.Digest[:]), Turns: turns, InvocationIDs: run.InvocationIDs,
		Invocations: invocations, Resolutions: resolutions, CreatedAt: run.Plan.CreatedAt}
}

func agentRunResolutionView(item agentapp.RunResolution) agentRunResolutionResponse {
	return agentRunResolutionResponse{ID: item.ID, RunID: item.RunID, Action: item.Action, Note: item.Note,
		ActorID: item.ActorID, RetryRunID: item.RetryRunID, CreatedAt: item.CreatedAt}
}

type agentConversationResponse struct {
	ID           ids.ConversationID `json:"id"`
	BoardroomID  ids.BoardroomID    `json:"boardroom_id"`
	Subject      string             `json:"subject"`
	State        string             `json:"state"`
	MessageCount uint64             `json:"message_count"`
	CreatedBy    ids.UserID         `json:"created_by"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

type agentConversationPageResponse struct {
	Items      []agentConversationResponse `json:"items"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}

func agentConversationView(item agentapp.Conversation) agentConversationResponse {
	return agentConversationResponse{ID: item.ID, BoardroomID: item.BoardroomID, Subject: item.Subject, State: item.State,
		MessageCount: item.MessageCount, CreatedBy: item.CreatedBy, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

type agentMessageResponse struct {
	ID               ids.MessageID               `json:"id"`
	ConversationID   ids.ConversationID          `json:"conversation_id"`
	Sequence         uint64                      `json:"sequence"`
	Role             agentapp.MessageRole        `json:"role"`
	Body             string                      `json:"body"`
	CreatedBy        ids.UserID                  `json:"created_by,omitempty"`
	RunID            ids.RunID                   `json:"run_id,omitempty"`
	InvocationID     ids.AgentInvocationID       `json:"invocation_id,omitempty"`
	PersonaVersionID ids.PersonaVersionID        `json:"persona_version_id,omitempty"`
	Result           *agentdomain.ResultEnvelope `json:"result,omitempty"`
	CreatedAt        time.Time                   `json:"created_at"`
}

type agentMessagePageResponse struct {
	Items      []agentMessageResponse `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

func agentMessageView(item agentapp.Message) agentMessageResponse {
	return agentMessageResponse{ID: item.ID, ConversationID: item.ConversationID, Sequence: item.Sequence, Role: item.Role, Body: item.Body,
		CreatedBy: item.CreatedBy, RunID: item.RunID, InvocationID: item.InvocationID, PersonaVersionID: item.PersonaVersionID, Result: item.Result, CreatedAt: item.CreatedAt}
}
