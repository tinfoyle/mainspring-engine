package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
)

const (
	testRunnerInvocation = "71000000-0000-4000-8000-000000000001"
	testRunnerPodUID     = "72000000-0000-4000-8000-000000000002"
	testRunnerJobUID     = "73000000-0000-4000-8000-000000000003"
	testRunnerPodName    = "spyglass-runner-pod-abc12"
	testRunnerToken      = "runner.jwt.token"
	testBrokerAudience   = "https://runner-broker.spyglass-reference.svc.cluster.local"
)

func TestRunnerIdentityBindsReviewedPodToExactJobAndInvocation(t *testing.T) {
	calls := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer reviewer-token" {
			t.Errorf("reviewer authorization=%q", request.Header.Get("Authorization"))
		}
		calls = append(calls, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/apis/authentication.k8s.io/v1/tokenreviews":
			var review struct {
				Spec struct {
					Token     string   `json:"token"`
					Audiences []string `json:"audiences"`
				} `json:"spec"`
			}
			if err := json.NewDecoder(request.Body).Decode(&review); err != nil {
				t.Fatal(err)
			}
			if request.Method != http.MethodPost || review.Spec.Token != testRunnerToken || len(review.Spec.Audiences) != 1 || review.Spec.Audiences[0] != testBrokerAudience {
				t.Errorf("TokenReview=%+v", review.Spec)
			}
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(validTokenReview(testBrokerAudience))
		case "/api/v1/namespaces/spyglass-reference/pods/" + testRunnerPodName:
			_ = json.NewEncoder(writer).Encode(validRunnerPod())
		case "/apis/batch/v1/namespaces/spyglass-reference/jobs/" + jobName(testRunnerInvocation):
			_ = json.NewEncoder(writer).Encode(validRunnerIdentityJob())
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	verifier := newTestRunnerIdentity(t, server)
	identity, err := verifier.Verify(context.Background(), testRunnerToken, testRunnerInvocation)
	if err != nil || identity.InvocationID != testRunnerInvocation || identity.Profile != "agent-small" || identity.PodName != testRunnerPodName || identity.PodUID != testRunnerPodUID || identity.JobName != jobName(testRunnerInvocation) || identity.JobUID != testRunnerJobUID {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	if len(calls) != 3 || !strings.HasSuffix(calls[1], "/pods/"+testRunnerPodName) || !strings.HasSuffix(calls[2], "/jobs/"+jobName(testRunnerInvocation)) {
		t.Fatalf("identity calls=%v", calls)
	}
}

func TestRunnerIdentityRejectsAudiencePodAndLifecycleMismatch(t *testing.T) {
	audience := testBrokerAudience
	pod := validRunnerPod()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/tokenreviews"):
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(validTokenReview(audience))
		case strings.Contains(request.URL.Path, "/pods/"):
			_ = json.NewEncoder(writer).Encode(pod)
		case strings.Contains(request.URL.Path, "/jobs/"):
			_ = json.NewEncoder(writer).Encode(validRunnerIdentityJob())
		}
	}))
	defer server.Close()
	verifier := newTestRunnerIdentity(t, server)

	audience = "https://different-audience.invalid"
	if _, err := verifier.Verify(context.Background(), testRunnerToken, testRunnerInvocation); !errors.Is(err, runnerbroker.ErrIdentityDenied) {
		t.Fatalf("wrong audience err=%v", err)
	}
	audience = testBrokerAudience
	if _, err := verifier.Verify(context.Background(), testRunnerToken, "74000000-0000-4000-8000-000000000004"); !errors.Is(err, runnerbroker.ErrIdentityDenied) {
		t.Fatalf("runner selected another invocation err=%v", err)
	}
	podMetadata := pod["metadata"].(map[string]any)
	podMetadata["deletionTimestamp"] = "2026-08-18T20:00:00Z"
	if _, err := verifier.Verify(context.Background(), testRunnerToken, testRunnerInvocation); !errors.Is(err, runnerbroker.ErrIdentityDenied) {
		t.Fatalf("terminating Pod err=%v", err)
	}
	delete(podMetadata, "deletionTimestamp")
	podMetadata["labels"].(map[string]string)["spyglass.io/invocation-id"] = "74000000-0000-4000-8000-000000000004"
	if _, err := verifier.Verify(context.Background(), testRunnerToken, testRunnerInvocation); !errors.Is(err, runnerbroker.ErrIdentityDenied) {
		t.Fatalf("cross-invocation Pod err=%v", err)
	}
}

