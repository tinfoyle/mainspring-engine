package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
)

func TestRunnerJobIsHardenedAndTerminalStateIsObserved(t *testing.T) {
	invocation := runnercontrol.Invocation{ID: "10000000-0000-4000-8000-000000000001", Profile: "agent-small"}
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer controller-token" {
			t.Errorf("authorization=%q", request.Header.Get("Authorization"))
		}
		switch request.Method {
		case http.MethodPost:
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			metadata := created["metadata"].(map[string]any)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]any{"metadata": metadata})
		case http.MethodGet:
			metadata := created["metadata"].(map[string]any)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"metadata": metadata,
				"status":   map[string]any{"conditions": []map[string]string{{"type": "Complete", "status": "True"}}},
			})
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	launcher := newTestLauncher(t, server)

	name, err := launcher.Ensure(context.Background(), invocation)
	if err != nil || name != jobName(invocation.ID) {
		t.Fatalf("ensure runner Job name=%q err=%v", name, err)
	}
	spec := created["spec"].(map[string]any)
	if spec["backoffLimit"].(float64) != 0 || spec["activeDeadlineSeconds"].(float64) != 900 || spec["ttlSecondsAfterFinished"].(float64) != 3600 {
		t.Fatalf("unsafe Job lifecycle: %#v", spec)
	}
	template := spec["template"].(map[string]any)["spec"].(map[string]any)
	if template["automountServiceAccountToken"].(bool) || template["restartPolicy"] != "Never" || template["serviceAccountName"] != "runner" || template["runtimeClassName"] != "gvisor" {
		t.Fatalf("unsafe pod identity: %#v", template)
	}
	container := template["containers"].([]any)[0].(map[string]any)
	security := container["securityContext"].(map[string]any)
	if !security["readOnlyRootFilesystem"].(bool) || security["allowPrivilegeEscalation"].(bool) || !security["runAsNonRoot"].(bool) {
		t.Fatalf("unsafe container security: %#v", security)
	}
	if security["capabilities"].(map[string]any)["drop"].([]any)[0] != "ALL" {
		t.Fatalf("Linux capabilities not dropped: %#v", security)
	}
	args := container["args"].([]any)
	if len(args) != 3 || args[0] != "runner-invocation" || args[2] != "--invocation-id="+invocation.ID {
		t.Fatalf("runner arguments=%#v", args)
	}

	invocation.JobName = name
	status, err := launcher.Inspect(context.Background(), invocation)
	if err != nil || !status.Terminal || status.Outcome != "completed" {
		t.Fatalf("terminal status=%+v err=%v", status, err)
	}
}

func TestRunnerJobConflictRequiresExactLaunchContract(t *testing.T) {
	invocation := runnercontrol.Invocation{ID: "20000000-0000-4000-8000-000000000002", Profile: "agent-small"}
	contract := ""
	mismatch := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			var job struct {
				Metadata jobMetadata `json:"metadata"`
			}
			if err := json.NewDecoder(request.Body).Decode(&job); err != nil {
				t.Fatal(err)
			}
			contract = job.Metadata.Annotations["spyglass.io/launch-contract-sha256"]
			writer.WriteHeader(http.StatusConflict)
			return
		}
		if mismatch {
			contract = "different-contract"
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"metadata": map[string]any{
			"name":        jobName(invocation.ID),
			"labels":      map[string]string{"spyglass.io/invocation-id": invocation.ID, "spyglass.io/profile": invocation.Profile},
			"annotations": map[string]string{"spyglass.io/launch-contract-sha256": contract},
		}})
	}))
	defer server.Close()
	launcher := newTestLauncher(t, server)
	if name, err := launcher.Ensure(context.Background(), invocation); err != nil || name != jobName(invocation.ID) {
		t.Fatalf("idempotent conflict name=%q err=%v", name, err)
	}
	mismatch = true
	if _, err := launcher.Ensure(context.Background(), invocation); err == nil {
		t.Fatal("mismatched existing Job accepted")
	}
}

func TestRunnerJobConfigurationRejectsUnboundedInputs(t *testing.T) {
	config := testConfig(nil)
	config.BrokerURL = "http://broker.internal"
	if _, err := NewRunnerJobs(config); err == nil {
		t.Fatal("non-HTTPS broker accepted")
	}
	config = testConfig(nil)
	config.Profiles["agent-small"] = ResourceProfile{}
	if _, err := NewRunnerJobs(config); err == nil {
		t.Fatal("empty resource profile accepted")
	}
	config = testConfig(nil)
	config.RunnerImage = "registry.invalid/infinite-ocean/spyglass-runner:latest"
	if _, err := NewRunnerJobs(config); err == nil {
		t.Fatal("tag-only runner image accepted")
	}
}

func newTestLauncher(t *testing.T, server *httptest.Server) *RunnerJobs {
	t.Helper()
	config := testConfig(server.Client())
	config.Endpoint = server.URL
	launcher, err := NewRunnerJobs(config)
	if err != nil {
		t.Fatal(err)
	}
	return launcher
}

func testConfig(client *http.Client) Config {
	return Config{
		Endpoint: "https://kubernetes.invalid", Namespace: "spyglass-reference", BearerToken: "controller-token", HTTPClient: client,
		RunnerImage: "registry.invalid/infinite-ocean/spyglass-runner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RunnerServiceAccount: "runner", RunnerRuntimeClass: "gvisor", BrokerURL: "https://runner-broker.spyglass-reference.svc.cluster.local",
		ActiveDeadlineSeconds: 900, TTLSecondsAfterFinished: 3600,
		Profiles: map[string]ResourceProfile{"agent-small": {CPURequest: "250m", CPULimit: "1", MemoryRequest: "256Mi", MemoryLimit: "1Gi", EphemeralStorageLimit: "1Gi"}},
	}
}
