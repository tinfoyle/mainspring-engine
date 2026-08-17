package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/development"
)

func TestPublicCatalogDoesNotLeakStripeReferences(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/v1/catalog/public")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", response.StatusCode, body)
	}
	if bytes.Contains(body, []byte("stripe")) {
		t.Fatalf("public catalog leaked provider mapping: %s", body)
	}
	if !bytes.Contains(body, []byte(`"work"`)) || !bytes.Contains(body, []byte(`"agents"`)) {
		t.Fatalf("catalog missing packages: %s", body)
	}
}

func TestRegistrationHTTPJourney(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	begin := postJSON(t, server.URL+"/api/v1/registrations", `{"email":"avery@example.com","display_name":"Avery Johnson","account_name":"Northstar Studio","region":"us-east"}`)
	if begin.StatusCode != http.StatusAccepted {
		t.Fatalf("begin status %d: %s", begin.StatusCode, begin.Body)
	}
	var accepted map[string]any
	if err := json.Unmarshal(begin.Body, &accepted); err != nil {
		t.Fatal(err)
	}
	token, ok := accepted["development_verification_token"].(string)
	if !ok || token == "" {
		t.Fatal("development token missing")
	}
	complete := postJSON(t, server.URL+"/api/v1/registrations/verify", `{"token":"`+token+`"}`)
	if complete.StatusCode != http.StatusCreated {
		t.Fatalf("complete status %d: %s", complete.StatusCode, complete.Body)
	}
	if !bytes.Contains(complete.Body, []byte(`"role":"owner"`)) || !bytes.Contains(complete.Body, []byte(`"type":"free"`)) {
		t.Fatalf("unexpected response: %s", complete.Body)
	}
	if !strings.Contains(complete.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("sensitive response was cacheable")
	}
}

type response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func postJSON(t *testing.T, url, body string) response {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 2 * time.Second}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	bytes, _ := io.ReadAll(result.Body)
	return response{StatusCode: result.StatusCode, Header: result.Header.Clone(), Body: bytes}
}
