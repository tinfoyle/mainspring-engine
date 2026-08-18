package approuter

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
)

const (
	routerAccount = "10000000-0000-4000-8000-000000000001"
	routerUser    = "20000000-0000-4000-8000-000000000002"
	routerRequest = "30000000-0000-4000-8000-000000000003"
)

func TestAuthenticatedRequestTraversesSignedCellBoundary(t *testing.T) {
	clock := fixedClock{time.Date(2026, 8, 18, 4, 0, 0, 0, time.UTC)}
	key := []byte("0123456789abcdef0123456789abcdef")
	cellID := ids.CellID("cell-us-east-01")
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	verifier, _ := routecontext.NewVerifier("router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, clock)
	receipts := &memoryReceipts{seen: map[string]struct{}{}}
	acceptor, _ := routecontext.NewAcceptor(verifier, receipts, clock)
	cell, err := cellapi.New(acceptor, slog.New(slog.NewTextHandler(io.Discard, nil)), cellapi.DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	var upstreamCookie, upstreamAuthorization, upstreamRouteContext string
	cellServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCookie, upstreamAuthorization, upstreamRouteContext = r.Header.Get("Cookie"), r.Header.Get("Authorization"), r.Header.Get(cellapi.RouteContextHeader)
		cell.Handler().ServeHTTP(w, r)
	}))
	defer cellServer.Close()
	authorizer := &captureAuthorizer{result: access.AccountContext{AccountID: ids.AccountID(routerAccount), CellID: cellID, PlacementGeneration: 7, EntitlementVersion: 4, Role: accounts.RoleOwner}}
	router, err := New(fakeSessions{authenticated: sessions.Authenticated{Session: sessions.Session{UserID: ids.UserID(routerUser)}, RotatedToken: "rotated"}}, authorizer, signer, fixedGenerator{routerRequest}, Config{SessionCookieName: "spyglass_test_session", TrustedOrigins: []string{"http://app.test"}, CellRoutes: map[ids.CellID]string{cellID: cellServer.URL}, AllowHTTPCells: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+routerAccount+"/context", nil)
	request.AddCookie(&http.Cookie{Name: "spyglass_test_session", Value: "opaque-session"})
	request.Header.Set("Authorization", "Bearer must-not-cross")
	response := httptest.NewRecorder()
	router.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"placement_generation":7`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	if upstreamCookie != "" || upstreamAuthorization != "" || upstreamRouteContext == "" {
		t.Fatalf("upstream headers cookie=%q authorization=%q route=%t", upstreamCookie, upstreamAuthorization, upstreamRouteContext != "")
	}
	if authorizer.requirement.Package != "" || authorizer.requirement.Mutation {
		t.Fatalf("requirement = %+v", authorizer.requirement)
	}
	if response.Header().Get("X-Request-ID") != routerRequest {
		t.Fatalf("request ID = %q", response.Header().Get("X-Request-ID"))
	}
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Value != "rotated" || !cookies[0].HttpOnly {
		t.Fatalf("rotated cookies = %#v", cookies)
	}
}

func TestMutationRequiresTrustedOriginBeforeAuthorization(t *testing.T) {
	clock := fixedClock{time.Now()}
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	authorizer := &captureAuthorizer{}
	router, err := New(fakeSessions{}, authorizer, signer, fixedGenerator{routerRequest}, Config{SessionCookieName: "test", TrustedOrigins: []string{"https://app.example"}, CellRoutes: map[ids.CellID]string{"cell": "http://cell.test"}, AllowHTTPCells: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+routerAccount+"/work-items", strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	router.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || authorizer.calls != 0 {
		t.Fatalf("response=%d calls=%d", response.Code, authorizer.calls)
	}
}

func TestWorkMutationCarriesOnlyAuthorizedPackageAccess(t *testing.T) {
	clock := fixedClock{time.Date(2026, 8, 18, 4, 0, 0, 0, time.UTC)}
	key := []byte("0123456789abcdef0123456789abcdef")
	cellID := ids.CellID("cell-us-east-01")
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	verifier, _ := routecontext.NewVerifier("router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, clock)
	var routed routecontext.Claims
	cellServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		binding, _ := routecontext.Bind(r.Method, routecontext.Target(r), body)
		claims, err := verifier.Verify(r.Header.Get(cellapi.RouteContextHeader), binding)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		routed = claims
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer cellServer.Close()
	packageAccess := entitlements.PackageAccess{Code: catalog.PackageWork, Version: 1, Mode: catalog.ModeEnabled, Limits: map[catalog.LimitCode]int64{"active_items": 100}, LimitPolicies: map[catalog.LimitCode]entitlements.LimitPolicy{"active_items": {Kind: catalog.LimitKindCapacity, Combine: catalog.LimitMaximum}}}
	authorizer := &captureAuthorizer{result: access.AccountContext{AccountID: ids.AccountID(routerAccount), CellID: cellID, PlacementGeneration: 7, EntitlementVersion: 4, Role: accounts.RoleMember, PackageAccess: &packageAccess}}
	router, err := New(fakeSessions{authenticated: sessions.Authenticated{Session: sessions.Session{UserID: ids.UserID(routerUser)}}}, authorizer, signer, fixedGenerator{routerRequest}, Config{SessionCookieName: "test", TrustedOrigins: []string{"https://app.example"}, CellRoutes: map[ids.CellID]string{cellID: cellServer.URL}, AllowHTTPCells: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+routerAccount+"/work-items", strings.NewReader(`{"title":"Close books"}`))
	request.AddCookie(&http.Cookie{Name: "test", Value: "session"})
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	if authorizer.requirement.Package != catalog.PackageWork || !authorizer.requirement.Mutation {
		t.Fatalf("requirement=%+v", authorizer.requirement)
	}
	if routed.Authority.PackageAccess == nil || routed.Authority.PackageAccess.Code != "work" || routed.Authority.PackageAccess.Mode != "enabled" || routed.Authority.PackageAccess.Limits["active_items"] != 100 {
		t.Fatalf("routed authority=%+v", routed.Authority)
	}
}

func TestCellRejectsReplayAndAlteredAccountPath(t *testing.T) {
	clock := fixedClock{time.Date(2026, 8, 18, 4, 0, 0, 0, time.UTC)}
	key := []byte("0123456789abcdef0123456789abcdef")
	cellID := ids.CellID("cell-us-east-01")
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	verifier, _ := routecontext.NewVerifier("router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, clock)
	acceptor, _ := routecontext.NewAcceptor(verifier, &memoryReceipts{seen: map[string]struct{}{}}, clock)
	cell, _ := cellapi.New(acceptor, slog.New(slog.NewTextHandler(io.Discard, nil)), cellapi.DefaultMaxBody)
	target := "/api/v1/accounts/" + routerAccount + "/context"
	binding, _ := routecontext.Bind(http.MethodGet, target, nil)
	token, _ := signer.Issue(routecontext.Audience(cellID), routecontext.Authority{RequestID: routerRequest, AccountID: ids.AccountID(routerAccount), ActorKind: "user", ActorID: routerUser, Role: "owner", CellID: cellID, PlacementGeneration: 1, EntitlementVersion: 1}, binding)
	call := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set(cellapi.RouteContextHeader, token)
		response := httptest.NewRecorder()
		cell.Handler().ServeHTTP(response, request)
		return response
	}
	if first := call(target); first.Code != http.StatusOK {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	if replay := call(target); replay.Code != http.StatusConflict || !strings.Contains(replay.Body.String(), "route_replay") {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	changed := "/api/v1/accounts/40000000-0000-4000-8000-000000000004/context"
	if altered := call(changed); altered.Code != http.StatusUnauthorized {
		t.Fatalf("altered=%d %s", altered.Code, altered.Body.String())
	}
}

type fakeSessions struct {
	authenticated sessions.Authenticated
	err           error
}

func (f fakeSessions) Authenticate(context.Context, string) (sessions.Authenticated, error) {
	return f.authenticated, f.err
}

type captureAuthorizer struct {
	result      access.AccountContext
	err         error
	calls       int
	requirement access.Requirement
}

func (f *captureAuthorizer) Authorize(_ context.Context, _ access.Actor, _ ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	f.calls++
	f.requirement = requirement
	return f.result, f.err
}

type fixedGenerator struct{ value string }

func (f fixedGenerator) New() string { return f.value }

type fixedClock struct{ now time.Time }

func (f fixedClock) Now() time.Time { return f.now }

type memoryReceipts struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func (m *memoryReceipts) Consume(_ context.Context, claims routecontext.Claims, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := string(claims.Authority.AccountID) + ":" + claims.Authority.RequestID
	if _, exists := m.seen[key]; exists {
		return routecontext.ErrReplay
	}
	m.seen[key] = struct{}{}
	return nil
}
