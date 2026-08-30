package googleidentity_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/googleidentity"
)

func TestAuthorizationAndVerifiedExchange(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const clientID = "stage-client.apps.googleusercontent.com"
	const clientSecret = "stage-secret"
	const nonce = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != clientID || r.Form.Get("client_secret") != clientSecret ||
				r.Form.Get("code") != "authorization-code-material" || r.Form.Get("code_verifier") != "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG" {
				http.Error(w, "invalid exchange", http.StatusBadRequest)
				return
			}
			claims := jwt.MapClaims{"iss": issuer, "sub": "google-subject", "aud": clientID, "exp": time.Now().Add(time.Minute).Unix(),
				"iat": time.Now().Add(-time.Second).Unix(), "nonce": nonce, "email": "person@example.com", "email_verified": true, "name": "Person"}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "test-key"
			signed, signErr := token.SignedString(key)
			if signErr != nil {
				http.Error(w, "sign", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id_token": signed})
		case "/certs":
			w.Header().Set("Cache-Control", "public, max-age=600")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
				"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": "AQAB",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL
	client, err := googleidentity.NewFixture(googleidentity.Config{ClientID: clientID, ClientSecret: clientSecret, HTTPClient: server.Client()},
		googleidentity.Endpoints{Authorization: server.URL + "/authorize", Token: server.URL + "/token", JWKSet: server.URL + "/certs"}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := client.AuthorizationURL("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", nonce, "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "http://app.example.test/auth/google/callback")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(destination)
	if parsed.Query().Get("scope") != "openid email profile" || parsed.Query().Get("nonce") != nonce || parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("unexpected authorization URL: %s", destination)
	}
	assertion, err := client.Exchange(context.Background(), "authorization-code-material", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "http://app.example.test/auth/google/callback", nonce)
	if err != nil {
		t.Fatal(err)
	}
	if assertion.Issuer != issuer || assertion.Subject != "google-subject" || assertion.Email != "person@example.com" || !assertion.EmailVerified {
		t.Fatalf("unexpected assertion: %+v", assertion)
	}
}

func TestExchangeRejectsNonceMismatch(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/certs" {
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "key", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": "AQAB"}}})
			return
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "sub": "subject", "aud": "client", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": "different-nonce-material", "email": "person@example.com", "email_verified": true})
		token.Header["kid"] = "key"
		signed, _ := token.SignedString(key)
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": signed})
	}))
	defer server.Close()
	issuer = server.URL
	client, _ := googleidentity.NewFixture(googleidentity.Config{ClientID: "client", ClientSecret: "secret", HTTPClient: server.Client()}, googleidentity.Endpoints{Authorization: server.URL + "/authorize", Token: server.URL + "/token", JWKSet: server.URL + "/certs"}, issuer)
	if _, err := client.Exchange(context.Background(), "authorization-code-material", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "http://app.example.test/auth/google/callback", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"); err == nil {
		t.Fatal("nonce mismatch was accepted")
	}
}

func TestClientFileRequiresPrivateRegularColonRecord(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "google-login")
	if err := os.WriteFile(filename, []byte("client-id:client-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := googleidentity.NewFromClientFile(googleidentity.Config{}, filename)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := client.AuthorizationURL(strings.Repeat("s", 43), strings.Repeat("n", 43), strings.Repeat("c", 43), "https://app.stage.infiniteocean.net/auth/google/callback")
	if err != nil || !strings.Contains(destination, url.QueryEscape("client-id")) {
		t.Fatalf("client file was not loaded: %v %s", err, destination)
	}
	if err := os.Chmod(filename, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := googleidentity.NewFromClientFile(googleidentity.Config{}, filename); err == nil {
		t.Fatal("permissive client file was accepted")
	}
	if err := os.WriteFile(filename, []byte(fmt.Sprintf("%s\n", "client:secret:extra")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := googleidentity.NewFromClientFile(googleidentity.Config{}, filename); err == nil {
		t.Fatal("malformed client record was accepted")
	}
}
