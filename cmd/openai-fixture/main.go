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
	"strings"
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
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Input        []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"input"`
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
	_ = json.Unmarshal(raw["instructions"], &request.Instructions)
	_ = json.Unmarshal(raw["input"], &request.Input)
	result := fixtureResult(request.Instructions, request.Input)
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

func fixtureResult(instructions string, input []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) string {
	if !strings.Contains(instructions, "first and main operations agent") {
		return `{"contribution":"The parts-order work is ready for human review.","findings":[],"recommendations":[],"questions":[],"citations":[],"proposed_actions":[],"delegations":[],"confidence":"high","baseline":null}`
	}
	latest := ""
	if len(input) != 0 {
		latest = strings.ToLower(input[len(input)-1].Content)
	}
	if strings.Contains(latest, "approved setup work from the business baseline") {
		return `{"contribution":"I can set up the dispatch review after one owner decision.","findings":[],"recommendations":[],"questions":["Which calendar or job system should I use as the source for the daily dispatch review?"],"citations":[],"proposed_actions":[],"delegations":[],"confidence":"high","baseline":null}`
	}
	baseline := map[string]any{
		"business_type": "Still learning", "business_type_confidence": "low", "captured_topics": []string{},
		"next_question_key": "baseline.business_description", "next_question": "What kind of work do you do, and what do customers pay you for?",
		"question_reason": "This tells me what kind of business I am helping.", "automation_offers": []any{}, "approved_work": []any{},
		"ready": false, "readiness_reason": "I need to learn what the business does first.", "missing_topics": []string{"Customers", "How work moves", "Biggest friction"},
	}
	contribution := "I’m your operations guide. I’ll learn how your business works, save what you tell me, and help set up the first useful work. What kind of work do you do, and what do customers pay you for?"
	if strings.Contains(latest, "plumb") || strings.Contains(latest, "landscap") || strings.Contains(latest, "service call") {
		baseline["business_type"] = "Field service business"
		baseline["business_type_confidence"] = "high"
		baseline["captured_topics"] = []string{"Field service", "Customer jobs"}
		baseline["next_question_key"] = "baseline.revenue_workflow"
		baseline["next_question"] = "Walk me through what happens from the moment a customer calls until the job is paid."
		baseline["question_reason"] = "That will show me where scheduling, handoffs, or follow-up break down."
		baseline["readiness_reason"] = "I know the business type and now need to understand how a job moves."
		baseline["missing_topics"] = []string{"Job scheduling", "Billing", "Current tools"}
		contribution = "That sounds like a field-service business, so I won’t drag you through questions meant for a software company or online shop. Walk me through what happens from the moment a customer calls until the job is paid."
	}
	if strings.Contains(latest, "yes, add") {
		baseline["business_type"] = "Field service business"
		baseline["business_type_confidence"] = "high"
		baseline["captured_topics"] = []string{"Field service", "Customer jobs", "Dispatch"}
		baseline["next_question_key"] = "baseline.systems"
		baseline["next_question"] = "What calendar, job system, or notes do you use today?"
		baseline["question_reason"] = "I need to know what the setup task should connect to."
		baseline["approved_work"] = []any{map[string]any{"key": "schedule.daily_dispatch", "title": "Set up a daily dispatch review", "description": "Choose the current schedule source and decide how Spyglass should surface unassigned calls and conflicts.", "priority": "high"}}
		baseline["readiness_reason"] = "The first setup task is approved; I still need to learn the current tools."
		baseline["missing_topics"] = []string{"Current tools", "Billing"}
		contribution = "I added a daily dispatch review to the setup work. What calendar, job system, or notes do you use today?"
	}
	result := map[string]any{"contribution": contribution, "findings": []any{}, "recommendations": []any{}, "questions": []any{}, "citations": []any{}, "proposed_actions": []any{}, "delegations": []any{}, "confidence": "high", "baseline": baseline}
	raw, _ := json.Marshal(result)
	return string(raw)
}
