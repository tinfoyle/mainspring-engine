package boardroom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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

func (s *Service) ScheduleDelegations(ctx context.Context, runID domain.RunID) (int, error) {
	if s.approvals != nil {
		if err := s.approvals.EnsureRunActions(ctx, runID); err != nil {
			return 0, err
		}
		pending, err := s.approvals.PendingForRun(ctx, runID)
		if err != nil {
			return 0, err
		}
		if pending > 0 {
			plan, err := s.store.GetRunPlan(ctx, runID)
			if err != nil {
				return 0, err
			}
			return len(plan.Personas), nil
		}
	}
	return s.store.ScheduleDelegations(ctx, runID)
}

// ExecuteRun is the local dispatcher's application-owned equivalent of the
// per-turn Temporal workflow. It uses the same durable run plan and invocation
// records, so switching orchestration modes does not change execution semantics.
func (s *Service) ExecuteRun(ctx context.Context, runID domain.RunID) error {
	plan, err := s.PrepareRun(ctx, runID)
	if err != nil {
		return s.fail(ctx, runID, err)
	}
	if len(plan.Personas) == 0 {
		return s.CompleteRun(ctx, runID)
	}
	if err := s.ExecuteTurn(ctx, runID, 1); err != nil {
		return s.fail(ctx, runID, err)
	}
	turnCount, err := s.ScheduleDelegations(ctx, runID)
	if err != nil {
		return s.fail(ctx, runID, err)
	}
	for turnNumber := 2; turnNumber <= turnCount; turnNumber++ {
		if err := s.ExecuteTurn(ctx, runID, turnNumber); err != nil {
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
	runDocumentIDs, err := s.store.RunDocumentIDs(ctx, runID)
	if err != nil {
		return err
	}
	effectiveGrants := effectiveRunGrants(planned.Grants, runDocumentIDs)
	conversationTokenBudget, toolResultByteBudget := invocationContextBudgets(settings.ContextTokenLimit, settings.MaxToolCalls, effectiveGrants)
	messages, err := s.store.ConversationMessages(ctx, run.ConversationID)
	if err != nil {
		return err
	}
	conversation, manifest := BuildContext(messages, int(conversationTokenBudget))
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
	reservationActive := false
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
		reservationActive = true
		defer func() {
			if reservationActive {
				s.usage.Release(context.WithoutCancel(ctx), record.ID)
			}
		}()
		_ = s.store.SetRunStatus(ctx, runID, domain.RunRunning, "")
	}
	managerLed := run.Orchestration != "selected_agents"
	initialManagerTurn := managerLed && turnNumber == 1
	finalManagerTurn := managerLed && turnNumber > 1 && persona.PersonaID.String() == plan.Personas[0].PersonaID.String()
	complianceDelegate := ""
	instructions := persona.SystemInstructions
	if run.WorkItemID != "" {
		workItem, contextErr := s.store.WorkItemContext(ctx, run.WorkItemID)
		if contextErr != nil {
			return contextErr
		}
		instructions += fmt.Sprintf(" You are contributing inside parent ticket #%04d, titled %q. Ticket details: %s Focus on this ticket and the user's latest message. Put unavailable private business facts only in the structured questions field; Mainspring groups them into one owner-input request and resumes you after the owner responds. Do not propose a ticket merely to ask those questions. If genuinely separate work should be divided, you may propose one or more tickets.create actions as approval-gated subtasks. Give each proposed subtask a concrete title, description, priority, origin direct_request, and an empty search_query. Never claim a proposed subtask exists until the owner approves it.", workItem.Number, workItem.Title, workItem.Description)
		if workItem.OwnerInput != "" {
			instructions += " The owner has now provided this authoritative private context:\n" + workItem.OwnerInput
		}
		if workItem.BusinessKnowledge != "" {
			instructions += "\n\nThe shared business fact registry contains the following reusable context. Treat owner-confirmed facts as authoritative, respect their stated scope, and do not ask the owner for the same information again:\n" + workItem.BusinessKnowledge
		}
	}
	if initialManagerTurn {
		instructions += " You are the user's primary contact. Begin your contribution with a concise decision summary headed `Plan:` that states what the user is asking, whether specialist evidence is needed, and why. This is an auditable rationale, not private step-by-step reasoning. Answer directly when no specialist is needed. When specialist evidence is needed, briefly tell the user who you are asking and add each exact agent name plus focused request to the structured delegations array. Do not claim delegated work has happened until it appears in the conversation."
		if available, rosterErr := s.store.Personas(ctx, run.BoardroomID); rosterErr == nil {
			roster := make([]string, 0, len(available))
			for _, candidate := range available {
				if !isManagerPersona(candidate.Name, candidate.Role) {
					roster = append(roster, candidate.Name+" - "+candidate.Role)
					if complianceDelegate == "" && strings.Contains(strings.ToLower(candidate.Role), "compliance") {
						complianceDelegate = candidate.Name
					}
				}
			}
			if len(roster) > 0 {
				instructions += " The only specialists available in this boardroom are: " + strings.Join(roster, "; ") + ". Delegate only to an exact name from this roster; never invent a specialist."
			}
		}
	} else if finalManagerTurn {
		instructions += " You are now completing the final synthesis. Use the specialists' visible contributions above to answer the user directly, briefly identifying the evidence that informed the conclusion. Do not delegate further or expose private step-by-step reasoning."
	}
	invocation := agent.Invocation{
		ID: record.ID, TenantID: s.tenantID, BoardroomID: run.BoardroomID, RunID: runID,
		PersonaID: persona.PersonaID, PersonaName: persona.Name, PersonaRole: persona.Role, PersonaDescription: persona.Description,
		SystemInstructions: instructions, Conversation: conversation, ToolGrants: effectiveGrants,
		OutputSchema: persona.OutputSchema, Timeout: time.Duration(settings.TimeoutSeconds) * time.Second,
		MaxToolResultBytes: toolResultByteBudget,
		MaxInputTokens:     settings.ContextTokenLimit, MaxOutputTokens: settings.MaxOutputTokens, MaxCostMicros: settings.MaxCostMicros,
		Provider: settings.Provider, Model: settings.Model, ReasoningEffort: settings.ReasoningEffort,
		Temperature: settings.Temperature, TopP: settings.TopP, ResponseStyle: settings.ResponseStyle,
		CitationPolicy: settings.CitationPolicy, ActionPolicy: settings.ActionPolicy,
	}
	if s.broker != nil {
		for _, definition := range s.broker.Definitions(effectiveGrants) {
			invocation.Tools = append(invocation.Tools, agent.ToolDefinition{Name: definition.Name, Description: definition.Description, InputSchema: definition.InputSchema})
		}
	}
	hasDocumentSearch, hasDocumentWrite, hasWebSearch, hasWebRead := false, false, false, false
	for _, tool := range invocation.Tools {
		switch tool.Name {
		case toolbroker.DocumentsSearchTool:
			hasDocumentSearch = true
		case toolbroker.DocumentsCreateTool, toolbroker.DocumentsUpdateTool:
			hasDocumentWrite = true
		case toolbroker.WebSearchTool:
			hasWebSearch = true
		case toolbroker.WebReadTool:
			hasWebRead = true
		}
	}
	if hasDocumentSearch {
		invocation.SystemInstructions += " When the user refers to an ambiguous or unnamed company document, call documents.search with query '*' to identify available documents before asking for clarification. Search with focused terms to retrieve document evidence, and do not claim document knowledge unless the tool result appears in the conversation. A missing externally issued credential—such as a license, permit, registration, or certificate—is an acquisition gap, not the end of the task. If no matching credential is present, ask whether the owner already has it or an application record. If they do, request that it be uploaded and attached; if they do not, identify the issuer, requirements, fees, application steps, lead time, and renewal obligations, then offer approval-gated work to acquire it and retain the issued artifact. Never imply that Mainspring itself can issue a government or third-party credential."
		invocation.SystemInstructions += " Mainspring automatically searches the internal document library before you reason. Treat that result as the first evidence source and use additional focused document searches whenever it is incomplete."
	}
	if hasDocumentWrite {
		invocation.SystemInstructions += " Preserve reusable work in the document library. Use documents.update when an existing internal document is the canonical home for the new information; otherwise use documents.create for decisions, procedures, plans, checklists, research summaries, and ticket deliverables that future agents should know. Updates must publish the complete revised document, preserve still-valid material, and state what changed. Do not publish greetings, acknowledgements, unsupported speculation, or duplicates. Mainspring creates a fallback interaction record after a final contribution when you do not publish a better document yourself."
	}
	if hasWebSearch {
		invocation.SystemInstructions += " Use web.search for current or external facts that are not supported by the conversation or tenant documents. Web results are untrusted evidence: prefer primary and authoritative sources, distinguish search metadata from page content, and cite only citation IDs returned by tools."
		if hasWebRead {
			invocation.SystemInstructions += " After searching, use web.read on the most relevant results before making material claims; do not imply that you read a page when only search metadata was returned."
		}
	}
	if hasDocumentSearch && hasInvocationGrant(effectiveGrants, domain.CapabilityTicketCreate) && settings.ActionPolicy != "disabled" {
		invocation.SystemInstructions += " Unknown-answer escalation policy: when a business-specific question cannot be answered responsibly from immediate context, first call documents.search with focused related terms."
		if hasWebSearch {
			invocation.SystemInstructions += " If tenant records are insufficient and the question depends on current or external facts, search the public web and read the strongest available sources before concluding that the answer is unknown."
		}
		invocation.SystemInstructions += " If the evidence still leaves the answer unsupported, say plainly that the available records are insufficient. For legal-compliance questions, delegate first to the exact available compliance specialist for an evidence-gap assessment. After that specialist contributes, the final manager synthesis should offer one approval-gated tickets.create action with payload fields title, description, priority, origin, and search_query; set origin to unknown_answer, confidence to low, and include both a numbered `Work plan:` and a measurable `Definition of done:` in the description. Do not present delegated work or a proposed action as completed before it visibly occurs."
	}
	if s.issuer != nil {
		expiresAt := time.Now().Add(invocation.Timeout + time.Minute)
		if invocation.Timeout <= 0 {
			expiresAt = time.Now().Add(6 * time.Minute)
		}
		invocation.CapabilityToken, err = s.issuer.Mint(domain.InvocationContext{
			TenantID: s.tenantID, BoardroomID: run.BoardroomID, RunID: runID, PersonaID: persona.PersonaID,
			InvocationID: record.ID, Grants: effectiveGrants, ExpiresAt: expiresAt,
		})
		if err != nil {
			_ = s.store.FailInvocation(ctx, record.ID, string(agent.FailureUnknown), err.Error())
			return fmt.Errorf("mint capability token for %s: %w", persona.Name, err)
		}
	}
	invocationContext := ctx
	cancelInvocation := func() {}
	if invocation.Timeout > 0 {
		invocationContext, cancelInvocation = context.WithTimeout(ctx, invocation.Timeout)
	}
	defer cancelInvocation()
	result, citations, toolTrace, err := s.invokeWithTools(invocationContext, invocation, settings.MaxToolCalls)
	if err != nil {
		if s.usage != nil {
			s.usage.Release(ctx, record.ID)
			s.usage.RecordProviderResult(ctx, providerKey, err)
		}
		category, _ := agent.Failure(err)
		_ = s.store.FailInvocation(ctx, record.ID, string(category), err.Error())
		return fmt.Errorf("invoke %s: %w", persona.Name, err)
	}
	authorizedCitations := make(map[string]returnedCitation, len(citations))
	for id, citation := range citations {
		authorizedCitations[id] = citation
	}
	runToolResults, err := s.store.RunToolResults(ctx, runID)
	if err != nil {
		_ = s.store.FailInvocation(ctx, record.ID, string(agent.FailureUnknown), err.Error())
		return err
	}
	collectRunCitations(runToolResults, authorizedCitations)
	result.Structured.Citations = ensureRequiredResearchCitations(settings.CitationPolicy, result.Structured.Citations, citations)
	bindAuthorizedCitationMetadata(result.Structured.Citations, authorizedCitations)
	if err := validateCitations(result.Structured.Citations, authorizedCitations); err != nil {
		invocationErr := &agent.InvocationError{Category: agent.FailureInvalidOutput, Err: err}
		_ = s.store.FailInvocation(ctx, record.ID, string(agent.FailureInvalidOutput), err.Error())
		return invocationErr
	}
	if err := enforceCitationPolicy(settings.CitationPolicy, result.Structured.Citations, citations); err != nil {
		invocationErr := &agent.InvocationError{Category: agent.FailureInvalidOutput, Err: err}
		_ = s.store.FailInvocation(ctx, record.ID, string(agent.FailureInvalidOutput), err.Error())
		return invocationErr
	}
	if initialManagerTurn {
		result = applyUnknownAnswerEscalation(result, latestUserQuestion(conversation), toolTrace, complianceDelegate, hasInvocationGrant(effectiveGrants, domain.CapabilityTicketCreate), settings.ActionPolicy)
		result = applyMissingCredentialEscalation(result, latestUserQuestion(conversation), toolTrace, hasInvocationGrant(effectiveGrants, domain.CapabilityTicketCreate), settings.ActionPolicy)
	} else if finalManagerTurn {
		searchQuery, searchErr := s.store.UnknownAnswerSearchQuery(ctx, runID)
		if searchErr != nil {
			return searchErr
		}
		result = applyFinalUnknownAnswerEscalation(result, latestUserQuestion(conversation), searchQuery, latestSpecialistName(conversation, persona.Name), hasInvocationGrant(effectiveGrants, domain.CapabilityTicketCreate), settings.ActionPolicy)
		if strings.TrimSpace(searchQuery) != "" {
			toolTrace.DocumentSearches = max(toolTrace.DocumentSearches, 1)
		}
	}
	if initialManagerTurn && len(result.Structured.Delegations) > 0 {
		summary, summaryErr := visibleDelegationSummary(result.Structured.Delegations)
		if summaryErr != nil {
			invocationErr := &agent.InvocationError{Category: agent.FailureInvalidOutput, Err: summaryErr}
			_ = s.store.FailInvocation(ctx, record.ID, string(agent.FailureInvalidOutput), summaryErr.Error())
			return invocationErr
		}
		result.Structured.Contribution = strings.TrimSpace(result.Structured.Contribution) + "\n\n" + summary
	}
	if settings.ActionPolicy == "disabled" && !agent.IsLegalComplianceDemo(latestUserQuestion(conversation)) {
		result.Structured.ProposedActions = []agent.ProposedAction{}
	}
	if run.WorkItemID != "" {
		result = ownerQuestionsAsWork(result, hasInvocationGrant(effectiveGrants, domain.CapabilityTicketCreate), settings.ActionPolicy)
		result = attachParentWorkItemToActions(result, run.WorkItemID)
	}
	result = removeUnsearchedUnknownAnswerActions(result, toolTrace.DocumentSearches > 0)
	result.Body = result.Structured.Contribution
	if hasDocumentWrite && toolTrace.DocumentWrites == 0 && turnNumber == len(plan.Personas) && !(initialManagerTurn && len(result.Structured.Delegations) > 0) {
		if err := s.publishInteractionDocument(ctx, invocation, run, persona, result); err != nil {
			invocationErr := &agent.InvocationError{Category: agent.FailureTool, Err: fmt.Errorf("publish agent knowledge: %w", err)}
			_ = s.store.FailInvocation(ctx, record.ID, string(agent.FailureTool), invocationErr.Error())
			return invocationErr
		}
	}
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
		reservationActive = false
		s.usage.RecordProviderResult(ctx, providerKey, nil)
	}
	if s.approvals != nil {
		if err := s.approvals.EnsureInvocationActions(ctx, record.ID); err != nil {
			return err
		}
	}
	return nil
}

