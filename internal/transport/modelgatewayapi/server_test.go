package modelgatewayapi

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

	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
)

func TestInvokeUsesPrivateTypedBoundary(t *testing.T) {
	gateway := &gatewayStub{result: modelgateway.Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test", ResponseID: "resp_1", StopReason: "completed", Output: json.RawMessage(`{"answer":"ok"}`)}}
	server, err := New(gateway, slog.New(slog.NewTextHandler(io.Discard, nil)), 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(validRequest())
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/model-turns:invoke", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || gateway.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, gateway.calls, response.Body.String())
	}
}

func TestInvokeMapsProviderFailureWithoutDetail(t *testing.T) {
	gateway := &gatewayStub{err: errors.New("wrapper: " + modelgateway.ErrProviderUnavailable.Error())}
	// errors.New does not preserve identity and must fall through to the generic,
	// non-sensitive response.
	server, _ := New(gateway, slog.New(slog.NewTextHandler(io.Discard, nil)), 64<<10)
	raw, _ := json.Marshal(validRequest())
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/model-turns:invoke", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || bytes.Contains(response.Body.Bytes(), []byte("wrapper")) {
		t.Fatalf("unsafe response %d %s", response.Code, response.Body.String())
	}
}

func validRequest() modelgateway.Request {
	return modelgateway.Request{SchemaVersion: 1, InvocationID: "10000000-0000-4000-8000-000000000001", OperationID: "20000000-0000-4000-8000-000000000002", Provider: "openai", Model: "gpt-test", Instructions: "Help.", Messages: []modelgateway.Message{{Role: "user", Content: "Status?"}}, OutputFormat: modelgateway.OutputFormat{Name: "result", Schema: json.RawMessage(`{"type":"object"}`)}, MaximumOutTokens: 100}
}

type gatewayStub struct {
	result modelgateway.Result
	err    error
	calls  int
}

func (g *gatewayStub) Invoke(_ context.Context, _ modelgateway.Request) (modelgateway.Result, error) {
	g.calls++
	return g.result, g.err
}
