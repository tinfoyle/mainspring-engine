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

	"github.com/tinfoyle/spyglass-engine/internal/application/oidcauth"
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
