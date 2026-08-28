// Command openai-fixture is a deterministic, local-only OpenAI Responses API
// stand-in used by the external-agent Docker certificate. The first provider
// call fails transiently so the certificate can prove the customer retry path;
// later calls return a schema-valid, content-free Agent result.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
)

var responseCalls atomic.Uint64

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/responses", responses)
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5_000_000_000}
	log.Fatal(server.ListenAndServe())
}

func responses(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request struct {
		Model string `json:"model"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	decoder.DisallowUnknownFields()
	// The real request contains more fields, so decode into a map after the
	// bounded syntax check and retain only the model needed by the fixture.
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil || json.Unmarshal(raw["model"], &request.Model) != nil || request.Model == "" {
		http.Error(w, "invalid fixture request", http.StatusBadRequest)
		return
	}
	call := responseCalls.Add(1)
	if call == 1 {
		http.Error(w, "deterministic transient provider failure", http.StatusServiceUnavailable)
		return
	}
	result := `{"contribution":"The parts-order work is ready for human review.","findings":[],"recommendations":[],"questions":[],"citations":[],"proposed_actions":[],"delegations":[],"confidence":"high"}`
	response := map[string]any{
		"id": fmt.Sprintf("resp_local_%d", call), "model": request.Model, "status": "completed",
		"output": []any{
			map[string]any{
				"type": "message",
				"content": []any{
					map[string]any{"type": "output_text", "text": result},
				},
			},
		},
		"usage": map[string]any{"input_tokens": 40, "output_tokens": 20, "total_tokens": 60, "input_tokens_details": map[string]any{"cached_tokens": 0}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
