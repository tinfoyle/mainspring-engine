package cellapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

const (
	agentAccount      = "11000000-0000-4000-8000-000000000001"
	agentUser         = "21000000-0000-4000-8000-000000000001"
	agentOperation    = "31000000-0000-4000-8000-000000000001"
	agentBoardroom    = "41000000-0000-4000-8000-000000000001"
	agentPersona      = "51000000-0000-4000-8000-000000000001"
	agentRun          = "61000000-0000-4000-8000-000000000001"
	agentRetryRun     = "62000000-0000-4000-8000-000000000002"
	agentConversation = "71000000-0000-4000-8000-000000000001"
	agentInvocation   = "91000000-0000-4000-8000-000000000001"
)

type agentTransportService struct {
	createCommand     agentapp.CreateBoardroomCommand
	managerCommand    agentapp.ConfigureBoardroomManagerCommand
	publishCommand    agentapp.PublishPersonaCommand
	runCommand        agentapp.StartRunCommand
	resolveCommand    agentapp.ResolveRunCommand
	conversationQuery agentapp.ConversationListQuery
	messageQuery      agentapp.MessageListQuery
	now               time.Time
}

func (service *agentTransportService) CreateBoardroom(_ context.Context, command agentapp.CreateBoardroomCommand) (agentdomain.Boardroom, bool, error) {
	service.createCommand = command
	return agentdomain.Boardroom{ID: ids.BoardroomID(command.RequestID), AccountID: command.AccountID, Name: command.Name, Purpose: command.Purpose, State: agentdomain.BoardroomActive, Version: 1, CreatedAt: service.now, UpdatedAt: service.now}, true, nil
}

func (service *agentTransportService) ConfigureBoardroomManager(_ context.Context, command agentapp.ConfigureBoardroomManagerCommand) (agentdomain.Boardroom, bool, error) {
	service.managerCommand = command
	return agentdomain.Boardroom{ID: command.BoardroomID, AccountID: command.AccountID, ManagerPersonaID: command.ManagerPersonaID, Name: "Operations Boardroom", Purpose: "Coordinate operational work", State: agentdomain.BoardroomActive, Version: command.ExpectedVersion + 1, CreatedAt: service.now, UpdatedAt: service.now}, true, nil
}

func (service *agentTransportService) PublishPersona(_ context.Context, command agentapp.PublishPersonaCommand) (agentapp.PersonaSummary, bool, error) {
	service.publishCommand = command
	policy := command.Policy
	policy.Tools = append(make([]agentdomain.ToolGrant, 0, len(policy.Tools)), policy.Tools...)
	policy.OutputSchema = agentdomain.ResultSchema()
	return agentapp.PersonaSummary{ID: command.PersonaID, BoardroomID: command.BoardroomID, State: "active", LatestVersion: 1,
		Published: agentdomain.PersonaVersion{PersonaVersionDraft: agentdomain.PersonaVersionDraft{ID: command.VersionID, PersonaID: command.PersonaID,
			AccountID: command.AccountID, Version: 1, Name: command.Name, Role: command.Role, Description: command.Description,
			SystemInstructions: command.SystemInstructions, Policy: policy, CreatedBy: command.Actor.UserID, CreatedAt: service.now}},
		CreatedAt: service.now, UpdatedAt: service.now}, true, nil
}

func (service *agentTransportService) StartRun(_ context.Context, command agentapp.StartRunCommand) (agentapp.Run, bool, error) {
	service.runCommand = command
	return agentapp.Run{Plan: agentdomain.RunPlan{RunID: ids.RunID(command.RequestID), AccountID: command.AccountID,
		BoardroomID: command.BoardroomID, ConversationID: ids.ConversationID(agentConversation), EntitlementVersion: 3,
		PolicyVersion: 1, Turns: []agentdomain.PlannedTurn{{Turn: 1, PersonaID: ids.PersonaID(agentPersona), PersonaVersionID: ids.PersonaVersionID(agentOperation)}},
		CreatedBy: command.Actor.UserID, CreatedAt: service.now, Digest: [32]byte{1}}, Mode: command.Mode, State: "planned",
		Subject: command.Subject, Prompt: command.Prompt, UserMessageID: ids.MessageID("81000000-0000-4000-8000-000000000001"),
		InvocationIDs: []ids.AgentInvocationID{ids.AgentInvocationID(agentInvocation)}, Invocations: []agentapp.RunInvocation{{ID: ids.AgentInvocationID(agentInvocation), Turn: 1, PersonaVersionID: ids.PersonaVersionID(agentOperation), Status: "queued"}}, Resolutions: []agentapp.RunResolution{}}, true, nil
}

