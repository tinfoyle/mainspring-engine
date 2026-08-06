package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

type Capabilities struct {
	StructuredOutput bool
	ToolCalling      bool
	SessionResume    bool
	StreamingEvents  bool
}

type ConversationMessage struct {
	Role        domain.MessageRole `json:"role"`
	PersonaName string             `json:"persona_name,omitempty"`
	Body        string             `json:"body"`
}

type Invocation struct {
	ID                 domain.InvocationID
	TenantID           domain.TenantID
	BoardroomID        domain.BoardroomID
	RunID              domain.RunID
	PersonaID          domain.PersonaID
	PersonaName        string
	PersonaRole        string
	SystemInstructions string
	Conversation       []ConversationMessage
	ToolGrants         []domain.ToolGrant
	CapabilityToken    string
	OutputSchema       json.RawMessage
	Timeout            time.Duration
}

type Usage struct {
	InputTokens       int64 `json:"input_tokens,omitempty"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens      int64 `json:"output_tokens,omitempty"`
}

type Result struct {
	Body             string         `json:"body"`
	Provider         string         `json:"provider"`
	Model            string         `json:"model,omitempty"`
	ProviderThreadID string         `json:"provider_thread_id,omitempty"`
	Usage            Usage          `json:"usage"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type Provider interface {
	Invoke(context.Context, Invocation) (Result, error)
	Cancel(context.Context, domain.InvocationID) error
	Capabilities() Capabilities
	Name() string
}