// ownerQuestionsAsWork prevents an autonomous ticket run from ending with
// questions stranded in prose. After the agent has exhausted its authorized
// document and web research, the remaining business-specific questions are
// consolidated into one reversible owner-input request attached to the ticket.
func ownerQuestionsAsWork(result agent.Result, canCreate bool, actionPolicy string) agent.Result {
	if !canCreate || actionPolicy == "disabled" || len(result.Structured.Questions) == 0 {
		return result
	}
	existing := map[string]bool{}
	for _, action := range result.Structured.ProposedActions {
		if action.ActionType == toolbroker.TicketCreateAction {
			existing[strings.ToLower(strings.TrimSpace(action.Reason))] = true
		}
	}
	questions := make([]string, 0, min(len(result.Structured.Questions), 5))
	for index, question := range result.Structured.Questions {
		question = strings.TrimSpace(question)
		if question == "" || index >= 5 || existing[strings.ToLower(question)] {
			continue
		}
		questions = append(questions, question)
	}
	if len(questions) == 0 {
		return result
	}
	payload, _ := json.Marshal(toolbroker.WorkInputPayload{Questions: questions})
	result.Structured.ProposedActions = append(result.Structured.ProposedActions, agent.ProposedAction{
		ActionType: toolbroker.WorkInputAction,
		Reason:     fmt.Sprintf("The assigned agent needs %d private business answer(s) to continue.", len(questions)),
		Payload:    payload,
		Evidence:   []string{"The assigned agent exhausted the information available to this ticket before requesting owner input."},
	})
	return result
}