func (service *agentTransportService) ResolveRun(_ context.Context, command agentapp.ResolveRunCommand) (agentapp.RunResolution, bool, error) {
	service.resolveCommand = command
	return agentapp.RunResolution{ID: ids.RunResolutionID(command.RequestID), RunID: command.RunID, Action: command.Action,
		Note: strings.TrimSpace(command.Note), ActorID: command.Actor.UserID, RetryRunID: ids.RunID(agentRetryRun), CreatedAt: service.now}, true, nil
}

func (*agentTransportService) ListBoardrooms(context.Context, access.Actor, ids.AccountID, int) ([]agentdomain.Boardroom, error) {
	return []agentdomain.Boardroom{}, nil
}

func (*agentTransportService) ListPersonas(context.Context, access.Actor, ids.AccountID, ids.BoardroomID, int) ([]agentapp.PersonaSummary, error) {
	return []agentapp.PersonaSummary{}, nil
}

func (service *agentTransportService) ListConversations(_ context.Context, _ access.Actor, accountID ids.AccountID, boardroomID ids.BoardroomID, query agentapp.ConversationListQuery) (agentapp.ConversationPage, error) {
	service.conversationQuery = query
	item := agentapp.Conversation{ID: ids.ConversationID(agentConversation), AccountID: accountID, BoardroomID: boardroomID, Subject: "Weekly review", State: "open", MessageCount: 2, CreatedBy: ids.UserID(agentUser), CreatedAt: service.now, UpdatedAt: service.now}
	return agentapp.ConversationPage{Items: []agentapp.Conversation{item}, NextCursor: &agentapp.ConversationCursor{UpdatedAt: service.now, ID: item.ID}}, nil
}

func (service *agentTransportService) GetConversation(_ context.Context, _ access.Actor, accountID ids.AccountID, conversationID ids.ConversationID) (agentapp.Conversation, error) {
	return agentapp.Conversation{ID: conversationID, AccountID: accountID, BoardroomID: ids.BoardroomID(agentBoardroom), Subject: "Weekly review", State: "open", MessageCount: 2, CreatedBy: ids.UserID(agentUser), CreatedAt: service.now, UpdatedAt: service.now}, nil
}

func (service *agentTransportService) ListMessages(_ context.Context, _ access.Actor, _ ids.AccountID, conversationID ids.ConversationID, query agentapp.MessageListQuery) (agentapp.MessagePage, error) {
	service.messageQuery = query
	result := agentdomain.ResultEnvelope{Contribution: "Focus on the overdue review.", Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []agentdomain.Citation{}, ProposedActions: []agentdomain.ProposedAction{}, Delegations: []agentdomain.Delegation{}, Confidence: agentdomain.ConfidenceHigh}
	next := uint64(2)
	return agentapp.MessagePage{Items: []agentapp.Message{
		{ID: "81000000-0000-4000-8000-000000000001", ConversationID: conversationID, Sequence: 1, Role: agentapp.MessageRoleUser, Body: "What should we prioritize?", CreatedBy: ids.UserID(agentUser), CreatedAt: service.now},
		{ID: "82000000-0000-4000-8000-000000000002", ConversationID: conversationID, Sequence: 2, Role: agentapp.MessageRolePersona, Body: result.Contribution, RunID: ids.RunID(agentRun), InvocationID: ids.AgentInvocationID(agentInvocation), PersonaVersionID: ids.PersonaVersionID(agentOperation), Result: &result, CreatedAt: service.now},
	}, NextAfterSequence: &next}, nil
}

