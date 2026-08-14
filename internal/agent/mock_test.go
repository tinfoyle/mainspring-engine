package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func TestMockProviderRecallsDocumentExcerpt(t *testing.T) {
	provider := MockProvider{}
	result, err := provider.Invoke(context.Background(), Invocation{
		PersonaRole:  "Main Manager",
		Conversation: []ConversationMessage{{Role: domain.MessageUser, Body: "[demo:document-search] closeout phrase"}},
		Tools:        []ToolDefinition{{Name: "documents.search"}},
		ToolResults: []ToolResult{{
			Name:    "documents.search",
			Content: json.RawMessage(`{"results":[{"citation_id":"doc:one:chunk:0","document_id":"one","chunk_id":"one:0","document_name":"Closeout protocol","excerpt":"The authorization phrase is ORCHID-7429."}]}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Body, "ORCHID-7429") || !strings.Contains(result.Body, "Closeout protocol") {
		t.Fatalf("document evidence was not recalled in the response: %q", result.Body)
	}
	if len(result.Structured.Citations) != 1 || result.Structured.Citations[0].DocumentID != "one" {
		t.Fatalf("document citation was not preserved: %#v", result.Structured.Citations)
	}
}

func TestMockProviderDoesNotRetryAnEmptyDocumentSearch(t *testing.T) {
	provider := MockProvider{}
	result, err := provider.Invoke(context.Background(), Invocation{
		PersonaRole:  "Main Manager",
		Conversation: []ConversationMessage{{Role: domain.MessageUser, Body: "[demo:document-search] missing phrase"}},
		Tools:        []ToolDefinition{{Name: "documents.search"}},
		ToolResults:  []ToolResult{{Name: "documents.search", Content: json.RawMessage(`{"results":[]}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Structured.ToolRequests) != 0 || !strings.Contains(result.Body, "no matching passage") {
		t.Fatalf("empty search should complete without a duplicate tool request: %#v", result.Structured)
	}
}

func TestMockProviderMissingCredentialSearchesDocumentsBeforeConcluding(t *testing.T) {
	provider := MockProvider{}
	invocation := Invocation{
		PersonaRole:  "Main Manager",
		Conversation: []ConversationMessage{{Role: domain.MessageUser, Body: "[demo:missing-credential] What license do we need?"}},
		Tools:        []ToolDefinition{{Name: "documents.search"}},
		ToolGrants:   []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead}, {Capability: domain.CapabilityTicketCreate}},
	}
	first, err := provider.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Structured.ToolRequests) != 1 || first.Structured.ToolRequests[0].Name != "documents.search" {
		t.Fatalf("credential question did not search documents first: %#v", first.Structured)
	}
	invocation.ToolResults = []ToolResult{{RequestID: first.Structured.ToolRequests[0].ID, Name: "documents.search", Content: json.RawMessage(`{"results":[]}`)}}
	second, err := provider.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Structured.ToolRequests) != 0 || !strings.Contains(second.Body, "unable to verify") {
		t.Fatalf("mock did not expose the missing-credential condition: %#v", second.Structured)
	}
}

func TestMockProviderContinuesAfterWebReadFailure(t *testing.T) {
	provider := MockProvider{}
	invocation := Invocation{
		PersonaRole:  "Main Manager",
		Conversation: []ConversationMessage{{Role: domain.MessageUser, Body: "[demo:web-read-recovery] Check this source"}},
		Tools:        []ToolDefinition{{Name: "web.read"}},
	}
	first, err := provider.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Structured.ToolRequests) != 1 || first.Structured.ToolRequests[0].Name != "web.read" {
		t.Fatalf("recovery test did not request web.read: %#v", first.Structured)
	}
	invocation.ToolResults = []ToolResult{{RequestID: first.Structured.ToolRequests[0].ID, Name: "web.read", Content: json.RawMessage(`{"error":"The requested source page could not be read.","retryable":false}`)}}
	second, err := provider.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Structured.ToolRequests) != 0 || !strings.Contains(second.Body, "continued the run") || !strings.Contains(second.Body, "not treated as verified evidence") {
		t.Fatalf("mock did not recover from web.read failure: %#v", second.Structured)
	}
}

