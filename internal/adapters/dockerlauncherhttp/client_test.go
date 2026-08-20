package dockerlauncherhttp_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/dockerlauncherhttp"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/transport/dockerlauncherapi"
)

const (
	controllerToken = "controller-token-000000000000000000000000000000000000"
	brokerToken     = "broker-token-000000000000000000000000000000000000000"
	invocationID    = "78000000-0000-4000-8000-000000000001"
	accountID       = "78000000-0000-4000-8000-000000000002"
)

func TestClientAndServerPreserveLauncherContractsAndAuthority(t *testing.T) {
	launcher := &launcherStub{}
	serverAPI, err := dockerlauncherapi.New(launcher, dockerlauncherapi.Config{ControllerToken: controllerToken, BrokerToken: brokerToken}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(serverAPI.Handler())
	defer server.Close()
	controller := newClient(t, server, controllerToken)
	broker := newClient(t, server, brokerToken)
	invocation := runnercontrol.Invocation{ID: invocationID, AccountID: ids.AccountID(accountID), Profile: "agent-small", State: "launching", AttemptCount: 1}

	name, err := controller.Ensure(context.Background(), invocation)
	if err != nil || name != "spyglass-runner-test" || launcher.ensured.ID != invocation.ID {
		t.Fatalf("name=%q ensured=%+v err=%v", name, launcher.ensured, err)
	}
	invocation.State, invocation.JobName = "launched", name
	status, err := controller.Inspect(context.Background(), invocation)
	if err != nil || !status.Observed {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	identity, err := broker.Verify(context.Background(), "runner-token", invocation.ID)
	if err != nil || identity.InvocationID != invocation.ID {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	if _, err := controller.Verify(context.Background(), "runner-token", invocation.ID); !errors.Is(err, runnerbroker.ErrIdentityDenied) {
		t.Fatalf("controller reached broker-only endpoint: %v", err)
	}
	if _, err := broker.Ensure(context.Background(), invocation); err == nil {
		t.Fatal("broker reached controller-only endpoint")
	}
	invocation.State = "canceling"
	if err := controller.Cancel(context.Background(), invocation); err != nil || launcher.canceled.ID != invocation.ID {
		t.Fatalf("canceled=%+v err=%v", launcher.canceled, err)
	}
}

func TestUncertainLaunchRetainsDeterministicIdentity(t *testing.T) {
	launcher := &launcherStub{ensureErr: runnercontrol.ErrLaunchUncertain}
	serverAPI, err := dockerlauncherapi.New(launcher, dockerlauncherapi.Config{ControllerToken: controllerToken, BrokerToken: brokerToken}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(serverAPI.Handler())
	defer server.Close()
	client := newClient(t, server, controllerToken)
	name, err := client.Ensure(context.Background(), runnercontrol.Invocation{ID: invocationID, AccountID: ids.AccountID(accountID), Profile: "agent-small", State: "launching", AttemptCount: 1})
	if name != "spyglass-runner-test" || !errors.Is(err, runnercontrol.ErrLaunchUncertain) {
		t.Fatalf("name=%q err=%v", name, err)
	}
}

func TestLauncherFailureReportsHTTPStatusInsteadOfDecodingProblemAsSuccess(t *testing.T) {
	launcher := &launcherStub{ensureErr: errors.New("engine unavailable")}
	serverAPI, err := dockerlauncherapi.New(launcher, dockerlauncherapi.Config{ControllerToken: controllerToken, BrokerToken: brokerToken}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(serverAPI.Handler())
	defer server.Close()
	client := newClient(t, server, controllerToken)
	_, err = client.Ensure(context.Background(), runnercontrol.Invocation{ID: invocationID, AccountID: ids.AccountID(accountID), Profile: "agent-small", State: "launching", AttemptCount: 1})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("err=%v", err)
	}
}

func newClient(t *testing.T, server *httptest.Server, token string) *dockerlauncherhttp.Client {
	t.Helper()
	client, err := dockerlauncherhttp.New(dockerlauncherhttp.Config{Origin: server.URL, Token: token, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type launcherStub struct {
	ensured, canceled runnercontrol.Invocation
	ensureErr         error
}

func (l *launcherStub) Ensure(_ context.Context, invocation runnercontrol.Invocation) (string, error) {
	l.ensured = invocation
	return "spyglass-runner-test", l.ensureErr
}
func (l *launcherStub) Cancel(_ context.Context, invocation runnercontrol.Invocation) error {
	l.canceled = invocation
	return nil
}
func (*launcherStub) Inspect(context.Context, runnercontrol.Invocation) (runnercontrol.TerminalStatus, error) {
	return runnercontrol.TerminalStatus{Observed: true}, nil
}
func (*launcherStub) Verify(_ context.Context, token, invocation string) (runnerbroker.Identity, error) {
	if token != "runner-token" || invocation != invocationID {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	return runnerbroker.Identity{InvocationID: invocation, Profile: "agent-small", JobName: "spyglass-runner-test", JobUID: strings.Repeat("a", 64), PodName: "spyglass-runner-test", PodUID: strings.Repeat("a", 64)}, nil
}
func (*launcherStub) Ready(context.Context) error { return nil }
