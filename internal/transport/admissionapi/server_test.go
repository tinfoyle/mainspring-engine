package admissionapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/platform/workloadidentity"
)

const (
	testAccount   = "10000000-0000-4000-8000-000000000001"
	testActor     = "20000000-0000-4000-8000-000000000002"
	testOperation = "30000000-0000-4000-8000-000000000003"
	testOtherOp   = "40000000-0000-4000-8000-000000000004"
	testItem      = "50000000-0000-4000-8000-000000000005"
)

func TestAdmissionRejectsOperationMismatchBeforeUsage(t *testing.T) {
	cellID := ids.CellID("cell-us-east-01")
	binding, err := routecontext.Bind(http.MethodPost, "/api/v1/accounts/"+testAccount+"/work-items", []byte(`{"title":"Close books"}`))
	if err != nil {
		t.Fatal(err)
	}
	claims := routecontext.Claims{Authority: routecontext.Authority{
		OperationID: testOperation, AccountID: ids.AccountID(testAccount), ActorKind: "user", ActorID: testActor, CellID: cellID,
		PackageAccess: &routecontext.PackageAccess{Code: string(catalog.PackageWork), Mode: string(catalog.ModeEnabled)},
	}, Binding: binding}
	usage := &testUsage{}
	server, err := New(usage, map[ids.CellID]Verifier{cellID: testVerifier{claims: claims}}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(capacityRequest{CellID: cellID, RequestID: testOtherOp, RouteContext: "signed-proof", Binding: binding})
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/work/capacity/reserve", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || usage.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, usage.calls, response.Body.String())
	}
}

func TestWorkMutationBindingIsAnExactAllowlist(t *testing.T) {
	base := "/api/v1/accounts/" + testAccount + "/work-items"
	for _, test := range []struct {
		name    string
		method  string
		target  string
		allowed bool
	}{
		{name: "create", method: http.MethodPost, target: base, allowed: true},
		{name: "reopen", method: http.MethodPost, target: base + "/" + testItem + "/transitions", allowed: true},
		{name: "assignment has no capacity effect", method: http.MethodPatch, target: base + "/" + testItem + "/assignment"},
		{name: "extra suffix", method: http.MethodPost, target: base + "/" + testItem + "/transitions/retry"},
		{name: "invalid item", method: http.MethodPost, target: base + "/not-a-uuid/transitions"},
		{name: "other account", method: http.MethodPost, target: "/api/v1/accounts/" + testOtherOp + "/work-items"},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(testAccount)}, Binding: routecontext.Binding{Method: test.method, Target: test.target}}
			if got := workMutationBinding(claims); got != test.allowed {
				t.Fatalf("allowed=%t want=%t", got, test.allowed)
			}
		})
	}
}

func TestReviewCreationBindingIsAnExactAllowlist(t *testing.T) {
	base := "/api/v1/accounts/" + testAccount + "/attention/work-reviews"
	for _, test := range []struct {
		name    string
		method  string
		target  string
		account ids.AccountID
		op      string
		allowed bool
	}{
		{name: "create", method: http.MethodPost, target: base, account: testAccount, op: testOperation, allowed: true},
		{name: "read", method: http.MethodGet, target: base, account: testAccount, op: testOperation},
		{name: "missing operation", method: http.MethodPost, target: base, account: testAccount},
		{name: "detail", method: http.MethodPost, target: base + "/" + testItem, account: testAccount, op: testOperation},
		{name: "other account", method: http.MethodPost, target: base, account: testOtherOp, op: testOperation},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: test.account, OperationID: test.op}, Binding: routecontext.Binding{Method: test.method, Target: test.target}}
			if got := reviewCreationBinding(claims); got != test.allowed {
				t.Fatalf("allowed=%t want=%t", got, test.allowed)
			}
		})
	}
}