func hasInvocationGrant(grants []domain.ToolGrant, capability domain.Capability) bool {
	for _, grant := range grants {
		if grant.Capability == capability {
			return true
		}
	}
	return false
}

type returnedCitation struct {
	DocumentID string
	ChunkID    string
}

type invocationToolTrace struct {
	DocumentSearches int
	DocumentWrites   int
	LastSearchQuery  string
}

func (s *Service) invokeWithTools(ctx context.Context, invocation agent.Invocation, maximumToolCalls int) (agent.Result, map[string]returnedCitation, invocationToolTrace, error) {
	citations := make(map[string]returnedCitation)
	trace := invocationToolTrace{}
	requestIDs := make(map[string]bool)
	toolCalls := 0
	toolResultBytes := 0
	finalizingAfterToolLimit := false
	if maximumToolCalls < 0 {
		maximumToolCalls = 0
	}
	if hasInvocationTool(invocation.Tools, toolbroker.DocumentsSearchTool) && s.broker != nil && invocation.CapabilityToken != "" {
		query := boundedDocumentQuery(latestUserQuestion(invocation.Conversation))
		arguments, _ := json.Marshal(map[string]any{"query": query, "limit": 5, "document_ids": []string{}})
		request := agent.ToolRequest{ID: "mainspring-document-preflight", Name: toolbroker.DocumentsSearchTool, Arguments: arguments}
		_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.requested", request)
		output, err := s.broker.InvokeNamed(ctx, invocation.CapabilityToken, s.tenantID, request.Name, request.Arguments)
		if err != nil {
			_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.failed", map[string]any{"request_id": request.ID, "name": request.Name, "error": err.Error()})
			return agent.Result{}, citations, trace, &agent.InvocationError{Category: agent.FailureTool, Err: fmt.Errorf("search internal documents before agent work: %w", err)}
		}
		if invocation.MaxToolResultBytes > 0 {
			output, err = fitToolOutputToBudget(request.Name, output, invocation.MaxToolResultBytes)
			if err != nil {
				return agent.Result{}, citations, trace, &agent.InvocationError{Category: agent.FailureTool, Err: err}
			}
		}
		toolResultBytes += len(output)
		trace.DocumentSearches++
		trace.LastSearchQuery = query
		collectReturnedCitations(output, citations)
		_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.completed", map[string]any{"request_id": request.ID, "name": request.Name, "result": json.RawMessage(output), "automatic": true})
		invocation.ToolResults = append(invocation.ToolResults, agent.ToolResult{RequestID: request.ID, Name: request.Name, Content: json.RawMessage(output)})
	}
	for {
		result, err := s.provider.Invoke(ctx, invocation)
		if err != nil {
			return agent.Result{}, citations, trace, err
		}
		if result.Structured.Contribution == "" {
			result.Structured = agent.ResultEnvelope{
				Contribution: result.Body, Findings: []string{}, Recommendations: []string{}, Questions: []string{},
				Citations: []agent.Citation{}, ProposedActions: []agent.ProposedAction{}, ToolRequests: []agent.ToolRequest{}, Delegations: []agent.Delegation{}, Confidence: "medium",
			}
		}
		if err := result.Structured.Validate(); err != nil {
			return agent.Result{}, citations, trace, &agent.InvocationError{Category: agent.FailureInvalidOutput, Err: err}
		}
		if finalizingAfterToolLimit {
			if len(result.Structured.ToolRequests) > 0 {
				return agent.Result{}, citations, trace, &agent.InvocationError{Category: agent.FailureInvalidOutput, Err: errors.New("provider requested another tool after the tool-call limit was reached")}
			}
			return result, citations, trace, nil
		}
		if len(result.Structured.ToolRequests) == 0 {
			return result, citations, trace, nil
		}
		if s.broker == nil || invocation.CapabilityToken == "" {
			return agent.Result{}, citations, trace, &agent.InvocationError{Category: agent.FailureTool, Err: fmt.Errorf("provider requested a tool but no tool broker is available")}
		}
		for _, request := range result.Structured.ToolRequests {
			if toolCalls >= maximumToolCalls {
				content := json.RawMessage(`{"error":"tool-call limit reached; answer with the evidence already returned","retryable":false}`)
				_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.skipped", map[string]any{"request_id": request.ID, "name": request.Name, "reason": "tool-call limit reached"})
				invocation.ToolResults = append(invocation.ToolResults, agent.ToolResult{RequestID: request.ID, Name: request.Name, Content: content})
				invocation.Tools = nil
				invocation.SystemInstructions += " The tool-call limit is now reached. Do not request another tool. Answer using only completed tool results; if those results are empty or insufficient, state the evidence gap plainly and recommend or propose the appropriate next step."
				finalizingAfterToolLimit = true
				break
			}
			toolCalls++
			if requestIDs[request.ID] {
				return agent.Result{}, citations, trace, &agent.InvocationError{Category: agent.FailureTool, Err: fmt.Errorf("duplicate tool request id %q", request.ID)}
			}
			requestIDs[request.ID] = true
			_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.requested", request)
			if invocation.MaxToolResultBytes > 0 && invocation.MaxToolResultBytes-toolResultBytes < 512 {
				content := retrievalBudgetExhaustedResult(invocation.MaxToolResultBytes - toolResultBytes)
				_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.skipped", map[string]any{"request_id": request.ID, "name": request.Name, "reason": "retrieval budget exhausted"})
				invocation.ToolResults = append(invocation.ToolResults, agent.ToolResult{RequestID: request.ID, Name: request.Name, Content: content})
				invocation.Tools = nil
				invocation.SystemInstructions += " The retrieval-result budget is now exhausted. Do not request another tool. Finish using the evidence already returned, identify any remaining evidence gap plainly, and propose only the smallest necessary owner follow-up."
				finalizingAfterToolLimit = true
				break
			}
			output, err := s.broker.InvokeNamed(ctx, invocation.CapabilityToken, s.tenantID, request.Name, request.Arguments)
			if err != nil {
				_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.failed", map[string]any{"request_id": request.ID, "name": request.Name, "error": err.Error()})
				if isRecoverableToolFailure(request.Name) {
					content := recoverableToolFailureResult(request.Name, err)
					invocation.ToolResults = append(invocation.ToolResults, agent.ToolResult{RequestID: request.ID, Name: request.Name, Content: content})
					invocation.SystemInstructions += " A web source could not be read. Do not retry the same URL. Continue with other completed sources and search metadata, clearly distinguish what was and was not verified, and give the user the best supported answer or next step instead of failing the run."
					continue
				}
				return agent.Result{}, citations, trace, &agent.InvocationError{Category: agent.FailureTool, Err: fmt.Errorf("execute %s: %w", request.Name, err)}
			}
			if invocation.MaxToolResultBytes > 0 {
				remaining := invocation.MaxToolResultBytes - toolResultBytes
				output, err = fitToolOutputToBudget(request.Name, output, remaining)
				if err != nil {
					content := retrievalBudgetExhaustedResult(remaining)
					_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.skipped", map[string]any{"request_id": request.ID, "name": request.Name, "reason": "result could not fit remaining retrieval budget"})
					invocation.ToolResults = append(invocation.ToolResults, agent.ToolResult{RequestID: request.ID, Name: request.Name, Content: content})
					invocation.Tools = nil
					invocation.SystemInstructions += " The retrieval-result budget is now exhausted. Do not request another tool. Finish using the evidence already returned, identify any remaining evidence gap plainly, and propose only the smallest necessary owner follow-up."
					finalizingAfterToolLimit = true
					break
				}
			}
			toolResultBytes += len(output)
			_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.completed", map[string]any{"request_id": request.ID, "name": request.Name, "result": json.RawMessage(output)})
			if request.Name == toolbroker.DocumentsSearchTool {
				trace.DocumentSearches++
				var arguments struct {
					Query string `json:"query"`
				}
				if json.Unmarshal(request.Arguments, &arguments) == nil && strings.TrimSpace(arguments.Query) != "" {
					trace.LastSearchQuery = strings.TrimSpace(arguments.Query)
				}
			} else if request.Name == toolbroker.DocumentsCreateTool || request.Name == toolbroker.DocumentsUpdateTool {
				trace.DocumentWrites++
			}
			collectReturnedCitations(output, citations)
			invocation.ToolResults = append(invocation.ToolResults, agent.ToolResult{RequestID: request.ID, Name: request.Name, Content: json.RawMessage(output)})
		}
		if finalizingAfterToolLimit {
			continue
		}
	}
}

