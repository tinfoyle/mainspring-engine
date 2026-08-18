package observability

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const instrumentationName = "github.com/tinfoyle/spyglass-engine/internal/platform/observability"

var (
	releaseRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)
	cellLabel       = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
	disabledTracing = &Tracing{}
)

type TracingConfig struct {
	Service        string
	Environment    string
	Revision       string
	CellID         string
	Endpoint       string
	SampleRatio    float64
	AccountHashKey []byte
	HTTPClient     *http.Client
}

// Tracing owns one process-wide OTLP trace provider. It deliberately uses only
// W3C trace context; baggage is neither extracted nor injected because it is an
// uncontrolled customer-data channel.
type Tracing struct {
	enabled    bool
	provider   *sdktrace.TracerProvider
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	accountKey []byte
	cellID     string
	httpClient *http.Client
}

func DisabledTracing() *Tracing { return disabledTracing }

func NewTracing(ctx context.Context, config TracingConfig) (*Tracing, error) {
	if err := validateTracingConfig(config); err != nil {
		return nil, err
	}
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(config.Endpoint),
		otlptracehttp.WithHTTPClient(config.HTTPClient),
		otlptracehttp.WithHeaders(map[string]string{}),
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
		otlptracehttp.WithMaxRequestSize(1<<20),
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: true, InitialInterval: time.Second, MaxInterval: 2 * time.Second, MaxElapsedTime: 10 * time.Second}),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize OTLP trace exporter: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(boundedParentSampler(config.SampleRatio)),
		sdktrace.WithResource(resource.NewWithAttributes("",
			attribute.String("service.name", config.Service),
			attribute.String("deployment.environment.name", config.Environment),
			attribute.String("service.version", config.Revision),
			attribute.String("spyglass.cell.id", staticCell(config.CellID)),
		)),
		sdktrace.WithBatcher(exporter,
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithMaxExportBatchSize(256),
			sdktrace.WithBatchTimeout(2*time.Second),
			sdktrace.WithExportTimeout(5*time.Second),
		),
	)
	return newTracing(provider, propagation.TraceContext{}, config.AccountHashKey, config.CellID, config.HTTPClient), nil
}

func boundedParentSampler(ratio float64) sdktrace.Sampler {
	root := sdktrace.TraceIDRatioBased(ratio)
	return sdktrace.ParentBased(root, sdktrace.WithRemoteParentSampled(root), sdktrace.WithRemoteParentNotSampled(root))
}

func validateTracingConfig(config TracingConfig) error {
	if !metricLabel.MatchString(config.Service) || !metricLabel.MatchString(config.Environment) || !releaseRevision.MatchString(config.Revision) {
		return errors.New("trace service, environment, or release revision is invalid")
	}
	if config.CellID != "" && !cellLabel.MatchString(config.CellID) {
		return errors.New("trace cell identity is invalid")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Opaque != "" || endpoint.Path != "/v1/traces" || endpoint.RawPath != "" || endpoint.ForceQuery || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.String() != config.Endpoint {
		return errors.New("trace endpoint must be an exact HTTPS /v1/traces URL")
	}
	if config.SampleRatio <= 0 || config.SampleRatio > 1 || math.IsNaN(config.SampleRatio) || math.IsInf(config.SampleRatio, 0) {
		return errors.New("trace sample ratio must be greater than zero and at most one")
	}
	if len(config.AccountHashKey) != 32 {
		return errors.New("trace Account hash key must contain exactly 32 bytes")
	}
	if config.HTTPClient == nil || config.HTTPClient.Transport == nil || config.HTTPClient.Timeout <= 0 || config.HTTPClient.Timeout > 10*time.Second {
		return errors.New("trace exporter requires a bounded explicit HTTP client")
	}
	if config.HTTPClient.CheckRedirect == nil {
		return errors.New("trace exporter must reject redirects")
	}
	return nil
}

func newTracing(provider *sdktrace.TracerProvider, propagator propagation.TextMapPropagator, accountKey []byte, cellID string, client *http.Client) *Tracing {
	return &Tracing{enabled: true, provider: provider, tracer: provider.Tracer(instrumentationName), propagator: propagator, accountKey: append([]byte(nil), accountKey...), cellID: cellID, httpClient: client}
}

type tracingContextKey struct{}

func WithTracing(ctx context.Context, tracing *Tracing) context.Context {
	if tracing == nil {
		tracing = disabledTracing
	}
	return context.WithValue(ctx, tracingContextKey{}, tracing)
}

func TracingFromContext(ctx context.Context) *Tracing {
	if tracing, ok := ctx.Value(tracingContextKey{}).(*Tracing); ok && tracing != nil {
		return tracing
	}
	return disabledTracing
}

func (t *Tracing) Handler(next http.Handler) http.Handler {
	if t == nil || !t.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		parent := t.propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		if spanContext := trace.SpanContextFromContext(parent); spanContext.IsValid() {
			parent = trace.ContextWithRemoteSpanContext(parent, spanContext.WithTraceState(trace.TraceState{}))
		}
		ctx, span := t.tracer.Start(parent, "HTTP "+boundedMethod(r.Method), trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attribute.String("http.request.method", boundedMethod(r.Method))))
		writer := &traceResponseWriter{ResponseWriter: w}
		tracedRequest := r.WithContext(ctx)
		defer func() {
			recovered := recover()
			status := writer.status
			if recovered != nil && status == 0 {
				status = http.StatusInternalServerError
			}
			if status == 0 {
				status = http.StatusOK
			}
			route := boundedRoute(tracedRequest.Pattern)
			span.SetName(route)
			span.SetAttributes(attribute.String("http.route", route), attribute.Int("http.response.status_code", status))
			if accountID := tracedRequest.PathValue("accountID"); ids.Validate(accountID) == nil {
				span.SetAttributes(attribute.String("spyglass.account.ref", accountReference(t.accountKey, accountID)))
			}
			if t.cellID != "" {
				span.SetAttributes(attribute.String("spyglass.cell.id", t.cellID))
			}
			if recovered != nil || status >= 500 {
				span.SetStatus(codes.Error, "request failed")
			}
			span.End()
			if recovered != nil {
				panic(recovered)
			}
		}()
		next.ServeHTTP(writer, tracedRequest)
	})
}

