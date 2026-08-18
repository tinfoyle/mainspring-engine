package observability

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const traceAccount = "10000000-0000-4000-8000-000000000001"

func TestAccountReferenceIsStableKeyedAndDomainSeparated(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	first := accountReference(key, traceAccount)
	if len(first) != 32 || first != accountReference(key, traceAccount) || first == accountReference(key, "10000000-0000-4000-8000-000000000002") || strings.Contains(first, traceAccount) {
		t.Fatalf("unsafe Account reference=%q", first)
	}
}

func TestTracingUsesRemoteParentAndPseudonymousAccountRoute(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	tracing := newTracing(provider, propagation.TraceContext{}, []byte("0123456789abcdef0123456789abcdef"), "cell-us-east-01", nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/work-items/{itemID}", func(w http.ResponseWriter, r *http.Request) {
		if !trace.SpanFromContext(r.Context()).SpanContext().IsValid() {
			t.Error("handler did not receive trace context")
		}
		w.WriteHeader(http.StatusNotFound)
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+traceAccount+"/work-items/20000000-0000-4000-8000-000000000002?customer=secret", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request.Header.Set("tracestate", "customer=secret")
	response := httptest.NewRecorder()
	tracing.Handler(mux).ServeHTTP(response, request)

	spans := recorder.Ended()
	if response.Code != http.StatusNotFound || len(spans) != 1 {
		t.Fatalf("response=%d span_count=%d", response.Code, len(spans))
	}
	if spans[0].Name() != "GET /api/v1/accounts/{accountID}/work-items/{itemID}" || spans[0].Parent().SpanID().String() != "00f067aa0ba902b7" || spans[0].SpanContext().TraceState().String() != "" {
		t.Fatalf("span name=%q parent=%s attributes=%v", spans[0].Name(), spans[0].Parent().SpanID(), spans[0].Attributes())
	}
	attributes := spanAttributes(spans[0].Attributes())
	if attributes["http.request.method"] != "GET" || attributes["http.response.status_code"] != "404" || attributes["spyglass.cell.id"] != "cell-us-east-01" || len(attributes["spyglass.account.ref"]) != 32 {
		t.Fatalf("trace attributes=%v", attributes)
	}
	serialized := spans[0].Name() + attributes["http.route"] + attributes["spyglass.account.ref"]
	for _, forbidden := range []string{traceAccount, "20000000-0000-4000-8000-000000000002", "customer", "secret"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("trace leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestRemoteSampleFlagCannotOverrideConfiguredRatio(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(boundedParentSampler(math.SmallestNonzeroFloat64)), sdktrace.WithSpanProcessor(recorder))
	tracing := newTracing(provider, propagation.TraceContext{}, []byte("0123456789abcdef0123456789abcdef"), "", nil)
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	request.Header.Set("traceparent", "00-ffffffffffffffffffffffffffffffff-00f067aa0ba902b7-01")
	tracing.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(httptest.NewRecorder(), request)
	if len(recorder.Ended()) != 0 {
		t.Fatalf("remote sampled flag forced %d spans", len(recorder.Ended()))
	}
}

func TestTraceTransportInjectsOnlyTraceContextAndDoesNotMutateRequest(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	tracing := newTracing(provider, propagation.TraceContext{}, []byte("0123456789abcdef0123456789abcdef"), "", nil)
	base := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("traceparent") == "" || request.Header.Get("baggage") != "" {
			t.Fatalf("outbound propagation headers=%v", request.Header)
		}
		return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: http.NoBody}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "https://provider.example/customer/secret", nil)
	request.Header.Set("baggage", "customer=secret")
	response, err := tracing.Transport(base).RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusBadGateway || request.Header.Get("traceparent") != "" || request.Header.Get("baggage") != "customer=secret" {
		t.Fatalf("response=%v error=%v original_headers=%v", response, err, request.Header)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "HTTP POST" || spans[0].Status().Code != codes.Error {
		t.Fatalf("client spans=%+v", spans)
	}
	for key, value := range spanAttributes(spans[0].Attributes()) {
		if strings.Contains(key+value, "provider.example") || strings.Contains(key+value, "customer") || strings.Contains(key+value, "secret") {
			t.Fatalf("client trace leaked target: %s=%s", key, value)
		}
	}
}

func TestExternalTraceTransportStripsPropagation(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	tracing := newTracing(provider, propagation.TraceContext{}, []byte("0123456789abcdef0123456789abcdef"), "", nil)
	base := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("traceparent") != "" || request.Header.Get("tracestate") != "" || request.Header.Get("baggage") != "" {
			t.Fatalf("external provider received propagation headers=%v", request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "https://provider.example/v1", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request.Header.Set("tracestate", "vendor=value")
	request.Header.Set("baggage", "customer=secret")
	if _, err := tracing.ExternalTransport(base).RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("traceparent") == "" || request.Header.Get("tracestate") == "" || request.Header.Get("baggage") == "" || len(recorder.Ended()) != 1 {
		t.Fatalf("caller request mutated or client span missing: headers=%v spans=%d", request.Header, len(recorder.Ended()))
	}
}

func TestTracingDoesNotTraceMetricsScrapes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	tracing := newTracing(provider, propagation.TraceContext{}, []byte("0123456789abcdef0123456789abcdef"), "", nil)
	response := httptest.NewRecorder()
	tracing.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || len(recorder.Ended()) != 0 {
		t.Fatalf("metrics response=%d spans=%d", response.Code, len(recorder.Ended()))
	}
}

func TestTracingPreservesRoutePatternsForMetrics(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	tracing := newTracing(provider, propagation.TraceContext{}, []byte("0123456789abcdef0123456789abcdef"), "", nil)
	metrics, _ := NewHTTPMetrics("app-api")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/{accountID}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := tracing.Handler(metrics.Handler(mux))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+traceAccount, nil))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !bytes.Contains(response.Body.Bytes(), []byte(`route="GET /api/v1/accounts/{accountID}"`)) || len(recorder.Ended()) != 1 {
		t.Fatalf("metrics=%s spans=%d", response.Body.String(), len(recorder.Ended()))
	}
}

func TestTracingConfigFailsClosed(t *testing.T) {
	valid := TracingConfig{Service: "app-router", Environment: "staging-us-east", Revision: strings.Repeat("a", 40), CellID: "cell-us-east-01", Endpoint: "https://otel.internal.example/v1/traces", SampleRatio: 0.1, AccountHashKey: []byte("0123456789abcdef0123456789abcdef"), HTTPClient: &http.Client{Transport: http.DefaultTransport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return context.Canceled }}}
	if err := validateTracingConfig(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*TracingConfig)
	}{
		{"raw path", func(value *TracingConfig) { value.Endpoint = "https://otel.internal.example/customer" }},
		{"encoded path", func(value *TracingConfig) { value.Endpoint = "https://otel.internal.example/v1/%74races" }},
		{"insecure endpoint", func(value *TracingConfig) { value.Endpoint = "http://otel.internal.example/v1/traces" }},
		{"endpoint query", func(value *TracingConfig) { value.Endpoint += "?token=secret" }},
		{"empty endpoint query", func(value *TracingConfig) { value.Endpoint += "?" }},
		{"unbounded sampling", func(value *TracingConfig) { value.SampleRatio = 1.1 }},
		{"short Account key", func(value *TracingConfig) { value.AccountHashKey = []byte("short") }},
		{"unbounded client", func(value *TracingConfig) { value.HTTPClient = http.DefaultClient }},
		{"unknown revision", func(value *TracingConfig) { value.Revision = "unknown" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate)
			if validateTracingConfig(candidate) == nil {
				t.Fatal("invalid tracing configuration was accepted")
			}
		})
	}
}

