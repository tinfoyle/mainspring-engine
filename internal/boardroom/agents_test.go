package boardroom

import (
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
