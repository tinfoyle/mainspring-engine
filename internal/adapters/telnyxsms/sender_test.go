package telnyxsms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/multifactor"
)

func TestSenderUsesTelnyxV2PayloadAndBearerAuthentication(t *testing.T) {
	var payload map[string]string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: method=%s headers=%v", r.Method, r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"message-id","to":[{"status":"queued"}]}}`))
	}))
	defer server.Close()
	sender, err := newWithEndpoint(Config{APIKey: "secret", From: "+17575550199", Client: server.Client()}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	err = sender.SendMultifactor(context.Background(), multifactor.Message{ID: "challenge-id", Kind: multifactor.KindSMS, Destination: "+12025550199", Code: "123456", ExpiresAt: time.Now().Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if payload["to"] != "+12025550199" || payload["from"] != "+17575550199" || !strings.Contains(payload["text"], "123456") || !strings.Contains(payload["text"], "Msg frequency varies") || !strings.Contains(payload["text"], "Msg & data rates may apply") || !strings.Contains(payload["text"], "Reply STOP") || !strings.Contains(payload["text"], "HELP for help") || len(payload["text"]) > 160 {
		t.Fatalf("payload=%v", payload)
	}
	if _, exists := payload["message"]; exists {
		t.Fatalf("generic gateway field leaked into Telnyx payload: %v", payload)
	}
}

func TestSenderRequiresKeyAndE164Sender(t *testing.T) {
	for name, config := range map[string]Config{
		"missing key":  {From: "+17575550199"},
		"missing from": {APIKey: "secret"},
		"alpha sender": {APIKey: "secret", From: "InfiniteOcean"},
		"formatted":    {APIKey: "secret", From: "+1 (757) 555-0199"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(config); err == nil {
				t.Fatal("configuration was accepted")
			}
		})
	}
}

func TestSenderRejectsProviderErrorsAndMalformedAcceptance(t *testing.T) {
	for name, testCase := range map[string]struct {
		status int
		body   string
	}{
		"provider error": {http.StatusUnauthorized, `{"errors":[{"detail":"secret detail"}]}`},
		"malformed":      {http.StatusOK, `{"data":{}}`},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()
			sender, err := newWithEndpoint(Config{APIKey: "secret", From: "+17575550199", Client: server.Client()}, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if err := sender.SendMultifactor(context.Background(), multifactor.Message{Kind: multifactor.KindSMS, Destination: "+12025550199", Code: "123456"}); err == nil || strings.Contains(err.Error(), "secret detail") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