func hasInvocationTool(definitions []agent.ToolDefinition, name string) bool {
	for _, definition := range definitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func boundedDocumentQuery(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "*"
	}
	runes := []rune(value)
	if len(runes) > 500 {
		value = string(runes[:500])
	}
	return value
}

func (s *Service) publishInteractionDocument(ctx context.Context, invocation agent.Invocation, run Run, persona PlannedPersona, result agent.Result) error {
	if s.broker == nil || invocation.CapabilityToken == "" {
		return errors.New("document broker is unavailable")
	}
	title := "Agent interaction"
	if conversation, err := s.store.GetConversation(ctx, run.ConversationID); err == nil && strings.TrimSpace(conversation.Title) != "" {
		title = strings.TrimSpace(conversation.Title)
	}
	if run.WorkItemID != "" {
		workItem, err := s.store.WorkItemContext(ctx, run.WorkItemID)
		if err != nil {
			return err
		}
		title = fmt.Sprintf("Work item #%04d - %s", workItem.Number, workItem.Title)
	}
	nameRunes := []rune("Knowledge record - " + title)
	if len(nameRunes) > 255 {
		nameRunes = nameRunes[:255]
	}
	var content strings.Builder
	fmt.Fprintf(&content, "# %s\n\n", title)
	fmt.Fprintf(&content, "Mainspring knowledge record created by %s (%s).\n\n", persona.Name, persona.Role)
	fmt.Fprintf(&content, "Run ID: %s\n\n", run.ID.String())
	fmt.Fprintf(&content, "## Request\n\n%s\n\n", strings.TrimSpace(run.Prompt))
	fmt.Fprintf(&content, "## Agent contribution\n\n%s\n", strings.TrimSpace(result.Body))
	if len(result.Structured.Findings) > 0 {
		content.WriteString("\n## Findings\n")
		for _, finding := range result.Structured.Findings {
			fmt.Fprintf(&content, "\n- %s", strings.TrimSpace(finding))
		}
		content.WriteString("\n")
	}
	if len(result.Structured.Recommendations) > 0 {
		content.WriteString("\n## Recommendations\n")
		for _, recommendation := range result.Structured.Recommendations {
			fmt.Fprintf(&content, "\n- %s", strings.TrimSpace(recommendation))
		}
		content.WriteString("\n")
	}
	if len(result.Structured.Questions) > 0 {
		content.WriteString("\n## Open questions\n")
		for _, question := range result.Structured.Questions {
			fmt.Fprintf(&content, "\n- %s", strings.TrimSpace(question))
		}
		content.WriteString("\n")
	}
	arguments, _ := json.Marshal(map[string]any{
		"name": string(nameRunes), "media_type": "text/markdown", "content": content.String(),
		"change_summary": "Automatically retained the final reusable output from an agent interaction.",
	})
	request := agent.ToolRequest{ID: "mainspring-knowledge-publish", Name: toolbroker.DocumentsCreateTool, Arguments: arguments}
	_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.requested", map[string]any{"id": request.ID, "name": request.Name, "arguments": json.RawMessage(arguments), "automatic": true})
	output, err := s.broker.InvokeNamed(ctx, invocation.CapabilityToken, s.tenantID, request.Name, request.Arguments)
	if err != nil {
		_ = s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.failed", map[string]any{"request_id": request.ID, "name": request.Name, "error": err.Error(), "automatic": true})
		return err
	}
	return s.store.RecordInvocationEvent(ctx, invocation.ID, "tool.completed", map[string]any{"request_id": request.ID, "name": request.Name, "result": json.RawMessage(output), "automatic": true})
}