func (service *agentTransportService) GetRun(_ context.Context, _ access.Actor, accountID ids.AccountID, runID ids.RunID) (agentapp.Run, error) {
	return agentapp.Run{Plan: agentdomain.RunPlan{RunID: runID, AccountID: accountID, BoardroomID: ids.BoardroomID(agentBoardroom),
		ConversationID: ids.ConversationID(agentConversation), EntitlementVersion: 3, PolicyVersion: 1,
		Turns:     []agentdomain.PlannedTurn{{Turn: 1, PersonaID: ids.PersonaID(agentPersona), PersonaVersionID: ids.PersonaVersionID(agentOperation)}},
		CreatedBy: ids.UserID(agentUser), CreatedAt: service.now, Digest: [32]byte{1}}, Mode: agentapp.RunModeSelected, State: "planned", Subject: "Weekly review",
		Prompt: "What should we prioritize?", UserMessageID: ids.MessageID("81000000-0000-4000-8000-000000000001"),
		InvocationIDs: []ids.AgentInvocationID{ids.AgentInvocationID(agentInvocation)}, Invocations: []agentapp.RunInvocation{{ID: ids.AgentInvocationID(agentInvocation), Turn: 1, PersonaVersionID: ids.PersonaVersionID(agentOperation), Status: "queued"}}, Resolutions: []agentapp.RunResolution{}}, nil
}

