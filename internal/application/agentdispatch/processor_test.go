package agentdispatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
)

type dispatchClock struct{ now time.Time }

func (c dispatchClock) Now() time.Time { return c.now }

type dispatchIDs struct{ value string }

func (g dispatchIDs) New() string { return g.value }

type dispatchQueue struct {
	claim     Claim
	found     bool
	snapshot  Snapshot
	completed bool
	digest    [32]byte
	failed    bool
	retry     bool
	code      string
}

func (q *dispatchQueue) Claim(_ context.Context, lease string, _ time.Time, _ time.Duration) (Claim, bool, error) {
	q.claim.LeaseID = lease
	return q.claim, q.found, nil
}
func (q *dispatchQueue) Load(context.Context, Claim) (Snapshot, error) { return q.snapshot, nil }
func (q *dispatchQueue) Complete(_ context.Context, _ Claim, digest [32]byte, _ time.Time) error {
	q.completed, q.digest = true, digest
	return nil
}
func (q *dispatchQueue) Fail(_ context.Context, _ Claim, retry bool, _ time.Time, code string, _ time.Time, _ int) (string, error) {
	q.failed, q.retry, q.code = true, retry, code
	return "dead_letter", nil
}
func (*dispatchQueue) Stats(context.Context, time.Time) (Stats, error) { return Stats{}, nil }

type dispatchProvisioner struct {
	command runnerbroker.ProvisionCommand
	err     error
}

func (p *dispatchProvisioner) Provision(_ context.Context, command runnerbroker.ProvisionCommand) (bool, error) {
	p.command = command
	return true, p.err
}

func validSnapshot(t *testing.T, now time.Time) Snapshot {
	t.Helper()
	version, err := agentdomain.NewPersonaVersion(agentdomain.PersonaVersionDraft{
		ID: "51000000-0000-4000-8000-000000000001", PersonaID: "41000000-0000-4000-8000-000000000001",
		AccountID: "11000000-0000-4000-8000-000000000001", Version: 1, Name: "Operations Lead", Role: "Operations",
		Description: "Coordinates work.", SystemInstructions: "Coordinate operational work and report evidence clearly.",
		Policy: agentdomain.PersonaPolicy{Provider: "openai", Model: "gpt-test", FallbackModels: []string{"gpt-fallback"}, MaximumInputTokens: 100000, MaximumOutputTokens: 4000,
			MaximumToolSteps: 1, CitationPolicy: "best_effort", ActionPolicy: "propose", OutputSchema: agentdomain.ResultSchema(),
			Tools: []agentdomain.ToolGrant{{Name: "read_work", Capability: "work.summary.read", Description: "Read the Work summary.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)}}},
		CreatedBy: "21000000-0000-4000-8000-000000000001", CreatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	contextPayload := []byte(`{"schema_version":1,"items":[]}`)
	return Snapshot{AccountID: version.AccountID, InvocationID: "61000000-0000-4000-8000-000000000001", Profile: "agent-medium", QueuedAt: now.Add(-time.Minute), RequestExpiresAt: now.Add(time.Hour), Persona: version,
		Messages:          []modelgateway.Message{{Role: "user", Content: "What should we prioritize?"}},
		ModelOperationIDs: []string{"71000000-0000-4000-8000-000000000001", "71000000-0000-4000-8000-000000000002", "71000000-0000-4000-8000-000000000003", "71000000-0000-4000-8000-000000000004"}, ToolOperationIDs: []string{"81000000-0000-4000-8000-000000000001"},
		ContextPayload: contextPayload, ContextDigest: sha256.Sum256(contextPayload)}
}

func TestBuildFreezesCompiledTurnAndCapabilities(t *testing.T) {
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	command, digest, err := Build(validSnapshot(t, now), now)
	if err != nil || digest == ([32]byte{}) || command.Request.Kind != "agent.turn.execute" || command.Invocation.Profile != "agent-medium" {
		t.Fatalf("command=%+v digest=%x err=%v", command, digest, err)
	}
	if len(command.Request.Capabilities) != 2 || command.Request.Capabilities[0] != "agents.model.turn" || command.Request.Capabilities[1] != "work.summary.read" {
		t.Fatalf("capabilities=%v", command.Request.Capabilities)
	}
	var input struct {
		Provider         string   `json:"provider"`
		Models           []string `json:"models"`
		MaximumToolSteps int      `json:"maximum_tool_steps"`
	}
	if err := json.Unmarshal(command.Request.Input, &input); err != nil || input.Provider != "openai" || input.MaximumToolSteps != 1 || !slices.Equal(input.Models, []string{"gpt-test", "gpt-fallback"}) {
		t.Fatalf("input=%s decoded=%v err=%v", command.Request.Input, input, err)
	}
}

func TestProcessorProvisionsAndDigestBindsCompletion(t *testing.T) {
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	snapshot := validSnapshot(t, now)
	queue := &dispatchQueue{found: true, snapshot: snapshot, claim: Claim{AccountID: snapshot.AccountID, InvocationID: snapshot.InvocationID, Attempt: 1}}
	provisioner := &dispatchProvisioner{}
	processor, err := New(queue, provisioner, dispatchClock{now}, dispatchIDs{"91000000-0000-4000-8000-000000000001"}, DefaultLease, DefaultMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessOne(context.Background())
	if err != nil || !result.Provisioned || !queue.completed || queue.digest == ([32]byte{}) || provisioner.command.Invocation.ID != snapshot.InvocationID {
		t.Fatalf("result=%+v completed=%v digest=%x command=%+v err=%v", result, queue.completed, queue.digest, provisioner.command, err)
	}
}

func TestProcessorDeadLettersDeterministicProvisionConflict(t *testing.T) {
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	snapshot := validSnapshot(t, now)
	queue := &dispatchQueue{found: true, snapshot: snapshot, claim: Claim{AccountID: snapshot.AccountID, InvocationID: snapshot.InvocationID, Attempt: 1}}
	processor, _ := New(queue, &dispatchProvisioner{err: runnerbroker.ErrExchangeConflict}, dispatchClock{now}, dispatchIDs{"91000000-0000-4000-8000-000000000001"}, DefaultLease, DefaultMaxAttempts)
	result, err := processor.ProcessOne(context.Background())
	if !errors.Is(err, runnerbroker.ErrExchangeConflict) || !result.DeadLetter || !queue.failed || queue.retry || queue.code != "provision_rejected" {
		t.Fatalf("result=%+v failed=%v retry=%v code=%s err=%v", result, queue.failed, queue.retry, queue.code, err)
	}
}