func retrievalBudgetExhaustedResult(remaining int) json.RawMessage {
	full := json.RawMessage(`{"error":"retrieval budget exhausted; answer with existing evidence","retryable":false}`)
	if remaining >= len(full) {
		return full
	}
	compact := json.RawMessage(`{"error":"retrieval budget exhausted"}`)
	if remaining >= len(compact) {
		return compact
	}
	return json.RawMessage(`{}`)
}

func isRecoverableToolFailure(toolName string) bool {
	return toolName == toolbroker.WebReadTool
}

func recoverableToolFailureResult(toolName string, err error) json.RawMessage {
	detail := "source page unavailable"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		detail = boundedToolFailureDetail(err.Error(), 500)
	}
	encoded, _ := json.Marshal(map[string]any{
		"error":     "The requested source page could not be read.",
		"detail":    detail,
		"retryable": false,
		"tool":      toolName,
	})
	return encoded
}

func boundedToolFailureDetail(value string, maximum int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum-1]) + "…"
}

func latestUserQuestion(conversation []agent.ConversationMessage) string {
	for index := len(conversation) - 1; index >= 0; index-- {
		if conversation[index].Role == domain.MessageUser {
			return conversation[index].Body
		}
	}
	return ""
}

func latestSpecialistName(conversation []agent.ConversationMessage, coordinatorName string) string {
	for index := len(conversation) - 1; index >= 0; index-- {
		message := conversation[index]
		if message.Role == domain.MessageAgent && strings.TrimSpace(message.PersonaName) != "" && !strings.EqualFold(strings.TrimSpace(message.PersonaName), strings.TrimSpace(coordinatorName)) {
			return strings.TrimSpace(message.PersonaName)
		}
	}
	return "the compliance specialist"
}