func TestAgentCommandContractsBindRoutedOperationAndExposeExplicitViews(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	service := &agentTransportService{now: now}
	server, err := New(claimAcceptor{claims: agentClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAgents(service))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}

	create := agentCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms", agentOperation,
		`{"name":"Operations Boardroom","purpose":"Coordinate operational work"}`)
	if create.Code != http.StatusCreated || create.Header().Get("Location") != "/api/v1/accounts/"+agentAccount+"/agent-boardrooms/"+agentOperation ||
		!strings.Contains(create.Body.String(), `"created_at":"2026-08-18T12:00:00Z"`) || strings.Contains(create.Body.String(), "AccountID") {
		t.Fatalf("create=%d location=%q body=%s", create.Code, create.Header().Get("Location"), create.Body.String())
	}
	if service.createCommand.RequestID != agentOperation || service.createCommand.Actor.UserID != ids.UserID(agentUser) {
		t.Fatalf("create command=%+v", service.createCommand)
	}
	managerTarget := "/api/v1/accounts/" + agentAccount + "/agent-boardrooms/" + agentBoardroom + "/manager"
	manager := agentCommandRequest(server.Handler(), http.MethodPut, managerTarget, agentOperation,
		`{"manager_persona_id":"`+agentPersona+`","expected_version":1}`)
	if manager.Code != http.StatusOK || service.managerCommand.ExpectedVersion != 1 || service.managerCommand.ManagerPersonaID != ids.PersonaID(agentPersona) || !strings.Contains(manager.Body.String(), `"manager_persona_id":"`+agentPersona+`"`) {
		t.Fatalf("manager=%d body=%s command=%+v", manager.Code, manager.Body.String(), service.managerCommand)
	}
	if err := contract.ValidateResponse(http.MethodPut, managerTarget, manager.Code, manager.Header(), manager.Body.Bytes()); err != nil {
		t.Fatal(err)
	}

	personaBody := `{"persona_id":"` + agentPersona + `","expected_latest_version":0,"name":"Operations Lead","role":"Operations","description":"Coordinates work","system_instructions":"Review evidence and report a recommendation.","policy":{"provider":"openai","model":"gpt-5","fallback_models":[],"maximum_input_tokens":128000,"maximum_output_tokens":4096,"maximum_cost_micros":100000,"maximum_tool_steps":0,"citation_policy":"best_effort","action_policy":"propose","tools":[]}}`
	publish := agentCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms/"+agentBoardroom+"/personas", agentOperation, personaBody)
	if publish.Code != http.StatusCreated || !strings.Contains(publish.Body.String(), `"persona_version_id":"`+agentOperation+`"`) ||
		!strings.Contains(publish.Body.String(), `"tools":[]`) || !strings.Contains(publish.Body.String(), `"output_schema":{`) || service.publishCommand.VersionID != ids.PersonaVersionID(agentOperation) {
		t.Fatalf("publish=%d body=%s command=%+v", publish.Code, publish.Body.String(), service.publishCommand)
	}

	run := agentCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms/"+agentBoardroom+"/runs", agentOperation,
		`{"subject":"Weekly review","prompt":"What should we prioritize?","mode":"selected","persona_ids":["`+agentPersona+`"],"context":{"knowledge_document_ids":["66000000-0000-4000-8000-000000000006"]}}`)
	if run.Code != http.StatusAccepted || run.Header().Get("Location") != "/api/v1/accounts/"+agentAccount+"/agent-runs/"+agentOperation ||
		service.runCommand.RequestID != agentOperation || !strings.Contains(run.Body.String(), `"state":"planned"`) ||
		!strings.Contains(run.Body.String(), `"turns":[{`) || !strings.Contains(run.Body.String(), `"invocation_ids":["`+agentInvocation+`"]`) || service.runCommand.Mode != agentapp.RunModeSelected || len(service.runCommand.Context.KnowledgeDocumentIDs) != 1 {
		t.Fatalf("run=%d location=%q body=%s command=%+v", run.Code, run.Header().Get("Location"), run.Body.String(), service.runCommand)
	}

	resolutionTarget := "/api/v1/accounts/" + agentAccount + "/agent-runs/" + agentRun + "/resolutions"
	resolution := agentCommandRequest(server.Handler(), http.MethodPost, resolutionTarget, agentOperation,
		`{"action":"retry_failed","note":"Retry the provider failure"}`)
	if resolution.Code != http.StatusCreated || resolution.Header().Get("Location") != "/api/v1/accounts/"+agentAccount+"/agent-runs/"+agentRetryRun ||
		service.resolveCommand.RunID != ids.RunID(agentRun) || service.resolveCommand.Action != agentapp.RunResolutionRetryFailed ||
		!strings.Contains(resolution.Body.String(), `"retry_run_id":"`+agentRetryRun+`"`) {
		t.Fatalf("resolution=%d location=%q body=%s command=%+v", resolution.Code, resolution.Header().Get("Location"), resolution.Body.String(), service.resolveCommand)
	}
	if err := contract.ValidateResponse(http.MethodPost, resolutionTarget, resolution.Code, resolution.Header(), resolution.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestPersonaPublicationRequiresVersionAndRejectsCallerOwnedOutputSchema(t *testing.T) {
	service := &agentTransportService{now: time.Now().UTC()}
	server, _ := New(claimAcceptor{claims: agentClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAgents(service))
	base := `{"persona_id":"` + agentPersona + `","name":"Operations Lead","role":"Operations","description":"Coordinates work","system_instructions":"Review evidence and report a recommendation.","policy":{"provider":"openai","model":"gpt-5","fallback_models":[],"maximum_input_tokens":128000,"maximum_output_tokens":4096,"maximum_cost_micros":100000,"maximum_tool_steps":0,"citation_policy":"best_effort","action_policy":"propose","tools":[]`
	missingVersion := agentCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms/"+agentBoardroom+"/personas", agentOperation, base+`}}`)
	if missingVersion.Code != http.StatusBadRequest || service.publishCommand.PersonaID != "" {
		t.Fatalf("missing version=%d body=%s command=%+v", missingVersion.Code, missingVersion.Body.String(), service.publishCommand)
	}
	callerSchema := strings.Replace(base, `"name":"Operations Lead"`, `"expected_latest_version":0,"name":"Operations Lead"`, 1) + `,"output_schema":{}}}`
	rejected := agentCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms/"+agentBoardroom+"/personas", agentOperation, callerSchema)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "invalid_agent_command") {
		t.Fatalf("caller output schema=%d body=%s", rejected.Code, rejected.Body.String())
	}
}

func TestAgentCommandsRequireExplicitEmptyCapableFields(t *testing.T) {
	service := &agentTransportService{now: time.Now().UTC()}
	server, _ := New(claimAcceptor{claims: agentClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAgents(service))
	missingPurpose := agentCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms", agentOperation, `{"name":"Operations"}`)
	if missingPurpose.Code != http.StatusBadRequest || service.createCommand.RequestID != "" {
		t.Fatalf("missing purpose=%d body=%s command=%+v", missingPurpose.Code, missingPurpose.Body.String(), service.createCommand)
	}
	missingTools := agentCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms/"+agentBoardroom+"/personas", agentOperation,
		`{"persona_id":"`+agentPersona+`","expected_latest_version":0,"name":"Operations Lead","role":"Operations","description":"","system_instructions":"Review evidence and report a recommendation.","policy":{"provider":"openai","model":"gpt-5","maximum_input_tokens":128000,"maximum_output_tokens":4096,"maximum_cost_micros":100000,"maximum_tool_steps":0,"citation_policy":"best_effort","action_policy":"propose"}}`)
	if missingTools.Code != http.StatusBadRequest || service.publishCommand.PersonaID != "" {
		t.Fatalf("missing tools=%d body=%s command=%+v", missingTools.Code, missingTools.Body.String(), service.publishCommand)
	}
	missingManagerVersion := agentCommandRequest(server.Handler(), http.MethodPut, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms/"+agentBoardroom+"/manager", agentOperation,
		`{"manager_persona_id":"`+agentPersona+`"}`)
	if missingManagerVersion.Code != http.StatusBadRequest || service.managerCommand.BoardroomID != "" {
		t.Fatalf("missing manager version=%d body=%s command=%+v", missingManagerVersion.Code, missingManagerVersion.Body.String(), service.managerCommand)
	}
	missingMode := agentCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms/"+agentBoardroom+"/runs", agentOperation,
		`{"subject":"Weekly review","prompt":"Review priorities","persona_ids":["`+agentPersona+`"]}`)
	if missingMode.Code != http.StatusBadRequest || service.runCommand.RequestID != "" {
		t.Fatalf("missing run mode=%d body=%s command=%+v", missingMode.Code, missingMode.Body.String(), service.runCommand)
	}
}

