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
	if login.StatusCode != http.StatusOK || !bytes.Contains(loginBody, []byte("Find the signal")) || !bytes.Contains(loginBody, []byte("INFINITE OCEAN")) {
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
	if signedIn.status != http.StatusOK || !bytes.Contains(signedIn.body, []byte("Northstar Studio")) || !bytes.Contains(signedIn.body, []byte("YOUR OPERATING PARTNER")) || !bytes.Contains(signedIn.body, []byte("FEATURE PACKAGES")) {
		t.Fatalf("app shell: %d %s", signedIn.status, signedIn.body)
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
