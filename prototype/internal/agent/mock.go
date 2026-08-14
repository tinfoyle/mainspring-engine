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
	if strings.Contains(question, "[demo:unknown-answer:legal-compliance]") && strings.Contains(strings.ToLower(invocation.PersonaRole), "compliance") {
		specialistName := strings.TrimSpace(invocation.PersonaName)
		if specialistName == "" {
			specialistName = "The compliance specialist"
		}
		body := specialistName + "'s compliance assessment: the document search contains no evidence sufficient to determine legal-compliance status. The assessment must identify applicable jurisdictions and obligations, then map licenses, permits, policies, audits, controls, and ownership evidence before qualified counsel validates any conclusion."
		envelope := ResultEnvelope{Contribution: body, Findings: []string{"The available records do not support a defensible yes-or-no compliance conclusion."}, Recommendations: []string{"Create a scoped compliance-assessment work item and track the missing evidence to closure."}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "low"}
		return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
	}
	if specialistName := complianceConversationPersona(invocation.Conversation, invocation.PersonaName); strings.Contains(question, "[demo:unknown-answer:legal-compliance]") && specialistName != "" {
		body := specialistName + "'s compliance assessment confirms that the available records are insufficient for a defensible compliance conclusion. I recommend tracking the scoped evidence review as a work item, with qualified legal counsel validating the final determination."
		envelope := ResultEnvelope{Contribution: body, Findings: []string{"Document search and specialist review both found an evidence gap."}, Recommendations: []string{"Approve a tracked compliance-assessment work item."}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "low"}
		return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
	}
	if strings.Contains(question, "[demo:ticket-workspace]") {
		body := fmt.Sprintf("%s reviewed the parent ticket and recommends splitting the evidence collection into a tracked subtask.", invocation.PersonaName)
		actions := []ProposedAction{}
		if hasGrant(invocation.ToolGrants, domain.CapabilityTicketCreate) {
			actions = append(actions, ProposedAction{
				ActionType: "tickets.create",
				Reason:     "Track the bounded evidence-collection step separately under the parent ticket.",
				Payload:    json.RawMessage(`{"title":"Collect supporting evidence","description":"Gather the records required by the parent ticket and summarize the remaining gaps.","priority":"normal","origin":"direct_request","search_query":""}`),
				Evidence:   []string{"The parent ticket contains a distinct evidence-collection workstream."},
			})
		}
		envelope := ResultEnvelope{Contribution: body, Findings: []string{"Evidence collection can be tracked independently."}, Recommendations: []string{"Approve the proposed subtask if separate ownership is useful."}, Questions: []string{}, Citations: []Citation{}, ProposedActions: actions, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "medium"}
		return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
	}
	if strings.Contains(question, "[demo:unknown-answer:legal-compliance]") && hasTool(invocation.Tools, "documents.search") {
		for index := len(invocation.ToolResults) - 1; index >= 0; index-- {
			toolResult := invocation.ToolResults[index]
			if toolResult.Name != "documents.search" {
				continue
			}
			var output struct {
				Results []struct {
					CitationID   string `json:"citation_id"`
					DocumentID   string `json:"document_id"`
					ChunkID      string `json:"chunk_id"`
					DocumentName string `json:"document_name"`
					Excerpt      string `json:"excerpt"`
				} `json:"results"`
			}
			if json.Unmarshal(toolResult.Content, &output) == nil && len(output.Results) > 0 {
				item := output.Results[0]
				body := fmt.Sprintf("I searched the authorized document library and found potentially relevant compliance evidence in %s: %s", item.DocumentName, strings.TrimSpace(item.Excerpt))
				envelope := ResultEnvelope{Contribution: body, Findings: []string{"Potentially relevant compliance documentation was found."}, Recommendations: []string{"Review the cited evidence before determining whether a compliance assessment is still needed."}, Questions: []string{}, Citations: []Citation{{ID: item.CitationID, DocumentID: item.DocumentID, ChunkID: item.ChunkID, Label: item.DocumentName}}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "medium"}
				return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
			}
			body := "I searched the authorized document library but did not find evidence that can establish whether the business is legally compliant. I cannot responsibly answer yes or no from the available records. I can create a tracked compliance-assessment work item with concrete investigation steps for owner approval."
			actions := []ProposedAction{}
			if hasGrant(invocation.ToolGrants, domain.CapabilityTicketCreate) {
				actions = append(actions, LegalComplianceWorkItemAction("legal compliance obligations licenses permits policies audits"))
			}
			envelope := ResultEnvelope{Contribution: body, Findings: []string{"Available documentation is insufficient to determine legal-compliance status."}, Recommendations: []string{"Create the proposed work item and complete its evidence checklist."}, Questions: []string{}, Citations: []Citation{}, ProposedActions: actions, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "low"}
			return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
		}
		envelope := ResultEnvelope{Contribution: "I need to search the authorized document library before deciding whether this question can be answered.", Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{{ID: "unknown-answer-documents-1", Name: "documents.search", Arguments: json.RawMessage(`{"query":"legal compliance obligations licenses permits policies audits","limit":5,"document_ids":[]}`)}}, Delegations: []Delegation{}, Confidence: "low"}
		return Result{Body: envelope.Contribution, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
	}
	if strings.Contains(question, "[demo:missing-credential]") && hasTool(invocation.Tools, "documents.search") {
		for index := len(invocation.ToolResults) - 1; index >= 0; index-- {
			if invocation.ToolResults[index].Name != "documents.search" {
				continue
			}
			body := "I searched the authorized document library but could not find a matching business license document, so I am unable to verify that the business holds it."
			envelope := ResultEnvelope{Contribution: body, Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "low"}
			return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
		}
		envelope := ResultEnvelope{Contribution: "I need to search the authorized document library for an existing license or application record.", Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{{ID: "missing-credential-documents-1", Name: "documents.search", Arguments: json.RawMessage(`{"query":"business license permit registration certificate application renewal","limit":5,"document_ids":[]}`)}}, Delegations: []Delegation{}, Confidence: "low"}
		return Result{Body: envelope.Contribution, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
	}
	if strings.Contains(question, "[demo:web-read-recovery]") && hasTool(invocation.Tools, "web.read") {
		for index := len(invocation.ToolResults) - 1; index >= 0; index-- {
			if invocation.ToolResults[index].Name != "web.read" {
				continue
			}
			body := "The requested source page could not be read, but I continued the run using the evidence that remained available. The unavailable page was not treated as verified evidence."
			envelope := ResultEnvelope{Contribution: body, Findings: []string{"One source page was unavailable."}, Recommendations: []string{"Use another authoritative source or retry that source later."}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "low"}
			return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
		}
		envelope := ResultEnvelope{Contribution: "I will attempt to read the requested public source.", Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{{ID: "web-read-recovery-1", Name: "web.read", Arguments: json.RawMessage(`{"url":"http://127.0.0.1/private","max_characters":1000}`)}}, Delegations: []Delegation{}, Confidence: "low"}
		return Result{Body: envelope.Contribution, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
	}
	if strings.Contains(question, "[demo:document-search]") && hasTool(invocation.Tools, "documents.search") {
		for index := len(invocation.ToolResults) - 1; index >= 0; index-- {
			toolResult := invocation.ToolResults[index]
			if toolResult.Name != "documents.search" {
				continue
			}
			var output struct {
				Results []struct {
					CitationID   string `json:"citation_id"`
					DocumentID   string `json:"document_id"`
					ChunkID      string `json:"chunk_id"`
					DocumentName string `json:"document_name"`
					Excerpt      string `json:"excerpt"`
				} `json:"results"`
			}
			if json.Unmarshal(toolResult.Content, &output) == nil && len(output.Results) > 0 {
				item := output.Results[0]
				excerpt := strings.TrimSpace(item.Excerpt)
				if len(excerpt) > 600 {
					excerpt = excerpt[:600] + "…"
				}
				body := fmt.Sprintf("As %s, I searched the authorized document library and found relevant context in %s.", invocation.PersonaRole, item.DocumentName)
				if excerpt != "" {
					body += " Evidence: " + excerpt
				}
				envelope := ResultEnvelope{Contribution: body, Findings: []string{"Relevant tenant document context was retrieved."}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{{ID: item.CitationID, DocumentID: item.DocumentID, ChunkID: item.ChunkID, Label: item.DocumentName}}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "high"}
				return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
			}
			body := fmt.Sprintf("As %s, I searched the authorized document library but found no matching passage for this request.", invocation.PersonaRole)
			envelope := ResultEnvelope{Contribution: body, Findings: []string{}, Recommendations: []string{}, Questions: []string{"Can you provide a more specific term from the document?"}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "low"}
			return Result{Body: body, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
		}
		query := strings.TrimSpace(strings.ReplaceAll(question, "[demo:document-search]", ""))
		envelope := ResultEnvelope{Contribution: "I need to search the authorized document library before answering.", Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{{ID: "documents-1", Name: "documents.search", Arguments: json.RawMessage(fmt.Sprintf(`{"query":%q,"limit":3,"document_ids":[]}`, query))}}, Delegations: []Delegation{}, Confidence: "low"}
		return Result{Body: envelope.Contribution, Structured: envelope, Provider: "mock", Model: "deterministic-development-provider"}, nil
	}
	body := fmt.Sprintf(
		"As %s, I reviewed %q. This is a development-mode response; the boardroom workflow, persona boundary, persistence, and event delivery are operating correctly.",
		invocation.PersonaRole,
		question,
	)
	envelope := ResultEnvelope{Contribution: body, Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, ToolRequests: []ToolRequest{}, Delegations: []Delegation{}, Confidence: "medium"}
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

func complianceConversationPersona(conversation []ConversationMessage, coordinatorName string) string {
	for index := len(conversation) - 1; index >= 0; index-- {
		message := conversation[index]
		if message.Role == domain.MessageAgent && !strings.EqualFold(strings.TrimSpace(message.PersonaName), strings.TrimSpace(coordinatorName)) && strings.Contains(strings.ToLower(message.Body), "compliance assessment") {
			return strings.TrimSpace(message.PersonaName)
		}
	}
	return ""
}

func (MockProvider) Cancel(context.Context, domain.InvocationID) error { return nil }
func (MockProvider) Name() string                                      { return "mock" }
func (MockProvider) Capabilities() Capabilities {
	return Capabilities{StructuredOutput: true}
}
