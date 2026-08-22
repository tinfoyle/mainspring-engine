// Package modelgateway owns Spyglass's provider-neutral, one-step model
// invocation boundary. It deliberately does not orchestrate tools: a runner
// executes each returned tool call through its separately authorized
// capability gateway, then supplies the result to the next model step.
package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	SchemaVersion          = 1
	MaximumRequestBytes    = 256 << 10
	MaximumResponseBytes   = 256 << 10
	MaximumMessages        = 128
	MaximumTools           = 32
	MaximumInstructions    = 32 << 10
	MaximumMessageContent  = 64 << 10
	MaximumToolDescription = 4 << 10
	MaximumOutputTokens    = 32_768
	MaximumProviderTimeout = 5 * time.Minute
	MaximumUsageTokens     = 10_000_000
	MaximumPriceMicros     = 1_000_000_000_000
	ModelTurnCapability    = "agents.model.turn"
)

var (
	ErrInvalidRequest       = errors.New("model gateway request is invalid")
	ErrProviderUnavailable  = errors.New("model provider is unavailable")
	ErrProviderFailed       = errors.New("model provider invocation failed")
	ErrInvalidProviderReply = errors.New("model provider reply is invalid")
	validName               = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)
	validProvider           = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	validModel              = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
	validReasoning          = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ToolOutput struct {
	CallID string          `json:"call_id"`
	Output json.RawMessage `json:"output"`
}

// ToolExchange preserves provider output and the application-authorized tool
// result in their original order without exposing provider-specific state to
// the orchestrator. A request carries the complete bounded history.
type ToolExchange struct {
	Continuation json.RawMessage `json:"continuation"`
	ToolOutput   ToolOutput      `json:"tool_output"`
}

type OutputFormat struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

// Request is one provider step. InvocationID and OperationID are injected by
// the trusted broker adapter and are never accepted as runner authority.
type Request struct {
	SchemaVersion    int              `json:"schema_version"`
	InvocationID     string           `json:"invocation_id"`
	OperationID      string           `json:"operation_id"`
	Provider         string           `json:"provider"`
	Model            string           `json:"model"`
	ReasoningEffort  string           `json:"reasoning_effort,omitempty"`
	Instructions     string           `json:"instructions"`
	Messages         []Message        `json:"messages"`
	Tools            []ToolDefinition `json:"tools"`
	OutputFormat     OutputFormat     `json:"output_format"`
	MaximumOutTokens int              `json:"maximum_output_tokens"`
	History          []ToolExchange   `json:"history,omitempty"`
}

type ToolCall struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	CostMicros   int64 `json:"cost_micros"`
}

type Result struct {
	SchemaVersion int             `json:"schema_version"`
	Provider      string          `json:"provider"`
	Model         string          `json:"model"`
	ResponseID    string          `json:"response_id"`
	StopReason    string          `json:"stop_reason"`
	Output        json.RawMessage `json:"output,omitempty"`
	ToolCalls     []ToolCall      `json:"tool_calls,omitempty"`
	Continuation  json.RawMessage `json:"continuation,omitempty"`
	Usage         Usage           `json:"usage"`
}

type Provider interface {
	Invoke(context.Context, Request) (Result, error)
}

type Definition struct {
	Name     string
	Timeout  time.Duration
	Provider Provider
	Pricing  []ModelPrice
}

// ModelPrice is an operator-controlled price snapshot for one requested model.
// Values are micro-units of account currency per one million tokens. The
// gateway, rather than an untrusted runner or provider response, calculates
// the billable cost returned with every step.
type ModelPrice struct {
	Model                        string
	InputMicrosPerMillionTokens  int64
	OutputMicrosPerMillionTokens int64
}

type pricedProvider struct {
	definition Definition
	pricing    map[string]ModelPrice
}

type Service struct {
	providers map[string]pricedProvider
}

