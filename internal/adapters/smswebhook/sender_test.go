package smswebhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/smswebhook"
	"github.com/tinfoyle/spyglass-engine/internal/application/multifactor"
)

func TestSenderUsesAuthenticatedIdempotentHTTPSRequest(t *testing.T) {
	var payload map[string]string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Idempotency-Key") != "challenge-id" {
			t.Fatalf("unexpected request: method=%s headers=%v", r.Method, r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	sender, err := smswebhook.New(smswebhook.Config{URL: server.URL, BearerToken: "secret", From: "InfiniteOcean", Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	err = sender.SendMultifactor(context.Background(), multifactor.Message{ID: "challenge-id", Kind: multifactor.KindSMS, Destination: "+12025550199", Code: "123456", ExpiresAt: time.Now().Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if payload["to"] != "+12025550199" || payload["from"] != "InfiniteOcean" || payload["idempotency_key"] != "challenge-id" || payload["message"] == "" {
		t.Fatalf("payload=%v", payload)
	}
}

func TestSenderRejectsInsecureOrIncompleteConfiguration(t *testing.T) {
	for name, config := range map[string]smswebhook.Config{
		"http":          {URL: "http://sms.example.test/send", BearerToken: "secret", From: "InfiniteOcean"},
		"credentials":   {URL: "https://name:password@sms.example.test/send", BearerToken: "secret", From: "InfiniteOcean"},
		"fragment":      {URL: "https://sms.example.test/send#ignored", BearerToken: "secret", From: "InfiniteOcean"},
		"missing token": {URL: "https://sms.example.test/send", From: "InfiniteOcean"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := smswebhook.New(config); err == nil {
				t.Fatal("configuration was accepted")
			}
		})
	}
}
