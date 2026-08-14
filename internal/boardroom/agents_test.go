package boardroom

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func validAgentInput() AgentInput {
	return AgentInput{
		BoardroomID: domain.NewBoardroomID(),
		Name:        "  Operations Advisor  ", Role: "  Operations  ",
		Description:        " Keeps the owner focused on delivery. ",
		SystemInstructions: " Review operating constraints and provide practical recommendations. ",
		Position:           1, Enabled: true, Settings: DefaultAgentSettings(),
		Grants: []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead, Conditions: map[string]string{"max_results": "3"}}},
	}
}

func TestAgentInputNormalizesAndAcceptsFullConfiguration(t *testing.T) {
	input := validAgentInput()
	temperature, topP := 0.4, 0.85
	input.Settings.Provider = "codex"
	input.Settings.Model = "gpt-example"
	input.Settings.ReasoningEffort = "high"
	input.Settings.Temperature = &temperature
	input.Settings.TopP = &topP
	input.Settings.ResponseStyle = "detailed"
	input.Settings.CitationPolicy = "required_for_research"
	input.Settings.ActionPolicy = "disabled"

	if err := input.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if input.Name != "Operations Advisor" || input.Role != "Operations" || strings.HasPrefix(input.SystemInstructions, " ") {
		t.Fatalf("input was not normalized: %#v", input)
	}
	wanted := map[domain.Capability]bool{domain.CapabilityWebSearch: true, domain.CapabilityWebRead: true}
	for _, grant := range input.Grants {
		delete(wanted, grant.Capability)
	}
	if len(wanted) != 0 {
		t.Fatalf("agent input missing baseline web capabilities: %#v", wanted)
	}
}

func TestAgentInputPreservesExplicitWebResearchConditions(t *testing.T) {
	input := validAgentInput()
	input.Grants = append(input.Grants,
		domain.ToolGrant{Capability: domain.CapabilityWebSearch, Conditions: map[string]string{"max_results": "3"}},
		domain.ToolGrant{Capability: domain.CapabilityWebRead, Conditions: map[string]string{"max_characters": "4000"}},
	)
	if err := input.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if len(input.Grants) != 6 {
		t.Fatalf("explicit baseline grants were duplicated: %#v", input.Grants)
	}
	if input.Grants[1].Conditions["max_results"] != "3" || input.Grants[2].Conditions["max_characters"] != "4000" {
		t.Fatalf("explicit baseline conditions were not preserved: %#v", input.Grants)
	}
}

