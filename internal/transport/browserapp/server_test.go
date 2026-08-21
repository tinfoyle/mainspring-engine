package browserapp_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/development"
)

func TestBrowserRegistrationLoginAndAppShell(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 4 * time.Second}
	login, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	loginBody, _ := io.ReadAll(login.Body)
	login.Body.Close()
	if login.StatusCode != http.StatusOK || !bytes.Contains(loginBody, []byte("Find the signal")) || !bytes.Contains(loginBody, []byte("INFINITE OCEAN")) || !bytes.Contains(loginBody, []byte("Sign in with a passkey")) || !bytes.Contains(loginBody, []byte("/assets/passkeys.js")) {
		t.Fatalf("login page: %d %s", login.StatusCode, loginBody)
	}
	protected, err := client.Get(server.URL + "/app")
	if err != nil {
		t.Fatal(err)
	}
	protected.Body.Close()
	if protected.Request.URL.Path != "/login" {
		t.Fatalf("protected page did not redirect to login: %s", protected.Request.URL)
	}
	signup := postForm(t, client, server.URL+"/signup", url.Values{"name": {"Avery Johnson"}, "email": {"avery@example.com"}, "account_name": {"Northstar Studio"}, "region": {"us-east"}})
	if signup.status != http.StatusAccepted {
		t.Fatalf("signup: %d %s", signup.status, signup.body)
	}
	match := regexp.MustCompile(`/verify\?token=([^"&]+)`).FindSubmatch(signup.body)
	if len(match) != 2 {
		t.Fatalf("development verification link missing: %s", signup.body)
	}
	token, _ := url.QueryUnescape(string(match[1]))
	verified := postForm(t, client, server.URL+"/verify", url.Values{"token": {token}, "password": {"correct horse battery staple"}})
	if verified.status != http.StatusOK || !bytes.Contains(verified.body, []byte("Identity verified")) {
		t.Fatalf("verify: %d %s", verified.status, verified.body)
	}
	signedIn := postForm(t, client, server.URL+"/login", url.Values{"email": {"avery@example.com"}, "password": {"correct horse battery staple"}})
	if signedIn.status != http.StatusOK || !bytes.Contains(signedIn.body, []byte("Northstar Studio")) || !bytes.Contains(signedIn.body, []byte("YOUR OPERATING PARTNER")) || !bytes.Contains(signedIn.body, []byte("FEATURE PACKAGES")) || !bytes.Contains(signedIn.body, []byte("BILLING & ACCESS")) || !bytes.Contains(signedIn.body, []byte("Team")) || !bytes.Contains(signedIn.body, []byte("OWNER IDENTITY SETUP")) || !bytes.Contains(signedIn.body, []byte("Secure the helm")) || !bytes.Contains(signedIn.body, []byte("Save recovery codes")) || bytes.Contains(signedIn.body, []byte("People with access")) {
		t.Fatalf("app shell: %d %s", signedIn.status, signedIn.body)
	}
	workPage, err := client.Get(server.URL + "/app/work")
	if err != nil {
		t.Fatal(err)
	}
	workBody, _ := io.ReadAll(workPage.Body)
	workPage.Body.Close()
	if workPage.StatusCode != http.StatusOK || !bytes.Contains(workBody, []byte("Bring every commitment")) || !bytes.Contains(workBody, []byte("Review Account plans")) || bytes.Contains(workBody, []byte("/assets/work.js")) {
		t.Fatalf("locked Work page: %d %s", workPage.StatusCode, workBody)
	}
	if policy := workPage.Header.Get("Content-Security-Policy"); !strings.Contains(policy, "script-src 'self'") || !strings.Contains(policy, "connect-src 'self'") {
		t.Fatalf("Work page content security policy: %q", policy)
	}
	agentsPage, err := client.Get(server.URL + "/app/agents")
	if err != nil {
		t.Fatal(err)
	}
	agentsBody, _ := io.ReadAll(agentsPage.Body)
	agentsPage.Body.Close()
	if agentsPage.StatusCode != http.StatusOK || !bytes.Contains(agentsBody, []byte("Convene the right minds")) || !bytes.Contains(agentsBody, []byte("Operating plan")) || bytes.Contains(agentsBody, []byte("/assets/agents.js")) {
		t.Fatalf("locked Agents page: %d %s", agentsPage.StatusCode, agentsBody)
	}
	parsed, _ := url.Parse(server.URL)
	cookies := jar.Cookies(parsed)
	foundSession := false
	for _, cookie := range cookies {
		if cookie.Name == "spyglass_development_session" && cookie.Value != "" {
			foundSession = true
		}
	}
	if !foundSession {
		t.Fatalf("session cookie missing: %#v", cookies)
	}
	security, err := client.Get(server.URL + "/app/security")
	if err != nil {
		t.Fatal(err)
	}
	securityBody, _ := io.ReadAll(security.Body)
	security.Body.Close()
	if security.StatusCode != http.StatusOK || !bytes.Contains(securityBody, []byte("Where you are signed in")) || !bytes.Contains(securityBody, []byte("Current session")) || !bytes.Contains(securityBody, []byte("Signed in with password")) || !bytes.Contains(securityBody, []byte("Last confirmed with password")) || !bytes.Contains(securityBody, []byte("Recent identity activity")) || !bytes.Contains(securityBody, []byte("Signed in")) || !bytes.Contains(securityBody, []byte("Phishing-resistant sign-in")) || !bytes.Contains(securityBody, []byte("Add passkey")) || !bytes.Contains(securityBody, []byte("Know the last-resort path")) || !bytes.Contains(securityBody, []byte("support cannot view or recreate recovery codes")) || !bytes.Contains(securityBody, []byte("Your current login is <strong>avery@example.com</strong>")) || !bytes.Contains(securityBody, []byte("Changing it requires a recent passkey confirmation")) {
		t.Fatalf("security center: %d %s", security.StatusCode, securityBody)
	}
	confirmed := postForm(t, client, server.URL+"/app/security/reauthenticate", url.Values{"password": {"correct horse battery staple"}})
	if confirmed.status != http.StatusOK || !bytes.Contains(confirmed.body, []byte("Password confirmed for factor recovery")) || !bytes.Contains(confirmed.body, []byte("Use a passkey to unlock verified-email, Membership, invitation, and billing changes")) {
		t.Fatalf("password confirmation: %d %s", confirmed.status, confirmed.body)
	}
	passwordOnlyContact := postForm(t, client, server.URL+"/app/security/contact-change", url.Values{"new_email": {"new-avery@example.com"}})
	if passwordOnlyContact.status != http.StatusOK || !bytes.Contains(passwordOnlyContact.body, []byte("Confirm with a passkey before changing the identity email")) || !bytes.Contains(passwordOnlyContact.body, []byte("avery@example.com")) {
		t.Fatalf("browser verified-contact strong-auth gate: %d %s", passwordOnlyContact.status, passwordOnlyContact.body)
	}
	accountMatch := regexp.MustCompile(`<option value="([0-9a-f-]+)"`).FindSubmatch(signedIn.body)
	if len(accountMatch) != 2 {
		t.Fatalf("Account ID missing from app shell: %s", signedIn.body)
	}
	passwordOnlyInvite := postForm(t, client, server.URL+"/app/invitations", url.Values{"account_id": {string(accountMatch[1])}, "email": {"member@example.com"}, "role": {"member"}})
	if passwordOnlyInvite.status != http.StatusOK || !bytes.Contains(passwordOnlyInvite.body, []byte("Account owners must add a passkey and save recovery codes")) {
		t.Fatalf("browser owner enrollment gate: %d %s", passwordOnlyInvite.status, passwordOnlyInvite.body)
	}
	recoveryStarted := postForm(t, client, server.URL+"/forgot-password", url.Values{"email": {"avery@example.com"}})
	if recoveryStarted.status != http.StatusAccepted || !bytes.Contains(recoveryStarted.body, []byte("If that email belongs to an Infinite Ocean identity")) {
		t.Fatalf("browser recovery start: %d %s", recoveryStarted.status, recoveryStarted.body)
	}
	recoveryMatch := regexp.MustCompile(`/reset-password\?token=([^"&]+)`).FindSubmatch(recoveryStarted.body)
	if len(recoveryMatch) != 2 {
		t.Fatalf("development recovery link missing: %s", recoveryStarted.body)
	}
	recoveryToken, _ := url.QueryUnescape(string(recoveryMatch[1]))
	recovered := postForm(t, client, server.URL+"/reset-password", url.Values{"token": {recoveryToken}, "password": {"replacement password material"}})
	if recovered.status != http.StatusOK || !bytes.Contains(recovered.body, []byte("Password updated. Sign in again on every device.")) {
		t.Fatalf("browser recovery completion: %d %s", recovered.status, recovered.body)
	}
	oldPassword := postForm(t, client, server.URL+"/login", url.Values{"email": {"avery@example.com"}, "password": {"correct horse battery staple"}})
	if oldPassword.status != http.StatusUnauthorized {
		t.Fatalf("old browser password status: %d", oldPassword.status)
	}
	newPassword := postForm(t, client, server.URL+"/login", url.Values{"email": {"avery@example.com"}, "password": {"replacement password material"}})
	if newPassword.status != http.StatusOK || !bytes.Contains(newPassword.body, []byte("Northstar Studio")) {
		t.Fatalf("new browser password login: %d %s", newPassword.status, newPassword.body)
	}
}

