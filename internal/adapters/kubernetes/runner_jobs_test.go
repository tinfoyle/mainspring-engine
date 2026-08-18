package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
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
	if len(args) != 5 || args[0] != "runner-invocation" || args[2] != "--invocation-id="+invocation.ID || args[3] != "--identity-token-file="+runnerIdentityTokenFile || args[4] != "--broker-ca-file="+runnerBrokerCAFile {
		t.Fatalf("runner arguments=%#v", args)
	}
	identityVolume := template["volumes"].([]any)[2].(map[string]any)["projected"].(map[string]any)
	identitySource := identityVolume["sources"].([]any)[0].(map[string]any)["serviceAccountToken"].(map[string]any)
	if identityVolume["defaultMode"].(float64) != 0o400 || identitySource["audience"] != testConfig(nil).BrokerURL || identitySource["expirationSeconds"].(float64) != float64(runnerTokenLifetime) || identitySource["path"] != "token" {
		t.Fatalf("runner broker identity projection=%#v", identityVolume)
	}
	identityMount := container["volumeMounts"].([]any)[2].(map[string]any)
	if identityMount["mountPath"] != runnerIdentityDirectory || !identityMount["readOnly"].(bool) {
		t.Fatalf("runner identity mount=%#v", identityMount)
	}
	caVolume := template["volumes"].([]any)[3].(map[string]any)["configMap"].(map[string]any)
	caItem := caVolume["items"].([]any)[0].(map[string]any)
	if caVolume["name"] != "runner-broker-ca" || caVolume["defaultMode"].(float64) != 0o444 || caItem["key"] != "ca.crt" || caItem["path"] != "ca.crt" {
		t.Fatalf("runner broker CA volume=%#v", caVolume)
	}
	caMount := container["volumeMounts"].([]any)[3].(map[string]any)
	if caMount["mountPath"] != runnerBrokerCADirectory || !caMount["readOnly"].(bool) {
		t.Fatalf("runner broker CA mount=%#v", caMount)
	}

	invocation.JobName = name
	invocation.State = "launched"
	status, err := launcher.Inspect(context.Background(), invocation)
	if err != nil || !status.Terminal || status.Outcome != "completed" {
		t.Fatalf("terminal status=%+v err=%v", status, err)
	}
}

func TestRunnerCancellationUsesExactForegroundDeleteAndWaitsForAbsence(t *testing.T) {
	invocation := runnercontrol.Invocation{ID: "30000000-0000-4000-8000-000000000003", Profile: "agent-small"}
	var metadata map[string]any
	deleted := false
	postDeleteGets := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			var job map[string]any
			if err := json.NewDecoder(request.Body).Decode(&job); err != nil {
				t.Fatal(err)
			}
			metadata = job["metadata"].(map[string]any)
			metadata["uid"] = "job-uid-3"
			metadata["resourceVersion"] = "42"
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]any{"metadata": metadata})
		case http.MethodGet:
			if deleted {
				if postDeleteGets > 0 {
					writer.WriteHeader(http.StatusNotFound)
					return
				}
				postDeleteGets++
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"metadata": metadata})
		case http.MethodDelete:
			var options struct {
				GracePeriodSeconds int64             `json:"gracePeriodSeconds"`
				PropagationPolicy  string            `json:"propagationPolicy"`
				Preconditions      map[string]string `json:"preconditions"`
			}
			if err := json.NewDecoder(request.Body).Decode(&options); err != nil {
				t.Fatal(err)
			}
			if options.GracePeriodSeconds != 0 || options.PropagationPolicy != "Foreground" || options.Preconditions["uid"] != "job-uid-3" || options.Preconditions["resourceVersion"] != "42" {
				t.Errorf("delete options=%+v", options)
			}
			deleted = true
			writer.WriteHeader(http.StatusAccepted)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	launcher := newTestLauncher(t, server)
	name, err := launcher.Ensure(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	invocation.JobName, invocation.State = name, "canceling"
	if err := launcher.Cancel(context.Background(), invocation); err != nil {
		t.Fatalf("cancel runner Job: %v", err)
	}
	status, err := launcher.Inspect(context.Background(), invocation)
	if err != nil || status.Terminal {
		t.Fatalf("foreground deletion released early: status=%+v err=%v", status, err)
	}
	status, err = launcher.Inspect(context.Background(), invocation)
	if err != nil || !status.Terminal || status.Outcome != "canceled" {
		t.Fatalf("cancellation status=%+v err=%v", status, err)
	}
}

