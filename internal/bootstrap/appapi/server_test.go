package appapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/admissionhttp"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
)

func TestHealthStatusExposesOnlyAggregateRouteCounters(t *testing.T) {
	routes, err := cellapi.New(rejectedRoute{}, slog.New(slog.NewTextHandler(io.Discard, nil)), cellapi.DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/20000000-0000-4000-8000-000000000002/context", nil)
	response := httptest.NewRecorder()
	routes.Handler().ServeHTTP(response, request)

	request = httptest.NewRequest(http.MethodGet, "/health/status", nil)
	response = httptest.NewRecorder()
	admission, _ := admissionhttp.New("https://admission.test", false, nil)
	withHealth(nil, routes, admission, http.NotFoundHandler()).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"missing_context":1`) || !strings.Contains(response.Body.String(), `"admission_transport":{"retry_attempts":0`) || strings.Contains(response.Body.String(), "20000000-0000-4000-8000-000000000002") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type rejectedRoute struct{}

func (rejectedRoute) Accept(context.Context, string, routecontext.Binding) (routecontext.Claims, error) {
	return routecontext.Claims{}, routecontext.ErrInvalid
}
