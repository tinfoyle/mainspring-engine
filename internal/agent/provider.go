package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	PersonaDescription string
	SystemInstructions string
	Conversation       []ConversationMessage
	ToolGrants         []domain.ToolGrant
	Tools              []ToolDefinition
	CapabilityToken    string
	OutputSchema       json.RawMessage
	Timeout            time.Duration
	MaxInputTokens     int64
	MaxOutputTokens    int64
	MaxCostMicros      int64
	Provider           string
	Model              string
	ReasoningEffort    string
	Temperature        *float64
	TopP               *float64
	ResponseStyle      string
	CitationPolicy     string
	ActionPolicy       string
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ToolRequest struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Citation struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id,omitempty"`
	ChunkID    string `json:"chunk_id,omitempty"`
	Label      string `json:"label,omitempty"`
}

type ProposedAction struct {
	ActionType string          `json:"action_type"`
	Reason     string          `json:"reason"`
	Payload    json.RawMessage `json:"payload"`
	Evidence   []string        `json:"evidence,omitempty"`
}

type ResultEnvelope struct {
	Contribution    string           `json:"contribution"`
	Findings        []string         `json:"findings"`
	Recommendations []string         `json:"recommendations"`
	Questions       []string         `json:"questions"`
	Citations       []Citation       `json:"citations"`
	ProposedActions []ProposedAction `json:"proposed_actions"`
	ToolRequests    []ToolRequest    `json:"tool_requests"`
	Confidence      string           `json:"confidence"`
}

func (r ResultEnvelope) Validate() error {
	if r.Contribution == "" {
		return errors.New("contribution is required")
	}
	switch r.Confidence {
	case "low", "medium", "high":
	default:
		return fmt.Errorf("confidence must be low, medium, or high")
	}
	for index, action := range r.ProposedActions {
		if action.ActionType == "" || action.Reason == "" || len(action.Payload) == 0 || !json.Valid(action.Payload) {
			return fmt.Errorf("proposed action %d is invalid", index)
		}
	}
	for index, request := range r.ToolRequests {
		if request.ID == "" || request.Name == "" || len(request.Arguments) == 0 || !json.Valid(request.Arguments) {
			return fmt.Errorf("tool request %d is invalid", index)
		}
	}
	return nil
}

func DefaultOutputSchema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["contribution","findings","recommendations","questions","citations","proposed_actions","tool_requests","confidence"],
  "properties":{
    "contribution":{"type":"string","minLength":1},
    "findings":{"type":"array","items":{"type":"string"}},
    "recommendations":{"type":"array","items":{"type":"string"}},
    "questions":{"type":"array","items":{"type":"string"}},
    "citations":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","document_id","chunk_id","label"],"properties":{"id":{"type":"string"},"document_id":{"type":"string"},"chunk_id":{"type":"string"},"label":{"type":"string"}}}},
    "proposed_actions":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["action_type","reason","payload","evidence"],"properties":{"action_type":{"type":"string","enum":["email.send"]},"reason":{"type":"string"},"payload":{"type":"object","additionalProperties":false,"required":["to","cc","subject","body"],"properties":{"to":{"type":"array","items":{"type":"string"}},"cc":{"type":"array","items":{"type":"string"}},"subject":{"type":"string"},"body":{"type":"string"}}},"evidence":{"type":"array","items":{"type":"string"}}}}},
    "tool_requests":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","name","arguments"],"properties":{"id":{"type":"string"},"name":{"type":"string","enum":["documents.search"]},"arguments":{"type":"object","additionalProperties":false,"required":["query","document_ids","limit"],"properties":{"query":{"type":"string"},"document_ids":{"type":"array","items":{"type":"string"}},"limit":{"type":"integer","minimum":1,"maximum":10}}}}}},
    "confidence":{"type":"string","enum":["low","medium","high"]}
  }
}`)
}

type Usage struct {
	InputTokens       int64 `json:"input_tokens,omitempty"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens      int64 `json:"output_tokens,omitempty"`
}

type Result struct {
	Body             string         `json:"body"`
	Structured       ResultEnvelope `json:"structured"`
	Provider         string         `json:"provider"`
	Model            string         `json:"model,omitempty"`
	ProviderThreadID string         `json:"provider_thread_id,omitempty"`
	Usage            Usage          `json:"usage"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type FailureCategory string

const (
	FailureAuthentication  FailureCategory = "authentication_failed"
	FailureRateLimited     FailureCategory = "rate_limited"
	FailureUnavailable     FailureCategory = "provider_unavailable"
	FailureContextTooLarge FailureCategory = "context_too_large"
	FailureInvalidOutput   FailureCategory = "invalid_output"
	FailureTimeout         FailureCategory = "timeout"
	FailureCanceled        FailureCategory = "canceled"
	FailureTool            FailureCategory = "tool_failure"
	FailureUnknown         FailureCategory = "unknown"
)

type InvocationError struct {
	Category  FailureCategory
	Retryable bool
	Err       error
}

func (e *InvocationError) Error() string {
	if e.Err == nil {
		return string(e.Category)
	}
	return e.Err.Error()
}

func (e *InvocationError) Unwrap() error { return e.Err }

func Failure(err error) (FailureCategory, bool) {
	var invocationError *InvocationError
	if errors.As(err, &invocationError) {
		return invocationError.Category, invocationError.Retryable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureTimeout, true
	}
	if errors.Is(err, context.Canceled) {
		return FailureCanceled, false
	}
	return FailureUnknown, true
}

type Provider interface {
	Invoke(context.Context, Invocation) (Result, error)
	Cancel(context.Context, domain.InvocationID) error
	Capabilities() Capabilities
	Name() string
}
