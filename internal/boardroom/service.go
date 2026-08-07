package boardroom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
)

type Service struct {
	logger            *slog.Logger
	store             *Store
	provider          agent.Provider
	tenantID          domain.TenantID
	timeout           time.Duration
	issuer            *toolbroker.TokenIssuer
	broker            *toolbroker.Broker
	approvals         *toolbroker.ApprovalService
	usage             *UsageService
	inputTokenBudget  int64
	outputTokenBudget int64
	costBudgetMicros  int64
}

func (s *Service) SetApprovalService(approvals *toolbroker.ApprovalService) { s.approvals = approvals }
func (s *Service) SetUsageService(usage *UsageService)                      { s.usage = usage }
func (s *Service) SetBudgets(inputTokens, outputTokens, costMicros int64) {
	if inputTokens > 0 {
		s.inputTokenBudget = inputTokens
	}
	if outputTokens > 0 {
		s.outputTokenBudget = outputTokens
	}
	if costMicros >= 0 {
		s.costBudgetMicros = costMicros
	}
}

func NewService(logger *slog.Logger, store *Store, provider agent.Provider, tenantID domain.TenantID, timeout time.Duration, issuer *toolbroker.TokenIssuer, brokers ...*toolbroker.Broker) *Service {
	service := &Service{logger: logger, store: store, provider: provider, tenantID: tenantID, timeout: timeout, issuer: issuer}
	service.inputTokenBudget = 12000
	service.outputTokenBudget = 2000
	if len(brokers) > 0 {
		service.broker = brokers[0]
	}
	return service
}

func (s *Service) CreateScheduledRun(ctx context.Context, boardroomID domain.BoardroomID, workflowID, title, prompt, scheduleID string) (Run, error) {
	return s.store.CreateScheduledRun(ctx, boardroomID, workflowID, title, prompt, scheduleID)
}

func (s *Service) PrepareRun(ctx context.Context, runID domain.RunID) (RunPlan, error) {
	return s.store.PrepareRun(ctx, runID)
}

// ExecuteRun is the local dispatcher's application-owned equivalent of the
// per-turn Temporal workflow. It uses the same durable run plan and invocation
// records, so switching orchestration modes does not change execution semantics.
func (s *Service) ExecuteRun(ctx context.Context, runID domain.RunID) error {
	plan, err := s.PrepareRun(ctx, runID)
	if err != nil {
		return s.fail(ctx, runID, err)
	}
	for _, persona := range plan.Personas {
		if err := s.ExecuteTurn(ctx, runID, persona.TurnNumber); err != nil {
			return s.fail(ctx, runID, err)
		}
	}
	return s.CompleteRun(ctx, runID)
}

