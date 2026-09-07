package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const (
	testInvocation = "10000000-0000-4000-8000-000000000001"
	testOperation  = "20000000-0000-4000-8000-000000000002"
)

func TestServiceValidatesAndDispatchesOneStep(t *testing.T) {
	provider := &providerStub{result: Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test", ResponseID: "resp_1", StopReason: "completed", Output: json.RawMessage(`{"answer":"ok"}`), Usage: Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}}}
	service, err := New([]Definition{{Name: "openai", Timeout: time.Second, Provider: provider, Pricing: []ModelPrice{{Model: "gpt-test", InputMicrosPerMillionTokens: 1_000_000, OutputMicrosPerMillionTokens: 2_000_000}}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Invoke(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "completed" || string(result.Output) != `{"answer":"ok"}` || result.Usage.CostMicros != 13 || provider.calls != 1 {
		t.Fatalf("unexpected result %#v calls=%d", result, provider.calls)
	}
}

func TestServiceRejectsUnpricedModelsBeforeProviderCall(t *testing.T) {
	provider := &providerStub{}
	service, err := New([]Definition{{Name: "openai", Timeout: time.Second, Provider: provider, Pricing: []ModelPrice{{Model: "another-model"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke(context.Background(), validRequest()); !errors.Is(err, ErrProviderUnavailable) || provider.calls != 0 {
		t.Fatalf("unpriced result err=%v calls=%d", err, provider.calls)
	}
}

func TestParsePricingJSONIsExactAndDeterministic(t *testing.T) {
	prices, err := ParsePricingJSON(`{"z-model":{"input_micros_per_million_tokens":3,"output_micros_per_million_tokens":4},"a-model":{"input_micros_per_million_tokens":1,"output_micros_per_million_tokens":2}}`)
	if err != nil || len(prices) != 2 || prices[0].Model != "a-model" || prices[1].Model != "z-model" {
		t.Fatalf("prices=%+v err=%v", prices, err)
	}
	if _, err := ParsePricingJSON(`{"gpt-test":{"input_micros_per_million_tokens":1,"output_micros_per_million_tokens":2,"currency":"usd"}}`); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected unknown price field rejection, got %v", err)
	}
}

func TestRequestRejectsUnboundContinuationAndDuplicateTools(t *testing.T) {
	request := validRequest()
	request.History = []ToolExchange{{Continuation: json.RawMessage(`[]`)}}
	if _, err := ValidateRequest(request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid continuation, got %v", err)
	}
	request = validRequest()
	request.Tools = append(request.Tools, request.Tools[0])
	if _, err := ValidateRequest(request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected duplicate tool rejection, got %v", err)
	}
}

func TestResultRejectsUndeclaredAndParallelToolCalls(t *testing.T) {
	request, _ := ValidateRequest(validRequest())
	base := Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test", ResponseID: "resp_1", StopReason: "tool_call", Continuation: json.RawMessage(`[{"type":"function_call"}]`), Usage: Usage{}}
	base.ToolCalls = []ToolCall{{CallID: "call_1", Name: "not_granted", Arguments: json.RawMessage(`{}`)}}
	if _, err := ValidateResult(request, base); !errors.Is(err, ErrInvalidProviderReply) {
		t.Fatalf("expected undeclared tool rejection, got %v", err)
	}
	base.ToolCalls = []ToolCall{{CallID: "call_1", Name: "read_work", Arguments: json.RawMessage(`{}`)}, {CallID: "call_2", Name: "read_work", Arguments: json.RawMessage(`{}`)}}
	if _, err := ValidateResult(request, base); !errors.Is(err, ErrInvalidProviderReply) {
		t.Fatalf("expected parallel call rejection, got %v", err)
	}
}

func validRequest() Request {
	return Request{SchemaVersion: 1, InvocationID: testInvocation, OperationID: testOperation, Provider: "openai", Model: "gpt-test", ReasoningEffort: "medium", Instructions: "Provide a concise result.", Messages: []Message{{Role: "user", Content: "Summarize the work."}}, Tools: []ToolDefinition{{Name: "read_work", Description: "Read current work.", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}}, OutputFormat: OutputFormat{Name: "agent_result", Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)}, MaximumOutTokens: 1000}
}

type providerStub struct {
	result Result
	err    error
	calls  int
}

func (p *providerStub) Invoke(_ context.Context, _ Request) (Result, error) {
	p.calls++
	return p.result, p.err
}

func TestCachedInputPricingPreservesLegacyAndFreeCacheRates(t *testing.T) {
	usage := Usage{InputTokens: 1000, CachedInputTokens: 800, OutputTokens: 100}
	for _, tc := range []struct {
		name, raw string
		want      int64
	}{
		{"legacy", `{"kimi-k3":{"input_micros_per_million_tokens":3000000,"output_micros_per_million_tokens":15000000}}`, 4500},
		{"discount", `{"kimi-k3":{"input_micros_per_million_tokens":3000000,"cached_input_micros_per_million_tokens":300000,"output_micros_per_million_tokens":15000000}}`, 2340},
		{"free", `{"kimi-k3":{"input_micros_per_million_tokens":3000000,"cached_input_micros_per_million_tokens":0,"output_micros_per_million_tokens":15000000}}`, 2100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prices, err := ParsePricingJSON(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			cost, err := prices[0].cost(usage)
			if err != nil || cost != tc.want {
				t.Fatalf("cost=%d error=%v", cost, err)
			}
		})
	}
	if _, err := ParsePricingJSON(`{"kimi-k3":{"cached_input_micros_per_million_tokens":-1}}`); err == nil {
		t.Fatal("negative cache price accepted")
	}
}

func TestCachedInputPriceIsFrozenByGateway(t *testing.T) {
	cached := int64(300000)
	provider := &providerStub{result: Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test", ResponseID: "resp_cache", StopReason: "completed", Output: json.RawMessage(`{"answer":"ok"}`), Usage: Usage{InputTokens: 1000, CachedInputTokens: 800, OutputTokens: 100, TotalTokens: 1100}}}
	service, err := New([]Definition{{Name: "openai", Timeout: time.Second, Provider: provider, Pricing: []ModelPrice{{Model: "gpt-test", InputMicrosPerMillionTokens: 3000000, CachedInputMicrosPerMillionTokens: &cached, OutputMicrosPerMillionTokens: 15000000}}}})
	if err != nil {
		t.Fatal(err)
	}
	cached = 9000000
	result, err := service.Invoke(context.Background(), validRequest())
	if err != nil || result.Usage.CostMicros != 2340 {
		t.Fatalf("price changed: %+v %v", result, err)
	}
}
