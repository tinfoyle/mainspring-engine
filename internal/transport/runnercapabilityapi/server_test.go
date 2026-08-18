package runnercapabilityapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
)

type fakeGateway struct {
	token, invocation string
	call              runnercapability.Call
	result            runnercapability.Result
	err               error
}

func (g *fakeGateway) Invoke(_ context.Context, token, invocation string, call runnercapability.Call) (runnercapability.Result, error) {
	g.token, g.invocation, g.call = token, invocation, call
	return g.result, g.err
}

func capabilityHandler(t *testing.T, gateway *fakeGateway) http.Handler {
	t.Helper()
	server, err := New(gateway, slog.New(slog.NewJSONHandler(io.Discard, nil)), DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func TestGatewayTransportInvokesBoundedCapability(t *testing.T) {
	gateway := &fakeGateway{result: runnercapability.Result{SchemaVersion: 1, Output: json.RawMessage(`{"items":[]}`)}}
	body := `{"schema_version":1,"operation_id":"41000000-0000-4000-8000-000000000001","capability":"work:read","input":{}}`
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/runner/invocations/11000000-0000-4000-8000-000000000001/capabilities:invoke", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer pod-token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	capabilityHandler(t, gateway).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || gateway.token != "pod-token" || gateway.call.Capability != "work:read" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d token=%q call=%+v headers=%v body=%s", recorder.Code, gateway.token, gateway.call, recorder.Header(), recorder.Body.String())
	}
}

func TestGatewayTransportMapsCancellationAndDenialWithoutDetails(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{{runnerbroker.ErrExchangeCanceled, http.StatusGone, "runner_exchange_canceled"}, {runnerbroker.ErrCapabilityDenied, http.StatusForbidden, "capability_denied"}, {runnercapability.ErrAuditUnavailable, http.StatusServiceUnavailable, "capability_audit_unavailable"}, {errors.New("private backend detail"), http.StatusServiceUnavailable, "capability_gateway_unavailable"}} {
		gateway := &fakeGateway{err: test.err}
		body := `{"schema_version":1,"operation_id":"41000000-0000-4000-8000-000000000001","capability":"work:read","input":{}}`
		request := httptest.NewRequest(http.MethodPost, "/internal/v1/runner/invocations/11000000-0000-4000-8000-000000000001/capabilities:invoke", bytes.NewBufferString(body))
		request.Header.Set("Authorization", "Bearer pod-token")
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		capabilityHandler(t, gateway).ServeHTTP(recorder, request)
		if recorder.Code != test.status || !bytes.Contains(recorder.Body.Bytes(), []byte(test.code)) || bytes.Contains(recorder.Body.Bytes(), []byte("private backend detail")) {
			t.Errorf("err=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
	}
}
