package admissionhttp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/admissionapi"
)

const (
	admissionAccount   = "10000000-0000-4000-8000-000000000001"
	admissionUser      = "20000000-0000-4000-8000-000000000002"
	admissionRoute     = "30000000-0000-4000-8000-000000000003"
	admissionOperation = "40000000-0000-4000-8000-000000000004"
)

func TestClientUsesSignedRouteProofForNarrowWorkCapacity(t *testing.T) {
	now := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	clock := admissionClock{now}
	key := []byte("0123456789abcdef0123456789abcdef")
	cellID := ids.CellID("cell-us-east-01")
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	verifier, _ := routecontext.NewVerifier("router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, clock)
	usage := &admissionUsage{result: usageadmission.Reservation{RequestID: admissionOperation, State: usageadmission.ReservationActive, Current: 4, Maximum: 100}}
	server, err := admissionapi.New(usage, map[ids.CellID]admissionapi.Verifier{cellID: verifier}, slog.New(slog.NewTextHandler(io.Discard, nil)), admissionapi.DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client, err := New(httpServer.URL, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := routecontext.Bind(http.MethodPost, "/api/v1/accounts/"+admissionAccount+"/work-items", []byte(`{"title":"Close books"}`))
	authority := routecontext.Authority{RequestID: admissionRoute, OperationID: admissionOperation, AccountID: ids.AccountID(admissionAccount), ActorKind: "user", ActorID: admissionUser, Role: "member", CellID: cellID, PlacementGeneration: 2, EntitlementVersion: 7, PackageAccess: &routecontext.PackageAccess{Code: "work", Version: 1, Mode: "enabled", Limits: map[string]int64{"active_items": 100}, LimitPolicies: map[string]routecontext.LimitPolicy{"active_items": {Kind: "capacity", Combine: "maximum"}}}}
	token, _ := signer.Issue(routecontext.Audience(cellID), authority, binding)
	claims, _ := verifier.Verify(token, binding)
	ctx := routecontext.WithClaims(context.Background(), claims)
	ctx = routecontext.WithProof(ctx, routecontext.Proof{Token: token, Binding: binding})
	reservation, err := client.Reserve(ctx, usageadmission.ReserveCommand{Actor: access.Actor{UserID: ids.UserID(admissionUser)}, AccountID: ids.AccountID(admissionAccount), PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, RequestID: admissionOperation})
	if err != nil || reservation.State != usageadmission.ReservationActive || reservation.Current != 4 || usage.reserve.AccountID != admissionAccount || usage.reserve.Actor.UserID != admissionUser {
		t.Fatalf("reservation=%+v command=%+v err=%v", reservation, usage.reserve, err)
	}
}

func TestClientMapsAdmissionDenialsAndRequiresMatchingOperation(t *testing.T) {
	client, _ := New("https://admission.test", false, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": []string{"application/problem+json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"https://infiniteocean.net/problems/limit_exceeded","title":"Forbidden","status":403,"code":"limit_exceeded","detail":"capacity reached","current":2,"maximum":2}`))}, nil
	}))
	claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(admissionAccount), OperationID: admissionOperation, CellID: "cell-us-east-01"}}
	binding, _ := routecontext.Bind(http.MethodPost, "/api/v1/accounts/"+admissionAccount+"/work-items", nil)
	ctx := routecontext.WithClaims(context.Background(), claims)
	ctx = routecontext.WithProof(ctx, routecontext.Proof{Token: "proof", Binding: binding})
	_, err := client.Reserve(ctx, usageadmission.ReserveCommand{AccountID: ids.AccountID(admissionAccount), PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, RequestID: admissionOperation})
	if !access.IsDenied(err, access.DenialLimitExceeded) {
		t.Fatalf("mapped error=%v", err)
	}
	_, err = client.Release(ctx, usageadmission.ReleaseCommand{AccountID: ids.AccountID(admissionAccount), RequestID: "50000000-0000-4000-8000-000000000005"})
	if err != usageadmission.ErrInvalidRequest {
		t.Fatalf("mismatched operation error=%v", err)
	}
}

func TestClientRejectsNonOriginAdmissionTargets(t *testing.T) {
	for _, target := range []string{"http://admission.test", "https://user@admission.test", "https://admission.test/path", "https://admission.test?", "https://admission.test?query=value", "https://admission.test#fragment"} {
		t.Run(target, func(t *testing.T) {
			if _, err := New(target, false, nil); err == nil {
				t.Fatal("expected unsafe admission target to fail closed")
			}
		})
	}
}

type admissionUsage struct {
	reserve usageadmission.ReserveCommand
	result  usageadmission.Reservation
}

func (u *admissionUsage) Reserve(_ context.Context, command usageadmission.ReserveCommand) (usageadmission.Reservation, error) {
	u.reserve = command
	return u.result, nil
}
func (u *admissionUsage) Release(context.Context, usageadmission.ReleaseCommand) (usageadmission.Reservation, error) {
	return u.result, nil
}

type admissionClock struct{ now time.Time }

func (c admissionClock) Now() time.Time { return c.now }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