func TestOTLPExporterFlushesContentFreeTrace(t *testing.T) {
	payloads := make(chan []byte, 1)
	collector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/traces" || r.Header.Get("Content-Type") != "application/x-protobuf" || r.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("collector request method=%s path=%s headers=%v", r.Method, r.URL.Path, r.Header)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		compressed, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, err := io.ReadAll(compressed)
		_ = compressed.Close()
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		payloads <- raw
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	client := collector.Client()
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return context.Canceled }
	tracing, err := NewTracing(context.Background(), TracingConfig{Service: "app-api", Environment: "staging-us-east", Revision: strings.Repeat("a", 40), CellID: "cell-us-east-01", Endpoint: collector.URL + "/v1/traces", SampleRatio: 1, AccountHashKey: []byte("0123456789abcdef0123456789abcdef"), HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/{accountID}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+traceAccount+"?customer=secret", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request.Header.Set("tracestate", "customer=secret")
	tracing.Handler(mux).ServeHTTP(httptest.NewRecorder(), request)
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tracing.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-payloads:
		if !bytes.Contains(raw, []byte("GET /api/v1/accounts/{accountID}")) || bytes.Contains(raw, []byte(traceAccount)) || bytes.Contains(raw, []byte("customer")) || bytes.Contains(raw, []byte("secret")) {
			t.Fatalf("unsafe OTLP payload=%q", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("trace payload was not exported")
	}
}

func spanAttributes(values []attribute.KeyValue) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		result[string(value.Key)] = value.Value.Emit()
	}
	return result
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
