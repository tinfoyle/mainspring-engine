package googleoauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationauthorization"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

const (
	testState     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"
	testChallenge = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq"
	testVerifier  = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	testCode      = "4/0code-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn"
	testRedirect  = "https://app.infiniteocean.net/api/v1/integrations/google/callback"
)

func TestClientBuildsExactConsentAndExchangesRefreshCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || request.ParseForm() != nil ||
				request.Form.Get("client_id") != "client" || request.Form.Get("client_secret") != "secret" || request.Form.Get("code") != testCode ||
				request.Form.Get("code_verifier") != testVerifier || request.Form.Get("redirect_uri") != testRedirect || request.Form.Get("grant_type") != "authorization_code" {
				http.Error(writer, "bad exchange", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "short-lived-access", "refresh_token": "durable-refresh",
				"expires_in": 3600, "scope": domain.GoogleDriveReadScope, "token_type": "Bearer"})
		case "/revoke":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("token") != "durable-refresh" {
				http.Error(writer, "bad token", http.StatusBadRequest)
				return
			}
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := testClient(t, server)
	authorizationURL, err := client.AuthorizationURL([]byte(testState), []byte(testChallenge), testRedirect)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorizationURL)
	query := parsed.Query()
	if parsed.Path != "/authorize" || query.Get("state") != testState || query.Get("code_challenge") != testChallenge ||
		query.Get("code_challenge_method") != "S256" || query.Get("redirect_uri") != testRedirect || query.Get("scope") != domain.GoogleDriveReadScope ||
		query.Get("access_type") != "offline" || query.Get("prompt") != "consent" || query.Get("include_granted_scopes") != "false" {
		t.Fatalf("authorization URL=%s", authorizationURL)
	}
	credential, err := client.Exchange(context.Background(), integrationauthorization.ExchangeRequest{Code: []byte(testCode), PKCEVerifier: []byte(testVerifier), RedirectURI: testRedirect})
	if err != nil || string(credential.RefreshToken) != "durable-refresh" {
		t.Fatalf("credential=%q err=%v", credential.RefreshToken, err)
	}
	if err := client.Revoke(context.Background(), credential.RefreshToken); err != nil {
		t.Fatal(err)
	}
	credential.Close()
	if credential.RefreshToken != nil {
		t.Fatalf("closed credential=%q", credential.RefreshToken)
	}
}

func TestClientRejectsScopeDriftProviderFailureAndRedirect(t *testing.T) {
	scope := "https://www.googleapis.com/auth/drive"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/token" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600, "scope": scope, "token_type": "Bearer"})
			return
		}
		writer.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	client := testClient(t, server)
	_, err := client.Exchange(context.Background(), integrationauthorization.ExchangeRequest{Code: []byte(testCode), PKCEVerifier: []byte(testVerifier), RedirectURI: testRedirect})
	if !errors.Is(err, integrationauthorization.ErrScopeMismatch) {
		t.Fatalf("scope drift error=%v", err)
	}
	if _, err := client.AuthorizationURL([]byte(testState), []byte(testChallenge), "https://user@example.com/callback"); !errors.Is(err, integrationauthorization.ErrInvalid) {
		t.Fatalf("credentialed redirect error=%v", err)
	}
	for _, redirect := range []string{"https://example.com/a/../callback", "https://example.com/callback?next=evil", "http://example.com/callback"} {
		if _, err := client.AuthorizationURL([]byte(testState), []byte(testChallenge), redirect); !errors.Is(err, integrationauthorization.ErrInvalid) {
			t.Fatalf("unsafe redirect %q error=%v", redirect, err)
		}
	}

	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", target.URL)
		writer.WriteHeader(http.StatusFound)
	}))
	defer source.Close()
	redirectClient, err := newClient(Config{ClientID: "client", ClientSecret: "secret"}, source.URL+"/authorize", source.URL, source.URL+"/revoke", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = redirectClient.Exchange(context.Background(), integrationauthorization.ExchangeRequest{Code: []byte(testCode), PKCEVerifier: []byte(testVerifier), RedirectURI: testRedirect})
	if !errors.Is(err, integrationauthorization.ErrProviderUnavailable) || redirected {
		t.Fatalf("redirect error=%v followed=%t", err, redirected)
	}
}

func TestClientRevocationReplayAndRestrictiveClientFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/revoke" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client := testClient(t, server)
	if err := client.Revoke(context.Background(), []byte("already-revoked-refresh")); err != nil {
		t.Fatalf("revocation replay=%v", err)
	}

	filename := filepath.Join(t.TempDir(), "oauth-client.json")
	if err := os.WriteFile(filename, []byte(`{"client_id":"client","client_secret":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewFromClientFile(Config{}, filename)
	if err != nil || loaded.clientID != "client" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if err := os.Chmod(filename, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFromClientFile(Config{}, filename); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("weak file error=%v", err)
	}
}

func TestClientRejectsUnknownOrOversizedTokenResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"scope":"`+domain.GoogleDriveReadScope+`","token_type":"Bearer","unexpected":"value"}`)
	}))
	defer server.Close()
	client := testClient(t, server)
	_, err := client.Exchange(context.Background(), integrationauthorization.ExchangeRequest{Code: []byte(testCode), PKCEVerifier: []byte(testVerifier), RedirectURI: testRedirect})
	if !errors.Is(err, integrationauthorization.ErrProviderUnavailable) {
		t.Fatalf("unknown token field error=%v", err)
	}

	large := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, strings.Repeat("x", maximumResponseBytes+1))
	}))
	defer large.Close()
	client = testClient(t, large)
	_, err = client.Exchange(context.Background(), integrationauthorization.ExchangeRequest{Code: []byte(testCode), PKCEVerifier: []byte(testVerifier), RedirectURI: testRedirect})
	if !errors.Is(err, integrationauthorization.ErrProviderUnavailable) {
		t.Fatalf("oversized token response error=%v", err)
	}
}

func testClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := newClient(Config{Client: server.Client(), ClientID: "client", ClientSecret: "secret"}, server.URL+"/authorize", server.URL+"/token", server.URL+"/revoke", true)
	if err != nil {
		t.Fatal(err)
	}
	return client
}
