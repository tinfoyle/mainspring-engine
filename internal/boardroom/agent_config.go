package boardroom

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var ErrPersonaNotFound = errors.New("agent not found")

type AgentInput struct {
	BoardroomID        domain.BoardroomID
	Name               string
	Role               string
	Description        string
	SystemInstructions string
	Position           int
	Enabled            bool
	Grants             []domain.ToolGrant
	Settings           AgentSettings
}

type AgentVersion struct {
	Version   int
	Name      string
	Role      string
	CreatedAt time.Time
}

func DefaultAgentSettings() AgentSettings {
	return AgentSettings{Provider: "inherit", ReasoningEffort: "inherit", ContextTokenLimit: 12000,
		MaxOutputTokens: 2000, TimeoutSeconds: 300, MaxToolCalls: 5, MaxCostMicros: 1,
		ResponseStyle: "balanced", CitationPolicy: "when_available", ActionPolicy: "propose_only"}
}

func (input *AgentInput) NormalizeAndValidate() error {
	input.Name = strings.TrimSpace(input.Name)
	input.Role = strings.TrimSpace(input.Role)
	input.Description = strings.TrimSpace(input.Description)
	input.SystemInstructions = strings.TrimSpace(input.SystemInstructions)
	input.Settings.Provider = strings.TrimSpace(input.Settings.Provider)
	input.Settings.Model = strings.TrimSpace(input.Settings.Model)
	input.Settings.ReasoningEffort = strings.TrimSpace(input.Settings.ReasoningEffort)
	if input.Name == "" || len(input.Name) > 80 {
		return errors.New("agent name must contain between 1 and 80 characters")
	}
	if input.Role == "" || len(input.Role) > 120 {
		return errors.New("agent role must contain between 1 and 120 characters")
	}
	if len(input.Description) > 500 {
		return errors.New("agent description cannot exceed 500 characters")
	}
	if len(input.SystemInstructions) < 20 || len(input.SystemInstructions) > 12000 {
		return errors.New("system instructions must contain between 20 and 12,000 characters")
	}
	if !oneOf(input.Settings.Provider, "inherit", "mock", "codex") {
		return errors.New("provider must be inherit, mock, or codex")
	}
	if len(input.Settings.Model) > 120 {
		return errors.New("model identifier cannot exceed 120 characters")
	}
	if !oneOf(input.Settings.ReasoningEffort, "inherit", "minimal", "low", "medium", "high", "xhigh") {
		return errors.New("reasoning effort is invalid")
	}
	if input.Settings.Temperature != nil && (*input.Settings.Temperature < 0 || *input.Settings.Temperature > 2) {
		return errors.New("temperature must be between 0 and 2")
	}
	if input.Settings.TopP != nil && (*input.Settings.TopP < 0 || *input.Settings.TopP > 1) {
		return errors.New("top-p must be between 0 and 1")
	}
	if input.Settings.ContextTokenLimit < 1000 || input.Settings.ContextTokenLimit > 200000 {
		return errors.New("context token limit must be between 1,000 and 200,000")
	}
	if input.Settings.MaxOutputTokens < 128 || input.Settings.MaxOutputTokens > 64000 {
		return errors.New("output token limit must be between 128 and 64,000")
	}
	if input.Settings.TimeoutSeconds < 10 || input.Settings.TimeoutSeconds > 3600 {
		return errors.New("timeout must be between 10 and 3,600 seconds")
	}
	if input.Settings.MaxToolCalls < 0 || input.Settings.MaxToolCalls > 20 {
		return errors.New("tool call limit must be between 0 and 20")
	}
	if input.Settings.MaxCostMicros < 0 || input.Settings.MaxCostMicros > 1_000_000_000 {
		return errors.New("cost reservation is invalid")
	}
	if !oneOf(input.Settings.ResponseStyle, "concise", "balanced", "detailed") ||
		!oneOf(input.Settings.CitationPolicy, "when_available", "required_for_research", "always") ||
		!oneOf(input.Settings.ActionPolicy, "disabled", "propose_only") {
		return errors.New("agent behavior policy is invalid")
	}
	seen := make(map[domain.Capability]bool)
	for _, grant := range input.Grants {
		if !KnownCapability(grant.Capability) || seen[grant.Capability] {
			return fmt.Errorf("tool capability %q is invalid or duplicated", grant.Capability)
		}
		seen[grant.Capability] = true
	}
	return nil
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func KnownCapability(value domain.Capability) bool {
	for _, capability := range AllCapabilities() {
		if value == capability {
			return true
		}
	}
	return false
}

func AllCapabilities() []domain.Capability {
	return []domain.Capability{
		domain.CapabilityWebSearch, domain.CapabilityWebRead, domain.CapabilityDocumentsRead, domain.CapabilityDocumentsComment,
		domain.CapabilityEmailDraft, domain.CapabilityEmailRead, domain.CapabilityEmailSend,
		domain.CapabilityTicketRead, domain.CapabilityTicketCreate, domain.CapabilityScheduleRead,
		domain.CapabilitySchedulePropose, domain.CapabilityScheduleModify, domain.CapabilityInvoicePrepare,
		domain.CapabilityInvoiceIssue, domain.CapabilityPaymentPropose, domain.CapabilityPaymentExecute,
	}
}