func New(definitions []Definition) (*Service, error) {
	if len(definitions) == 0 || len(definitions) > 16 {
		return nil, ErrInvalidRequest
	}
	providers := make(map[string]pricedProvider, len(definitions))
	for _, definition := range definitions {
		if !validProvider.MatchString(definition.Name) || definition.Provider == nil || definition.Timeout < time.Second || definition.Timeout > MaximumProviderTimeout || len(definition.Pricing) == 0 || len(definition.Pricing) > 256 {
			return nil, ErrInvalidRequest
		}
		if _, exists := providers[definition.Name]; exists {
			return nil, ErrInvalidRequest
		}
		prices := make(map[string]ModelPrice, len(definition.Pricing))
		for _, price := range definition.Pricing {
			if !validModel.MatchString(price.Model) || price.InputMicrosPerMillionTokens < 0 || price.InputMicrosPerMillionTokens > MaximumPriceMicros || price.OutputMicrosPerMillionTokens < 0 || price.OutputMicrosPerMillionTokens > MaximumPriceMicros {
				return nil, ErrInvalidRequest
			}
			if _, exists := prices[price.Model]; exists {
				return nil, ErrInvalidRequest
			}
			prices[price.Model] = price
		}
		providers[definition.Name] = pricedProvider{definition: definition, pricing: prices}
	}
	return &Service{providers: providers}, nil
}

func (s *Service) Invoke(ctx context.Context, request Request) (Result, error) {
	validated, err := ValidateRequest(request)
	if err != nil {
		return Result{}, err
	}
	provider, exists := s.providers[validated.Provider]
	if !exists {
		return Result{}, ErrProviderUnavailable
	}
	price, exists := provider.pricing[validated.Model]
	if !exists {
		return Result{}, ErrProviderUnavailable
	}
	executionContext, cancel := context.WithTimeout(ctx, provider.definition.Timeout)
	defer cancel()
	result, err := provider.definition.Provider.Invoke(executionContext, validated)
	if err != nil {
		return Result{}, err
	}
	if result.Usage.CostMicros != 0 {
		return Result{}, ErrInvalidProviderReply
	}
	result, err = ValidateResult(validated, result)
	if err != nil {
		return Result{}, err
	}
	result.Usage.CostMicros, err = price.cost(result.Usage)
	if err != nil {
		return Result{}, ErrInvalidProviderReply
	}
	return result, nil
}

func ValidateRequest(request Request) (Request, error) {
	request.Instructions = strings.TrimSpace(request.Instructions)
	if request.SchemaVersion != SchemaVersion || ids.Validate(request.InvocationID) != nil || ids.Validate(request.OperationID) != nil ||
		!validProvider.MatchString(request.Provider) || !validModel.MatchString(request.Model) ||
		(request.ReasoningEffort != "" && !validReasoning.MatchString(request.ReasoningEffort)) ||
		request.Instructions == "" || len(request.Instructions) > MaximumInstructions ||
		len(request.Messages) == 0 || len(request.Messages) > MaximumMessages || len(request.Tools) > MaximumTools ||
		request.MaximumOutTokens < 1 || request.MaximumOutTokens > MaximumOutputTokens || !validName.MatchString(request.OutputFormat.Name) {
		return Request{}, ErrInvalidRequest
	}
	outputSchema, err := canonicalObject(request.OutputFormat.Schema, 64<<10)
	if err != nil {
		return Request{}, ErrInvalidRequest
	}
	request.OutputFormat.Schema = outputSchema
	for index := range request.Messages {
		message := &request.Messages[index]
		message.Content = strings.TrimSpace(message.Content)
		if (message.Role != "user" && message.Role != "assistant") || message.Content == "" || len(message.Content) > MaximumMessageContent {
			return Request{}, ErrInvalidRequest
		}
	}
	toolNames := make(map[string]struct{}, len(request.Tools))
	for index := range request.Tools {
		tool := &request.Tools[index]
		tool.Description = strings.TrimSpace(tool.Description)
		if !validName.MatchString(tool.Name) || tool.Description == "" || len(tool.Description) > MaximumToolDescription {
			return Request{}, ErrInvalidRequest
		}
		if _, exists := toolNames[tool.Name]; exists {
			return Request{}, ErrInvalidRequest
		}
		toolNames[tool.Name] = struct{}{}
		tool.InputSchema, err = canonicalObject(tool.InputSchema, 64<<10)
		if err != nil {
			return Request{}, ErrInvalidRequest
		}
	}
	if len(request.History) > 5 {
		return Request{}, ErrInvalidRequest
	}
	for index := range request.History {
		exchange := &request.History[index]
		continuation, err := canonicalArray(exchange.Continuation, 128<<10)
		if err != nil || !validOpaqueID(exchange.ToolOutput.CallID) {
			return Request{}, ErrInvalidRequest
		}
		exchange.Continuation = continuation
		exchange.ToolOutput.Output, err = canonicalJSON(exchange.ToolOutput.Output, 64<<10)
		if err != nil {
			return Request{}, ErrInvalidRequest
		}
	}
	return request, nil
}