func TestRunnerCancellationRefusesMismatchedJob(t *testing.T) {
	invocation := runnercontrol.Invocation{ID: "40000000-0000-4000-8000-000000000004", Profile: "agent-small", State: "canceling"}
	invocation.JobName = jobName(invocation.ID)
	deleteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deleteCalls++
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"metadata": map[string]any{
			"name": invocation.JobName, "uid": "foreign", "resourceVersion": "1",
			"labels":      map[string]string{"spyglass.io/invocation-id": invocation.ID, "spyglass.io/profile": "different-profile"},
			"annotations": map[string]string{"spyglass.io/launch-contract-sha256": "foreign-contract"},
		}})
	}))
	defer server.Close()
	launcher := newTestLauncher(t, server)
	if err := launcher.Cancel(context.Background(), invocation); err == nil || deleteCalls != 0 {
		t.Fatalf("mismatched cancellation err=%v delete_calls=%d", err, deleteCalls)
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
	if _, err := launcher.Ensure(context.Background(), invocation); !errors.Is(err, runnercontrol.ErrLaunchUncertain) {
		t.Fatalf("mismatched existing Job result=%v", err)
	}
}

func TestRunnerJobServerFailureRetainsUncertainLaunchIdentity(t *testing.T) {
	invocation := runnercontrol.Invocation{ID: "50000000-0000-4000-8000-000000000005", Profile: "agent-small"}
	responseStatus := http.StatusServiceUnavailable
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(responseStatus)
	}))
	defer server.Close()
	launcher := newTestLauncher(t, server)
	name, err := launcher.Ensure(context.Background(), invocation)
	if name != jobName(invocation.ID) || !errors.Is(err, runnercontrol.ErrLaunchUncertain) {
		t.Fatalf("uncertain launch name=%q err=%v", name, err)
	}
	responseStatus = http.StatusUnprocessableEntity
	if name, err := launcher.Ensure(context.Background(), invocation); name != "" || err == nil || errors.Is(err, runnercontrol.ErrLaunchUncertain) {
		t.Fatalf("definitive rejection name=%q err=%v", name, err)
	}
}

func TestOnlyUncertainLaunchTreatsMissingJobAsConfirmedAbsence(t *testing.T) {
	invocation := runnercontrol.Invocation{ID: "60000000-0000-4000-8000-000000000006", Profile: "agent-small", State: "launch_uncertain"}
	invocation.JobName = jobName(invocation.ID)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	launcher := newTestLauncher(t, server)
	status, err := launcher.Inspect(context.Background(), invocation)
	if err != nil || status.Observed || status.Terminal {
		t.Fatalf("uncertain absence status=%+v err=%v", status, err)
	}
	invocation.State = "launched"
	if _, err := launcher.Inspect(context.Background(), invocation); err == nil {
		t.Fatal("confirmed launched Job disappearance was treated as safe absence")
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
	config = testConfig(nil)
	config.RunnerBrokerCAConfigMap = ""
	if _, err := NewRunnerJobs(config); err == nil {
		t.Fatal("missing runner broker CA ConfigMap accepted")
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
		RunnerImage: "registry.invalid/infinite-ocean/spyglass-runner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RunnerServiceAccount: "runner", RunnerRuntimeClass: "gvisor", BrokerURL: "https://runner-broker.spyglass-reference.svc.cluster.local", RunnerBrokerCAConfigMap: "runner-broker-ca",
		ActiveDeadlineSeconds: 900, TTLSecondsAfterFinished: 3600,
		Profiles: map[string]ResourceProfile{"agent-small": {CPURequest: "250m", CPULimit: "1", MemoryRequest: "256Mi", MemoryLimit: "1Gi", EphemeralStorageLimit: "1Gi"}},
	}
}
