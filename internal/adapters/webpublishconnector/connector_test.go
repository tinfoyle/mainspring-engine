package webpublishconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (function roundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestExecuteUsesExactCreateOnlyResourceAndDigest(t *testing.T) {
	var captured *http.Request
	var body []byte
	connector := testConnector(func(request *http.Request) (*http.Response, error) {
		captured = request.Clone(request.Context())
		body, _ = io.ReadAll(request.Body)
		return response(http.StatusCreated, request.Header.Get("Content-Digest")), nil
	})
	call := webCall(t)
	result := connector.Execute(context.Background(), call)
	if result.Outcome != domain.AttemptSucceeded || result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
	if captured.Method != http.MethodPut || captured.URL.String() != "https://publish.example.com/campaigns/f1500000-0000-4000-8000-000000000005.json" ||
		captured.Header.Get("Authorization") != "Bearer provider-secret" || captured.Header.Get("If-None-Match") != "*" ||
		captured.Header.Get("Idempotency-Key") != string(call.Claim.IdempotencyKey) || !bytes.Equal(body, call.Payload.ProviderPayload) {
		t.Fatalf("request=%s %s headers=%v body=%s", captured.Method, captured.URL, captured.Header, body)
	}
	if strings.Contains(captured.URL.String(), "provider-secret") || bytes.Contains(body, []byte("provider-secret")) {
		t.Fatal("provider credential escaped its header")
	}
}

func TestExecuteClassifiesRejectedAmbiguousAndInvalidResponses(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		digest   string
		want     domain.AttemptOutcome
		wantCode string
	}{
		{name: "rejected", status: http.StatusBadRequest, want: domain.AttemptFailed, wantCode: "web_publish_rejected"},
		{name: "server uncertain", status: http.StatusServiceUnavailable, want: domain.AttemptUnknown, wantCode: "web_publish_uncertain"},
		{name: "conflict uncertain", status: http.StatusPreconditionFailed, want: domain.AttemptUnknown, wantCode: "web_publish_uncertain"},
		{name: "missing digest", status: http.StatusCreated, want: domain.AttemptUnknown, wantCode: "web_publish_response_invalid"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			connector := testConnector(func(request *http.Request) (*http.Response, error) {
				digest := testCase.digest
				if digest == "echo" {
					digest = request.Header.Get("Content-Digest")
				}
				return response(testCase.status, digest), nil
			})
			result := connector.Execute(context.Background(), webCall(t))
			if result.Outcome != testCase.want || result.ErrorCode != testCase.wantCode {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestReconcileIsTheOnlyAutomaticRetryGate(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		status   int
		digest   string
		want     domain.AttemptOutcome
		wantCode string
	}{
		{name: "exact object exists", status: http.StatusOK, digest: "echo", want: domain.AttemptSucceeded},
		{name: "object absent", status: http.StatusNotFound, want: domain.AttemptNotApplied, wantCode: "web_publish_not_applied"},
		{name: "digest mismatch", status: http.StatusOK, digest: "sha-256=:AAAA:", want: domain.AttemptUnknown, wantCode: "web_publish_digest_mismatch"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			connector := testConnector(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodHead {
					t.Fatalf("method=%s", request.Method)
				}
				digest := testCase.digest
				if digest == "echo" {
					digest = request.Header.Get("Content-Digest")
				}
				return response(testCase.status, digest), nil
			})
			result := connector.Reconcile(context.Background(), webCall(t))
			if result.Outcome != testCase.want || result.ErrorCode != testCase.wantCode || (result.Outcome == domain.AttemptNotApplied && result.RetryAt == nil) {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestExecuteRejectsScopeMismatchAndPublicIPGateRejectsInternalTargets(t *testing.T) {
	called := false
	connector := &Connector{clients: func(context.Context, credential) (*http.Client, error) {
		called = true
		return &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return response(http.StatusCreated, ""), nil })}, nil
	}}
	call := webCall(t)
	var envelope integrationexecution.ProviderEnvelope
	if err := json.Unmarshal(call.Payload.ProviderPayload, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Scope.PathPrefix = "/other"
	call.Payload.ProviderPayload, _ = json.Marshal(envelope)
	result := connector.Execute(context.Background(), call)
	if result.Outcome != domain.AttemptFailed || result.ErrorCode != "web_publish_scope_mismatch" || called {
		t.Fatalf("result=%+v called=%v", result, called)
	}
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "192.0.2.1", "198.18.0.1", "203.0.113.10", "::1", "fc00::1", "2001:db8::1"} {
		if publicIP(net.ParseIP(value)) {
			t.Fatalf("internal address %s admitted", value)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) || !publicIP(net.ParseIP("2606:4700:4700::1111")) {
		t.Fatal("public test address rejected")
	}
}

func TestProbeUsesCredentialScopedHealthPathWithoutMutation(t *testing.T) {
	var captured *http.Request
	connector := testConnector(func(request *http.Request) (*http.Response, error) {
		captured = request
		return response(http.StatusNoContent, ""), nil
	})
	call := webCall(t)
	result := connector.Probe(context.Background(), integrationhealth.ProbeCall{Claim: integrationhealth.Claim{
		ConnectorKind: domain.ConnectorWebPublish, CredentialProvider: ProviderCode,
		Scope: domain.ConnectionScope{HTTPSOrigin: "https://publish.example.com", PathPrefix: "/campaigns"},
	}, Credential: call.Credential})
	if result.State != domain.HealthHealthy || captured.Method != http.MethodHead ||
		captured.URL.String() != "https://publish.example.com/campaigns/health" || captured.Header.Get("Authorization") != "Bearer provider-secret" {
		t.Fatalf("result=%+v request=%v", result, captured)
	}
}

func testConnector(handler roundTrip) *Connector {
	return &Connector{clients: func(context.Context, credential) (*http.Client, error) {
		return &http.Client{Transport: handler}, nil
	}}
}

func response(status int, digest string) *http.Response {
	header := make(http.Header)
	if digest != "" {
		header.Set("Content-Digest", digest)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(""))}
}

func webCall(t *testing.T) integrationexecution.ConnectorCall {
	t.Helper()
	claim := integrationexecution.Claim{ExecutionID: "f1500000-0000-4000-8000-000000000005", IdempotencyKey: "f1500000-0000-4000-8000-000000000005",
		Capability: domain.CapabilityWebPublish, CredentialProvider: ProviderCode}
	envelope := integrationexecution.ProviderEnvelope{Version: 1, Capability: domain.CapabilityWebPublish, IdempotencyKey: string(claim.IdempotencyKey),
		Scope: domain.ConnectionScope{HTTPSOrigin: "https://publish.example.com", PathPrefix: "/campaigns"},
		Assets: []integrationexecution.ProviderAsset{{ID: "f1700000-0000-4000-8000-000000000007", Kind: marketingdomain.AssetCopy,
			Title: "Launch page", MediaType: "text/plain", Content: []byte("approved web copy")}}}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	credential := []byte(`{"version":1,"https_origin":"https://publish.example.com","path_prefix":"/campaigns","health_path":"/campaigns/health","bearer_token":"provider-secret"}`)
	return integrationexecution.ConnectorCall{Claim: claim, Payload: integrationexecution.Payload{ProviderPayload: payload}, Credential: credential,
		At: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
}
