// Package runneragents contains the compiled Agents invocation executor. It
// owns a bounded deterministic model/tool loop but has no provider credential,
// Account selector, database access, or arbitrary network client.
package runneragents

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

	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerexecution"
	"github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const TurnExecutionKind = "agent.turn.execute"

var (
	ErrInvalidInput       = errors.New("agent turn runner input is invalid")
	ErrCapabilityDenied   = errors.New("agent turn capability was not granted")
	ErrModelFailed        = errors.New("agent model step failed")
	ErrToolFailed         = errors.New("agent tool step failed")
	ErrInvalidModelOutput = errors.New("agent model output is invalid")
	ErrToolLimit          = errors.New("agent tool step limit reached")
	ErrTokenLimit         = errors.New("agent token ceiling reached")
	ErrCostLimit          = errors.New("agent cost ceiling reached")
	validProvider         = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	validModel            = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
)

type Tool struct {
	Name        string          `json:"name"`
	Capability  string          `json:"capability"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type TurnInput struct {
	Provider            string                    `json:"provider"`
	Models              []string                  `json:"models"`
	ReasoningEffort     string                    `json:"reasoning_effort,omitempty"`
	Instructions        string                    `json:"instructions"`
	Messages            []modelgateway.Message    `json:"messages"`
	Tools               []Tool                    `json:"tools"`
	OutputFormat        modelgateway.OutputFormat `json:"output_format"`
	MaximumInputTokens  int64                     `json:"maximum_input_tokens"`
	MaximumOutputTokens int                       `json:"maximum_output_tokens"`
	MaximumCostMicros   int64                     `json:"maximum_cost_micros"`
	MaximumToolSteps    int                       `json:"maximum_tool_steps"`
	ModelOperationIDs   []string                  `json:"model_operation_ids"`
	ToolOperationIDs    []string                  `json:"tool_operation_ids"`
}

type TurnOutput struct {
	Provider       string                `json:"provider"`
	RequestedModel string                `json:"requested_model"`
	ResponseModel  string                `json:"response_model"`
	ResponseID     string                `json:"response_id"`
	Usage          modelgateway.Usage    `json:"usage"`
	Result         agents.ResultEnvelope `json:"result"`
}

type TurnExecutor struct{}

// ValidateTurnOutput is the trusted persistence-boundary check for an Agent
// turn. Expected values come from the immutable invocation plan, not from the
// runner result being validated.
func ValidateTurnOutput(output TurnOutput, expectedProvider string, permittedModels []string) (TurnOutput, error) {
	output.Provider = strings.TrimSpace(output.Provider)
	output.RequestedModel = strings.TrimSpace(output.RequestedModel)
	output.ResponseModel = strings.TrimSpace(output.ResponseModel)
	output.ResponseID = strings.TrimSpace(output.ResponseID)
	if !validProvider.MatchString(output.Provider) || output.Provider != expectedProvider ||
		!validModel.MatchString(output.RequestedModel) || !slices.Contains(permittedModels, output.RequestedModel) ||
		!validModel.MatchString(output.ResponseModel) || output.ResponseID == "" || len(output.ResponseID) > 200 ||
		strings.ContainsAny(output.ResponseID, " \t\r\n") || output.Usage.InputTokens < 0 || output.Usage.CachedInputTokens < 0 || output.Usage.CachedInputTokens > output.Usage.InputTokens || output.Usage.OutputTokens < 0 || output.Usage.ToolInvocations < 0 || output.Usage.CostMicros < 0 ||
		output.Usage.TotalTokens < 0 || output.Usage.TotalTokens != output.Usage.InputTokens+output.Usage.OutputTokens {
		return TurnOutput{}, ErrInvalidModelOutput
	}
	result, err := agents.ValidateResult(output.Result)
	if err != nil {
		return TurnOutput{}, ErrInvalidModelOutput
	}
	output.Result = result
	return output, nil
}

func (TurnExecutor) Execute(ctx context.Context, execution runnerexecution.Execution) (json.RawMessage, error) {
	if execution.Kind != TurnExecutionKind || execution.Gateway == nil || !slices.Contains(execution.Capabilities, modelgateway.ModelTurnCapability) {
		return nil, coded("capability_not_granted", ErrCapabilityDenied)
	}
	input, err := decodeExact[TurnInput](execution.Input)
	if err != nil || input.MaximumInputTokens < 1 || input.MaximumInputTokens > 2_000_000 || input.MaximumOutputTokens < 1 || input.MaximumOutputTokens > modelgateway.MaximumOutputTokens || input.MaximumCostMicros < 0 || input.MaximumCostMicros > 1_000_000_000 || input.MaximumToolSteps < 0 || input.MaximumToolSteps > agents.MaximumToolSteps ||
		len(input.Models) < 1 || len(input.Models) > agents.MaximumFallbackModels+1 || len(input.ModelOperationIDs) != (input.MaximumToolSteps+1)*len(input.Models) || len(input.ToolOperationIDs) != input.MaximumToolSteps {
		return nil, coded("invalid_input", ErrInvalidInput)
	}
	for index, model := range input.Models {
		if !validModel.MatchString(model) || slices.Contains(input.Models[:index], model) {
			return nil, coded("invalid_input", ErrInvalidInput)
		}
	}
	operations := append(append([]string(nil), input.ModelOperationIDs...), input.ToolOperationIDs...)
	for index, operation := range operations {
		if ids.Validate(operation) != nil || slices.Contains(operations[:index], operation) {
			return nil, coded("invalid_input", ErrInvalidInput)
		}
	}
	definitions := make([]modelgateway.ToolDefinition, len(input.Tools))
	capabilities := make(map[string]string, len(input.Tools))
	for index, tool := range input.Tools {
		if tool.Capability == modelgateway.ModelTurnCapability || !slices.Contains(execution.Capabilities, tool.Capability) {
			return nil, coded("capability_not_granted", ErrCapabilityDenied)
		}
		if _, duplicate := capabilities[tool.Name]; duplicate {
			return nil, coded("invalid_input", ErrInvalidInput)
		}
		capabilities[tool.Name] = tool.Capability
		definitions[index] = modelgateway.ToolDefinition{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}
	}
	if (input.MaximumToolSteps == 0) != (len(input.Tools) == 0) {
		return nil, coded("invalid_input", ErrInvalidInput)
	}
	request := modelgateway.Request{SchemaVersion: modelgateway.SchemaVersion, Provider: input.Provider, ReasoningEffort: input.ReasoningEffort, Instructions: input.Instructions, Messages: input.Messages, Tools: definitions, OutputFormat: input.OutputFormat, MaximumOutTokens: input.MaximumOutputTokens}
	var usage modelgateway.Usage
	selectedModel := -1
	for step := 0; ; step++ {
		var modelResult runnercapability.Result
		attemptStart, attemptEnd := 0, len(input.Models)
		if selectedModel >= 0 {
			attemptStart, attemptEnd = selectedModel, selectedModel+1
		}
		for target := attemptStart; target < attemptEnd; target++ {
			request.Model = input.Models[target]
			requestRaw, err := json.Marshal(request)
			if err != nil {
				return nil, coded("invalid_input", ErrInvalidInput)
			}
			modelResult, err = execution.Gateway.Invoke(ctx, runnercapability.Call{SchemaVersion: runnercapability.SchemaVersion, OperationID: input.ModelOperationIDs[step*len(input.Models)+target], Capability: modelgateway.ModelTurnCapability, Input: requestRaw})
			if err == nil {
				selectedModel = target
				break
			}
			reason, transient := runnercapability.ExecutionFailureCode(err)
			if selectedModel >= 0 || !transient || reason != "model_provider_unavailable" || target+1 == attemptEnd {
				return nil, coded("model_step_failed", ErrModelFailed)
			}
		}
		if modelResult.SchemaVersion != runnercapability.SchemaVersion {
			return nil, coded("model_output_invalid", ErrInvalidModelOutput)
		}
		result, err := decodeExact[modelgateway.Result](modelResult.Output)
		if err != nil {
			return nil, coded("model_output_invalid", ErrInvalidModelOutput)
		}
		result, err = modelgateway.ValidateResult(request, result)
		if err != nil || usage.InputTokens > math.MaxInt64-result.Usage.InputTokens || usage.CachedInputTokens > math.MaxInt64-result.Usage.CachedInputTokens || usage.OutputTokens > math.MaxInt64-result.Usage.OutputTokens || usage.TotalTokens > math.MaxInt64-result.Usage.TotalTokens || usage.CostMicros > math.MaxInt64-result.Usage.CostMicros {
			return nil, coded("model_output_invalid", ErrInvalidModelOutput)
		}
		usage.InputTokens += result.Usage.InputTokens
		usage.CachedInputTokens += result.Usage.CachedInputTokens
		usage.OutputTokens += result.Usage.OutputTokens
		usage.TotalTokens += result.Usage.TotalTokens
		usage.CostMicros += result.Usage.CostMicros
		if usage.InputTokens > input.MaximumInputTokens || usage.OutputTokens > int64(input.MaximumOutputTokens) {
			return nil, coded("token_limit_exceeded", ErrTokenLimit)
		}
		if usage.CostMicros > input.MaximumCostMicros {
			return nil, coded("cost_limit_exceeded", ErrCostLimit)
		}
		switch result.StopReason {
		case "completed":
			if len(result.ToolCalls) != 0 || len(result.Continuation) != 0 {
				return nil, coded("model_output_invalid", ErrInvalidModelOutput)
			}
			structured, err := decodeExact[agents.ResultEnvelope](result.Output)
			if err != nil {
				return nil, coded("model_output_invalid", ErrInvalidModelOutput)
			}
			structured, err = agents.ValidateResult(structured)
			if err != nil {
				return nil, coded("model_output_invalid", ErrInvalidModelOutput)
			}
			turnOutput, err := ValidateTurnOutput(TurnOutput{Provider: result.Provider, RequestedModel: input.Models[selectedModel], ResponseModel: result.Model, ResponseID: result.ResponseID, Usage: usage, Result: structured}, input.Provider, input.Models)
			if err != nil {
				return nil, coded("model_output_invalid", ErrInvalidModelOutput)
			}
			output, err := json.Marshal(turnOutput)
			if err != nil {
				return nil, coded("model_output_invalid", ErrInvalidModelOutput)
			}
			return output, nil
		case "tool_call":
			if step >= input.MaximumToolSteps {
				return nil, coded("tool_step_limit", ErrToolLimit)
			}
			if len(result.ToolCalls) != 1 || len(result.Output) != 0 || len(result.Continuation) == 0 {
				return nil, coded("model_output_invalid", ErrInvalidModelOutput)
			}
			call := result.ToolCalls[0]
			usage.ToolInvocations++
			capability, granted := capabilities[call.Name]
			if !granted {
				return nil, coded("tool_not_granted", ErrCapabilityDenied)
			}
			toolResult, err := execution.Gateway.Invoke(ctx, runnercapability.Call{SchemaVersion: runnercapability.SchemaVersion, OperationID: input.ToolOperationIDs[step], Capability: capability, Input: call.Arguments})
			if err != nil {
				code, safe := runnercapability.ExecutionFailureCode(err)
				if !safe || ctx.Err() != nil {
					return nil, coded("tool_step_failed", ErrToolFailed)
				}
				output, _ := json.Marshal(map[string]any{
					"ok": false, "error": code,
					"message": "The tool did not return a successful result. Do not claim success. The outcome of a mutation may be uncertain; do not repeat it with a new operation. Continue other useful work or explain the limitation.",
				})
				toolResult = runnercapability.Result{SchemaVersion: runnercapability.SchemaVersion, Output: output}
			}
			if toolResult.SchemaVersion != runnercapability.SchemaVersion || len(toolResult.Output) == 0 {
				return nil, coded("tool_output_invalid", ErrToolFailed)
			}
			request.History = append(request.History, modelgateway.ToolExchange{Continuation: result.Continuation, ToolOutput: modelgateway.ToolOutput{CallID: call.CallID, Output: toolResult.Output}})
			request.MaximumOutTokens = input.MaximumOutputTokens - int(usage.OutputTokens)
			if request.MaximumOutTokens < 1 {
				return nil, coded("token_limit_exceeded", ErrTokenLimit)
			}
		default:
			return nil, coded("model_output_invalid", ErrInvalidModelOutput)
		}
	}
}

type executorError struct {
	code  string
	cause error
}

func coded(code string, cause error) error { return &executorError{code: code, cause: cause} }
func (e *executorError) Error() string     { return e.code }
func (e *executorError) Code() string      { return e.code }
func (e *executorError) Unwrap() error     { return e.cause }

func decodeExact[T any](raw json.RawMessage) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || raw[0] != '{' || decoder.Decode(&value) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return value, ErrInvalidInput
	}
	return value, nil
}

var _ runnerexecution.Executor = TurnExecutor{}