func TestRouteCanaryVerifiesOnlyDedicatedWorkloadProofWithoutUsage(t *testing.T) {
	cellID := ids.CellID("cell-us-east-01")
	binding, err := routecontext.Bind(http.MethodGet, "/api/v1/accounts/"+testAccount+"/context", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := routecontext.Claims{KeyID: "candidate", Authority: routecontext.Authority{
		AccountID: testAccount, ActorKind: "workload", ActorID: routecontext.RotationCanaryActorID,
		CellID: cellID, PlacementGeneration: 1, EntitlementVersion: 1,
	}, Binding: binding}
	for _, test := range []struct {
		name   string
		claims routecontext.Claims
		status int
	}{
		{name: "candidate canary", claims: base, status: http.StatusOK},
		{name: "ordinary user proof", claims: func() routecontext.Claims {
			value := base
			value.Authority.ActorKind, value.Authority.ActorID, value.Authority.Role = "user", testActor, "owner"
			return value
		}(), status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			usage := &testUsage{}
			server, err := New(usage, map[ids.CellID]Verifier{cellID: testVerifier{claims: test.claims}}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(routeCanaryRequest{CellID: cellID, RouteContext: "signed-proof", Binding: binding})
			request := httptest.NewRequest(http.MethodPost, "/internal/v1/route-canary", bytes.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.status || usage.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, usage.calls, response.Body.String())
			}
			if test.status == http.StatusOK && !bytes.Contains(response.Body.Bytes(), []byte(`"key_id":"candidate"`)) {
				t.Fatalf("body=%s", response.Body.String())
			}
		})
	}
}

func TestWorkAgentAuthorizationResolvesCurrentGlobalAuthority(t *testing.T) {
	cellID := ids.CellID("cell-us-east-01")
	authorizer := &testAgentAuthorizer{result: access.AccountContext{AccountID: testAccount, CellID: cellID, EntitlementVersion: 9,
		Role: accounts.RoleMember, PackageAccess: &entitlements.PackageAccess{Code: catalog.PackageAgents, Mode: catalog.ModeEnabled,
			Limits: map[catalog.LimitCode]int64{"concurrent_runs": 3}}}}
	server, err := New(&testUsage{}, map[ids.CellID]Verifier{cellID: testVerifier{}}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody,
		WithAgentExecutionAuthorizer(authorizer))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(workAgentAuthorizationRequest{CellID: cellID, AccountID: testAccount, UserID: testActor, ExecutionID: testOperation})
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/agents/work-executions:authorize", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || authorizer.actor.UserID != testActor || authorizer.requirement.Package != catalog.PackageAgents || !authorizer.requirement.Mutation ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"maximum_concurrent_runs":3`)) {
		t.Fatalf("status=%d actor=%+v requirement=%+v body=%s", response.Code, authorizer.actor, authorizer.requirement, response.Body.String())
	}
}

func TestWorkAgentAuthorizationBindsVerifiedIdentityToCell(t *testing.T) {
	cellA, cellB := ids.CellID("cell-us-east-01"), ids.CellID("cell-us-west-01")
	authorizer := &testAgentAuthorizer{result: access.AccountContext{AccountID: testAccount, CellID: cellB, EntitlementVersion: 9,
		PackageAccess: &entitlements.PackageAccess{Code: catalog.PackageAgents, Mode: catalog.ModeEnabled, Limits: map[catalog.LimitCode]int64{"concurrent_runs": 2}}}}
	server, err := New(&testUsage{}, map[ids.CellID]Verifier{cellA: testVerifier{}, cellB: testVerifier{}}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody,
		WithAgentExecutionAuthorizer(authorizer))
	if err != nil {
		t.Fatal(err)
	}
	identity := "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/agent-dispatch-worker"
	secured, err := workloadidentity.RequireClientIdentity(server.Handler(), []string{identity}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(workAgentAuthorizationRequest{CellID: cellB, AccountID: testAccount, UserID: testActor, ExecutionID: testOperation})
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/agents/work-executions:authorize", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	identityURI, _ := url.Parse(identity)
	certificate := &x509.Certificate{URIs: []*url.URL{identityURI}}
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}
	response := httptest.NewRecorder()
	secured.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || authorizer.calls != 0 {
		t.Fatalf("status=%d authorization calls=%d body=%s", response.Code, authorizer.calls, response.Body.String())
	}
}

type testVerifier struct{ claims routecontext.Claims }

func (v testVerifier) Verify(string, routecontext.Binding) (routecontext.Claims, error) {
	return v.claims, nil
}

type testUsage struct{ calls int }

type testAgentAuthorizer struct {
	calls       int
	actor       access.Actor
	requirement access.Requirement
	result      access.AccountContext
	err         error
}

func (a *testAgentAuthorizer) Authorize(_ context.Context, actor access.Actor, _ ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	a.calls++
	a.actor, a.requirement = actor, requirement
	return a.result, a.err
}

func (u *testUsage) Reserve(context.Context, usageadmission.ReserveCommand) (usageadmission.Reservation, error) {
	u.calls++
	return usageadmission.Reservation{}, nil
}

func (u *testUsage) Release(context.Context, usageadmission.ReleaseCommand) (usageadmission.Reservation, error) {
	u.calls++
	return usageadmission.Reservation{}, nil
}