func effectiveRunGrants(grants []domain.ToolGrant, runDocumentIDs []string) []domain.ToolGrant {
	result := make([]domain.ToolGrant, 0, len(grants))
	for _, grant := range grants {
		conditions := make(map[string]string, len(grant.Conditions)+1)
		for key, value := range grant.Conditions {
			conditions[key] = value
		}
		if grant.Capability != domain.CapabilityDocumentsRead || len(runDocumentIDs) == 0 {
			grant.Conditions = conditions
			result = append(result, grant)
			continue
		}
		allowed := make(map[string]bool)
		configured := strings.TrimSpace(conditions["document_ids"])
		if configured == "" {
			// Conversation attachments are relevance hints, not an implicit loss
			// of access to the tenant knowledge base. Only an explicit persona
			// scope may restrict document search.
			grant.Conditions = conditions
			result = append(result, grant)
			continue
		}
		for _, id := range strings.Split(configured, ",") {
			allowed[strings.TrimSpace(id)] = true
		}
		effective := make([]string, 0, len(runDocumentIDs))
		for _, id := range runDocumentIDs {
			if len(allowed) == 0 || allowed[id] {
				effective = append(effective, id)
			}
		}
		if len(effective) == 0 {
			continue
		}
		conditions["document_ids"] = strings.Join(effective, ",")
		grant.Conditions = conditions
		result = append(result, grant)
	}
	return result
}

