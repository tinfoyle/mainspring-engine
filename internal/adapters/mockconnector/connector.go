// Package mockconnector provides the deterministic local connector used by
// Docker development and Stage certification. It performs no network I/O.
package mockconnector

import (
	"context"
	"sync"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Step struct {
	Mode   domain.AttemptMode
	Result integrationexecution.ConnectorResult
}

type Call struct {
	ExecutionID ids.IntegrationExecutionID
	AttemptID   ids.IntegrationAttemptID
	Mode        domain.AttemptMode
}

type Connector struct {
	mu      sync.Mutex
	scripts map[ids.IntegrationExecutionID][]Step
	calls   []Call
}

func New(scripts map[ids.IntegrationExecutionID][]Step) *Connector {
	cloned := make(map[ids.IntegrationExecutionID][]Step, len(scripts))
	for executionID, steps := range scripts {
		cloned[executionID] = append([]Step(nil), steps...)
	}
	return &Connector{scripts: cloned}
}

func (connector *Connector) Execute(_ context.Context, call integrationexecution.ConnectorCall) integrationexecution.ConnectorResult {
	return connector.next(call, domain.AttemptExecute)
}

func (connector *Connector) Reconcile(_ context.Context, call integrationexecution.ConnectorCall) integrationexecution.ConnectorResult {
	return connector.next(call, domain.AttemptReconcile)
}

func (connector *Connector) next(call integrationexecution.ConnectorCall, mode domain.AttemptMode) integrationexecution.ConnectorResult {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.calls = append(connector.calls, Call{ExecutionID: call.Claim.ExecutionID, AttemptID: call.Claim.AttemptID, Mode: mode})
	steps := connector.scripts[call.Claim.ExecutionID]
	if len(steps) == 0 || steps[0].Mode != mode {
		return integrationexecution.ConnectorResult{Outcome: domain.AttemptFailed, ErrorCode: "mock_script_mismatch"}
	}
	step := steps[0]
	connector.scripts[call.Claim.ExecutionID] = steps[1:]
	return step.Result
}

func (connector *Connector) Calls() []Call {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	return append([]Call(nil), connector.calls...)
}

var _ integrationexecution.Connector = (*Connector)(nil)
