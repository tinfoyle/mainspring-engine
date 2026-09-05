package approuter

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/routeaccess"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
)

type approvalRouteAuthorizer struct{ marketing catalog.PackageMode }

func (a approvalRouteAuthorizer) Authorize(_ context.Context, _ access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	if requirement.Package == catalog.PackageMarketing && a.marketing != catalog.ModeEnabled {
		code := access.DenialPackageNotEntitled
		if a.marketing == catalog.ModeReadOnly {
			code = access.DenialPackageReadOnly
		}
		return access.AccountContext{}, &access.DeniedError{Code: code, Package: requirement.Package}
	}
	return access.AccountContext{AccountID: accountID, CellID: "cell-us-east-01", PlacementGeneration: 7, EntitlementVersion: 4, Role: accounts.RoleOwner,
		PackageAccess: &entitlements.PackageAccess{Code: requirement.Package, Version: 1, Mode: catalog.ModeEnabled}}, nil
}

func TestApprovalDecisionCarriesOnlyCurrentlyGrantedMarketingAuthority(t *testing.T) {
	for _, mode := range []catalog.PackageMode{catalog.ModeEnabled, catalog.ModeReadOnly, catalog.ModeSuspended} {
		t.Run(string(mode), func(t *testing.T) {
			clock := fixedClock{time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)}
			key := []byte("0123456789abcdef0123456789abcdef")
			cellID := ids.CellID("cell-us-east-01")
			signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
			verifier, _ := routecontext.NewVerifier("router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, clock)
			called := false
			cellServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				body, _ := io.ReadAll(r.Body)
				binding, _ := routecontext.BindRequest(r, body)
				claims, err := verifier.Verify(r.Header.Get(cellapi.RouteContextHeader), binding)
				if err != nil {
					t.Error(err)
					w.WriteHeader(401)
					return
				}
				ctx := routecontext.WithClaims(r.Context(), claims)
				actor := access.Actor{UserID: ids.UserID(routerUser)}
				authorizer := routeaccess.NewAuthorizer()
				if _, err := authorizer.Authorize(ctx, actor, ids.AccountID(routerAccount), access.Requirement{Package: catalog.PackageAgents, Mutation: true}); err != nil {
					t.Error(err)
				}
				_, err = authorizer.Authorize(ctx, actor, ids.AccountID(routerAccount), access.Requirement{Package: catalog.PackageMarketing, Mutation: true})
				if (err == nil) != (mode == catalog.ModeEnabled) {
					t.Errorf("Marketing authorization in %s: %v", mode, err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"accepted":true}`))
			}))
			defer cellServer.Close()
			router, err := New(fakeSessions{authenticated: sessions.Authenticated{Session: sessions.Session{UserID: ids.UserID(routerUser)}}}, approvalRouteAuthorizer{mode}, directoryFor(t, cellID, 7, cellServer.URL), signer, fixedGenerator{routerRequest}, Config{SessionCookieName: "test", TrustedOrigins: []string{"https://app.example"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+routerAccount+"/attention/approvals/"+routerRequest+"/decisions", strings.NewReader(`{"decision":"approve","expected_version":1,"reason":"Reviewed"}`))
			request.AddCookie(&http.Cookie{Name: "test", Value: "session"})
			request.Header.Set("Origin", "https://app.example")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", routerRequest)
			response := httptest.NewRecorder()
			router.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK || !called {
				t.Fatalf("response=%d %s called=%v", response.Code, response.Body.String(), called)
			}
		})
	}
	for _, resource := range []string{"attention/approvals", "attention/approvals/" + routerRequest, "attention/approvals/" + routerRequest + "/cancellations", "attention/work-reviews/" + routerRequest + "/decisions"} {
		if humanApprovalDecisionRoute(http.MethodPost, resource) {
			t.Errorf("unexpected extra authority on %s", resource)
		}
	}
}
