package runnerbrokerapi

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
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
)

type fakeExchange struct {
	request   runnerbroker.Request
	result    runnerbroker.Result
	token, id string
	created   bool
	err       error
}

func (f *fakeExchange) Fetch(_ context.Context, token, id string) (runnerbroker.Request, error) {
	f.token, f.id = token, id
	return f.request, f.err
}
func (f *fakeExchange) Submit(_ context.Context, token, id string, result runnerbroker.Result) (bool, error) {
	f.token, f.id, f.result = token, id, result
	return f.created, f.err
}

func testServer(t *testing.T, exchange *fakeExchange) http.Handler {
	t.Helper()
	server, err := New(exchange, slog.New(slog.NewJSONHandler(io.Discard, nil)), DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func TestFetchReturnsOnlyVerifiedExchangeWithNoStoreHeaders(t *testing.T) {
	exchange := &fakeExchange{request: runnerbroker.Request{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{"prompt":"private"}`), Capabilities: []string{"work:read"}, ExpiresAt: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)}}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/runner/invocations/11000000-0000-4000-8000-000000000001/request", nil)
	request.Header.Set("Authorization", "Bearer reviewed-token")
	recorder := httptest.NewRecorder()
	testServer(t, exchange).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || exchange.token != "reviewed-token" || exchange.id != "11000000-0000-4000-8000-000000000001" {
		t.Fatalf("fetch status=%d token=%q id=%q body=%s", recorder.Code, exchange.token, exchange.id, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("Pragma") != "no-cache" || !bytes.Contains(recorder.Body.Bytes(), []byte("private")) {
		t.Fatalf("fetch headers=%v body=%s", recorder.Header(), recorder.Body.String())
	}
}

func TestSubmitIsBoundedStrictAndIdempotent(t *testing.T) {
	exchange := &fakeExchange{created: true}
	body := `{"schema_version":1,"outcome":"completed","output":{"summary":"done"}}`
	request := httptest.NewRequest(http.MethodPut, "/internal/v1/runner/invocations/11000000-0000-4000-8000-000000000001/result", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer reviewed-token")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	recorder := httptest.NewRecorder()
	testServer(t, exchange).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || exchange.result.Outcome != "completed" || !bytes.Contains(exchange.result.Output, []byte("done")) || !bytes.Contains(recorder.Body.Bytes(), []byte(`"newly_created":true`)) {
		t.Fatalf("submit status=%d result=%+v body=%s", recorder.Code, exchange.result, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/internal/v1/runner/invocations/11000000-0000-4000-8000-000000000001/result", bytes.NewBufferString(`{"schema_version":1,"outcome":"completed","output":{},"unknown":true}`))
	request.Header.Set("Authorization", "Bearer reviewed-token")
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	testServer(t, exchange).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown result field status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSubmitRejectsOversizedBodyBeforeExchange(t *testing.T) {
	exchange := &fakeExchange{}
	server, err := New(exchange, slog.New(slog.NewJSONHandler(io.Discard, nil)), 64)
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte(`{"schema_version":1,"outcome":"completed","output":{"v":"`), bytes.Repeat([]byte{'x'}, 65)...)
	body = append(body, []byte(`"}}`)...)
	request := httptest.NewRequest(http.MethodPut, "/internal/v1/runner/invocations/11000000-0000-4000-8000-000000000001/result", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer reviewed-token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge || exchange.token != "" || !bytes.Contains(recorder.Body.Bytes(), []byte("runner_result_too_large")) {
		t.Fatalf("oversized submit status=%d token=%q body=%s", recorder.Code, exchange.token, recorder.Body.String())
	}
}

func TestTransportDeniesMalformedCredentialsBeforeExchange(t *testing.T) {
	for _, authorization := range []string{"", "Basic abc", "Bearer ", "Bearer token with spaces"} {
		exchange := &fakeExchange{}
		request := httptest.NewRequest(http.MethodPost, "/internal/v1/runner/invocations/11000000-0000-4000-8000-000000000001/request", nil)
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		recorder := httptest.NewRecorder()
		testServer(t, exchange).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized || exchange.token != "" || !bytes.Contains(recorder.Body.Bytes(), []byte("runner_identity_denied")) {
			t.Errorf("authorization=%q status=%d token=%q body=%s", authorization, recorder.Code, exchange.token, recorder.Body.String())
		}
	}
}

func TestTransportMapsExchangeErrorsWithoutDetails(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{runnerbroker.ErrIdentityDenied, http.StatusUnauthorized, "runner_identity_denied"},
		{runnerbroker.ErrInvalidExchange, http.StatusBadRequest, "runner_exchange_invalid"},
		{runnerbroker.ErrExchangeNotReady, http.StatusConflict, "runner_exchange_not_ready"},
		{runnerbroker.ErrExchangeCanceled, http.StatusGone, "runner_exchange_canceled"},
		{runnerbroker.ErrExchangeExpired, http.StatusGone, "runner_exchange_expired"},
		{runnerbroker.ErrExchangeConflict, http.StatusConflict, "runner_exchange_conflict"},
		{errors.New("database secret detail"), http.StatusServiceUnavailable, "runner_broker_unavailable"},
	}
	for _, test := range tests {
		exchange := &fakeExchange{err: test.err}
		request := httptest.NewRequest(http.MethodPost, "/internal/v1/runner/invocations/11000000-0000-4000-8000-000000000001/request", nil)
		request.Header.Set("Authorization", "Bearer reviewed-token")
		recorder := httptest.NewRecorder()
		testServer(t, exchange).ServeHTTP(recorder, request)
		if recorder.Code != test.status || !bytes.Contains(recorder.Body.Bytes(), []byte(test.code)) || bytes.Contains(recorder.Body.Bytes(), []byte("database secret detail")) {
			t.Errorf("err=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
	}
}
