package modelgatewayapi

import (
	"bytes"
	"encoding/json"
	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewaySeparatesProviderCredentialsAndSupportsKimiOnly(t *testing.T) {
	counts := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		model := body["model"].(string)
		counts[model]++
		if r.URL.Path != "/v1/responses" {
			t.Errorf("wrong endpoint")
		}
		expectedKey := "Bearer openai-test-key"
		if model == "kimi-k3" {
			expectedKey = "Bearer kimi-test-key"
		}
		if r.Header.Get("Authorization") != expectedKey {
			t.Errorf("provider credential crossed boundary")
		}
		output := []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": `{"answer":"ok"}`}}}}
		if model == "kimi-k3" {
			if body["text"] != nil {
				t.Error("Kimi text constraint suppresses tools")
			}
			output = []any{map[string]any{"type": "function_call", "call_id": "finish_0", "name": "spyglass_final_result", "arguments": `{"answer":"ok"}`}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "resp_test", "model": model, "status": "completed", "output": output, "usage": map[string]any{"input_tokens": 1000, "input_tokens_details": map[string]any{"cached_tokens": 800}, "output_tokens": 100, "total_tokens": 1100}})
	}))
	defer upstream.Close()
	config := Config{OpenAIAPIKey: "openai-test-key", OpenAIOrigin: upstream.URL, OpenAIPricing: `{"gpt-test":{"input_micros_per_million_tokens":3000000,"output_micros_per_million_tokens":15000000}}`, OpenAIClient: upstream.Client(), KimiAPIKey: "kimi-test-key", KimiOrigin: upstream.URL, KimiPricing: `{"kimi-k3":{"input_micros_per_million_tokens":3000000,"cached_input_micros_per_million_tokens":300000,"output_micros_per_million_tokens":15000000}}`, KimiClient: upstream.Client()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, kimiOnly := range []bool{false, true} {
		if kimiOnly {
			config.OpenAIAPIKey, config.OpenAIPricing = "", ""
		}
		server, err := New(config, logger)
		if err != nil {
			t.Fatal(err)
		}
		for _, provider := range []string{"openai", "kimi"} {
			model := "gpt-test"
			wantCost := int64(4500)
			if provider == "kimi" {
				model = "kimi-k3"
				wantCost = 2340
			}
			input := modelgateway.Request{SchemaVersion: 1, InvocationID: "10000000-0000-4000-8000-000000000001", OperationID: "20000000-0000-4000-8000-000000000002", Provider: provider, Model: model, ReasoningEffort: "low", Instructions: "Answer briefly.", Messages: []modelgateway.Message{{Role: "user", Content: "Hello"}}, OutputFormat: modelgateway.OutputFormat{Name: "answer", Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)}, MaximumOutTokens: 1024}
			body, _ := json.Marshal(input)
			request := httptest.NewRequest(http.MethodPost, "/internal/v1/model-turns:invoke", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			if kimiOnly && provider == "openai" {
				if response.Code != http.StatusServiceUnavailable {
					t.Fatal("unconfigured provider available")
				}
				continue
			}
			var result modelgateway.Result
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Provider != provider || result.Usage.CostMicros != wantCost {
				t.Fatalf("wrong response: %d %s", response.Code, response.Body.String())
			}
		}
	}
	if counts["gpt-test"] != 1 || counts["kimi-k3"] != 2 {
		t.Fatalf("wrong provider calls %v", counts)
	}
}

func TestGatewayRejectsIncompleteProviderConfiguration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, config := range []Config{{}, {KimiAPIKey: "secret"}, {KimiPricing: `{"kimi-k3":{}}`}, {OpenAIAPIKey: "secret", OpenAIPricing: `{"gpt-test":{}}`, KimiPricing: `{"kimi-k3":{}}`}} {
		if _, err := New(config, logger); err == nil {
			t.Fatal("incomplete provider configuration accepted")
		}
	}
}