func TestPrivateBrowserAssetsRequireReleaseRevalidation(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	for _, asset := range []struct {
		path, contentType string
	}{
		{path: "/assets/spyglass.css", contentType: "text/css; charset=utf-8"},
		{path: "/assets/work.js", contentType: "text/javascript; charset=utf-8"},
		{path: "/assets/attention.js", contentType: "text/javascript; charset=utf-8"},
		{path: "/assets/agents.js", contentType: "text/javascript; charset=utf-8"},
		{path: "/assets/passkeys.js", contentType: "text/javascript; charset=utf-8"},
	} {
		response, err := http.Get(server.URL + asset.path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != asset.contentType || response.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("asset %s response = %d, content type %q, cache %q", asset.path, response.StatusCode, response.Header.Get("Content-Type"), response.Header.Get("Cache-Control"))
		}
	}
}

func TestContactVerificationPageIsDisplayOnlyAndMutationRequiresExactOrigin(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	token := strings.Repeat("a", 43)
	page, err := http.Get(server.URL + "/contact-change/verify?token=" + token)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK || page.Header.Get("Referrer-Policy") != "no-referrer" || !bytes.Contains(body, []byte(`method="post" action="/contact-change/verify"`)) || !bytes.Contains(body, []byte(`name="token" value="`+token+`"`)) || len(page.Cookies()) != 0 {
		t.Fatalf("contact verification display: %d cookies=%#v body=%s", page.StatusCode, page.Cookies(), body)
	}
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/contact-change/verify", strings.NewReader(url.Values{"token": {token}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	deniedBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || !bytes.Contains(deniedBody, []byte("could not be verified")) {
		t.Fatalf("originless contact verification mutation: %d %s", response.StatusCode, deniedBody)
	}
}

