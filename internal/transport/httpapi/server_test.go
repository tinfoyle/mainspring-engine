package httpapi_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	complete := postJSON(t, server.URL+"/api/v1/registrations/verify", `{"token":"`+token+`","password":"correct horse battery staple"}`)
	if complete.StatusCode != http.StatusCreated {
		t.Fatalf("complete status %d: %s", complete.StatusCode, complete.Body)
	}
	if !bytes.Contains(complete.Body, []byte(`"role":"owner"`)) || !bytes.Contains(complete.Body, []byte(`"type":"free"`)) {
		t.Fatalf("unexpected response: %s", complete.Body)
	}
	var provisioned struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := json.Unmarshal(complete.Body, &provisioned); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(complete.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("sensitive response was cacheable")
	}
	login := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"avery@example.com","password":"correct horse battery staple"}`)
	if login.StatusCode != http.StatusCreated {
		t.Fatalf("login status %d: %s", login.StatusCode, login.Body)
	}
	cookies := (&http.Response{Header: login.Header}).Cookies()
	if len(cookies) != 1 || cookies[0].Name != "spyglass_development_session" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected session cookie: %#v", cookies)
	}
	secondLogin := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"avery@example.com","password":"correct horse battery staple"}`)
	if secondLogin.StatusCode != http.StatusCreated {
		t.Fatalf("second login status %d: %s", secondLogin.StatusCode, secondLogin.Body)
	}
	sessionsRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/sessions", nil)
	sessionsRequest.AddCookie(cookies[0])
	sessionsResponse, err := http.DefaultClient.Do(sessionsRequest)
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(sessionsResponse.Body).Decode(&inventory); err != nil {
		t.Fatal(err)
	}
	sessionsResponse.Body.Close()
	if sessionsResponse.StatusCode != http.StatusOK || len(inventory.Sessions) != 2 {
		t.Fatalf("session inventory: %d %+v", sessionsResponse.StatusCode, inventory.Sessions)
	}
	otherSessionID := ""
	for _, item := range inventory.Sessions {
		if !item.Current {
			otherSessionID = item.ID
		}
	}
	if otherSessionID == "" {
		t.Fatal("session inventory did not distinguish the current session")
	}
	revokeRequest, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/sessions/"+otherSessionID, nil)
	revokeRequest.AddCookie(cookies[0])
	revokeResponse, err := http.DefaultClient.Do(revokeRequest)
	if err != nil {
		t.Fatal(err)
	}
	revokeResponse.Body.Close()
	if revokeResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke other session status: %d", revokeResponse.StatusCode)
	}
	wrongConfirmation := postJSONCookie(t, server.URL+"/api/v1/session/reauthenticate", `{"password":"wrong password"}`, cookies[0])
	if wrongConfirmation.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong reauthentication status: %d", wrongConfirmation.StatusCode)
	}
	confirmation := postJSONCookie(t, server.URL+"/api/v1/session/reauthenticate", `{"password":"correct horse battery staple"}`, cookies[0])
	if confirmation.StatusCode != http.StatusNoContent {
		t.Fatalf("reauthentication status: %d %s", confirmation.StatusCode, confirmation.Body)
	}
	accountsRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session/accounts", nil)
	accountsRequest.AddCookie(cookies[0])
	accountsResponse, err := http.DefaultClient.Do(accountsRequest)
	if err != nil {
		t.Fatal(err)
	}
	accountsBody, _ := io.ReadAll(accountsResponse.Body)
	accountsResponse.Body.Close()
	if accountsResponse.StatusCode != http.StatusOK || !bytes.Contains(accountsBody, []byte(provisioned.Account.ID)) {
		t.Fatalf("accounts response: %d %s", accountsResponse.StatusCode, accountsBody)
	}
	selectRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/session/account", strings.NewReader(`{"account_id":"`+provisioned.Account.ID+`"}`))
	selectRequest.Header.Set("Content-Type", "application/json")
	selectRequest.AddCookie(cookies[0])
	selectResponse, err := http.DefaultClient.Do(selectRequest)
	if err != nil {
		t.Fatal(err)
	}
	selectBody, _ := io.ReadAll(selectResponse.Body)
	selectResponse.Body.Close()
	if selectResponse.StatusCode != http.StatusOK || !bytes.Contains(selectBody, []byte(`"placement_generation":1`)) {
		t.Fatalf("select response: %d %s", selectResponse.StatusCode, selectBody)
	}
	selectedCookies := selectResponse.Cookies()
	if len(selectedCookies) != 1 || selectedCookies[0].Name != "spyglass_development_account" || !selectedCookies[0].HttpOnly {
		t.Fatalf("unexpected account cookie: %#v", selectedCookies)
	}
	memberBegin := postJSON(t, server.URL+"/api/v1/registrations", `{"email":"member@example.com","display_name":"Morgan Lee","account_name":"Member Sandbox","region":"us-east"}`)
	var memberPending map[string]any
	if err := json.Unmarshal(memberBegin.Body, &memberPending); err != nil {
		t.Fatal(err)
	}
	memberToken, _ := memberPending["development_verification_token"].(string)
	memberComplete := postJSON(t, server.URL+"/api/v1/registrations/verify", `{"token":"`+memberToken+`","password":"member password material"}`)
	if memberComplete.StatusCode != http.StatusCreated {
		t.Fatalf("member registration: %d %s", memberComplete.StatusCode, memberComplete.Body)
	}
	memberLogin := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"member@example.com","password":"member password material"}`)
	memberCookies := (&http.Response{Header: memberLogin.Header}).Cookies()
	if len(memberCookies) != 1 {
		t.Fatalf("member login: %d %s", memberLogin.StatusCode, memberLogin.Body)
	}
	invite := postJSONCookie(t, server.URL+"/api/v1/accounts/"+provisioned.Account.ID+"/invitations", `{"email":"member@example.com","role":"member"}`, cookies[0])
	if invite.StatusCode != http.StatusCreated {
		t.Fatalf("invite: %d %s", invite.StatusCode, invite.Body)
	}
	var invited map[string]any
	if err := json.Unmarshal(invite.Body, &invited); err != nil {
		t.Fatal(err)
	}
	invitationToken, _ := invited["development_invitation_token"].(string)
	if invitationToken == "" {
		t.Fatal("development invitation token missing")
	}
	acceptedInvitation := postJSONCookie(t, server.URL+"/api/v1/invitations/accept", `{"token":"`+invitationToken+`"}`, memberCookies[0])
	if acceptedInvitation.StatusCode != http.StatusCreated {
		t.Fatalf("accept invitation: %d %s", acceptedInvitation.StatusCode, acceptedInvitation.Body)
	}
	memberAccountsRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session/accounts", nil)
	memberAccountsRequest.AddCookie(memberCookies[0])
	memberAccountsResponse, err := http.DefaultClient.Do(memberAccountsRequest)
	if err != nil {
		t.Fatal(err)
	}
	memberAccountsBody, _ := io.ReadAll(memberAccountsResponse.Body)
	memberAccountsResponse.Body.Close()
	if memberAccountsResponse.StatusCode != http.StatusOK || !bytes.Contains(memberAccountsBody, []byte(provisioned.Account.ID)) {
		t.Fatalf("invited account missing: %d %s", memberAccountsResponse.StatusCode, memberAccountsBody)
	}
	request, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/session", nil)
	request.AddCookie(cookies[0])
	logout, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer logout.Body.Close()
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status: %d", logout.StatusCode)
	}
}

func postJSONCookie(t *testing.T, url, body string, cookie *http.Cookie) response {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	client := http.Client{Timeout: 3 * time.Second}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	payload, _ := io.ReadAll(result.Body)
	return response{StatusCode: result.StatusCode, Header: result.Header.Clone(), Body: payload}
}

func TestStripeWebhookHTTPAcceptsThenDeduplicatesSignedEvent(t *testing.T) {
	secret := "whsec_http_test_secret"
	t.Setenv("SPYGLASS_STRIPE_WEBHOOK_SECRET", secret)
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	now := time.Now().UTC()
	payload := []byte(fmt.Sprintf(`{"id":"evt_http_phase2","type":"customer.subscription.updated","created":%d,"livemode":false,"data":{"object":{"id":"sub_http"}}}`, now.Unix()))
	header := stripeSignature(secret, now.Unix(), payload)
	first := postWebhook(t, server.URL+"/webhooks/stripe", payload, header)
	if first.StatusCode != http.StatusOK || !bytes.Contains(first.Body, []byte(`"status":"accepted"`)) {
		t.Fatalf("first delivery: %d %s", first.StatusCode, first.Body)
	}
	second := postWebhook(t, server.URL+"/webhooks/stripe", payload, header)
	if second.StatusCode != http.StatusOK || !bytes.Contains(second.Body, []byte(`"status":"duplicate"`)) {
		t.Fatalf("duplicate delivery: %d %s", second.StatusCode, second.Body)
	}
	tampered := postWebhook(t, server.URL+"/webhooks/stripe", append(payload, ' '), header)
	if tampered.StatusCode != http.StatusBadRequest {
		t.Fatalf("tampered delivery status: %d", tampered.StatusCode)
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

func postWebhook(t *testing.T, url string, body []byte, signature string) response {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Stripe-Signature", signature)
	client := http.Client{Timeout: 2 * time.Second}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	responseBody, _ := io.ReadAll(result.Body)
	return response{StatusCode: result.StatusCode, Header: result.Header.Clone(), Body: responseBody}
}

func stripeSignature(secret string, timestamp int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(append([]byte(fmt.Sprintf("%d.", timestamp)), payload...))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}
