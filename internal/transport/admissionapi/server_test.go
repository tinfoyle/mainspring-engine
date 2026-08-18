package admissionapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
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

type testVerifier struct{ claims routecontext.Claims }

func (v testVerifier) Verify(string, routecontext.Binding) (routecontext.Claims, error) {
	return v.claims, nil
}

type testUsage struct{ calls int }

func (u *testUsage) Reserve(context.Context, usageadmission.ReserveCommand) (usageadmission.Reservation, error) {
	u.calls++
	return usageadmission.Reservation{}, nil
}

func (u *testUsage) Release(context.Context, usageadmission.ReleaseCommand) (usageadmission.Reservation, error) {
	u.calls++
	return usageadmission.Reservation{}, nil
}