func TestMockProviderUnknownAnswerSearchesThenOffersComplianceWorkItem(t *testing.T) {
	provider := MockProvider{}
	invocation := Invocation{
		PersonaRole:  "Main Manager",
		Conversation: []ConversationMessage{{Role: domain.MessageUser, Body: "[demo:unknown-answer:legal-compliance] Are we in legal compliance?"}},
		Tools:        []ToolDefinition{{Name: "documents.search"}},
		ToolGrants:   []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead}, {Capability: domain.CapabilityTicketCreate}},
	}
	first, err := provider.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Structured.ToolRequests) != 1 || first.Structured.ToolRequests[0].Name != "documents.search" || len(first.Structured.ProposedActions) != 0 {
		t.Fatalf("unknown answer must search before proposing work: %#v", first.Structured)
	}
	invocation.ToolResults = []ToolResult{{RequestID: first.Structured.ToolRequests[0].ID, Name: "documents.search", Content: json.RawMessage(`{"results":[]}`)}}
	second, err := provider.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if second.Structured.Confidence != "low" || len(second.Structured.ProposedActions) != 1 {
		t.Fatalf("missing evidence should produce one low-confidence action: %#v", second.Structured)
	}
	action := second.Structured.ProposedActions[0]
	if action.ActionType != "tickets.create" || !strings.Contains(string(action.Payload), "Work plan") || !strings.Contains(string(action.Payload), "Definition of done") {
		t.Fatalf("unexpected compliance work item: %#v", action)
	}
}

func TestMockProviderUnknownAnswerDoesNotOfferWorkWhenDocumentsAnswer(t *testing.T) {
	provider := MockProvider{}
	result, err := provider.Invoke(context.Background(), Invocation{
		PersonaRole:  "Main Manager",
		Conversation: []ConversationMessage{{Role: domain.MessageUser, Body: "[demo:unknown-answer:legal-compliance] Are we in legal compliance?"}},
		Tools:        []ToolDefinition{{Name: "documents.search"}},
		ToolGrants:   []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead}, {Capability: domain.CapabilityTicketCreate}},
		ToolResults:  []ToolResult{{Name: "documents.search", Content: json.RawMessage(`{"results":[{"citation_id":"doc:one:chunk:0","document_id":"one","chunk_id":"one:0","document_name":"Compliance review","excerpt":"Annual compliance assessment completed."}]}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Structured.ProposedActions) != 0 || len(result.Structured.Citations) != 1 {
		t.Fatalf("document evidence should be returned for review before work is proposed: %#v", result.Structured)
	}
}

func TestMockProviderComplianceSpecialistContributesVisibleAssessment(t *testing.T) {
	result, err := (MockProvider{}).Invoke(context.Background(), Invocation{
		PersonaName: "Jordan",
		PersonaRole: "Security & Compliance Advisor",
		Conversation: []ConversationMessage{{
			Role: domain.MessageUser,
			Body: "[demo:unknown-answer:legal-compliance] Are we in legal compliance?",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Body, "Jordan's compliance assessment") || len(result.Structured.Delegations) != 0 || len(result.Structured.ProposedActions) != 0 {
		t.Fatalf("unexpected Jordan assessment: %#v", result)
	}
}

func TestMockProviderManagerSynthesizesJordanAssessment(t *testing.T) {
	result, err := (MockProvider{}).Invoke(context.Background(), Invocation{
		PersonaName: "Main Manager",
		PersonaRole: "Main Manager",
		Conversation: []ConversationMessage{
			{Role: domain.MessageUser, Body: "[demo:unknown-answer:legal-compliance] Are we in legal compliance?"},
			{Role: domain.MessageAgent, PersonaName: "Jordan", Body: "Jordan's compliance assessment found the available records insufficient."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Body, "Jordan's compliance assessment confirms") || result.Structured.Confidence != "low" {
		t.Fatalf("unexpected manager synthesis: %#v", result)
	}
}

func TestMockProviderTicketWorkspaceProposesSubtask(t *testing.T) {
	result, err := (MockProvider{}).Invoke(context.Background(), Invocation{
		PersonaName:  "Avery",
		PersonaRole:  "Product Strategist",
		Conversation: []ConversationMessage{{Role: domain.MessageUser, Body: "[demo:ticket-workspace] Break this work down."}},
		ToolGrants:   []domain.ToolGrant{{Capability: domain.CapabilityTicketCreate}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Body, "Avery reviewed") || len(result.Structured.ProposedActions) != 1 || result.Structured.ProposedActions[0].ActionType != "tickets.create" {
		t.Fatalf("unexpected ticket workspace result: %#v", result)
	}
}
