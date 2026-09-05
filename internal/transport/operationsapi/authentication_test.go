package operationsapi_test

import (
	"context"
	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"net/http"
	"strings"
	"testing"
	"time"
)

type adminLimiter struct{}

func (adminLimiter) Consume(context.Context, abuse.Scope, [32]byte, time.Time, abuse.Policy) (bool, error) {
	return true, nil
}
func TestAuthenticatorModeRejectsGoogleOnlyPasskeysAndCrossOriginRequests(t *testing.T) {
	server, _, _, ss := fixture(t)
	guard, _ := abuse.NewGuard(adminLimiter{})
	if err := server.WithAuthenticator(&operationsauth.Service{}, guard, "https://app.infiniteocean.net"); err != nil {
		t.Fatal(err)
	}
	for _, method := range []sessions.AuthenticationMethod{sessions.AuthenticationMethodOIDC, sessions.AuthenticationMethodPassword, sessions.AuthenticationMethodPasskey, sessions.AuthenticationMethodEmailOTP} {
		ss.authenticated.Session.AuthenticationMethod = method
		response := request(t, server, "GET", "/api/operations/v1/session", "", true, false)
		if response.Code != 401 {
			t.Fatalf("%s accepted: %d", method, response.Code)
		}
	}
	ss.authenticated.Session.AuthenticationMethod = sessions.AuthenticationMethodGoogleTOTP
	if response := request(t, server, "GET", "/api/operations/v1/session", "", true, false); response.Code != 200 {
		t.Fatal(response.Code)
	}
	for _, path := range []string{"/auth/start", "/auth/verify", "/auth/enrollment", "/auth/reauthenticate", "/auth/google/complete"} {
		if response := request(t, server, "POST", "/api/operations/v1"+path, "{}", true, false); response.Code != http.StatusForbidden {
			t.Fatalf("origin check %s: %d", path, response.Code)
		}
	}
	if response := request(t, server, "POST", "/api/operations/v1/passkey-login/challenges", "{}", false, true); response.Code != 404 {
		t.Fatal("legacy passkey bypass available", response.Code)
	}
	ss.authenticated.RotatedToken = "rotated-admin-token"
	ss.authenticated.Session.ReauthenticatedAt = time.Now().Add(-16 * time.Minute)
	if response := request(t, server, "POST", "/api/operations/v1/support-grants", "{}", true, true); response.Code != 403 {
		t.Fatal("sensitive action accepted stale authentication", response.Code)
	} else if !strings.Contains(response.Header().Get("Set-Cookie"), "rotated-admin-token") {
		t.Fatal("reauthentication prompt lost rotated session")
	}
}
