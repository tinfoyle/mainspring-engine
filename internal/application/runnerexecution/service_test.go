package runnerexecution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
)

type executionBroker struct {
	request   runnerbroker.Request
	result    runnerbroker.Result
	fetchErr  error
	submitErr error
	submits   int
}

func (b *executionBroker) Fetch(context.Context) (runnerbroker.Request, error) {
	return b.request, b.fetchErr
}
func (b *executionBroker) Submit(_ context.Context, result runnerbroker.Result) (bool, error) {
	b.submits++
	b.result = result
	return true, b.submitErr
}

type executionGateway struct{}

func (executionGateway) Invoke(context.Context, runnercapability.Call) (runnercapability.Result, error) {
	return runnercapability.Result{SchemaVersion: 1, Output: json.RawMessage(`{}`)}, nil
}

type executorError string

func (e executorError) Error() string { return "private executor detail" }
func (e executorError) Code() string  { return string(e) }

func TestExecutionFetchesRunsAndSubmitsCompletedResult(t *testing.T) {
	expires := time.Now().Add(time.Minute).UTC()
	broker := &executionBroker{request: runnerbroker.Request{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{"prompt":"bounded"}`), Capabilities: []string{"work:read"}, ExpiresAt: expires}}
	service, err := New(broker, executionGateway{}, []Definition{{Kind: "agent.execute", Executor: ExecutorFunc(func(ctx context.Context, execution Execution) (json.RawMessage, error) {
		deadline, ok := ctx.Deadline()
		if !ok || !deadline.Equal(expires) || execution.Kind != "agent.execute" || len(execution.Capabilities) != 1 || execution.Gateway == nil {
			t.Errorf("execution=%+v deadline=%v ok=%v", execution, deadline, ok)
		}
		return json.RawMessage(`{"z":1,"a":2}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Run(context.Background()); err != nil || broker.submits != 1 || broker.result.Outcome != "completed" || string(broker.result.Output) != `{"a":2,"z":1}` {
		t.Fatalf("run err=%v submits=%d result=%+v", err, broker.submits, broker.result)
	}
}

func TestExecutionFailureSubmitsOnlyMachineCode(t *testing.T) {
	broker := &executionBroker{request: runnerbroker.Request{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Minute)}}
	service, _ := New(broker, executionGateway{}, []Definition{{Kind: "agent.execute", Executor: ExecutorFunc(func(context.Context, Execution) (json.RawMessage, error) {
		return nil, executorError("provider_timeout")
	})}})
	if err := service.Run(context.Background()); !errors.Is(err, ErrExecutionFailed) || broker.result.Outcome != "execution_failed" || broker.result.ErrorCode != "provider_timeout" || string(broker.result.Output) != `{}` {
		t.Fatalf("run err=%v result=%+v", err, broker.result)
	}
}

func TestExecutionFailsClosedOnCancellationUnsupportedKindAndInvalidOutput(t *testing.T) {
	canceled := &executionBroker{fetchErr: runnerbroker.ErrExchangeCanceled}
	service, _ := New(canceled, executionGateway{}, []Definition{{Kind: "agent.execute", Executor: ExecutorFunc(func(context.Context, Execution) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })}})
	if err := service.Run(context.Background()); !errors.Is(err, runnerbroker.ErrExchangeCanceled) || canceled.submits != 0 {
		t.Fatalf("canceled run=%v submits=%d", err, canceled.submits)
	}
	unsupported := &executionBroker{request: runnerbroker.Request{Kind: "finance.execute", ExpiresAt: time.Now().Add(time.Minute)}}
	service, _ = New(unsupported, executionGateway{}, []Definition{{Kind: "agent.execute", Executor: ExecutorFunc(func(context.Context, Execution) (json.RawMessage, error) { return nil, nil })}})
	if err := service.Run(context.Background()); !errors.Is(err, ErrExecutionFailed) || unsupported.result.ErrorCode != "unsupported_kind" {
		t.Fatalf("unsupported run=%v result=%+v", err, unsupported.result)
	}
	invalid := &executionBroker{request: runnerbroker.Request{Kind: "agent.execute", ExpiresAt: time.Now().Add(time.Minute)}}
	service, _ = New(invalid, executionGateway{}, []Definition{{Kind: "agent.execute", Executor: ExecutorFunc(func(context.Context, Execution) (json.RawMessage, error) { return json.RawMessage(`[]`), nil })}})
	if err := service.Run(context.Background()); !errors.Is(err, ErrExecutionFailed) || invalid.result.ErrorCode != "invalid_output" {
		t.Fatalf("invalid output run=%v result=%+v", err, invalid.result)
	}
}
