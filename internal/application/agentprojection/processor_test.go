package agentprojection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/agentresultpolicy"
	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"github.com/tinfoyle/spyglass-engine/internal/application/runneragents"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type projectionClock struct{ now time.Time }

func (c projectionClock) Now() time.Time { return c.now }

type projectionIDs struct{ values []string }

func (g *projectionIDs) New() string {
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

type projectionQueue struct {
	claim          Claim
	found          bool
	claimErr       error
	projectErr     error
	success        *Success
	failure        *Failure
	failed         bool
	retry          bool
	failureCode    string
	failureState   string
	failureNext    time.Time
	claimedLeaseID string
}

func (q *projectionQueue) Claim(_ context.Context, leaseID string, _ time.Time, _ time.Duration) (Claim, bool, error) {
	q.claimedLeaseID = leaseID
	q.claim.LeaseID = leaseID
	return q.claim, q.found, q.claimErr
}
func (q *projectionQueue) ProjectSuccess(_ context.Context, value Success) error {
	q.success = &value
	return q.projectErr
}
func (q *projectionQueue) ProjectFailure(_ context.Context, value Failure) error {
	q.failure = &value
	return q.projectErr
}
func (q *projectionQueue) Fail(_ context.Context, _ Claim, retry bool, next time.Time, code string, _ time.Time, _ int) (string, error) {
	q.failed, q.retry, q.failureCode, q.failureNext = true, retry, code, next
	if q.failureState == "" {
		q.failureState = "dead_letter"
	}
	return q.failureState, nil
}
func (q *projectionQueue) Stats(context.Context, time.Time) (Stats, error) { return Stats{}, nil }

type resultVerifier struct{ identity runnerbroker.Identity }

func (v resultVerifier) Verify(context.Context, string, string) (runnerbroker.Identity, error) {
	return v.identity, nil
}

type resultRepository struct{ result runnerbroker.StoredResult }

func (*resultRepository) Provision(context.Context, runnercontrol.Invocation, runnerbroker.StoredRequest) (bool, error) {
	return false, nil
}
func (*resultRepository) Claim(context.Context, runnerbroker.Identity, time.Time) (runnerbroker.StoredRequest, error) {
	return runnerbroker.StoredRequest{}, nil
}
func (r *resultRepository) Submit(_ context.Context, _ runnerbroker.Identity, value runnerbroker.StoredResult, _ time.Time) (bool, error) {
	r.result = value
	return true, nil
}

func storedProjectionResult(t *testing.T, now time.Time, outcome string, output json.RawMessage, code string) (*runnerbroker.Cipher, runnerbroker.StoredResult) {
	t.Helper()
	cipher, err := runnerbroker.NewCipher(map[int][]byte{1: bytes.Repeat([]byte{0x31}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	identity := runnerbroker.Identity{InvocationID: "11000000-0000-4000-8000-000000000001", Profile: "agent-small", JobName: "runner", JobUID: "21000000-0000-4000-8000-000000000001", PodName: "runner-pod", PodUID: "31000000-0000-4000-8000-000000000001"}
	repository := &resultRepository{}
	service, err := runnerbroker.NewService(repository, resultVerifier{identity}, cipher, projectionClock{now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), "token", identity.InvocationID, runnerbroker.Result{SchemaVersion: 1, Outcome: outcome, Output: output, ErrorCode: code}); err != nil {
		t.Fatal(err)
	}
	return cipher, repository.result
}

func validTurnOutput(t *testing.T) json.RawMessage {
	t.Helper()
	result := agents.ResultEnvelope{Contribution: "Prioritize the oldest blocked work.", Findings: []string{}, Recommendations: []string{"Review aging daily."}, Questions: []string{}, Citations: []agents.Citation{}, ProposedActions: []agents.ProposedAction{}, Delegations: []agents.Delegation{}, Confidence: agents.ConfidenceHigh}
	raw, err := json.Marshal(runneragents.TurnOutput{Provider: "openai", RequestedModel: "gpt-test", ResponseModel: "gpt-test-2026", ResponseID: "resp_123", Usage: modelgateway.Usage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14}, Result: result})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testProcessor(t *testing.T, queue *projectionQueue, cipher *runnerbroker.Cipher, now time.Time) *Processor {
	t.Helper()
	processor, err := New(queue, cipher, projectionClock{now}, &projectionIDs{values: []string{"41000000-0000-4000-8000-000000000001", "51000000-0000-4000-8000-000000000001"}}, DefaultLease, DefaultMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

func validProjectionClaim(stored runnerbroker.StoredResult, attempt int) Claim {
	return Claim{AccountID: ids.AccountID("61000000-0000-4000-8000-000000000001"), InvocationID: stored.InvocationID, Attempt: attempt,
		ExpectedProvider: "openai", RequestedModel: "gpt-test", PermittedModels: []string{"gpt-test"}, ResultPolicyVersion: 1,
		CitationPolicy: "best_effort", ActionPolicy: "propose", CurrentPersonaID: ids.PersonaID("71000000-0000-4000-8000-000000000001"), Result: stored}
}

func TestProcessorProjectsValidatedCompletedTurn(t *testing.T) {
	now := time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC)
	cipher, stored := storedProjectionResult(t, now.Add(-time.Second), "completed", validTurnOutput(t), "")
	queue := &projectionQueue{found: true, claim: validProjectionClaim(stored, 1)}
	result, err := testProcessor(t, queue, cipher, now).ProcessOne(context.Background())
	if err != nil || !result.Projected || queue.success == nil || queue.failure != nil || queue.failed {
		t.Fatalf("result=%+v success=%+v failure=%+v failed=%v err=%v", result, queue.success, queue.failure, queue.failed, err)
	}
	if queue.success.MessageID != "51000000-0000-4000-8000-000000000001" || queue.success.Body != "Prioritize the oldest blocked work." || queue.success.TotalTokens != 14 || queue.success.ResultDigest == ([32]byte{}) {
		t.Fatalf("unexpected success projection: %+v", queue.success)
	}
}

func TestProcessorProjectsPolicyV2ActionsAsDeterministicApprovals(t *testing.T) {
	now := time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC)
	var turn runneragents.TurnOutput
	if err := json.Unmarshal(validTurnOutput(t), &turn); err != nil {
		t.Fatal(err)
	}
	turn.Result.ProposedActions = []agents.ProposedAction{{Kind: "work.create", Reason: "Track the follow-up", Payload: json.RawMessage(`{"title":"Follow up"}`), Evidence: []string{"finding-1"}}}
	raw, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	cipher, stored := storedProjectionResult(t, now.Add(-time.Second), "completed", raw, "")
	claim := validProjectionClaim(stored, 1)
	claim.ResultPolicyVersion = agentresultpolicy.CurrentVersion
	claim.ActionCapabilities = []string{"work.create"}
	queue := &projectionQueue{found: true, claim: claim}
	result, err := testProcessor(t, queue, cipher, now).ProcessOne(context.Background())
	if err != nil || !result.Projected || queue.success == nil || len(queue.success.Proposals) != 1 {
		t.Fatalf("result=%+v success=%+v err=%v", result, queue.success, err)
	}
	proposal := queue.success.Proposals[0]
	wantID, _ := ids.Derive(stored.InvocationID, "action/1/approval")
	if proposal.ApprovalID != wantID || proposal.Capability != "work.create" || proposal.ProposerID != "agent:"+string(claim.CurrentPersonaID) ||
		proposal.PolicyVersion != agentresultpolicy.CurrentVersion || proposal.ExpiresAt != stored.SubmittedAt.Add(24*time.Hour) || proposal.InputDigest == ([32]byte{}) || proposal.EvidenceDigest == ([32]byte{}) {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
}

func TestProcessorProjectsWorkQuestionsAsDeterministicInformationRequests(t *testing.T) {
	now := time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC)
	var turn runneragents.TurnOutput
	if err := json.Unmarshal(validTurnOutput(t), &turn); err != nil {
		t.Fatal(err)
	}
	turn.Result.Questions = []string{"Which system is the operational source of truth?"}
	raw, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	cipher, stored := storedProjectionResult(t, now.Add(-time.Second), "completed", raw, "")
	claim := validProjectionClaim(stored, 1)
	claim.ResultPolicyVersion = agentresultpolicy.CurrentVersion
	claim.WorkItemID = ids.WorkItemID("81000000-0000-4000-8000-000000000001")
	queue := &projectionQueue{found: true, claim: claim}
	result, err := testProcessor(t, queue, cipher, now).ProcessOne(context.Background())
	if err != nil || !result.Projected || queue.success == nil || len(queue.success.InformationRequests) != 1 {
		t.Fatalf("result=%+v success=%+v err=%v", result, queue.success, err)
	}
	request := queue.success.InformationRequests[0]
	wantRequestID, _ := ids.Derive(stored.InvocationID, "question/1/request")
	wantEventID, _ := ids.Derive(stored.InvocationID, "question/1/event")
	wantWorkEventID, _ := ids.Derive(stored.InvocationID, "questions/work-event")
	if request.RequestID != wantRequestID || request.EventID != wantEventID || request.Question != turn.Result.Questions[0] ||
		request.FactKey != "agent.owner_question.30408080bd1a993a6db4ecc104a77b56" ||
		request.RequesterID != "agent:"+string(claim.CurrentPersonaID) || queue.success.WorkEventID != wantWorkEventID {
		t.Fatalf("unexpected information request=%+v work_event=%s", request, queue.success.WorkEventID)
	}

	// Boardroom-only turns remain conversational. Questions are published in
	// the result but cannot fabricate a Work/Attention lifecycle.
	directClaim := claim
	directClaim.WorkItemID = ""
	directQueue := &projectionQueue{found: true, claim: directClaim}
	result, err = testProcessor(t, directQueue, cipher, now).ProcessOne(context.Background())
	if err != nil || !result.Projected || directQueue.success == nil || len(directQueue.success.InformationRequests) != 0 || directQueue.success.WorkEventID != "" {
		t.Fatalf("direct result=%+v success=%+v err=%v", result, directQueue.success, err)
	}
}

func TestProcessorProjectsRunnerExecutionFailureWithoutModelPayload(t *testing.T) {
	now := time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC)
	cipher, stored := storedProjectionResult(t, now.Add(-time.Second), "execution_failed", json.RawMessage(`{"retryable":false}`), "provider_denied")
	queue := &projectionQueue{found: true, claim: validProjectionClaim(stored, 1)}
	result, err := testProcessor(t, queue, cipher, now).ProcessOne(context.Background())
	if err != nil || !result.Projected || queue.failure == nil || queue.failure.FailureCode != "provider_denied" || queue.success != nil {
		t.Fatalf("result=%+v failure=%+v success=%+v err=%v", result, queue.failure, queue.success, err)
	}
}

func TestProcessorDeadLettersTamperedOrSemanticallyInvalidResult(t *testing.T) {
	now := time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC)
	cipher, stored := storedProjectionResult(t, now.Add(-time.Second), "completed", validTurnOutput(t), "")
	stored.Ciphertext[0] ^= 0xff
	queue := &projectionQueue{found: true, claim: validProjectionClaim(stored, 1)}
	result, err := testProcessor(t, queue, cipher, now).ProcessOne(context.Background())
	if !errors.Is(err, ErrInvalidPayload) || !result.DeadLetter || queue.failureCode != "result_envelope_invalid" || queue.retry {
		t.Fatalf("tampered result=%+v code=%s retry=%v err=%v", result, queue.failureCode, queue.retry, err)
	}
}

func TestProcessorDeadLettersResultDeniedByFrozenPolicy(t *testing.T) {
	now := time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC)
	var turn runneragents.TurnOutput
	if err := json.Unmarshal(validTurnOutput(t), &turn); err != nil {
		t.Fatal(err)
	}
	turn.Result.ProposedActions = []agents.ProposedAction{{Kind: "work.create", Reason: "Track the follow-up", Payload: json.RawMessage(`{}`), Evidence: []string{}}}
	raw, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	cipher, stored := storedProjectionResult(t, now.Add(-time.Second), "completed", raw, "")
	claim := validProjectionClaim(stored, 1)
	claim.ActionPolicy = "none"
	queue := &projectionQueue{found: true, claim: claim}
	result, err := testProcessor(t, queue, cipher, now).ProcessOne(context.Background())
	if !errors.Is(err, ErrInvalidPayload) || !result.DeadLetter || queue.failureCode != "result_policy_denied" || queue.retry {
		t.Fatalf("result=%+v code=%s retry=%v err=%v", result, queue.failureCode, queue.retry, err)
	}
}

func TestProcessorRetriesProjectionFailureWithBoundedBackoff(t *testing.T) {
	now := time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC)
	cipher, stored := storedProjectionResult(t, now.Add(-time.Second), "completed", validTurnOutput(t), "")
	queue := &projectionQueue{found: true, projectErr: errors.New("database unavailable"), failureState: "retry", claim: validProjectionClaim(stored, 3)}
	result, err := testProcessor(t, queue, cipher, now).ProcessOne(context.Background())
	if err == nil || !result.Worked || result.DeadLetter || !queue.retry || queue.failureCode != "success_projection_failed" || queue.failureNext != now.Add(4*time.Second) {
		t.Fatalf("result=%+v retry=%v code=%s next=%s err=%v", result, queue.retry, queue.failureCode, queue.failureNext, err)
	}
}
