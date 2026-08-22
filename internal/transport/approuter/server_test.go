package approuter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
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
	routerRetry   = "40000000-0000-4000-8000-000000000004"
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
	directory := directoryFor(t, cellID, 7, cellServer.URL)
	router, err := New(fakeSessions{authenticated: sessions.Authenticated{Session: sessions.Session{UserID: ids.UserID(routerUser)}, RotatedToken: "rotated"}}, authorizer, directory, signer, fixedGenerator{routerRequest}, Config{SessionCookieName: "spyglass_test_session", TrustedOrigins: []string{"http://app.test"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if directory.accountID != routerAccount || directory.cellID != cellID || directory.generation != 7 {
		t.Fatalf("directory resolution account=%s cell=%s generation=%d", directory.accountID, directory.cellID, directory.generation)
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
	router, err := New(fakeSessions{}, authorizer, &fakeDirectory{}, signer, fixedGenerator{routerRequest}, Config{SessionCookieName: "test", TrustedOrigins: []string{"https://app.example"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

func TestRouterRejectsUnpublishedWorkCommandsBeforeAuthentication(t *testing.T) {
	clock := fixedClock{time.Now()}
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	authorizer := &captureAuthorizer{}
	router, _ := New(fakeSessions{}, authorizer, &fakeDirectory{}, signer, fixedGenerator{routerRequest}, Config{SessionCookieName: "test", TrustedOrigins: []string{"https://app.example"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, target := range []struct{ method, path string }{
		{http.MethodDelete, "/api/v1/accounts/" + routerAccount + "/work-items/" + routerRequest},
		{http.MethodPost, "/api/v1/accounts/" + routerAccount + "/work-items/" + routerRequest + "/children"},
		{http.MethodPatch, "/api/v1/accounts/" + routerAccount + "/work-items/not-a-uuid/assignment"},
	} {
		request := httptest.NewRequest(target.method, target.path, nil)
		response := httptest.NewRecorder()
		router.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d %s", target.method, target.path, response.Code, response.Body.String())
		}
	}
	if authorizer.calls != 0 {
		t.Fatalf("unpublished routes reached authorization %d times", authorizer.calls)
	}
}

func TestAgentRouteAllowlistMatchesCellSurface(t *testing.T) {
	tests := []struct {
		method, resource string
		mutation         bool
		allowed          bool
	}{
		{http.MethodGet, "agent-boardrooms", false, true},
		{http.MethodPost, "agent-boardrooms", true, true},
		{http.MethodGet, "agent-boardrooms/" + routerRequest + "/personas", false, true},
		{http.MethodPost, "agent-boardrooms/" + routerRequest + "/personas", true, true},
		{http.MethodGet, "agent-boardrooms/" + routerRequest + "/conversations", false, true},
		{http.MethodPost, "agent-boardrooms/" + routerRequest + "/runs", true, true},
		{http.MethodGet, "agent-conversations/" + routerRequest, false, true},
		{http.MethodGet, "agent-conversations/" + routerRequest + "/messages", false, true},
		{http.MethodGet, "agent-runs/" + routerRequest, false, true},
		{http.MethodPost, "agent-runs/" + routerRequest + "/resolutions", true, true},
		{http.MethodDelete, "agent-boardrooms", false, false},
		{http.MethodGet, "agent-boardrooms/not-a-uuid/personas", false, false},
		{http.MethodGet, "agent-boardrooms/" + routerRequest + "/runs", false, false},
		{http.MethodGet, "agent-conversations/" + routerRequest + "/unknown", false, false},
		{http.MethodGet, "agent-conversations/not-a-uuid/messages", false, false},
		{http.MethodPost, "agent-runs/" + routerRequest, false, false},
		{http.MethodGet, "agent-runs/" + routerRequest + "/resolutions", false, false},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.resource, func(t *testing.T) {
			requirement, allowed := routeRequirement(test.method, test.resource)
			if allowed != test.allowed {
				t.Fatalf("allowed=%t want %t requirement=%+v", allowed, test.allowed, requirement)
			}
			if test.allowed && (requirement.Package != catalog.PackageAgents || requirement.Mutation != test.mutation) {
				t.Fatalf("requirement=%+v", requirement)
			}
		})
	}
}

func TestAttentionRouteAllowlistMatchesCellSurface(t *testing.T) {
	tests := []struct {
		method, resource string
		packageCode      catalog.PackageCode
		mutation         bool
		allowed          bool
	}{
		{http.MethodGet, "attention/information-requests", catalog.PackageWork, false, true},
		{http.MethodPost, "attention/information-requests", catalog.PackageWork, true, true},
		{http.MethodGet, "attention/information-requests/" + routerRequest, catalog.PackageWork, false, true},
		{http.MethodPost, "attention/information-requests/" + routerRequest + "/answers", catalog.PackageWork, true, true},
		{http.MethodPost, "attention/information-requests/" + routerRequest + "/cancellations", catalog.PackageWork, true, true},
		{http.MethodGet, "attention/work-reviews", catalog.PackageWork, false, true},
		{http.MethodPost, "attention/work-reviews/" + routerRequest + "/decisions", catalog.PackageWork, true, true},
		{http.MethodGet, "attention/approvals", catalog.PackageAgents, false, true},
		{http.MethodPost, "attention/approvals", catalog.PackageAgents, true, true},
		{http.MethodPost, "attention/approvals/" + routerRequest + "/cancellations", catalog.PackageAgents, true, true},
		{http.MethodGet, "attention/actions", catalog.PackageAgents, false, true},
		{http.MethodGet, "attention/actions/" + routerRequest, catalog.PackageAgents, false, true},
		{http.MethodPost, "attention/actions/" + routerRequest + "/resolution-requests", catalog.PackageAgents, true, true},
		{http.MethodPost, "attention/actions/" + routerRequest + "/resolutions/" + routerRequest + "/confirmations", catalog.PackageAgents, true, true},
		{http.MethodPost, "attention/actions", "", false, false},
		{http.MethodPost, "attention/actions/" + routerRequest + "/confirmations", "", false, false},
		{http.MethodPost, "attention/actions/" + routerRequest + "/resolutions/not-a-uuid/confirmations", "", false, false},
		{http.MethodDelete, "attention/information-requests", "", false, false},
		{http.MethodGet, "attention/information-requests/not-a-uuid", "", false, false},
		{http.MethodPost, "attention/information-requests/" + routerRequest + "/decisions", "", false, false},
		{http.MethodPost, "attention/work-reviews/" + routerRequest + "/answers", "", false, false},
		{http.MethodGet, "attention/approvals/" + routerRequest + "/decisions", "", false, false},
		{http.MethodPost, "attention/unknown", "", false, false},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.resource, func(t *testing.T) {
			requirement, allowed := routeRequirement(test.method, test.resource)
			if allowed != test.allowed {
				t.Fatalf("allowed=%t want %t requirement=%+v", allowed, test.allowed, requirement)
			}
			if test.allowed && (requirement.Package != test.packageCode || requirement.Mutation != test.mutation) {
				t.Fatalf("requirement=%+v", requirement)
			}
		})
	}
}

func TestKnowledgeRouteAllowlistMatchesCellSurface(t *testing.T) {
	tests := []struct {
		method, resource string
		mutation         bool
		allowed          bool
	}{
		{http.MethodPost, "knowledge/evidence", true, true},
		{http.MethodGet, "knowledge/facts", false, true},
		{http.MethodPost, "knowledge/claims", true, true},
		{http.MethodGet, "knowledge/claims/" + routerRequest, false, true},
		{http.MethodPost, "knowledge/claims/" + routerRequest + "/decisions", true, true},
		{http.MethodGet, "knowledge/evidence", false, false},
		{http.MethodPost, "knowledge/facts", false, false},
		{http.MethodGet, "knowledge/claims", false, false},
		{http.MethodGet, "knowledge/claims/not-a-uuid", false, false},
		{http.MethodGet, "knowledge/claims/" + routerRequest + "/decisions", false, false},
		{http.MethodDelete, "knowledge/claims/" + routerRequest, false, false},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.resource, func(t *testing.T) {
			requirement, allowed := routeRequirement(test.method, test.resource)
			if allowed != test.allowed {
				t.Fatalf("allowed=%t want %t requirement=%+v", allowed, test.allowed, requirement)
			}
			if test.allowed && (requirement.Package != catalog.PackageKnowledge || requirement.Mutation != test.mutation) {
				t.Fatalf("requirement=%+v", requirement)
			}
		})
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
		binding, _ := routecontext.BindRequest(r, body)
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
	router, err := New(fakeSessions{authenticated: sessions.Authenticated{Session: sessions.Session{UserID: ids.UserID(routerUser)}}}, authorizer, directoryFor(t, cellID, 7, cellServer.URL), signer, fixedGenerator{routerRequest}, Config{SessionCookieName: "test", TrustedOrigins: []string{"https://app.example"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+routerAccount+"/work-items", strings.NewReader(`{"title":"Close books"}`))
	request.AddCookie(&http.Cookie{Name: "test", Value: "session"})
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "50000000-0000-4000-8000-000000000005")
	response := httptest.NewRecorder()
	router.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	if authorizer.requirement.Package != catalog.PackageWork || !authorizer.requirement.Mutation {
		t.Fatalf("requirement=%+v", authorizer.requirement)
	}
	if routed.Authority.OperationID != "50000000-0000-4000-8000-000000000005" || routed.Authority.PackageAccess == nil || routed.Authority.PackageAccess.Code != "work" || routed.Authority.PackageAccess.Mode != "enabled" || routed.Authority.PackageAccess.Limits["active_items"] != 100 {
		t.Fatalf("routed authority=%+v", routed.Authority)
	}
}

func TestCellTransportRetryStaysInCellAndPreservesMutationIdempotency(t *testing.T) {
	clock := fixedClock{time.Date(2026, 8, 18, 4, 0, 0, 0, time.UTC)}
	key := []byte("0123456789abcdef0123456789abcdef")
	cellID := ids.CellID("cell-us-east-01")
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	verifier, _ := routecontext.NewVerifier("router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, clock)
	type attempt struct {
		host, path, body, operationID, requestID string
	}
	operationID := "50000000-0000-4000-8000-000000000005"
	var attempts []attempt
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		binding, bindErr := routecontext.BindRequest(request, body)
		claims, verifyErr := verifier.Verify(request.Header.Get(cellapi.RouteContextHeader), binding)
		if bindErr != nil || verifyErr != nil {
			t.Fatalf("verify routed attempt: binding=%v proof=%v", bindErr, verifyErr)
		}
		attempts = append(attempts, attempt{host: request.URL.Host, path: request.URL.RequestURI(), body: string(body), operationID: claims.Authority.OperationID, requestID: claims.Authority.RequestID})
		if len(attempts) == 1 {
			return nil, errors.New("connection reset after dispatch")
		}
		return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Etag":         []string{`W/"1"`},
			"Location":     []string{"/api/v1/accounts/" + routerAccount + "/work-items/" + operationID},
		}, Body: io.NopCloser(strings.NewReader(`{"accepted":true}`))}, nil
	})
	authorizer := &captureAuthorizer{result: access.AccountContext{AccountID: ids.AccountID(routerAccount), CellID: cellID, PlacementGeneration: 7, EntitlementVersion: 4, Role: accounts.RoleOwner}}
	generator := &sequenceGenerator{values: []string{routerRequest, routerRetry}}
	router, err := New(fakeSessions{authenticated: sessions.Authenticated{Session: sessions.Session{UserID: ids.UserID(routerUser)}}}, authorizer, directoryFor(t, cellID, 7, "https://cell-a.test"), signer, generator, Config{SessionCookieName: "test", TrustedOrigins: []string{"https://app.example"}, Transport: transport}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://app.example/api/v1/accounts/"+routerAccount+"/work-items?view=compact", strings.NewReader(`{"title":"Close books"}`))
	request.AddCookie(&http.Cookie{Name: "test", Value: "session"})
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID)
	response := httptest.NewRecorder()
	router.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("X-Request-ID") != routerRetry || response.Header().Get("ETag") != `W/"1"` || response.Header().Get("Location") != "/api/v1/accounts/"+routerAccount+"/work-items/"+operationID {
		t.Fatalf("response=%d request_id=%q etag=%q location=%q body=%s", response.Code, response.Header().Get("X-Request-ID"), response.Header().Get("ETag"), response.Header().Get("Location"), response.Body.String())
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts=%d", len(attempts))
	}
	for _, value := range attempts {
		if value.host != "cell-a.test" || value.path != "/api/v1/accounts/"+routerAccount+"/work-items?view=compact" || value.body != `{"title":"Close books"}` || value.operationID != operationID {
			t.Fatalf("routed attempt=%+v", value)
		}
	}
	if attempts[0].requestID != routerRequest || attempts[1].requestID != routerRetry || attempts[0].requestID == attempts[1].requestID {
		t.Fatalf("route request IDs=%q,%q", attempts[0].requestID, attempts[1].requestID)
	}
	if stats := router.TransportStats(); stats.RetryAttempts != 1 || stats.RetryRecovered != 1 || stats.RequestsFailed != 0 {
		t.Fatalf("transport stats=%+v", stats)
	}
}

func TestCellTransportDoesNotRetryHTTPResponse(t *testing.T) {
	clock := fixedClock{time.Date(2026, 8, 18, 4, 0, 0, 0, time.UTC)}
	key := []byte("0123456789abcdef0123456789abcdef")
	cellID := ids.CellID("cell-us-east-01")
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	calls := 0
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": []string{"application/problem+json"}}, Body: io.NopCloser(strings.NewReader(`{"code":"cell_busy"}`))}, nil
	})
	authorizer := &captureAuthorizer{result: access.AccountContext{AccountID: ids.AccountID(routerAccount), CellID: cellID, PlacementGeneration: 1, EntitlementVersion: 1, Role: accounts.RoleOwner}}
	router, err := New(fakeSessions{authenticated: sessions.Authenticated{Session: sessions.Session{UserID: ids.UserID(routerUser)}}}, authorizer, directoryFor(t, cellID, 1, "https://cell-a.test"), signer, fixedGenerator{routerRequest}, Config{SessionCookieName: "test", TrustedOrigins: []string{"https://app.example"}, Transport: transport}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://app.example/api/v1/accounts/"+routerAccount+"/context", nil)
	request.AddCookie(&http.Cookie{Name: "test", Value: "session"})
	response := httptest.NewRecorder()
	router.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || calls != 1 || router.TransportStats() != (TransportStats{}) {
		t.Fatalf("response=%d calls=%d stats=%+v body=%s", response.Code, calls, router.TransportStats(), response.Body.String())
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

type fakeDirectory struct {
	route      accountdirectory.Route
	err        error
	calls      int
	accountID  ids.AccountID
	cellID     ids.CellID
	generation uint64
}

func (f *fakeDirectory) Resolve(_ context.Context, accountID ids.AccountID, cellID ids.CellID, generation uint64) (accountdirectory.Route, error) {
	f.calls++
	f.accountID, f.cellID, f.generation = accountID, cellID, generation
	return f.route, f.err
}

func directoryFor(t *testing.T, cellID ids.CellID, generation uint64, origin string) *fakeDirectory {
	t.Helper()
	parsed, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeDirectory{route: accountdirectory.Route{CellID: cellID, PlacementGeneration: generation, Origin: *parsed}}
}

func (f *captureAuthorizer) Authorize(_ context.Context, _ access.Actor, _ ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	f.calls++
	f.requirement = requirement
	return f.result, f.err
}

type fixedGenerator struct{ value string }

func (f fixedGenerator) New() string { return f.value }

type sequenceGenerator struct {
	mu     sync.Mutex
	values []string
	next   int
}

func (g *sequenceGenerator) New() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	value := g.values[g.next]
	g.next++
	return value
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

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
