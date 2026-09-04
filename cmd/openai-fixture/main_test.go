package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agents "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
)

func TestResponsesFailsOnceThenReturnsValidEnvelope(t *testing.T) {
	responseCalls.Store(0)
	request := []byte(`{"model":"gpt-test","input":[]}`)
	first := httptest.NewRecorder()
	responses(first, httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(request)))
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first status=%d", first.Code)
	}
	second := httptest.NewRecorder()
	responses(second, httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(request)))
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	var envelope struct {
		Model  string `json:"model"`
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &envelope); err != nil || envelope.Model != "gpt-test" || len(envelope.Output) != 1 || len(envelope.Output[0].Content) != 1 {
		t.Fatalf("invalid envelope: %#v err=%v", envelope, err)
	}
}

func TestFixtureResultDrivesAnAdaptiveBaselineAndApprovedWork(t *testing.T) {
	input := []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{{Role: "user", Content: "We run a plumbing company and answer service calls."}}
	var first agents.ResultEnvelope
	if err := json.Unmarshal([]byte(fixtureResult("You are the first and main operations agent.", input)), &first); err != nil {
		t.Fatal(err)
	}
	validated, err := agents.ValidateResult(first)
	if err != nil || validated.Baseline == nil || validated.Baseline.BusinessType != "Field service business" || validated.Baseline.NextQuestionKey != "baseline.revenue_workflow" {
		t.Fatalf("result=%+v err=%v", validated, err)
	}
	input = append(input, struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: "user", Content: "Yes, add the daily dispatch review."})
	var approved agents.ResultEnvelope
	if err := json.Unmarshal([]byte(fixtureResult("You are the first and main operations agent.", input)), &approved); err != nil {
		t.Fatal(err)
	}
	validated, err = agents.ValidateResult(approved)
	if err != nil || validated.Baseline == nil || len(validated.Baseline.ApprovedWork) != 1 || validated.Baseline.ApprovedWork[0].Key != "schedule.daily_dispatch" {
		t.Fatalf("result=%+v err=%v", validated, err)
	}
}

func TestFixtureResultTurnsApprovedSetupWorkIntoYourTurnQuestion(t *testing.T) {
	input := []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{{Role: "user", Content: "Set up a daily dispatch review.\n\nThis is approved setup Work from the Business Baseline. Work on this task rather than continuing the onboarding interview."}}
	var result agents.ResultEnvelope
	if err := json.Unmarshal([]byte(fixtureResult("You are the first and main operations agent.", input)), &result); err != nil {
		t.Fatal(err)
	}
	validated, err := agents.ValidateResult(result)
	if err != nil || validated.Baseline != nil || len(validated.Questions) != 1 {
		t.Fatalf("result=%+v err=%v", validated, err)
	}
}
