package dockerengine

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const testInvocationID = "77000000-0000-4000-8000-000000000001"
const testAccountID = "77000000-0000-4000-8000-000000000002"

func TestRunnerContainersLifecycleSurvivesAmbiguityAndRestart(t *testing.T) {
	engine := newFakeEngine()
	server := httptest.NewServer(engine)
	defer server.Close()
	config := testConfig(t, server)
	launcher, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	invocation := testInvocation()

	engine.abortCreate = true
	name, err := launcher.Ensure(context.Background(), invocation)
	if !errors.Is(err, runnercontrol.ErrLaunchUncertain) || name != containerName(invocation.ID) {
		t.Fatalf("name=%q err=%v", name, err)
	}

	uncertain := invocation
	uncertain.State, uncertain.JobName = "launch_uncertain", name
	status, err := launcher.Inspect(context.Background(), uncertain)
	if err != nil || !status.Observed || status.Terminal {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if engine.startCount != 1 {
		t.Fatalf("start count=%d", engine.startCount)
	}

	// A new launcher instance uses only the persisted identity directory and
	// immutable Docker labels; no in-memory launch state is required.
	restarted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	duplicateName, err := restarted.Ensure(context.Background(), invocation)
	if err != nil || duplicateName != name || engine.createCount != 1 {
		t.Fatalf("duplicate name=%q creates=%d err=%v", duplicateName, engine.createCount, err)
	}
	token, err := os.ReadFile(filepath.Join(config.IdentityDirectory, invocation.ID, "identity", "token"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := restarted.Verify(context.Background(), string(token), invocation.ID)
	if err != nil || identity.InvocationID != invocation.ID || identity.Profile != invocation.Profile || identity.JobName != name || identity.JobUID == "" || ids.Validate(identity.PodUID) != nil || identity.PodUID == identity.JobUID {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	if _, err := restarted.Verify(context.Background(), "wrong-token", invocation.ID); !errors.Is(err, runnerbroker.ErrIdentityDenied) {
		t.Fatalf("wrong token err=%v", err)
	}

	canceling := invocation
	canceling.State, canceling.JobName = "canceling", name
	if err := restarted.Cancel(context.Background(), canceling); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config.IdentityDirectory, invocation.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identity directory remains: %v", err)
	}
	status, err = restarted.Inspect(context.Background(), canceling)
	if err != nil || !status.Terminal || status.Outcome != "canceled" {
		t.Fatalf("canceled status=%+v err=%v", status, err)
	}
}

func TestDockerPodUIDIsStableAndRejectsNonDockerIdentities(t *testing.T) {
	container := strings.Repeat("a", 64)
	first, err := dockerPodUID(container)
	if err != nil || ids.Validate(first) != nil {
		t.Fatalf("pod uid=%q err=%v", first, err)
	}
	second, err := dockerPodUID(container)
	if err != nil || second != first {
		t.Fatalf("pod uid was not stable: first=%q second=%q err=%v", first, second, err)
	}
	other, err := dockerPodUID(strings.Repeat("b", 64))
	if err != nil || other == first {
		t.Fatalf("pod uid did not bind the full container identity: first=%q other=%q err=%v", first, other, err)
	}
	if _, err := dockerPodUID("container-" + testInvocationID); err == nil {
		t.Fatal("non-Docker identity was accepted")
	}
}

func TestRunnerContainersEnforcesSecurityContractAndCleansExpiredOrphans(t *testing.T) {
	engine := newFakeEngine()
	server := httptest.NewServer(engine)
	defer server.Close()
	config := testConfig(t, server)
	launcher, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	invocation := testInvocation()
	name, err := launcher.Ensure(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	container := engine.containers[name]
	host := container.payload["HostConfig"].(map[string]any)
	if container.payload["Image"] != config.RunnerImage || container.payload["User"] != "65532:65532" || host["ReadonlyRootfs"] != true || host["NetworkMode"] != config.Network || host["AutoRemove"] != false {
		t.Fatalf("unsafe create payload=%v", container.payload)
	}
	if len(host["CapDrop"].([]any)) != 1 || host["CapDrop"].([]any)[0] != "ALL" || len(host["Binds"].([]any)) != 2 || len(container.payload["Env"].([]any)) != 0 {
		t.Fatalf("runner authority contract=%v", host)
	}

	container.labels[deadlineLabel] = "1"
	removed, err := launcher.CleanupExpired(context.Background(), time.Now().UTC(), 10)
	if err != nil || removed != 1 || engine.removeCount != 1 {
		t.Fatalf("removed=%d engine removes=%d err=%v", removed, engine.removeCount, err)
	}
}

func testInvocation() runnercontrol.Invocation {
	return runnercontrol.Invocation{ID: testInvocationID, AccountID: ids.AccountID(testAccountID), Profile: "agent-small", State: "launching", AttemptCount: 1}
}

func testConfig(t *testing.T, server *httptest.Server) Config {
	t.Helper()
	directory := t.TempDir()
	caFile := filepath.Join(directory, "broker-ca.crt")
	writeTestCA(t, caFile)
	return Config{
		Endpoint: server.URL, APIVersion: defaultAPIVersion, HTTPClient: server.Client(),
		RunnerImage: "ghcr.io/infinite-ocean/spyglass@sha256:" + strings.Repeat("a", 64),
		Network:     "spyglass-runner-egress", BrokerURL: "https://runner-broker:8443",
		IdentityDirectory: filepath.Join(directory, "identities"), BrokerCAFile: caFile,
		Profiles:       map[string]ResourceProfile{"agent-small": {NanoCPUs: 1_000_000_000, MemoryBytes: 1 << 30, PidsLimit: 128, WorkTmpfsBytes: 1 << 30}},
		ActiveDeadline: 15 * time.Minute, Retention: time.Hour,
	}
}

func writeTestCA(t *testing.T, path string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

type fakeContainer struct {
	id      string
	name    string
	labels  map[string]string
	state   string
	running bool
	exit    int
	payload map[string]any
}

type fakeEngine struct {
	mu                                              sync.Mutex
	containers                                      map[string]*fakeContainer
	abortCreate                                     bool
	createCount, startCount, stopCount, removeCount int
}

func newFakeEngine() *fakeEngine { return &fakeEngine{containers: map[string]*fakeContainer{}} }

func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/"+defaultAPIVersion)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/_ping":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("OK"))
	case r.Method == http.MethodPost && path == "/containers/create":
		name := r.URL.Query().Get("name")
		if _, exists := f.containers[name]; exists {
			w.WriteHeader(http.StatusConflict)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		labels := map[string]string{}
		for key, value := range payload["Labels"].(map[string]any) {
			labels[key] = value.(string)
		}
		container := &fakeContainer{id: strings.Repeat("a", 64), name: name, labels: labels, state: "created", payload: payload}
		f.containers[name] = container
		f.createCount++
		if f.abortCreate {
			f.abortCreate = false
			panic(http.ErrAbortHandler)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": container.id})
	case r.Method == http.MethodGet && path != "/containers/json" && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		container := f.find(name)
		if container == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": container.id, "Name": "/" + container.name, "Config": map[string]any{"Labels": container.labels}, "State": map[string]any{"Status": container.state, "Running": container.running, "ExitCode": container.exit}})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/start"):
		container := f.find(strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/start"))
		if container == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		container.state, container.running = "running", true
		f.startCount++
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/stop"):
		container := f.find(strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/stop"))
		if container == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		container.state, container.running = "exited", false
		f.stopCount++
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/containers/"):
		container := f.find(strings.TrimPrefix(path, "/containers/"))
		if container == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.containers, container.name)
		f.removeCount++
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && path == "/containers/json":
		values := make([]map[string]any, 0, len(f.containers))
		for _, container := range f.containers {
			values = append(values, map[string]any{"Id": container.id, "Labels": container.labels})
		}
		_ = json.NewEncoder(w).Encode(values)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeEngine) find(nameOrID string) *fakeContainer {
	if container := f.containers[nameOrID]; container != nil {
		return container
	}
	for _, container := range f.containers {
		if container.id == nameOrID {
			return container
		}
	}
	return nil
}
