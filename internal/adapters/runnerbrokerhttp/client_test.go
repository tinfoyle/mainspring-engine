package runnerbrokerhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
)

func TestClientRereadsProjectedTokenForEveryOperation(t *testing.T) {
	invocationID := "11000000-0000-4000-8000-000000000001"
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("token-one"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/v1/runner/invocations/" + invocationID + "/request":
			_ = json.NewEncoder(w).Encode(runnerbroker.Request{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
		case "/internal/v1/runner/invocations/" + invocationID + "/result":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"status":"accepted","newly_created":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(Config{BrokerURL: server.URL, InvocationID: invocationID, IdentityTokenFile: tokenFile, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte("token-two"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := client.Submit(context.Background(), runnerbroker.Result{SchemaVersion: 1, Outcome: "completed", Output: json.RawMessage(`{}`)})
	if err != nil || !created {
		t.Fatalf("submit created=%v err=%v", created, err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(seen) != 2 || seen[0] != "Bearer token-one" || seen[1] != "Bearer token-two" {
		t.Fatalf("presented tokens=%v", seen)
	}
}

func TestClientMapsContentFreeProblems(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("valid-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		code string
		err  error
	}{
		{"runner_identity_denied", runnerbroker.ErrIdentityDenied},
		{"runner_exchange_not_ready", runnerbroker.ErrExchangeNotReady},
		{"runner_exchange_canceled", runnerbroker.ErrExchangeCanceled},
		{"runner_exchange_expired", runnerbroker.ErrExchangeExpired},
		{"runner_exchange_conflict", runnerbroker.ErrExchangeConflict},
	}
	for _, test := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"` + test.code + `","detail":"must not be returned"}`))
		}))
		client, err := New(Config{BrokerURL: server.URL, InvocationID: "11000000-0000-4000-8000-000000000001", IdentityTokenFile: tokenFile, HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Fetch(context.Background())
		server.Close()
		if !errors.Is(err, test.err) || bytes.Contains([]byte(err.Error()), []byte("must not be returned")) {
			t.Errorf("code=%s err=%v", test.code, err)
		}
	}
}

func TestClientRejectsInvalidTokenAndRedirect(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("token with spaces"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.RedirectHandler("https://example.invalid", http.StatusTemporaryRedirect))
	defer server.Close()
	client, _ := New(Config{BrokerURL: server.URL, InvocationID: "11000000-0000-4000-8000-000000000001", IdentityTokenFile: tokenFile, HTTPClient: server.Client()})
	if _, err := client.Fetch(context.Background()); err == nil || !bytes.Contains([]byte(err.Error()), []byte("token is invalid")) {
		t.Fatalf("invalid token err=%v", err)
	}
	if err := os.WriteFile(tokenFile, []byte("valid-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background()); err == nil || !bytes.Contains([]byte(err.Error()), []byte("redirects are denied")) {
		t.Fatalf("redirect err=%v", err)
	}
}
