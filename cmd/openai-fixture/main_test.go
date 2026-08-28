package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