func (t *Tracing) Transport(base http.RoundTripper) http.RoundTripper {
	return t.transport(base, true)
}

// ExternalTransport creates a content-free client span but never discloses
// Spyglass trace context to a third-party provider.
func (t *Tracing) ExternalTransport(base http.RoundTripper) http.RoundTripper {
	return t.transport(base, false)
}

func (t *Tracing) transport(base http.RoundTripper, propagate bool) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if t == nil || !t.enabled {
		return base
	}
	if wrapped, ok := base.(*traceTransport); ok {
		if wrapped.propagate == propagate {
			return base
		}
		base = wrapped.base
	}
	return &traceTransport{base: base, tracer: t.tracer, propagator: t.propagator, propagate: propagate}
}

func (t *Tracing) Shutdown(ctx context.Context) error {
	if t == nil || !t.enabled {
		return nil
	}
	err := t.provider.Shutdown(ctx)
	if t.httpClient != nil {
		t.httpClient.CloseIdleConnections()
	}
	return err
}

type traceTransport struct {
	base       http.RoundTripper
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	propagate  bool
}

func (t *traceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	method := boundedMethod(request.Method)
	ctx, span := t.tracer.Start(request.Context(), "HTTP "+method, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("http.request.method", method)))
	defer span.End()
	clone := request.Clone(ctx)
	clone.Header = request.Header.Clone()
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	clone.Header.Del("baggage")
	clone.Header.Del("traceparent")
	clone.Header.Del("tracestate")
	if t.propagate {
		t.propagator.Inject(ctx, propagation.HeaderCarrier(clone.Header))
	}
	response, err := t.base.RoundTrip(clone)
	if err != nil {
		span.SetStatus(codes.Error, "transport failed")
		return nil, err
	}
	if response == nil {
		span.SetStatus(codes.Error, "transport failed")
		return nil, errors.New("traced transport returned no response")
	}
	span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
	if response.StatusCode >= 500 {
		span.SetStatus(codes.Error, "request failed")
	}
	return response, nil
}

func accountReference(key []byte, accountID string) string {
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte("spyglass/trace/account/v1\x00" + accountID))
	return hex.EncodeToString(digest.Sum(nil)[:16])
}

func staticCell(value string) string {
	if strings.TrimSpace(value) == "" {
		return "global"
	}
	return value
}

type traceResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *traceResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *traceResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *traceResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

var _ http.RoundTripper = (*traceTransport)(nil)
var _ interface{ Unwrap() http.ResponseWriter } = (*traceResponseWriter)(nil)
