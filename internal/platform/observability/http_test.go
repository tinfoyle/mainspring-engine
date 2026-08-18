package observability_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/observability"
)

func TestHTTPMetricsUseRoutePatternsAndBoundedLabels(t *testing.T) {
	metrics, err := observability.NewHTTPMetrics("account-api")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/{accountID}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	handler := metrics.Handler(mux)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/accounts/10000000-0000-4000-8000-000000000001", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("CUSTOM-CUSTOMER-METHOD", "/invented/customer/path", nil))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("10000000-0000-4000-8000-000000000001")) || bytes.Contains(response.Body.Bytes(), []byte("CUSTOM-CUSTOMER-METHOD")) || bytes.Contains(response.Body.Bytes(), []byte("invented/customer")) {
		t.Fatalf("metrics leaked unbounded request data: %d %s", response.Code, response.Body.String())
	}
	for _, expected := range [][]byte{[]byte(`route="GET /api/v1/accounts/{accountID}"`), []byte(`method="OTHER",route="unmatched"`), []byte(`status_class="4xx"`)} {
		if !bytes.Contains(response.Body.Bytes(), expected) {
			t.Fatalf("metrics missing %s: %s", expected, response.Body.String())
		}
	}
}

func TestHTTPMetricsExposeCumulativeDurationHistogram(t *testing.T) {
	metrics, err := observability.NewHTTPMetrics("app-api")
	if err != nil {
		t.Fatal(err)
	}
	handler := metrics.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(12 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/work", nil))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	output := response.Body.String()
	for _, expected := range []string{
		"# TYPE spyglass_http_request_duration_seconds histogram",
		`spyglass_http_request_duration_seconds_bucket{service="app-api",method="GET",route="unmatched",status_class="2xx",le="0.01"} 0`,
		`spyglass_http_request_duration_seconds_bucket{service="app-api",method="GET",route="unmatched",status_class="2xx",le="+Inf"} 1`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("histogram missing %q:\n%s", expected, output)
		}
	}
}

func TestHTTPMetricsRejectDynamicServiceLabel(t *testing.T) {
	if _, err := observability.NewHTTPMetrics("account/customer"); err == nil {
		t.Fatal("dynamic service label was accepted")
	}
}
