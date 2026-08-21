package stripeaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func actionCall() runnercapability.AuthorizedCall {
	operation := "40000000-0000-4000-8000-000000000004"
	return runnercapability.AuthorizedCall{
		Grant:       runnerbroker.CapabilityGrant{AccountID: ids.AccountID("10000000-0000-4000-8000-000000000001")},
		OperationID: operation, Input: json.RawMessage(`{"email":"owner@example.com","name":"Ocean Owner"}`),
		InputDigest: sha256.Sum256([]byte("input")), Action: &runnercapability.ActionLease{IdempotencyKey: operation},
	}
}

func TestCustomerExecuteUsesStableIdempotencyAndBoundMetadata(t *testing.T) {
	call := actionCall()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/customers" || r.Header.Get("Idempotency-Key") != call.OperationID {
			t.Fatalf("request %s %s idempotency=%q", r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"))
		}
		_ = r.ParseForm()
		if r.Form.Get("metadata[spyglass_account_id]") != string(call.Grant.AccountID) || r.Form.Get("metadata[spyglass_operation_id]") != call.OperationID {
			t.Fatalf("metadata=%v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cus_test","metadata":{"spyglass_account_id":"10000000-0000-4000-8000-000000000001","spyglass_operation_id":"40000000-0000-4000-8000-000000000004"}}`))
	}))
	defer server.Close()
	handler, _ := NewCustomerHandler("sk_test_action_secret", "", server.Client())
	handler.baseURL = server.URL
	output, err := handler.Execute(context.Background(), call)
	if err != nil || !strings.Contains(string(output), "cus_test") {
		t.Fatalf("output=%s err=%v", output, err)
	}
}

func TestCustomerReconcileIsLookupOnlyAndExact(t *testing.T) {
	call := actionCall()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/customers/search" || r.Header.Get("Idempotency-Key") != "" {
			t.Fatalf("reconcile request %s %s", r.Method, r.URL.Path)
		}
		query, _ := url.QueryUnescape(r.URL.Query().Get("query"))
		if !strings.Contains(query, call.OperationID) || !strings.Contains(query, string(call.Grant.AccountID)) {
			t.Fatalf("lookup query=%q", query)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"cus_found","metadata":{"spyglass_account_id":"10000000-0000-4000-8000-000000000001","spyglass_operation_id":"40000000-0000-4000-8000-000000000004"}}]}`))
	}))
	defer server.Close()
	handler, _ := NewCustomerHandler("sk_test_action_secret", "", server.Client())
	handler.baseURL = server.URL
	output, outcome, err := handler.Reconcile(context.Background(), call)
	if err != nil || outcome != runnercapability.ActionSucceeded || !strings.Contains(string(output), "cus_found") {
		t.Fatalf("output=%s outcome=%s err=%v", output, outcome, err)
	}
}

func TestCustomerErrorsClassifyOnlyProvenNoEffectAsDefinitive(t *testing.T) {
	for _, test := range []struct {
		status     int
		code       string
		definitive bool
	}{{http.StatusTooManyRequests, "stripe_rate_limited", true}, {http.StatusBadRequest, "stripe_request_rejected", true}, {http.StatusInternalServerError, "stripe_provider_unknown", false}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(test.status) }))
		handler, _ := NewCustomerHandler("sk_test_action_secret", "", &http.Client{Timeout: time.Second})
		handler.baseURL = server.URL
		_, err := handler.Execute(context.Background(), actionCall())
		server.Close()
		coded, ok := err.(interface{ Code() string })
		definitive, definitiveOK := err.(interface{ Definitive() bool })
		if !ok || coded.Code() != test.code || !definitiveOK || definitive.Definitive() != test.definitive {
			t.Fatalf("status=%d error=%v", test.status, err)
		}
	}
}