func TestAgentInputRejectsInvalidLimitsAndDuplicateCapabilities(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AgentInput)
	}{
		{"provider", func(input *AgentInput) { input.Settings.Provider = "unknown" }},
		{"context", func(input *AgentInput) { input.Settings.ContextTokenLimit = 999 }},
		{"output", func(input *AgentInput) { input.Settings.MaxOutputTokens = 127 }},
		{"timeout", func(input *AgentInput) { input.Settings.TimeoutSeconds = 9 }},
		{"tool calls", func(input *AgentInput) { input.Settings.MaxToolCalls = 21 }},
		{"duplicate grant", func(input *AgentInput) { input.Grants = append(input.Grants, input.Grants[0]) }},
		{"unknown grant", func(input *AgentInput) { input.Grants = []domain.ToolGrant{{Capability: "shell.execute"}} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validAgentInput()
			test.mutate(&input)
			if err := input.NormalizeAndValidate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCitationPolicyRequiresReturnedResearchEvidence(t *testing.T) {
	returned := map[string]returnedCitation{"doc:1": {DocumentID: "document-1", ChunkID: "chunk-1"}}
	if err := enforceCitationPolicy("when_available", nil, returned); err != nil {
		t.Fatal(err)
	}
	if err := enforceCitationPolicy("required_for_research", nil, returned); err == nil {
		t.Fatal("expected required citation error")
	}
	citations := []agent.Citation{{ID: "doc:1", DocumentID: "document-1", ChunkID: "chunk-1"}}
	if err := enforceCitationPolicy("always", citations, returned); err != nil {
		t.Fatal(err)
	}
}

func TestRequiredResearchCitationsUseOnlyReturnedSources(t *testing.T) {
	returned := map[string]returnedCitation{
		"web:z": {},
		"doc:a": {DocumentID: "document-1", ChunkID: "chunk-1"},
	}
	citations := ensureRequiredResearchCitations("required_for_research", nil, returned)
	if len(citations) != 2 || citations[0].ID != "doc:a" || citations[1].ID != "web:z" {
		t.Fatalf("fallback citations = %#v", citations)
	}
	if err := validateCitations(citations, returned); err != nil {
		t.Fatal(err)
	}
}

func TestCitationFallbackDoesNotReplaceModelSelection(t *testing.T) {
	selected := []agent.Citation{{ID: "web:selected"}}
	got := ensureRequiredResearchCitations("required_for_research", selected, map[string]returnedCitation{"web:other": {}})
	if len(got) != 1 || got[0].ID != "web:selected" {
		t.Fatalf("selected citations changed: %#v", got)
	}
}

func TestFinalTurnCanValidateCitationFromEarlierAuthorizedToolResult(t *testing.T) {
	authorized := make(map[string]returnedCitation)
	collectRunCitations([]json.RawMessage{
		json.RawMessage(`{"results":[{"citation_id":"web:epa-608"}]}`),
		json.RawMessage(`{"results":[{"citation_id":"doc:license","document_id":"document-1","chunk_id":"chunk-1"}]}`),
	}, authorized)
	citations := []agent.Citation{
		{ID: "web:epa-608", Label: "EPA Section 608"},
		{ID: "doc:license", DocumentID: "document-1", ChunkID: "chunk-1"},
	}
	if err := validateCitations(citations, authorized); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizedCitationMetadataOverridesModelSuppliedBindings(t *testing.T) {
	authorized := map[string]returnedCitation{
		"web:license": {},
		"doc:license": {DocumentID: "document-1", ChunkID: "chunk-1"},
	}
	citations := []agent.Citation{
		{ID: "web:license", DocumentID: "invented-document", ChunkID: "invented-chunk"},
		{ID: "doc:license", DocumentID: "wrong-document", ChunkID: "wrong-chunk"},
	}

	bindAuthorizedCitationMetadata(citations, authorized)

	if citations[0].DocumentID != "" || citations[0].ChunkID != "" {
		t.Fatalf("web citation retained model-supplied bindings: %#v", citations[0])
	}
	if citations[1].DocumentID != "document-1" || citations[1].ChunkID != "chunk-1" {
		t.Fatalf("document citation did not use authorized bindings: %#v", citations[1])
	}
	if err := validateCitations(citations, authorized); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownCitationStillFailsAfterBinding(t *testing.T) {
	citations := []agent.Citation{{ID: "web:not-authorized", DocumentID: "invented"}}
	bindAuthorizedCitationMetadata(citations, map[string]returnedCitation{})
	if err := validateCitations(citations, map[string]returnedCitation{}); err == nil {
		t.Fatal("expected unknown citation to be rejected")
	}
}

func TestWebReadResultIsSafelyTrimmedToRemainingContextBudget(t *testing.T) {
	original, err := json.Marshal(map[string]any{"results": []map[string]any{{
		"url": "https://example.gov/license", "title": "Licensing", "citation_id": "web:license", "content": strings.Repeat("evidence ", 1000),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	bounded, err := fitToolOutputToBudget("web.read", original, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) > 2000 {
		t.Fatalf("bounded web result is %d bytes", len(bounded))
	}
	var decoded struct {
		Results []struct {
			CitationID string `json:"citation_id"`
			Content    string `json:"content"`
			Truncated  bool   `json:"truncated_by_context_budget"`
		} `json:"results"`
	}
	if err := json.Unmarshal(bounded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 1 || decoded.Results[0].CitationID != "web:license" || len(decoded.Results[0].Content) < 500 || !decoded.Results[0].Truncated {
		t.Fatalf("citation provenance or useful excerpt was lost: %#v", decoded.Results)
	}
}

func TestRetrievalBudgetExhaustionAlwaysProducesValidToolResult(t *testing.T) {
	for _, remaining := range []int{0, 20, 50, 200} {
		result := retrievalBudgetExhaustedResult(remaining)
		if !json.Valid(result) {
			t.Fatalf("remaining %d produced invalid JSON: %q", remaining, result)
		}
		if remaining >= 2 && len(result) > remaining {
			t.Fatalf("remaining %d produced %d-byte result", remaining, len(result))
		}
	}
}

func TestWebReadFailureIsRecoverableAndBounded(t *testing.T) {
	if !isRecoverableToolFailure("web.read") {
		t.Fatal("web.read failure should be recoverable")
	}
	for _, tool := range []string{"web.search", "documents.search", "tickets.create"} {
		if isRecoverableToolFailure(tool) {
			t.Fatalf("%s failure was incorrectly made recoverable", tool)
		}
	}
	encoded := recoverableToolFailureResult("web.read", errors.New(strings.Repeat("source failed ", 100)))
	var result struct {
		Error     string `json:"error"`
		Detail    string `json:"detail"`
		Retryable bool   `json:"retryable"`
		Tool      string `json:"tool"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if result.Tool != "web.read" || result.Retryable || result.Error == "" || len([]rune(result.Detail)) > 500 {
		t.Fatalf("unexpected recoverable failure result: %#v", result)
	}
}

func TestEffectiveRunGrantsIntersectAttachedDocuments(t *testing.T) {
	grants := []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead, Conditions: map[string]string{
		"document_ids": "doc-a,doc-b", "max_results": "3",
	}}}
	effective := effectiveRunGrants(grants, []string{"doc-b", "doc-c"})
	if len(effective) != 1 || effective[0].Conditions["document_ids"] != "doc-b" {
		t.Fatalf("unexpected effective document grant: %#v", effective)
	}
	if grants[0].Conditions["document_ids"] != "doc-a,doc-b" {
		t.Fatal("effective grant calculation mutated the persona snapshot")
	}
}

func TestEffectiveRunGrantsRemoveEmptyDocumentScope(t *testing.T) {
	grants := []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead, Conditions: map[string]string{"document_ids": "doc-a"}}}
	if effective := effectiveRunGrants(grants, []string{"doc-b"}); len(effective) != 0 {
		t.Fatalf("expected document tool to be removed, got %#v", effective)
	}
}

func TestEffectiveRunGrantsKeepTenantDocumentAccessWhenAttachmentsArePresent(t *testing.T) {
	grants := []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead, Conditions: map[string]string{"max_results": "5"}}}
	effective := effectiveRunGrants(grants, []string{"attached-document"})
	if len(effective) != 1 || effective[0].Conditions["document_ids"] != "" || effective[0].Conditions["max_results"] != "5" {
		t.Fatalf("attachments unexpectedly restricted the tenant knowledge base: %#v", effective)
	}
}

func TestBoundedDocumentQueryAlwaysProducesValidPreflightInput(t *testing.T) {
	if query := boundedDocumentQuery("   "); query != "*" {
		t.Fatalf("empty query = %q, want catalog lookup", query)
	}
	query := boundedDocumentQuery(strings.Repeat("knowledge ", 100))
	if len([]rune(query)) != 500 {
		t.Fatalf("bounded query contains %d runes", len([]rune(query)))
	}
}

func TestDocumentMutationResultCanBeCited(t *testing.T) {
	citations := map[string]returnedCitation{}
	collectReturnedCitations(json.RawMessage(`{"citation_id":"doc:abc","document_id":"abc","revision":1}`), citations)
	if citation, ok := citations["doc:abc"]; !ok || citation.DocumentID != "abc" {
		t.Fatalf("agent-created document citation was not authorized: %#v", citations)
	}
}

func TestInvocationContextBudgetsReserveDocumentRetrievalSpace(t *testing.T) {
	conversation, results := invocationContextBudgets(12000, 5, []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead}})
	if conversation != 6000 || results != 24000 {
		t.Fatalf("unexpected context budgets: conversation=%d results=%d", conversation, results)
	}
}
