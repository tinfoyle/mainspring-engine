package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

type MockProvider struct{}

func (MockProvider) Invoke(ctx context.Context, invocation Invocation) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	question := "the current request"
	for index := len(invocation.Conversation) - 1; index >= 0; index-- {
		message := invocation.Conversation[index]
		if message.Role == domain.MessageUser && strings.TrimSpace(message.Body) != "" {
			question = message.Body
			break
		}
	}
	if strings.Contains(question, "[demo:document-search]") && hasTool(invocation.Tools, "documents.search") {
		for index := len(invocation.Conversation) - 1; index >= 0; index-- {
			message := invocation.Conversation[index]
			if message.Role != domain.MessageSystem || !strings.Contains(message.Body, "Mainspring tool result") {
				continue
			}
			var output struct {
				Results []struct {
					CitationID   string `json:"citation_id"`
					DocumentID   string `json:"document_id"`
					ChunkID      string `json:"chunk_id"`
					DocumentName string `json:"document_name"`
				} `json:"results"`
			}
			start := strings.Index(message.Body, "{")
			if start >= 0 && json.Unmarshal([]byte(message.Body[start:]), &output) == nil && len(output.Results) > 0 {
				item := output.Results[0]
				body := fmt.Sprintf("As %s, I searched the authorized document library and found relevant context in %s.", invocation.PersonaRole, item.DocumentName)
				envelope := ResultEnvelope{Contribution: body, Findings: []string{"Relevant tenant document context was retrieved."}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{{ID: item.CitationID, DocumentID: item.DocumentID, ChunkID: item.ChunkID, Label: item.DocumentName}}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Confidence: "high"}
				return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
			}
		}
		query := strings.TrimSpace(strings.ReplaceAll(question, "[demo:document-search]", ""))
		envelope := ResultEnvelope{Contribution: "I need to search the authorized document library before answering.", Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{{ID: "documents-1", Name: "documents.search", Arguments: json.RawMessage(fmt.Sprintf(`{"query":%q,"limit":3}`, query))}}, Confidence: "low"}
		return Result{Body: envelope.Contribution, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
	}
	body := fmt.Sprintf(
		"As %s, I reviewed %q. This is a development-mode response; the boardroom workflow, persona boundary, persistence, and event delivery are operating correctly.",
		invocation.PersonaRole,
		question,
	)
	envelope := ResultEnvelope{Contribution: body, Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Confidence: "medium"}
	if strings.Contains(question, "[demo:propose-email]") && hasGrant(invocation.ToolGrants, domain.CapabilityEmailSend) {
		envelope.ProposedActions = []ProposedAction{{
			ActionType: "email.send", Reason: "Send the owner-requested demonstration follow-up.",
			Payload:  json.RawMessage(`{"to":["customer@example.test"],"cc":[],"subject":"Mainspring approval demonstration","body":"This message was proposed by an agent and approved by the owner."}`),
			Evidence: []string{"The owner explicitly requested the approval demonstration in this conversation."},
		}}
	}
	return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
}

func hasTool(tools []ToolDefinition, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func hasGrant(grants []domain.ToolGrant, capability domain.Capability) bool {
	for _, grant := range grants {
		if grant.Capability == capability {
			return true
		}
	}
	return false
}

func (MockProvider) Cancel(context.Context, domain.InvocationID) error { return nil }
func (MockProvider) Name() string                                      { return "mock" }
func (MockProvider) Capabilities() Capabilities {
	return Capabilities{StructuredOutput: true}
}
