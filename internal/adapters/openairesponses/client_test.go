package openairesponses

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
)

func TestInvokeBuildsStrictStatelessRequestAndDecodesToolCall(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing provider authorization")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","model":"gpt-test","status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_work","arguments":"{}"}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`))
	}))
	defer server.Close()
	client, err := New(Config{APIKey: "secret", Origin: server.URL, HTTPClient: &http.Client{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Invoke(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "tool_call" || len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "read_work" || len(result.Continuation) == 0 {
		t.Fatalf("unexpected result %#v", result)
	}
	if received["store"] != false || received["parallel_tool_calls"] != false || received["tool_choice"] != "auto" {
		t.Fatalf("unsafe request controls %#v", received)
	}
	tools := received["tools"].([]any)
	if tools[0].(map[string]any)["strict"] != true {
		t.Fatal("tool schema was not strict")
	}
	text := received["text"].(map[string]any)["format"].(map[string]any)
	if text["strict"] != true || text["type"] != "json_schema" {
		t.Fatal("output schema was not strict")
	}
}

func TestInvokeAppendsOpaqueContinuationAndBoundToolOutput(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		_, _ = w.Write([]byte(`{"id":"resp_2","model":"gpt-test","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"answer\":\"done\"}"}]}],"usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}}`))
	}))
	defer server.Close()
	client, _ := New(Config{APIKey: "secret", Origin: server.URL, HTTPClient: server.Client()})
	req := request()
	req.Continuation = json.RawMessage(`[{"type":"function_call","call_id":"call_1","name":"read_work","arguments":"{}"}]`)
	req.ToolOutputs = []modelgateway.ToolOutput{{CallID: "call_1", Output: json.RawMessage(`{"active":3}`)}}
	result, err := client.Invoke(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "completed" || string(result.Output) != `{"answer":"done"}` {
		t.Fatalf("unexpected result %#v", result)
	}
	input := received["input"].([]any)
	last := input[len(input)-1].(map[string]any)
	if last["type"] != "function_call_output" || last["call_id"] != "call_1" || last["output"] != `{"active":3}` {
		t.Fatalf("unexpected tool output %#v", last)
	}
}

func TestProviderErrorsDoNotExposeResponseOrSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"sensitive"}}`, http.StatusBadRequest)
	}))
	defer server.Close()
	client, _ := New(Config{APIKey: "super-secret", Origin: server.URL, HTTPClient: server.Client()})
	_, err := client.Invoke(context.Background(), request())
	if err == nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("unsafe error %v", err)
	}
}

func request() modelgateway.Request {
	return modelgateway.Request{SchemaVersion: 1, InvocationID: "10000000-0000-4000-8000-000000000001", OperationID: "20000000-0000-4000-8000-000000000002", Provider: "openai", Model: "gpt-test", Instructions: "Help.", Messages: []modelgateway.Message{{Role: "user", Content: "Status?"}}, Tools: []modelgateway.ToolDefinition{{Name: "read_work", Description: "Read work.", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}}, OutputFormat: modelgateway.OutputFormat{Name: "agent_result", Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)}, MaximumOutTokens: 1000}
}