func ValidateResult(request Request, result Result) (Result, error) {
	if result.SchemaVersion != SchemaVersion || result.Provider != request.Provider || !validModel.MatchString(result.Model) || !validOpaqueID(result.ResponseID) ||
		result.Usage.InputTokens < 0 || result.Usage.InputTokens > MaximumUsageTokens || result.Usage.OutputTokens < 0 || result.Usage.OutputTokens > MaximumUsageTokens || result.Usage.TotalTokens < 0 || result.Usage.TotalTokens > MaximumUsageTokens || result.Usage.CostMicros < 0 ||
		result.Usage.TotalTokens != result.Usage.InputTokens+result.Usage.OutputTokens {
		return Result{}, ErrInvalidProviderReply
	}
	switch result.StopReason {
	case "completed":
		if len(result.ToolCalls) != 0 || len(result.Continuation) != 0 {
			return Result{}, ErrInvalidProviderReply
		}
		output, err := canonicalObject(result.Output, MaximumResponseBytes)
		if err != nil {
			return Result{}, ErrInvalidProviderReply
		}
		result.Output = output
	case "tool_call":
		if len(result.Output) != 0 || len(result.ToolCalls) != 1 {
			return Result{}, ErrInvalidProviderReply
		}
		continuation, err := canonicalArray(result.Continuation, 128<<10)
		if err != nil {
			return Result{}, ErrInvalidProviderReply
		}
		result.Continuation = continuation
		call := &result.ToolCalls[0]
		if !validOpaqueID(call.CallID) || !validName.MatchString(call.Name) || !slices.ContainsFunc(request.Tools, func(tool ToolDefinition) bool { return tool.Name == call.Name }) {
			return Result{}, ErrInvalidProviderReply
		}
		call.Arguments, err = canonicalObject(call.Arguments, 64<<10)
		if err != nil {
			return Result{}, ErrInvalidProviderReply
		}
	default:
		return Result{}, ErrInvalidProviderReply
	}
	return result, nil
}

func (price ModelPrice) cost(usage Usage) (int64, error) {
	input, err := tokenCost(usage.InputTokens, price.InputMicrosPerMillionTokens)
	if err != nil {
		return 0, err
	}
	output, err := tokenCost(usage.OutputTokens, price.OutputMicrosPerMillionTokens)
	if err != nil || input > math.MaxInt64-output {
		return 0, ErrInvalidProviderReply
	}
	return input + output, nil
}

func tokenCost(tokens, rate int64) (int64, error) {
	if tokens < 0 || tokens > MaximumUsageTokens || rate < 0 || rate > MaximumPriceMicros {
		return 0, ErrInvalidProviderReply
	}
	whole := (tokens / 1_000_000) * rate
	remainder := tokens % 1_000_000
	partial := remainder * rate
	if partial > 0 {
		partial = (partial + 999_999) / 1_000_000
	}
	if whole > math.MaxInt64-partial {
		return 0, ErrInvalidProviderReply
	}
	return whole + partial, nil
}

func canonicalObject(raw json.RawMessage, maximum int) (json.RawMessage, error) {
	return canonicalContainer(raw, maximum, byte('{'))
}

func canonicalArray(raw json.RawMessage, maximum int) (json.RawMessage, error) {
	return canonicalContainer(raw, maximum, byte('['))
}

func canonicalContainer(raw json.RawMessage, maximum int, prefix byte) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maximum {
		return nil, ErrInvalidRequest
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != prefix {
		return nil, ErrInvalidRequest
	}
	return canonicalJSON(trimmed, maximum)
}

func canonicalJSON(raw json.RawMessage, maximum int) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maximum {
		return nil, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || value == nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return nil, ErrInvalidRequest
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > maximum {
		return nil, ErrInvalidRequest
	}
	return canonical, nil
}

func validOpaqueID(value string) bool {
	return value != "" && len(value) <= 200 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n\t ")
}
