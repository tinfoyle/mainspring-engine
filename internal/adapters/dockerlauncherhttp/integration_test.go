package dockerlauncherhttp

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/workloadidentity"
)

const integrationInvocationID = "79000000-0000-4000-8000-000000000001"
const integrationAccountID = "79000000-0000-4000-8000-000000000002"

func TestDockerLauncherIntegration(t *testing.T) {
	if os.Getenv("SPYGLASS_DOCKER_LAUNCHER_INTEGRATION") != "1" {
		t.Skip("Docker launcher integration is opt-in")
	}
	controller := integrationClient(t, "SPYGLASS_DOCKER_LAUNCHER_CONTROLLER_TOKEN", "/runner-controller")
	broker := integrationClient(t, "SPYGLASS_DOCKER_LAUNCHER_BROKER_TOKEN", "/runner-broker")
	invocation := runnercontrol.Invocation{ID: integrationInvocationID, AccountID: ids.AccountID(integrationAccountID), Profile: "agent-small", State: "launching", AttemptCount: 1}
	name, err := controller.Ensure(context.Background(), invocation)
	if err != nil || name == "" {
		t.Fatalf("ensure name=%q err=%v", name, err)
	}

	tokenPath := filepath.Join(os.Getenv("SPYGLASS_DOCKER_RUNNER_IDENTITY_DIRECTORY"), invocation.ID, "identity", "token")
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := broker.Verify(context.Background(), string(token), invocation.ID)
	if err != nil || identity.InvocationID != invocation.ID || identity.Profile != invocation.Profile || identity.JobName != name || identity.JobUID == "" {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}

	if os.Getenv("SPYGLASS_DOCKER_LAUNCHER_INTEGRATION_PHASE") == "ensure" {
		return
	}
	duplicate, err := controller.Ensure(context.Background(), invocation)
	if err != nil || duplicate != name {
		t.Fatalf("restart ensure name=%q err=%v", duplicate, err)
	}
	canceling := invocation
	canceling.State, canceling.JobName = "canceling", name
	if err := controller.Cancel(context.Background(), canceling); err != nil {
		t.Fatal(err)
	}
	status, err := controller.Inspect(context.Background(), canceling)
	if err != nil || !status.Terminal || status.Outcome != "canceled" {
		t.Fatalf("cancel status=%+v err=%v", status, err)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(tokenPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runner identity directory was not removed: %v", err)
	}
}

func integrationClient(t *testing.T, tokenEnvironment, certificateDirectory string) *Client {
	t.Helper()
	transport, err := workloadidentity.NewClientTransport(workloadidentity.Files{Certificate: filepath.Join(certificateDirectory, "tls.crt"), PrivateKey: filepath.Join(certificateDirectory, "tls.key"), TrustBundle: filepath.Join(certificateDirectory, "ca.crt")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	client, err := New(Config{Origin: os.Getenv("SPYGLASS_DOCKER_LAUNCHER_ORIGIN"), Token: os.Getenv(tokenEnvironment), HTTPClient: &http.Client{Transport: transport, Timeout: 15 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