func (s *Service) ExecuteTurn(ctx context.Context, runID domain.RunID, turnNumber int) error {
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == domain.RunCompleted || run.Status == domain.RunCanceled {
		return nil
	}
	plan, err := s.store.GetRunPlan(ctx, runID)
	if err != nil {
		return err
	}
	if turnNumber < 1 || turnNumber > len(plan.Personas) {
		return fmt.Errorf("turn %d is outside the prepared run plan", turnNumber)
	}
	planned := plan.Personas[turnNumber-1]
	settings := planned.Settings
	if settings.ContextTokenLimit <= 0 {
		settings = DefaultAgentSettings()
	}
	providerKey := s.provider.Name()
	if settings.Provider != "" && settings.Provider != "inherit" {
		providerKey += ":" + settings.Provider
	}
	messages, err := s.store.ConversationMessages(ctx, run.ConversationID)
	if err != nil {
		return err
	}
	conversation, manifest := BuildContext(messages, int(settings.ContextTokenLimit))
	record, persona, err := s.store.StartInvocation(ctx, runID, turnNumber, providerKey, manifest)
	if err != nil {
		return err
	}
	if record.Status == "succeeded" {
		if s.approvals != nil {
			return s.approvals.EnsureInvocationActions(ctx, record.ID)
		}
		return nil
	}
	if s.usage != nil {
		if err := s.usage.Reserve(ctx, record.ID, providerKey, settings.ContextTokenLimit+settings.MaxOutputTokens, settings.MaxCostMicros); err != nil {
			if errors.Is(err, ErrInvocationCapacity) || errors.Is(err, ErrProviderCircuitOpen) {
				_ = s.store.SetRunStatus(ctx, runID, domain.RunQueued, err.Error())
			}
			invocationErr := CapacityInvocationError(err)
			category, _ := agent.Failure(invocationErr)
			_ = s.store.FailInvocation(ctx, record.ID, string(category), invocationErr.Error())
			return invocationErr
		}
		_ = s.store.SetRunStatus(ctx, runID, domain.RunRunning, "")
	}
	invocation := agent.Invocation{
		ID: record.ID, TenantID: s.tenantID, BoardroomID: run.BoardroomID, RunID: runID,
		PersonaID: persona.PersonaID, PersonaName: persona.Name, PersonaRole: persona.Role, PersonaDescription: persona.Description,
		SystemInstructions: persona.SystemInstructions, Conversation: conversation, ToolGrants: persona.Grants,
		OutputSchema: persona.OutputSchema, Timeout: time.Duration(settings.TimeoutSeconds) * time.Second,
		MaxInputTokens: settings.ContextTokenLimit, MaxOutputTokens: settings.MaxOutputTokens, MaxCostMicros: settings.MaxCostMicros,
		Provider: settings.Provider, Model: settings.Model, ReasoningEffort: settings.ReasoningEffort,
		Temperature: settings.Temperature, TopP: settings.TopP, ResponseStyle: settings.ResponseStyle,
		CitationPolicy: settings.CitationPolicy, ActionPolicy: settings.ActionPolicy,
	}
	if s.broker != nil {
		for _, definition := range s.broker.Definitions(persona.Grants) {
			invocation.Tools = append(invocation.Tools, agent.ToolDefinition{Name: definition.Name, Description: definition.Description, InputSchema: definition.InputSchema})
		}
	}
	if s.issuer != nil {
		expiresAt := time.Now().Add(invocation.Timeout)
		if invocation.Timeout <= 0 {
			expiresAt = time.Now().Add(5 * time.Minute)
		}
		invocation.CapabilityToken, err = s.issuer.Mint(domain.InvocationContext{
			TenantID: s.tenantID, BoardroomID: run.BoardroomID, RunID: runID, PersonaID: persona.PersonaID,
			InvocationID: record.ID, Grants: persona.Grants, ExpiresAt: expiresAt,
		})
		if err != nil {
			_ = s.store.FailInvocation(ctx, record.ID, string(agent.FailureUnknown), err.Error())
			return fmt.Errorf("mint capability token for %s: %w", persona.Name, err)
		}
	}
	result, citations, err := s.invokeWithTools(ctx, invocation, settings.MaxToolCalls)
	if err != nil {
		if s.usage != nil {
			s.usage.Release(ctx, record.ID)
			s.usage.RecordProviderResult(ctx, providerKey, err)
		}
		category, _ := agent.Failure(err)
		_ = s.store.FailInvocation(ctx, record.ID, string(category), err.Error())
		return fmt.Errorf("invoke %s: %w", persona.Name, err)
	}
	if err := validateCitations(result.Structured.Citations, citations); err != nil {
		invocationErr := &agent.InvocationError{Category: agent.FailureInvalidOutput, Err: err}
		_ = s.store.FailInvocation(ctx, record.ID, string(agent.FailureInvalidOutput), err.Error())
		return invocationErr
	}
	if err := enforceCitationPolicy(settings.CitationPolicy, result.Structured.Citations, citations); err != nil {
		invocationErr := &agent.InvocationError{Category: agent.FailureInvalidOutput, Err: err}
		_ = s.store.FailInvocation(ctx, record.ID, string(agent.FailureInvalidOutput), err.Error())
		return invocationErr
	}
	if settings.ActionPolicy == "disabled" {
		result.Structured.ProposedActions = []agent.ProposedAction{}
	}
	result.Body = result.Structured.Contribution
	if err := s.store.CompleteInvocation(ctx, record, persona, result, settings.MaxCostMicros); err != nil {
		if s.usage != nil {
			s.usage.Release(ctx, record.ID)
		}
		return err
	}
	if s.usage != nil {
		if err := s.usage.Reconcile(ctx, record.ID, result.Usage, settings.MaxCostMicros); err != nil {
			return err
		}
		s.usage.RecordProviderResult(ctx, providerKey, nil)
	}
	if s.approvals != nil {
		if err := s.approvals.EnsureInvocationActions(ctx, record.ID); err != nil {
			return err
		}
	}
	return nil
}

type returnedCitation struct {
	DocumentID string
	ChunkID    string
}

