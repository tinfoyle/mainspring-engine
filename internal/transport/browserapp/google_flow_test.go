package browserapp

import (
	"bytes"
	"context"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/oidcauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
)

func TestGoogleLoginFlowPreservesSafeReturnAndRejectsStateMismatch(t *testing.T) {
	provider := &googleProviderStub{}
	server := &Server{googleProvider: provider, googleRedirectURI: "https://app.example.test/auth/google/callback",
		config: Config{SessionCookieName: "session", AccountCookieName: "account", TrustedOrigins: []string{"https://app.example.test"}},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	begin := httptest.NewRecorder()
	server.beginGoogleLogin(begin, httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/google?return_to=%2Fapp%2Fyour-turn", nil))
	if begin.Code != http.StatusSeeOther || begin.Header().Get("Location") != "https://accounts.example.test/authorize" {
		t.Fatalf("unexpected begin response: %d %s", begin.Code, begin.Header().Get("Location"))
	}
	cookies := begin.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != googleFlowCookie || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected flow cookie: %+v", cookies)
	}
	flowRequest := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/google/callback", nil)
	flowRequest.AddCookie(cookies[0])
	flow, ok := server.googleFlow(flowRequest)
	if !ok || flow.ReturnTo != "/app/your-turn" || flow.Mode != "login" || provider.state != flow.State || provider.nonce != flow.Nonce {
		t.Fatalf("unexpected flow: %+v", flow)
	}
	callback := httptest.NewRecorder()
	badState := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/google/callback?state="+url.QueryEscape(strings.Repeat("x", 43))+"&code=authorization-code", nil)
	badState.AddCookie(cookies[0])
	server.completeGoogleLogin(callback, badState)
	if callback.Code != http.StatusSeeOther || callback.Header().Get("Location") != "/login?status=google_failed" || provider.exchanges != 0 {
		t.Fatalf("state mismatch reached provider: %d %s exchanges=%d", callback.Code, callback.Header().Get("Location"), provider.exchanges)
	}
}

func TestGoogleSignupFlowPreservesAccountAndPurchaseIntent(t *testing.T) {
	provider := &googleProviderStub{}
	published := catalog.Default(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	server := &Server{
		registrations: &registration.Service{}, googleProvider: provider, googleRedirectURI: "https://app.example.test/auth/google/callback",
		catalog: func() catalog.PublishedCatalog { return published },
		config:  Config{SessionCookieName: "session", AccountCookieName: "account", TrustedOrigins: []string{"https://app.example.test"}},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	form := url.Values{"account_name": {"Northstar Studio"}, "region": {"us-east"}, "offer_code": {"team-monthly-v2"}, "return_to": {"/app/checkout?offer=team-monthly-v2"}}
	request := httptest.NewRequest(http.MethodPost, "https://app.example.test/auth/google/signup", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://app.example.test")
	response := httptest.NewRecorder()
	server.beginGoogleSignup(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "https://accounts.example.test/authorize" {
		t.Fatalf("unexpected signup begin response: %d %s", response.Code, response.Header().Get("Location"))
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("missing Google signup flow cookie: %+v", cookies)
	}
	callback := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/google/callback", nil)
	callback.AddCookie(cookies[0])
	flow, ok := server.googleFlow(callback)
	if !ok || flow.Mode != "signup" || flow.AccountName != "Northstar Studio" || flow.Region != "us-east" || flow.OfferCode != "team-monthly-v2" || flow.ReturnTo != "/app/checkout?offer=team-monthly-v2" {
		t.Fatalf("unexpected signup flow: %+v", flow)
	}
}

type googleProviderStub struct {
	state, nonce, challenge string
	exchanges               int
	assertion               oidcauth.Assertion
}

func (p *googleProviderStub) AuthorizationURL(state, nonce, challenge, _ string) (string, error) {
	p.state, p.nonce, p.challenge = state, nonce, challenge
	return "https://accounts.example.test/authorize", nil
}
func (p *googleProviderStub) Exchange(context.Context, string, string, string, string) (oidcauth.Assertion, error) {
	p.exchanges++
	return p.assertion, nil
}

func TestAdminGoogleHandoffUsesVerifiedAssertionAndNoCustomerSession(t *testing.T) {
	cipher, _ := operationsauth.NewCipher(map[int][]byte{1: bytes.Repeat([]byte{2}, 32)}, 1)
	provider := &googleProviderStub{assertion: oidcauth.Assertion{Issuer: "https://accounts.google.com", Subject: "staff-google-subject", Email: "staff@example.test", EmailVerified: true}}
	server := &Server{googleProvider: provider, googleRedirectURI: "https://app.example.test/auth/google/callback", operationsOrigin: "https://ops.example.test", operationsCipher: cipher,
		config: Config{SecureCookies: true, SessionCookieName: "customer-session", TrustedOrigins: []string{"https://app.example.test"}}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	state, _ := operationsauth.RandomToken()
	begin := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "https://app.example.test/auth/google/operations?state="+state, nil)
	// Existing customer cookies are deliberately irrelevant to admin sign-in.
	request.AddCookie(&http.Cookie{Name: "customer-session", Value: "unrelated-customer"})
	server.beginOperationsGoogle(begin, request)
	if begin.Code != 303 {
		t.Fatal(begin.Code)
	}
	callback := httptest.NewRequest("GET", "https://app.example.test/auth/google/callback?state="+provider.state+"&code=google-code", nil)
	callback.AddCookie(begin.Result().Cookies()[0])
	response := httptest.NewRecorder()
	server.completeGoogleLogin(response, callback)
	if response.Code != 200 || provider.exchanges != 1 {
		t.Fatal(response.Code)
	}
	if response.Header().Get("Location") != "" {
		t.Fatal("ticket leaked into URL")
	}
	body := response.Body.String()
	match := regexp.MustCompile(`name="ticket" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("missing handoff form")
	}
	ticket, err := operationsauth.OpenTicket(cipher, html.UnescapeString(match[1]), server.operationsOrigin, state, time.Now())
	if err != nil || ticket.Identifier != "https://accounts.google.com\x1fstaff-google-subject" {
		t.Fatal("handoff is not bound to verified Google subject", err)
	}
	if response.Header().Get("Referrer-Policy") != "origin" {
		t.Fatal("native handoff must preserve Origin without forwarding callback query")
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "form-action https://ops.example.test") {
		t.Fatal("unbounded handoff destination")
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name != "__Host-spyglass_google_flow" || cookie.MaxAge != -1 {
			t.Fatal("customer session created by admin handoff")
		}
	}
}