func TestRunnerIdentityFailsClosedWithoutLeakingPresentedToken(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(testRunnerToken))
	}))
	defer server.Close()
	verifier := newTestRunnerIdentity(t, server)
	if _, err := verifier.Verify(context.Background(), " token-with-space ", testRunnerInvocation); !errors.Is(err, runnerbroker.ErrIdentityDenied) || calls != 0 {
		t.Fatalf("malformed token err=%v calls=%d", err, calls)
	}
	if _, err := verifier.Verify(context.Background(), strings.Repeat("a", maximumRunnerTokenBytes+1), testRunnerInvocation); !errors.Is(err, runnerbroker.ErrIdentityDenied) || calls != 0 {
		t.Fatalf("oversized token err=%v calls=%d", err, calls)
	}
	if _, err := verifier.Verify(context.Background(), testRunnerToken, testRunnerInvocation); err == nil || strings.Contains(err.Error(), testRunnerToken) {
		t.Fatalf("Kubernetes failure leaked token: %v", err)
	}
}

func TestRunnerIdentityConfigurationRequiresPurposeBoundAudience(t *testing.T) {
	config := RunnerIdentityConfig{Endpoint: "https://kubernetes.invalid", Namespace: "spyglass-reference", ReviewerToken: "reviewer", Audience: "kubernetes-api", RunnerServiceAccount: "runner"}
	if _, err := NewRunnerIdentity(config); err == nil {
		t.Fatal("non-HTTPS runner audience accepted")
	}
}

func newTestRunnerIdentity(t *testing.T, server *httptest.Server) *RunnerIdentity {
	t.Helper()
	verifier, err := NewRunnerIdentity(RunnerIdentityConfig{
		Endpoint: server.URL, Namespace: "spyglass-reference", ReviewerToken: "reviewer-token", Audience: testBrokerAudience, RunnerServiceAccount: "runner", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func validTokenReview(audience string) map[string]any {
	return map[string]any{"status": map[string]any{
		"authenticated": true, "audiences": []string{audience},
		"user": map[string]any{
			"username": "system:serviceaccount:spyglass-reference:runner",
			"uid":      "service-account-uid",
			"groups":   []string{"system:serviceaccounts", "system:serviceaccounts:spyglass-reference", "system:authenticated"},
			"extra": map[string][]string{
				"authentication.kubernetes.io/pod-name": {testRunnerPodName},
				"authentication.kubernetes.io/pod-uid":  {testRunnerPodUID},
			},
		},
	}}
}

func validRunnerPod() map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"name": testRunnerPodName, "uid": testRunnerPodUID,
			"labels":          map[string]string{"spyglass.io/invocation-id": testRunnerInvocation, "spyglass.io/profile": "agent-small"},
			"ownerReferences": []map[string]any{{"apiVersion": "batch/v1", "kind": "Job", "name": jobName(testRunnerInvocation), "uid": testRunnerJobUID, "controller": true}},
		},
		"spec": map[string]any{"serviceAccountName": "runner"},
	}
}

func validRunnerIdentityJob() map[string]any {
	return map[string]any{"metadata": map[string]any{
		"name": jobName(testRunnerInvocation), "uid": testRunnerJobUID,
		"labels":      map[string]string{"spyglass.io/invocation-id": testRunnerInvocation, "spyglass.io/profile": "agent-small"},
		"annotations": map[string]string{"spyglass.io/launch-contract-sha256": strings.Repeat("a", 64)},
	}}
}
