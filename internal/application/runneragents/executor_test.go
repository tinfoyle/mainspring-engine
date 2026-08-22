package runneragents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerexecution"
)

const (
	modelOperation1 = "10000000-0000-4000-8000-000000000001"
	toolOperation1  = "20000000-0000-4000-8000-000000000002"
	modelOperation2 = "30000000-0000-4000-8000-000000000003"
	modelOperation3 = "40000000-0000-4000-8000-000000000004"
	modelOperation4 = "50000000-0000-4000-8000-000000000005"
)

func TestTurnExecutorRunsOneDeterministicToolLoopAndValidatesResult(t *testing.T) {
	first := modelgateway.Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test", ResponseID: "resp_1", StopReason: "tool_call", ToolCalls: []modelgateway.ToolCall{{CallID: "call_1", Name: "read_work", Arguments: json.RawMessage(`{}`)}}, Continuation: json.RawMessage(`[{"type":"function_call","call_id":"call_1","name":"read_work","arguments":"{}"}]`), Usage: modelgateway.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12, CostMicros: 5}}
	second := modelgateway.Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test-2026", ResponseID: "resp_2", StopReason: "completed", Output: validStructuredResult(), Usage: modelgateway.Usage{InputTokens: 14, OutputTokens: 6, TotalTokens: 20, CostMicros: 7}}
	gateway := &gatewayStub{results: []runnercapability.Result{capabilityResult(first), {SchemaVersion: 1, Output: json.RawMessage(`{"active":3}`)}, capabilityResult(second)}}
	input := validTurnInput()
	raw, _ := json.Marshal(input)
	output, err := (TurnExecutor{}).Execute(context.Background(), runnerexecution.Execution{Kind: TurnExecutionKind, Input: raw, Capabilities: []string{modelgateway.ModelTurnCapability, "work.summary.read"}, Gateway: gateway, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	var turn TurnOutput
	if err := json.Unmarshal(output, &turn); err != nil {
		t.Fatal(err)
	}
	if turn.Result.Contribution != "Reconcile the backlog." || turn.Usage.TotalTokens != 32 || turn.Usage.CostMicros != 12 || turn.ResponseID != "resp_2" || turn.ResponseModel != "gpt-test-2026" {
		t.Fatalf("unexpected output %#v", turn)
	}
	if len(gateway.calls) != 3 || gateway.calls[0].Capability != modelgateway.ModelTurnCapability || gateway.calls[0].OperationID != modelOperation1 || gateway.calls[1].Capability != "work.summary.read" || gateway.calls[1].OperationID != toolOperation1 || gateway.calls[2].OperationID != modelOperation2 {
		t.Fatalf("unexpected calls %#v", gateway.calls)
	}
	var continued modelgateway.Request
	if err := json.Unmarshal(gateway.calls[2].Input, &continued); err != nil {
		t.Fatal(err)
	}
	if len(continued.History) != 1 || continued.History[0].ToolOutput.CallID != "call_1" || string(continued.History[0].ToolOutput.Output) != `{"active":3}` {
		t.Fatalf("tool output not bound %#v", continued)
	}
	if continued.MaximumOutTokens != 998 {
		t.Fatalf("remaining output ceiling=%d", continued.MaximumOutTokens)
	}
}

func TestTurnExecutorStopsAtImmutableCostCeiling(t *testing.T) {
	result := modelgateway.Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test", ResponseID: "resp_1", StopReason: "completed", Output: validStructuredResult(), Usage: modelgateway.Usage{CostMicros: 11}}
	input := validTurnInput()
	input.Tools, input.ToolOperationIDs, input.MaximumToolSteps = nil, nil, 0
	input.ModelOperationIDs = []string{modelOperation1}
	input.MaximumCostMicros = 10
	raw, _ := json.Marshal(input)
	_, err := (TurnExecutor{}).Execute(context.Background(), runnerexecution.Execution{Kind: TurnExecutionKind, Input: raw, Capabilities: []string{modelgateway.ModelTurnCapability}, Gateway: &gatewayStub{results: []runnercapability.Result{capabilityResult(result)}}})
	if !errors.Is(err, ErrCostLimit) {
		t.Fatalf("expected cost limit, got %v", err)
	}
}

func TestTurnExecutorSelectsFallbackOnlyForInitialProviderUnavailability(t *testing.T) {
	result := modelgateway.Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-fallback-2026", ResponseID: "resp_fallback", StopReason: "completed", Output: validStructuredResult(), Usage: modelgateway.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10, CostMicros: 4}}
	input := validTurnInput()
	input.Models = []string{"gpt-primary", "gpt-fallback"}
	input.Tools, input.ToolOperationIDs, input.MaximumToolSteps = nil, nil, 0
	input.ModelOperationIDs = []string{modelOperation1, modelOperation2}
	gateway := &gatewayStub{results: []runnercapability.Result{{}, capabilityResult(result)}, errors: []error{runnercapability.NewExecutionFailure("model_provider_unavailable")}}
	raw, _ := json.Marshal(input)
	output, err := (TurnExecutor{}).Execute(context.Background(), runnerexecution.Execution{Kind: TurnExecutionKind, Input: raw, Capabilities: []string{modelgateway.ModelTurnCapability}, Gateway: gateway})
	if err != nil {
		t.Fatal(err)
	}
	var turn TurnOutput
	if err := json.Unmarshal(output, &turn); err != nil {
		t.Fatal(err)
	}
	if turn.RequestedModel != "gpt-fallback" || len(gateway.calls) != 2 || gateway.calls[0].OperationID != modelOperation1 || gateway.calls[1].OperationID != modelOperation2 {
		t.Fatalf("unexpected fallback output=%+v calls=%+v", turn, gateway.calls)
	}
}

func TestTurnExecutorDoesNotFallbackForUnclassifiedFailure(t *testing.T) {
	input := validTurnInput()
	input.Models = []string{"gpt-primary", "gpt-fallback"}
	input.Tools, input.ToolOperationIDs, input.MaximumToolSteps = nil, nil, 0
	input.ModelOperationIDs = []string{modelOperation1, modelOperation2}
	raw, _ := json.Marshal(input)
	gateway := &gatewayStub{errors: []error{errors.New("network failed")}}
	_, err := (TurnExecutor{}).Execute(context.Background(), runnerexecution.Execution{Kind: TurnExecutionKind, Input: raw, Capabilities: []string{modelgateway.ModelTurnCapability}, Gateway: gateway})
	if !errors.Is(err, ErrModelFailed) || len(gateway.calls) != 1 {
		t.Fatalf("expected terminal primary failure, calls=%d err=%v", len(gateway.calls), err)
	}
}

func TestTurnExecutorRejectsUngrantableToolAndStepOverflow(t *testing.T) {
	input := validTurnInput()
	raw, _ := json.Marshal(input)
	_, err := (TurnExecutor{}).Execute(context.Background(), runnerexecution.Execution{Kind: TurnExecutionKind, Input: raw, Capabilities: []string{modelgateway.ModelTurnCapability}, Gateway: &gatewayStub{}})
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("expected tool grant denial, got %v", err)
	}

	overflow := modelgateway.Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test", ResponseID: "resp_2", StopReason: "tool_call", ToolCalls: []modelgateway.ToolCall{{CallID: "call_2", Name: "read_work", Arguments: json.RawMessage(`{}`)}}, Continuation: json.RawMessage(`[{"type":"function_call"}]`), Usage: modelgateway.Usage{}}
	gateway := &gatewayStub{results: []runnercapability.Result{capabilityResult(overflow), {SchemaVersion: 1, Output: json.RawMessage(`{}`)}, capabilityResult(overflow)}}
	_, err = (TurnExecutor{}).Execute(context.Background(), runnerexecution.Execution{Kind: TurnExecutionKind, Input: raw, Capabilities: []string{modelgateway.ModelTurnCapability, "work.summary.read"}, Gateway: gateway})
	if !errors.Is(err, ErrToolLimit) {
		t.Fatalf("expected tool limit, got %v", err)
	}
}

func TestTurnExecutorRejectsMissingStrictResultLists(t *testing.T) {
	result := modelgateway.Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test", ResponseID: "resp_1", StopReason: "completed", Output: json.RawMessage(`{"contribution":"Incomplete","confidence":"high"}`), Usage: modelgateway.Usage{}}
	gateway := &gatewayStub{results: []runnercapability.Result{capabilityResult(result)}}
	input := validTurnInput()
	input.Tools, input.ToolOperationIDs, input.MaximumToolSteps = nil, nil, 0
	input.ModelOperationIDs = []string{modelOperation1}
	raw, _ := json.Marshal(input)
	_, err := (TurnExecutor{}).Execute(context.Background(), runnerexecution.Execution{Kind: TurnExecutionKind, Input: raw, Capabilities: []string{modelgateway.ModelTurnCapability}, Gateway: gateway})
	if !errors.Is(err, ErrInvalidModelOutput) {
		t.Fatalf("expected invalid model output, got %v", err)
	}
}

func validTurnInput() TurnInput {
	return TurnInput{Provider: "openai", Models: []string{"gpt-test"}, ReasoningEffort: "medium", Instructions: "Act as the operations lead.", Messages: []modelgateway.Message{{Role: "user", Content: "Review the backlog."}}, Tools: []Tool{{Name: "read_work", Capability: "work.summary.read", Description: "Read current Work summary.", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}}, OutputFormat: modelgateway.OutputFormat{Name: "agent_result", Schema: json.RawMessage(`{"type":"object"}`)}, MaximumInputTokens: 100000, MaximumOutputTokens: 1000, MaximumCostMicros: 1000, MaximumToolSteps: 1, ModelOperationIDs: []string{modelOperation1, modelOperation2}, ToolOperationIDs: []string{toolOperation1}}
}

func validStructuredResult() json.RawMessage {
	return json.RawMessage(`{"contribution":"Reconcile the backlog.","findings":[],"recommendations":[],"questions":[],"citations":[],"proposed_actions":[],"delegations":[],"confidence":"high"}`)
}

func capabilityResult(value modelgateway.Result) runnercapability.Result {
	raw, _ := json.Marshal(value)
	return runnercapability.Result{SchemaVersion: 1, Output: raw}
}

type gatewayStub struct {
	results []runnercapability.Result
	errors  []error
	calls   []runnercapability.Call
}

func (g *gatewayStub) Invoke(_ context.Context, call runnercapability.Call) (runnercapability.Result, error) {
	g.calls = append(g.calls, call)
	index := len(g.calls) - 1
	if index < len(g.errors) && g.errors[index] != nil {
		return runnercapability.Result{}, g.errors[index]
	}
	if index >= len(g.results) {
		return runnercapability.Result{}, errors.New("unexpected call")
	}
	return g.results[index], nil
}