func TestPublishedOfferIntentSurvivesSecureSignupJourney(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 4 * time.Second}

	signupPage, err := client.Get(server.URL + "/signup?offer=team-monthly-v1")
	if err != nil {
		t.Fatal(err)
	}
	signupPageBody, _ := io.ReadAll(signupPage.Body)
	signupPage.Body.Close()
	if !bytes.Contains(signupPageBody, []byte(`name="offer_code" value="team-monthly-v1"`)) {
		t.Fatalf("published offer was not accepted by signup: %s", signupPageBody)
	}

	signup := postForm(t, client, server.URL+"/signup", url.Values{"name": {"Taylor Morgan"}, "email": {"taylor@example.com"}, "account_name": {"Signal Works"}, "region": {"us-east"}, "offer_code": {"team-monthly-v1"}})
	match := regexp.MustCompile(`/verify\?token=([^"&]+)&(?:amp;)?offer=team-monthly-v1`).FindSubmatch(signup.body)
	if signup.status != http.StatusAccepted || len(match) != 2 {
		t.Fatalf("offer-aware signup: %d %s", signup.status, signup.body)
	}
	token, _ := url.QueryUnescape(string(match[1]))
	verified := postForm(t, client, server.URL+"/verify", url.Values{"token": {token}, "password": {"correct horse battery staple"}, "offer_code": {"team-monthly-v1"}})
	if verified.status != http.StatusOK || !bytes.Contains(verified.body, []byte("Identity verified")) || !bytes.Contains(verified.body, []byte("team-monthly-v1")) {
		t.Fatalf("offer-aware verification: %d %s", verified.status, verified.body)
	}
	signedIn := postForm(t, client, server.URL+"/login", url.Values{"email": {"taylor@example.com"}, "password": {"correct horse battery staple"}, "return_to": {"/app?offer=team-monthly-v1&status=welcome#billing"}})
	if signedIn.status != http.StatusOK || !bytes.Contains(signedIn.body, []byte("Your selected plan is highlighted")) || !bytes.Contains(signedIn.body, []byte("SELECTED ON INFINITE OCEAN")) {
		t.Fatalf("selected offer landing: %d %s", signedIn.status, signedIn.body)
	}
}

func TestPublicOriginCannotSubmitSignupMutation(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/signup", strings.NewReader(url.Values{"name": {"Public Post"}, "email": {"public@example.com"}, "account_name": {"Should Not Exist"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://infiniteocean.net")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("public-origin signup mutation status = %d", response.StatusCode)
	}
}

type formResponse struct {
	status int
	body   []byte
}

func postForm(t *testing.T, client *http.Client, target string, values url.Values) formResponse {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://localhost:8080")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return formResponse{status: response.StatusCode, body: body}
}
