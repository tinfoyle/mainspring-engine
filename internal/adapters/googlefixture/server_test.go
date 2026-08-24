package googlefixture

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/googledrive"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/googleoauth"
	integrationauthorization "github.com/tinfoyle/spyglass-engine/internal/application/integrationauthorization"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

func TestOAuthAndDriveFixtureEnforcesOneUsePKCEAndRevocation(t *testing.T) {
	const (
		clientID       = "spyglass-local-fixture"
		clientSecret   = "local-fixture-secret-not-production"
		redirectOrigin = "https://app.infiniteocean.localhost:8444"
		redirectURI    = redirectOrigin + "/api/v1/accounts/10000000-0000-4000-8000-000000000001/integrations/google/authorization-callback"
	)
	fixture, err := New(Config{ClientID: clientID, ClientSecret: clientSecret, RedirectOrigin: redirectOrigin})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(fixture.Handler())
	defer upstream.Close()
	client := upstream.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	provider, err := googleoauth.NewFixture(googleoauth.Config{Client: client, ClientID: clientID, ClientSecret: clientSecret}, googleoauth.FixtureEndpoints{
		Authorization: upstream.URL + "/o/oauth2/v2/auth", Token: upstream.URL + "/token", Revocation: upstream.URL + "/revoke",
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier := []byte(strings.Repeat("v", 43))
	digest := sha256.Sum256(verifier)
	challenge := []byte(base64.RawURLEncoding.EncodeToString(digest[:]))
	state := []byte(strings.Repeat("s", 43))
	authorizationURL, err := provider.AuthorizationURL(state, challenge, redirectURI)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("authorization status=%d", response.StatusCode)
	}
	callback, err := url.Parse(response.Header.Get("Location"))
	if err != nil || callback.Query().Get("state") != string(state) || callback.Query().Get("code") == "" {
		t.Fatalf("callback=%q err=%v", response.Header.Get("Location"), err)
	}
	exchange := integrationauthorization.ExchangeRequest{Code: []byte(callback.Query().Get("code")), PKCEVerifier: verifier, RedirectURI: redirectURI}
	credential, err := provider.Exchange(context.Background(), exchange)
	if err != nil || len(credential.RefreshToken) == 0 {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
	if _, err := provider.Exchange(context.Background(), exchange); !errors.Is(err, integrationauthorization.ErrProviderRejected) {
		t.Fatalf("authorization code replay error=%v", err)
	}

	form := url.Values{"client_id": {clientID}, "client_secret": {clientSecret}, "grant_type": {"refresh_token"}, "refresh_token": {string(credential.RefreshToken)}}
	tokenResponse, err := client.Post(upstream.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if tokenResponse.StatusCode != http.StatusOK || json.NewDecoder(tokenResponse.Body).Decode(&token) != nil || token.AccessToken == "" {
		t.Fatalf("refresh status=%d token=%+v", tokenResponse.StatusCode, token)
	}
	tokenResponse.Body.Close()
	driveRequest, _ := http.NewRequest(http.MethodGet, upstream.URL+"/drive/v3/files/folder-a", nil)
	driveRequest.Header.Set("Authorization", "Bearer "+token.AccessToken)
	driveResponse, err := client.Do(driveRequest)
	if err != nil || driveResponse.StatusCode != http.StatusOK {
		t.Fatalf("Drive response=%+v err=%v", driveResponse, err)
	}
	driveResponse.Body.Close()
	driveProvider, err := googledrive.NewFixture(googledrive.Config{Client: client, ClientID: clientID, ClientSecret: clientSecret},
		googledrive.FixtureEndpoints{Token: upstream.URL + "/token", Drive: upstream.URL + "/drive/v3"})
	if err != nil {
		t.Fatal(err)
	}
	material, _ := json.Marshal(map[string]string{"refresh_token": string(credential.RefreshToken)})
	page, err := driveProvider.Sync(context.Background(), integrationsync.ProviderRequest{CredentialProvider: googledrive.ProviderCode,
		SourceKind: domain.ConnectorGoogleDrive, FolderIDs: []string{"folder-a"}, Credential: material})
	if err != nil || len(page.Changes) != 1 || page.Changes[0].ObjectID != "fixture-file-1" || string(page.Changes[0].Content) != "Local Google Drive fixture document.\n" {
		t.Fatalf("Drive page=%+v err=%v", page, err)
	}

	if err := provider.Revoke(context.Background(), credential.RefreshToken); err != nil {
		t.Fatal(err)
	}
	revokedResponse, err := client.Post(upstream.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer revokedResponse.Body.Close()
	if revokedResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("revoked refresh status=%d", revokedResponse.StatusCode)
	}
}

func TestFixtureRejectsOpenRedirectAndWrongClient(t *testing.T) {
	fixture, err := New(Config{ClientID: "fixture-client", ClientSecret: "fixture-secret-at-least-sixteen", RedirectOrigin: "https://app.infiniteocean.localhost:8444"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fixture.Handler())
	defer server.Close()
	request := server.URL + "/o/oauth2/v2/auth?client_id=wrong&response_type=code&scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fdrive.readonly&access_type=offline&prompt=consent&include_granted_scopes=false&code_challenge_method=S256&code_challenge=" + strings.Repeat("c", 43) + "&state=" + strings.Repeat("s", 43) + "&redirect_uri=https%3A%2F%2Fevil.example%2Fcallback"
	response, err := http.Get(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", response.StatusCode)
	}
}
