package cellapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

func TestRouteBoundaryClassifiesContentFreeOperationalCounters(t *testing.T) {
	tests := []struct {
		name, code string
		err        error
		status     int
		counter    func(RouteStats) uint64
	}{
		{"replay", "route_replay", routecontext.ErrReplay, http.StatusConflict, func(value RouteStats) uint64 { return value.ReplayDenied }},
		{"stale", "stale_route", routecontext.ErrPlacement, http.StatusConflict, func(value RouteStats) uint64 { return value.StalePlacementDenied }},
		{"unavailable", "account_unavailable", routecontext.ErrUnavailable, http.StatusServiceUnavailable, func(value RouteStats) uint64 { return value.AccountUnavailable }},
		{"receipt store", "route_boundary_unavailable", routecontext.ErrReceiptStore, http.StatusServiceUnavailable, func(value RouteStats) uint64 { return value.ReceiptStoreUnavailable }},
		{"invalid", "invalid_route_context", routecontext.ErrSignature, http.StatusUnauthorized, func(value RouteStats) uint64 { return value.VerificationDenied }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := New(statusAcceptor{err: test.err}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody)
			response := contextRequest(server, true)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) || test.counter(server.Stats()) != 1 {
				t.Fatalf("response=%d %s stats=%+v", response.Code, response.Body.String(), server.Stats())
			}
		})
	}
}

func TestRouteBoundaryCountsMissingAndAcceptedContext(t *testing.T) {
	claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(workAccount)}}
	server, _ := New(statusAcceptor{claims: claims}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody)
	if response := contextRequest(server, false); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing status=%d", response.Code)
	}
	if response := contextRequest(server, true); response.Code != http.StatusOK {
		t.Fatalf("accepted status=%d body=%s", response.Code, response.Body.String())
	}
	stats := server.Stats()
	if stats.MissingContext != 1 || stats.Accepted != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func contextRequest(server *Server, includeToken bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+workAccount+"/context", nil)
	if includeToken {
		request.Header.Set(RouteContextHeader, "route-proof")
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

type statusAcceptor struct {
	claims routecontext.Claims
	err    error
}

func (a statusAcceptor) Accept(context.Context, string, routecontext.Binding) (routecontext.Claims, error) {
	return a.claims, a.err
}
