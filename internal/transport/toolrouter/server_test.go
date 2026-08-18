package toolrouter

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/platform/toolcontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
)

const (
	testAccount    = "10000000-0000-4000-8000-000000000001"
	testInvocation = "20000000-0000-4000-8000-000000000002"
	testPod        = "30000000-0000-4000-8000-000000000003"
	testOperation  = "40000000-0000-4000-8000-000000000004"
	testRequest    = "50000000-0000-4000-8000-000000000005"
)

type acceptingBoundary struct {
	claims  toolcontext.Claims
	binding routecontext.Binding
}

func (a *acceptingBoundary) Accept(_ context.Context, _ string, binding routecontext.Binding) (toolcontext.Claims, error) {
	a.binding = binding
	return a.claims, nil
}

type workloadAuthorizer struct{ actor access.Actor }

func (a *workloadAuthorizer) Authorize(_ context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	a.actor = actor
	if accountID != testAccount || requirement.Package != catalog.PackageWork || requirement.Mutation {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialCorruptContext}
	}
	return access.AccountContext{AccountID: accountID, CellID: "cell-a", PlacementGeneration: 3, EntitlementVersion: 7, PackageAccess: &entitlements.PackageAccess{Code: catalog.PackageWork, Version: 1, Mode: catalog.ModeReadOnly}}, nil
}

type directory struct{ origin url.URL }

func (d directory) Resolve(_ context.Context, _ ids.AccountID, _ ids.CellID, _ uint64) (accountdirectory.Route, error) {
	return accountdirectory.Route{CellID: "cell-a", PlacementGeneration: 3, Origin: d.origin}, nil
}

type routeSigner struct{ authority routecontext.Authority }

func (s *routeSigner) Issue(_ string, authority routecontext.Authority, _ routecontext.Binding) (string, error) {
	s.authority = authority
	return "cell-route-token", nil
}

type fixedIDs struct{}

func (fixedIDs) New() string { return testRequest }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestWorkSummaryDispatchReauthorizesAndRoutesAsWorkload(t *testing.T) {
	boundary := &acceptingBoundary{claims: toolcontext.Claims{Authority: toolcontext.Authority{
		RequestID: "60000000-0000-4000-8000-000000000006", AccountID: testAccount, InvocationID: testInvocation,
		PodUID: testPod, OperationID: testOperation, Capability: WorkSummaryCapability,
	}}}
	authorizer := &workloadAuthorizer{}
	signer := &routeSigner{}
	cellOrigin, _ := url.Parse("http://cell.internal")
	transport := roundTrip(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/accounts/"+testAccount+"/work-items/summary" || request.Header.Get(cellapi.RouteContextHeader) != "cell-route-token" {
			t.Fatalf("unexpected cell request: %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"active":4,"in_progress":2,"waiting":1,"urgent":1,"done":8}`))}, nil
	})
	server, err := New(boundary, authorizer, directory{origin: *cellOrigin}, signer, fixedIDs{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Transport: transport, AllowHTTPCells: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/tools:invoke", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ContextHeader, "tool-proof")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"active":4`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	if boundary.binding.Method != http.MethodPost || boundary.binding.Target != "/internal/v1/tools:invoke" {
		t.Fatalf("dispatch was not request-bound: %#v", boundary.binding)
	}
	if authorizer.actor.WorkloadID != "runner-invocation:"+testInvocation || authorizer.actor.UserID != "" {
		t.Fatalf("unexpected actor: %#v", authorizer.actor)
	}
	if signer.authority.ActorKind != "workload" || signer.authority.Role != "" || signer.authority.OperationID != testOperation || signer.authority.PackageAccess == nil || signer.authority.PackageAccess.Mode != "read_only" {
		t.Fatalf("unexpected cell authority: %#v", signer.authority)
	}
}

func TestToolDispatchRejectsInputAndUnavailableCapabilityBeforeCell(t *testing.T) {
	for _, test := range []struct {
		name, body, capability string
		status                 int
	}{
		{"input", `{"scope":"all"}`, WorkSummaryCapability, http.StatusBadRequest},
		{"capability", `{}`, "finance.balance.read", http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			boundary := &acceptingBoundary{claims: toolcontext.Claims{Authority: toolcontext.Authority{AccountID: testAccount, InvocationID: testInvocation, OperationID: testOperation, Capability: test.capability}}}
			origin, _ := url.Parse("http://cell.internal")
			server, _ := New(boundary, &workloadAuthorizer{}, directory{origin: *origin}, &routeSigner{}, fixedIDs{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Transport: roundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("cell called"); return nil, nil }), AllowHTTPCells: true})
			request := httptest.NewRequest(http.MethodPost, "/internal/v1/tools:invoke", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(ContextHeader, "proof")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