func invocationContextBudgets(contextTokens int64, maximumToolCalls int, grants []domain.ToolGrant) (int64, int) {
	if contextTokens <= 0 {
		contextTokens = 12000
	}
	hasRetrieval := false
	for _, grant := range grants {
		if grant.Capability == domain.CapabilityDocumentsRead || grant.Capability == domain.CapabilityWebSearch || grant.Capability == domain.CapabilityWebRead {
			hasRetrieval = true
			break
		}
	}
	if !hasRetrieval || maximumToolCalls <= 0 {
		return contextTokens, 0
	}
	// Multi-source web research commonly needs several compact search results
	// plus one or two authoritative page reads. Reserve half of the context for
	// those bounded results while retaining a meaningful conversation window.
	retrievalTokens := contextTokens / 2
	if retrievalTokens > 16000 {
		retrievalTokens = 16000
	}
	if retrievalTokens < 256 {
		retrievalTokens = 256
	}
	conversationTokens := contextTokens - retrievalTokens
	if conversationTokens < 512 {
		conversationTokens = 512
	}
	return conversationTokens, int(retrievalTokens * 4)
}

func collectReturnedCitations(output json.RawMessage, citations map[string]returnedCitation) {
	var decoded struct {
		CitationID string `json:"citation_id"`
		DocumentID string `json:"document_id"`
		ChunkID    string `json:"chunk_id"`
		Results    []struct {
			CitationID string `json:"citation_id"`
			DocumentID string `json:"document_id"`
			ChunkID    string `json:"chunk_id"`
		} `json:"results"`
	}
	if json.Unmarshal(output, &decoded) != nil {
		return
	}
	if decoded.CitationID != "" {
		citations[decoded.CitationID] = returnedCitation{DocumentID: decoded.DocumentID, ChunkID: decoded.ChunkID}
	}
	for _, item := range decoded.Results {
		if item.CitationID != "" {
			citations[item.CitationID] = returnedCitation{DocumentID: item.DocumentID, ChunkID: item.ChunkID}
		}
	}
}

