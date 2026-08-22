package mcpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type routedReceiptStore struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func (s *routedReceiptStore) Consume(_ context.Context, claims routecontext.Claims, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[claims.Authority.RequestID]; ok {
		return routecontext.ErrReplay
	}
	s.seen[claims.Authority.RequestID] = struct{}{}
	return nil
}

func TestRoutedHandlerConsumesProofAndReplacesExternalCredential(t *testing.T) {
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	clock := fixedMCPClock{now: now}
	key := []byte("0123456789abcdef0123456789abcdef")
	verifier, _ := routecontext.NewVerifier("router", routecontext.Audience("cell-us-east-01"), map[string][]byte{"active": key}, routecontext.MaximumLifetime, routecontext.DefaultClockSkew, clock)
	acceptor, _ := routecontext.NewAcceptor(verifier, &routedReceiptStore{seen: map[string]struct{}{}}, clock)
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	request := httptest.NewRequest(http.MethodPost, "https://cell.internal/internal/v1/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	binding, _ := routecontext.BindRequest(request, []byte(body))
	signer, _ := routecontext.NewSigner("router", "active", key, 20*time.Second, clock)
	proof, _ := signer.Issue(routecontext.Audience("cell-us-east-01"), routecontext.Authority{RequestID: "30000000-0000-4000-8000-000000000003", AccountID: "10000000-0000-4000-8000-000000000001", ActorKind: "user", ActorID: "20000000-0000-4000-8000-000000000002", Role: "owner", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 1}, binding)
	innerCalls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalls++
		claims, ok := routecontext.FromContext(r.Context())
		if !ok || claims.Authority.AccountID != "10000000-0000-4000-8000-000000000001" || r.Header.Get(routecontext.HeaderName) != "" || r.Header.Get("Authorization") != "Bearer "+routedCredential {
			t.Fatalf("claims=%+v route=%q authorization=%q", claims, r.Header.Get(routecontext.HeaderName), r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler, err := RoutedHandler(acceptor, inner, DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(routecontext.HeaderName, proof)
	request.Header.Set("Authorization", "Bearer customer-secret-must-disappear")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || innerCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, innerCalls, response.Body.String())
	}

	replay := httptest.NewRequest(http.MethodPost, "https://cell.internal/internal/v1/mcp", strings.NewReader(body))
	replay.Header.Set("Content-Type", "application/json")
	replay.Header.Set(routecontext.HeaderName, proof)
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusConflict || !strings.Contains(replayResponse.Body.String(), "route_replay") || innerCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", replayResponse.Code, innerCalls, replayResponse.Body.String())
	}
}

func TestRoutedAuthorityReauthorizesSignedPackage(t *testing.T) {
	authority := NewRoutedAuthority()
	claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: "10000000-0000-4000-8000-000000000001", ActorKind: "user", ActorID: "20000000-0000-4000-8000-000000000002", Role: "owner", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 1, PackageAccess: &routecontext.PackageAccess{Code: "finance", Version: 1, Mode: "enabled"}}}
	ctx := routecontext.WithClaims(context.Background(), claims)
	actor, err := authority.Authenticate(ctx, routedCredential)
	if err != nil || actor.UserID != ids.UserID("20000000-0000-4000-8000-000000000002") {
		t.Fatalf("actor=%+v err=%v", actor, err)
	}
	if _, err := authority.Authorize(ctx, actor, "10000000-0000-4000-8000-000000000001", accessRequirement("finance", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Authorize(ctx, actor, "10000000-0000-4000-8000-000000000001", accessRequirement("work", false)); err == nil {
		t.Fatal("mismatched package was authorized")
	}
	if _, err := authority.Authenticate(ctx, "customer-token"); err == nil {
		t.Fatal("external credential was accepted")
	}
}

func accessRequirement(packageCode string, mutation bool) access.Requirement {
	return access.Requirement{Package: catalog.PackageCode(packageCode), Mutation: mutation}
}

type fixedMCPClock struct{ now time.Time }

func (c fixedMCPClock) Now() time.Time { return c.now }
