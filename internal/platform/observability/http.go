package observability

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type httpMetricKey struct {
	Method, Route, StatusClass string
}

type httpMetricValue struct {
	count      atomic.Uint64
	durationNS atomic.Uint64
	buckets    [len(httpDurationBuckets)]atomic.Uint64
}

var httpDurationBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type HTTPMetrics struct {
	service  string
	inFlight atomic.Int64
	values   sync.Map
}

func NewHTTPMetrics(service string) (*HTTPMetrics, error) {
	if !metricLabel.MatchString(service) {
		return nil, fmt.Errorf("HTTP metric service %q is invalid", service)
	}
	return &HTTPMetrics{service: service}, nil
}

func (m *HTTPMetrics) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/metrics" {
			m.serve(w)
			return
		}
		started := time.Now()
		m.inFlight.Add(1)
		writer := &metricResponseWriter{ResponseWriter: w}
		defer func() {
			m.inFlight.Add(-1)
			status := writer.status
			if recovered := recover(); recovered != nil {
				if status == 0 {
					status = http.StatusInternalServerError
				}
				m.observe(r, status, time.Since(started))
				panic(recovered)
			}
			if status == 0 {
				status = http.StatusOK
			}
			m.observe(r, status, time.Since(started))
		}()
		next.ServeHTTP(writer, r)
	})
}

func (m *HTTPMetrics) observe(r *http.Request, status int, duration time.Duration) {
	key := httpMetricKey{Method: boundedMethod(r.Method), Route: boundedRoute(r.Pattern), StatusClass: fmt.Sprintf("%dxx", status/100)}
	loaded, _ := m.values.LoadOrStore(key, &httpMetricValue{})
	value := loaded.(*httpMetricValue)
	value.count.Add(1)
	if duration > 0 {
		value.durationNS.Add(uint64(duration))
	}
	seconds := duration.Seconds()
	for index, upperBound := range httpDurationBuckets {
		if seconds <= upperBound {
			value.buckets[index].Add(1)
		}
	}
}

func (m *HTTPMetrics) serve(w http.ResponseWriter) {
	type snapshot struct {
		key                  httpMetricKey
		count, durationNanos uint64
		buckets              [len(httpDurationBuckets)]uint64
	}
	values := make([]snapshot, 0)
	m.values.Range(func(rawKey, rawValue any) bool {
		key, value := rawKey.(httpMetricKey), rawValue.(*httpMetricValue)
		current := snapshot{key: key, count: value.count.Load(), durationNanos: value.durationNS.Load()}
		for index := range value.buckets {
			current.buckets[index] = value.buckets[index].Load()
		}
		values = append(values, current)
		return true
	})
	sort.Slice(values, func(i, j int) bool {
		left, right := values[i].key, values[j].key
		return left.Method+left.Route+left.StatusClass < right.Method+right.Route+right.StatusClass
	})
	var output bytes.Buffer
	output.WriteString("# HELP spyglass_http_in_flight Requests currently executing in this process.\n# TYPE spyglass_http_in_flight gauge\n")
	fmt.Fprintf(&output, "spyglass_http_in_flight{service=%q} %d\n", m.service, m.inFlight.Load())
	output.WriteString("# HELP spyglass_http_requests_total Completed HTTP requests.\n# TYPE spyglass_http_requests_total counter\n")
	output.WriteString("# HELP spyglass_http_request_duration_seconds Request duration without customer-derived labels.\n# TYPE spyglass_http_request_duration_seconds histogram\n")
	for _, value := range values {
		labels := fmt.Sprintf("service=%q,method=%q,route=%q,status_class=%q", m.service, value.key.Method, value.key.Route, value.key.StatusClass)
		fmt.Fprintf(&output, "spyglass_http_requests_total{%s} %d\n", labels, value.count)
		for index, upperBound := range httpDurationBuckets {
			fmt.Fprintf(&output, "spyglass_http_request_duration_seconds_bucket{%s,le=%q} %d\n", labels, strconv.FormatFloat(upperBound, 'g', -1, 64), value.buckets[index])
		}
		fmt.Fprintf(&output, "spyglass_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, value.count)
		fmt.Fprintf(&output, "spyglass_http_request_duration_seconds_sum{%s} %.9f\n", labels, float64(value.durationNanos)/float64(time.Second))
		fmt.Fprintf(&output, "spyglass_http_request_duration_seconds_count{%s} %d\n", labels, value.count)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(output.Bytes())
}

func boundedMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func boundedRoute(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || len(pattern) > 160 {
		return "unmatched"
	}
	for _, value := range pattern {
		if !(value == ' ' || value == '/' || value == '{' || value == '}' || value == ':' || value == '-' || value == '_' || value == '.' || value == '$' || value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z') {
			return "unmatched"
		}
	}
	return pattern
}

type metricResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *metricResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *metricResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
