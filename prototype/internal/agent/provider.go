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

// ToolResult carries application-authorized output separately from the boardroom
// conversation. Providers must treat Content as untrusted source data, never as
// instructions that can override the persona or application policy.
type ToolResult struct {
	RequestID string          `json:"request_id"`
	Name      string          `json:"name"`
	Content   json.RawMessage `json:"content"`
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
	ToolResults        []ToolResult
	ToolGrants         []domain.ToolGrant
	Tools              []ToolDefinition
	CapabilityToken    string `json:"-"`
	MaxToolResultBytes int
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

// Delegation is a manager's request for a specialist contribution. It is part
// of the structured agent result rather than a provider tool call because
// Mainspring, not the model provider, owns boardroom turn scheduling.
type Delegation struct {
	Agent   string `json:"agent"`
	Request string `json:"request"`
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
	Delegations     []Delegation     `json:"delegations"`
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
	for index, delegation := range r.Delegations {
		if delegation.Agent == "" || delegation.Request == "" {
			return fmt.Errorf("delegation %d is invalid", index)
		}
	}
	return nil
}

func DefaultOutputSchema() json.RawMessage {
	schema := json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["contribution","findings","recommendations","questions","citations","proposed_actions","tool_requests","delegations","confidence"],
  "properties":{
    "contribution":{"type":"string","minLength":1},
    "findings":{"type":"array","items":{"type":"string"}},
    "recommendations":{"type":"array","items":{"type":"string"}},
    "questions":{"type":"array","items":{"type":"string"}},
    "citations":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","document_id","chunk_id","label"],"properties":{"id":{"type":"string"},"document_id":{"type":"string"},"chunk_id":{"type":"string"},"label":{"type":"string"}}}},
    "proposed_actions":{"type":"array","items":{"anyOf":[{"type":"object","additionalProperties":false,"required":["action_type","reason","payload","evidence"],"properties":{"action_type":{"type":"string","const":"email.send"},"reason":{"type":"string"},"payload":{"type":"object","additionalProperties":false,"required":["to","cc","subject","body"],"properties":{"to":{"type":"array","items":{"type":"string"}},"cc":{"type":"array","items":{"type":"string"}},"subject":{"type":"string"},"body":{"type":"string"}}},"evidence":{"type":"array","items":{"type":"string"}}}},{"type":"object","additionalProperties":false,"required":["action_type","reason","payload","evidence"],"properties":{"action_type":{"type":"string","const":"tickets.create"},"reason":{"type":"string"},"payload":{"type":"object","additionalProperties":false,"required":["title","description","priority","origin","search_query","parent_work_item_id"],"properties":{"title":{"type":"string"},"description":{"type":"string"},"priority":{"type":"string","enum":["low","normal","high","urgent"]},"origin":{"type":"string","enum":["unknown_answer","direct_request"]},"search_query":{"type":"string"},"parent_work_item_id":{"type":["string","null"]}}},"evidence":{"type":"array","items":{"type":"string"}}}}]}},
    "tool_requests":{"type":"array","items":{"anyOf":[{"type":"object","additionalProperties":false,"required":["id","name","arguments"],"properties":{"id":{"type":"string"},"name":{"type":"string","const":"documents.search"},"arguments":{"type":"object","additionalProperties":false,"required":["query","document_ids","limit"],"properties":{"query":{"type":"string"},"document_ids":{"type":"array","items":{"type":"string"}},"limit":{"type":"integer","minimum":1,"maximum":10}}}}},{"type":"object","additionalProperties":false,"required":["id","name","arguments"],"properties":{"id":{"type":"string"},"name":{"type":"string","const":"web.search"},"arguments":{"type":"object","additionalProperties":false,"required":["query","limit","include_domains","exclude_domains","recency_days"],"properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":10},"include_domains":{"type":"array","items":{"type":"string"}},"exclude_domains":{"type":"array","items":{"type":"string"}},"recency_days":{"type":["integer","null"],"minimum":1,"maximum":3650}}}}},{"type":"object","additionalProperties":false,"required":["id","name","arguments"],"properties":{"id":{"type":"string"},"name":{"type":"string","const":"web.read"},"arguments":{"type":"object","additionalProperties":false,"required":["url","max_characters"],"properties":{"url":{"type":"string"},"max_characters":{"type":["integer","null"],"minimum":500,"maximum":12000}}}}}]}},
    "delegations":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["agent","request"],"properties":{"agent":{"type":"string"},"request":{"type":"string"}}}},
    "confidence":{"type":"string","enum":["low","medium","high"]}
  }
}`)
	return withDocumentWriteToolSchemas(schema)
}

func withDocumentWriteToolSchemas(schema json.RawMessage) json.RawMessage {
	var root map[string]any
	if json.Unmarshal(schema, &root) != nil {
		return schema
	}
	properties, _ := root["properties"].(map[string]any)
	toolRequests, _ := properties["tool_requests"].(map[string]any)
	items, _ := toolRequests["items"].(map[string]any)
	anyOf, _ := items["anyOf"].([]any)
	for _, raw := range []string{
		`{"type":"object","additionalProperties":false,"required":["id","name","arguments"],"properties":{"id":{"type":"string"},"name":{"type":"string","const":"documents.create"},"arguments":{"type":"object","additionalProperties":false,"required":["name","media_type","content","change_summary"],"properties":{"name":{"type":"string"},"media_type":{"type":"string"},"content":{"type":"string"},"change_summary":{"type":"string"}}}}}`,
		`{"type":"object","additionalProperties":false,"required":["id","name","arguments"],"properties":{"id":{"type":"string"},"name":{"type":"string","const":"documents.update"},"arguments":{"type":"object","additionalProperties":false,"required":["document_id","name","media_type","content","change_summary"],"properties":{"document_id":{"type":"string"},"name":{"type":"string"},"media_type":{"type":"string"},"content":{"type":"string"},"change_summary":{"type":"string"}}}}}`,
	} {
		var definition any
		if json.Unmarshal([]byte(raw), &definition) == nil {
			anyOf = append(anyOf, definition)
		}
	}
	items["anyOf"] = anyOf
	encoded, err := json.Marshal(root)
	if err != nil {
		return schema
	}
	return encoded
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
	FailureInvalidRequest  FailureCategory = "invalid_request"
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
