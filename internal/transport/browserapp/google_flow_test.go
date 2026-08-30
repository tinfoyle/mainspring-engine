package browserapp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/oidcauth"
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
}

func (p *googleProviderStub) AuthorizationURL(state, nonce, challenge, _ string) (string, error) {
	p.state, p.nonce, p.challenge = state, nonce, challenge
	return "https://accounts.example.test/authorize", nil
}
func (p *googleProviderStub) Exchange(context.Context, string, string, string, string) (oidcauth.Assertion, error) {
	p.exchanges++
	return oidcauth.Assertion{}, nil
}