func (s *Service) invokeWithTools(ctx context.Context, invocation agent.Invocation, maximumToolCalls int) (agent.Result, map[string]returnedCitation, error) {
	citations := make(map[string]returnedCitation)
	requestIDs := make(map[string]bool)
	toolCalls := 0
	if maximumToolCalls < 0 {
		maximumToolCalls = 0
	}
	for {
		result, err := s.provider.Invoke(ctx, invocation)
		if err != nil {
			return agent.Result{}, citations, err
		}
		if result.Structured.Contribution == "" {
			result.Structured = agent.ResultEnvelope{
				Contribution: result.Body, Findings: []string{}, Recommendations: []string{}, Questions: []string{},
				Citations: []agent.Citation{}, ProposedActions: []agent.ProposedAction{}, ToolRequests: []agent.ToolRequest{}, Confidence: "medium",
			}
		}
		if err := result.Structured.Validate(); err != nil {
			return agent.Result{}, citations, &agent.InvocationError{Category: agent.FailureInvalidOutput, Err: err}
		}
		if len(result.Structured.ToolRequests) == 0 {
			return result, citations, nil
		}
		if s.broker == nil || invocation.CapabilityToken == "" {
			return agent.Result{}, citations, &agent.InvocationError{Category: agent.FailureTool, Err: fmt.Errorf("provider requested a tool but no tool broker is available")}
		}
		for _, request := range result.Structured.ToolRequests {
			toolCalls++
			if toolCalls > maximumToolCalls {
				return agent.Result{}, citations, &agent.InvocationError{Category: agent.FailureTool, Err: fmt.Errorf("invocation exceeded the %d tool-call limit", maximumToolCalls)}
			}
			if requestIDs[request.ID] {
				return agent.Result{}, citations, &agent.InvocationError{Category: agent.FailureTool, Err: fmt.Errorf("duplicate tool request id %q", request.ID)}
			}
			requestIDs[request.ID] = true
			_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.requested", request)
			output, err := s.broker.InvokeNamed(ctx, invocation.CapabilityToken, s.tenantID, request.Name, request.Arguments)
			if err != nil {
				_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.failed", map[string]any{"request_id": request.ID, "name": request.Name, "error": err.Error()})
				return agent.Result{}, citations, &agent.InvocationError{Category: agent.FailureTool, Err: fmt.Errorf("execute %s: %w", request.Name, err)}
			}
			_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.completed", map[string]any{"request_id": request.ID, "name": request.Name, "result": json.RawMessage(output)})
			collectReturnedCitations(output, citations)
			invocation.Conversation = append(invocation.Conversation, agent.ConversationMessage{
				Role: domain.MessageSystem,
				Body: fmt.Sprintf("Mainspring tool result for request %s (%s): %s", request.ID, request.Name, output),
			})
		}
	}
}

func collectReturnedCitations(output json.RawMessage, citations map[string]returnedCitation) {
	var decoded struct {
		Results []struct {
			CitationID string `json:"citation_id"`
			DocumentID string `json:"document_id"`
			ChunkID    string `json:"chunk_id"`
		} `json:"results"`
	}
	if json.Unmarshal(output, &decoded) != nil {
		return
	}
	for _, item := range decoded.Results {
		if item.CitationID != "" {
			citations[item.CitationID] = returnedCitation{DocumentID: item.DocumentID, ChunkID: item.ChunkID}
		}
	}
}

func validateCitations(citations []agent.Citation, returned map[string]returnedCitation) error {
	for _, citation := range citations {
		source, exists := returned[citation.ID]
		if !exists {
			return fmt.Errorf("citation %q was not returned by an authorized tool call", citation.ID)
		}
		if citation.DocumentID != "" && citation.DocumentID != source.DocumentID {
			return fmt.Errorf("citation %q has the wrong document id", citation.ID)
		}
		if citation.ChunkID != "" && citation.ChunkID != source.ChunkID {
			return fmt.Errorf("citation %q has the wrong chunk id", citation.ID)
		}
	}
	return nil
}

func enforceCitationPolicy(policy string, citations []agent.Citation, returned map[string]returnedCitation) error {
	if (policy == "required_for_research" || policy == "always") && len(returned) > 0 && len(citations) == 0 {
		return errors.New("agent citation policy requires citing evidence returned by research tools")
	}
	return nil
}

func (s *Service) CompleteRun(ctx context.Context, runID domain.RunID) error {
	_, err := s.FinalizeRun(ctx, runID)
	return err
}

func (s *Service) FinalizeRun(ctx context.Context, runID domain.RunID) (bool, error) {
	plan, err := s.store.GetRunPlan(ctx, runID)
	if err != nil {
		return false, err
	}
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return false, err
	}
	if run.TurnCount < len(plan.Personas) {
		return false, fmt.Errorf("run has completed %d of %d planned turns", run.TurnCount, len(plan.Personas))
	}
	if s.approvals != nil {
		pending, err := s.approvals.PendingForRun(ctx, runID)
		if err != nil {
			return false, err
		}
		if pending > 0 {
			return true, s.store.SetRunStatus(ctx, runID, domain.RunAwaitingApproval, "")
		}
	}
	return false, s.store.SetRunStatus(ctx, runID, domain.RunCompleted, "")
}

func (s *Service) FailRun(ctx context.Context, runID domain.RunID, detail string) error {
	return s.store.SetRunStatus(ctx, runID, domain.RunFailed, detail)
}

func (s *Service) fail(ctx context.Context, runID domain.RunID, runErr error) error {
	if err := s.store.SetRunStatus(ctx, runID, domain.RunFailed, runErr.Error()); err != nil {
		s.logger.Error("record run failure", "run_id", runID.String(), "error", err)
	}
	return runErr
}

func StructuredResultJSON(result agent.ResultEnvelope) json.RawMessage {
	encoded, _ := json.Marshal(result)
	return encoded
}