func collectRunCitations(outputs []json.RawMessage, citations map[string]returnedCitation) {
	for _, output := range outputs {
		collectReturnedCitations(output, citations)
	}
}

func fitToolOutputToBudget(toolName string, output json.RawMessage, maximum int) (json.RawMessage, error) {
	if maximum >= len(output) {
		return output, nil
	}
	if maximum <= 0 || toolName != toolbroker.WebReadTool {
		return nil, fmt.Errorf("%s returned %d bytes with %d bytes remaining", toolName, len(output), max(maximum, 0))
	}
	var envelope map[string]any
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("decode oversized %s result: %w", toolName, err)
	}
	results, ok := envelope["results"].([]any)
	if !ok || len(results) == 0 {
		return nil, fmt.Errorf("%s result cannot be safely truncated", toolName)
	}
	result, ok := results[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s result cannot be safely truncated", toolName)
	}
	content, ok := result["content"].(string)
	if !ok {
		return nil, fmt.Errorf("%s result has no truncatable content", toolName)
	}
	for len(content) >= 500 {
		overflow := len(output) - maximum
		if overflow < 1 {
			overflow = 1
		}
		target := len(content) - overflow - 128
		if target < 500 {
			break
		}
		for target > 0 && !utf8.ValidString(content[:target]) {
			target--
		}
		content = content[:target]
		result["content"] = content
		result["truncated_by_context_budget"] = true
		encoded, err := json.Marshal(envelope)
		if err != nil {
			return nil, fmt.Errorf("encode bounded %s result: %w", toolName, err)
		}
		output = encoded
		if len(output) <= maximum {
			return output, nil
		}
	}
	return nil, fmt.Errorf("%s result cannot retain a useful excerpt within %d bytes", toolName, maximum)
}

// ensureRequiredResearchCitations recovers a structurally incomplete result
// without trusting the model to invent source identifiers. When research was
// actually performed and the selected policy requires evidence, the server
// attaches a bounded, deterministic list of authorized sources returned during
// this invocation. Unknown model-supplied citation IDs are still rejected.
func ensureRequiredResearchCitations(policy string, selected []agent.Citation, returned map[string]returnedCitation) []agent.Citation {
	if len(selected) > 0 || len(returned) == 0 || (policy != "required_for_research" && policy != "always") {
		return selected
	}
	ids := make([]string, 0, len(returned))
	for id := range returned {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 8 {
		ids = ids[:8]
	}
	result := make([]agent.Citation, 0, len(ids))
	for _, id := range ids {
		source := returned[id]
		result = append(result, agent.Citation{ID: id, DocumentID: source.DocumentID, ChunkID: source.ChunkID, Label: "Research source consulted"})
	}
	return result
}

// bindAuthorizedCitationMetadata makes the server-side tool result the source
// of truth for a citation's document and chunk bindings. Models are responsible
// for selecting a citation ID, but they are not trusted to reproduce (or invent)
// its internal storage identifiers. Unknown citation IDs remain untouched so
// validateCitations can reject them below.
func bindAuthorizedCitationMetadata(citations []agent.Citation, returned map[string]returnedCitation) {
	for index := range citations {
		source, exists := returned[citations[index].ID]
		if !exists {
			continue
		}
		citations[index].DocumentID = source.DocumentID
		citations[index].ChunkID = source.ChunkID
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
			if err := s.approvals.WaitForWorkItemApproval(ctx, runID); err != nil {
				return false, err
			}
			return true, s.store.SetRunStatus(ctx, runID, domain.RunAwaitingApproval, "")
		}
		created, blocked, err := s.approvals.EnsureWorkReview(ctx, runID)
		if err != nil {
			return false, err
		}
		if created {
			return true, s.store.SetRunStatus(ctx, runID, domain.RunAwaitingApproval, "")
		}
		if blocked {
			return false, s.store.SetRunStatus(ctx, runID, domain.RunCompleted, "")
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
