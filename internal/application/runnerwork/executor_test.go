package runnerwork

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerexecution"
)

type gatewayStub struct {
	call   runnercapability.Call
	result runnercapability.Result
	err    error
	calls  int
}

func (g *gatewayStub) Invoke(_ context.Context, call runnercapability.Call) (runnercapability.Result, error) {
	g.calls++
	g.call = call
	return g.result, g.err
}

func summaryExecution(gateway *gatewayStub) runnerexecution.Execution {
	return runnerexecution.Execution{
		Kind: SummarySnapshotKind, Input: json.RawMessage(`{"operation_id":"41000000-0000-4000-8000-000000000001"}`),
		Capabilities: []string{runnercapability.WorkSummaryCapability}, Gateway: gateway, ExpiresAt: time.Now().Add(time.Minute),
	}
}

func TestSummarySnapshotUsesOnlyBoundWorkCapability(t *testing.T) {
	gateway := &gatewayStub{result: runnercapability.Result{SchemaVersion: runnercapability.SchemaVersion, Output: json.RawMessage(`{"active":7,"in_progress":3,"waiting":2,"urgent":1,"done":11}`)}}
	raw, err := (SummarySnapshotExecutor{}).Execute(context.Background(), summaryExecution(gateway))
	if err != nil || string(raw) != `{"active":7,"in_progress":3,"waiting":2,"urgent":1,"done":11}` {
		t.Fatalf("output=%s err=%v", raw, err)
	}
	if gateway.calls != 1 || gateway.call.OperationID != "41000000-0000-4000-8000-000000000001" || gateway.call.Capability != runnercapability.WorkSummaryCapability || string(gateway.call.Input) != `{}` {
		t.Fatalf("gateway call=%+v count=%d", gateway.call, gateway.calls)
	}
}

func TestSummarySnapshotRejectsInputAndGrantWidening(t *testing.T) {
	for _, test := range []struct {
		name      string
		execution runnerexecution.Execution
		cause     error
	}{
		{"unknown input", runnerexecution.Execution{Kind: SummarySnapshotKind, Input: json.RawMessage(`{"operation_id":"41000000-0000-4000-8000-000000000001","account_id":"guessed"}`), Capabilities: []string{runnercapability.WorkSummaryCapability}, Gateway: &gatewayStub{}}, ErrInvalidInput},
		{"missing grant", runnerexecution.Execution{Kind: SummarySnapshotKind, Input: json.RawMessage(`{"operation_id":"41000000-0000-4000-8000-000000000001"}`), Gateway: &gatewayStub{}}, ErrCapabilityDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := (SummarySnapshotExecutor{}).Execute(context.Background(), test.execution); !errors.Is(err, test.cause) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSummarySnapshotMapsPrivateGatewayFailuresAndInvalidOutput(t *testing.T) {
	private := errors.New("private upstream detail")
	gateway := &gatewayStub{err: private}
	if _, err := (SummarySnapshotExecutor{}).Execute(context.Background(), summaryExecution(gateway)); !errors.Is(err, ErrCapabilityFailed) || err.Error() != "capability_failed" {
		t.Fatalf("gateway failure=%v", err)
	}
	gateway.err = nil
	gateway.result = runnercapability.Result{SchemaVersion: runnercapability.SchemaVersion, Output: json.RawMessage(`{"active":1,"unexpected":true}`)}
	if _, err := (SummarySnapshotExecutor{}).Execute(context.Background(), summaryExecution(gateway)); !errors.Is(err, ErrInvalidToolOutput) || err.Error() != "capability_output_invalid" {
		t.Fatalf("invalid output=%v", err)
	}
}