func TestAgentRunSubjectBelongsOnlyToNewConversation(t *testing.T) {
	service := &agentTransportService{now: time.Now().UTC()}
	server, _ := New(claimAcceptor{claims: agentClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAgents(service))
	target := "/api/v1/accounts/" + agentAccount + "/agent-boardrooms/" + agentBoardroom + "/runs"
	missing := agentCommandRequest(server.Handler(), http.MethodPost, target, agentOperation, `{"prompt":"Review priorities","mode":"selected","persona_ids":["`+agentPersona+`"]}`)
	if missing.Code != http.StatusBadRequest || service.runCommand.RequestID != "" {
		t.Fatalf("new conversation without subject=%d body=%s", missing.Code, missing.Body.String())
	}
	ambiguous := agentCommandRequest(server.Handler(), http.MethodPost, target, agentOperation,
		`{"conversation_id":"`+agentConversation+`","subject":"Ignored subject","prompt":"Review priorities","mode":"selected","persona_ids":["`+agentPersona+`"]}`)
	if ambiguous.Code != http.StatusBadRequest || service.runCommand.RequestID != "" {
		t.Fatalf("existing conversation with subject=%d body=%s", ambiguous.Code, ambiguous.Body.String())
	}
	existing := agentCommandRequest(server.Handler(), http.MethodPost, target, agentOperation,
		`{"conversation_id":"`+agentConversation+`","prompt":"Review priorities","mode":"selected","persona_ids":["`+agentPersona+`"]}`)
	if existing.Code != http.StatusAccepted || service.runCommand.ConversationID != ids.ConversationID(agentConversation) || service.runCommand.Subject != "" {
		t.Fatalf("existing conversation=%d body=%s command=%+v", existing.Code, existing.Body.String(), service.runCommand)
	}
}

func TestAgentQueryContractsExposeBoundedCollectionsAndRunView(t *testing.T) {
	service := &agentTransportService{now: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)}
	server, _ := New(claimAcceptor{claims: agentClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAgents(service))
	for _, target := range []string{
		"/api/v1/accounts/" + agentAccount + "/agent-boardrooms",
		"/api/v1/accounts/" + agentAccount + "/agent-boardrooms/" + agentBoardroom + "/personas",
	} {
		response := routedRequest(t, server.Handler(), target)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"items":[]`) {
			t.Fatalf("collection %s=%d body=%s", target, response.Code, response.Body.String())
		}
	}
	run := routedRequest(t, server.Handler(), "/api/v1/accounts/"+agentAccount+"/agent-runs/"+agentRun)
	if run.Code != http.StatusOK || !strings.Contains(run.Body.String(), `"id":"`+agentRun+`"`) ||
		!strings.Contains(run.Body.String(), `"plan_digest":"01`) || !strings.Contains(run.Body.String(), `"invocation_ids":["`+agentInvocation+`"]`) {
		t.Fatalf("run=%d body=%s", run.Code, run.Body.String())
	}
}

func TestAgentConversationAndMessageReadContracts(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	service := &agentTransportService{now: now}
	server, _ := New(claimAcceptor{claims: agentClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAgents(service))
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}

	conversationsTarget := "/api/v1/accounts/" + agentAccount + "/agent-boardrooms/" + agentBoardroom + "/conversations?limit=25"
	conversations := routedRequest(t, server.Handler(), conversationsTarget)
	if conversations.Code != http.StatusOK || service.conversationQuery.Limit != 25 || !strings.Contains(conversations.Body.String(), `"message_count":2`) || !strings.Contains(conversations.Body.String(), `"next_cursor":"`) {
		t.Fatalf("conversations=%d query=%+v body=%s", conversations.Code, service.conversationQuery, conversations.Body.String())
	}
	if err := contract.ValidateResponse(http.MethodGet, conversationsTarget, conversations.Code, conversations.Header(), conversations.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	conversationTarget := "/api/v1/accounts/" + agentAccount + "/agent-conversations/" + agentConversation
	conversation := routedRequest(t, server.Handler(), conversationTarget)
	if conversation.Code != http.StatusOK || !strings.Contains(conversation.Body.String(), `"subject":"Weekly review"`) {
		t.Fatalf("conversation=%d body=%s", conversation.Code, conversation.Body.String())
	}
	if err := contract.ValidateResponse(http.MethodGet, conversationTarget, conversation.Code, conversation.Header(), conversation.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	messagesTarget := conversationTarget + "/messages?limit=20"
	messages := routedRequest(t, server.Handler(), messagesTarget)
	if messages.Code != http.StatusOK || service.messageQuery.Limit != 20 || !strings.Contains(messages.Body.String(), `"role":"user"`) || !strings.Contains(messages.Body.String(), `"role":"persona"`) || !strings.Contains(messages.Body.String(), `"confidence":"high"`) || !strings.Contains(messages.Body.String(), `"next_cursor":"`) {
		t.Fatalf("messages=%d query=%+v body=%s", messages.Code, service.messageQuery, messages.Body.String())
	}
	if err := contract.ValidateResponse(http.MethodGet, messagesTarget, messages.Code, messages.Header(), messages.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if json.Unmarshal(messages.Body.Bytes(), &page) != nil || page.NextCursor == "" {
		t.Fatalf("message cursor body=%s", messages.Body.String())
	}
	next := routedRequest(t, server.Handler(), "/api/v1/accounts/"+agentAccount+"/agent-conversations/"+agentConversation+"/messages?cursor="+page.NextCursor)
	if next.Code != http.StatusOK || service.messageQuery.AfterSequence != 2 || service.messageQuery.Limit != 50 {
		t.Fatalf("next messages=%d query=%+v body=%s", next.Code, service.messageQuery, next.Body.String())
	}
	invalid := routedRequest(t, server.Handler(), "/api/v1/accounts/"+agentAccount+"/agent-conversations/"+agentConversation+"/messages?unknown=true")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_agent_query") {
		t.Fatalf("invalid messages=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestAgentRoutesRejectMismatchedIdempotencyAndCrossAccountAuthority(t *testing.T) {
	service := &agentTransportService{now: time.Now().UTC()}
	server, _ := New(claimAcceptor{claims: agentClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAgents(service))
	mismatch := agentCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+agentAccount+"/agent-boardrooms", "32000000-0000-4000-8000-000000000002", `{"name":"Operations","purpose":"Work"}`)
	if mismatch.Code != http.StatusBadRequest || !strings.Contains(mismatch.Body.String(), "invalid_idempotency_key") {
		t.Fatalf("mismatch=%d %s", mismatch.Code, mismatch.Body.String())
	}
	crossAccount := routedRequest(t, server.Handler(), "/api/v1/accounts/12000000-0000-4000-8000-000000000002/agent-boardrooms")
	if crossAccount.Code != http.StatusNotFound {
		t.Fatalf("cross account=%d %s", crossAccount.Code, crossAccount.Body.String())
	}
}

func agentCommandRequest(handler http.Handler, method, target, operationID, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func agentClaims() routecontext.Claims {
	return routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(agentAccount), ActorKind: "user", ActorID: agentUser,
		Role: "owner", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 3, OperationID: agentOperation,
		PackageAccess: &routecontext.PackageAccess{Code: "agents", Version: 1, Mode: "enabled"}}}
}

var _ AgentService = (*agentTransportService)(nil)
