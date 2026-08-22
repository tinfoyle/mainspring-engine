package approvedaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type repositoryStub struct {
	claim       Claim
	found       bool
	claimErr    error
	completeErr error
	completion  runnercapability.ActionCompletion
}

func (stub *repositoryStub) Claim(context.Context, string, time.Time, time.Time) (Claim, bool, error) {
	return stub.claim, stub.found, stub.claimErr
}
func (stub *repositoryStub) Complete(_ context.Context, completion runnercapability.ActionCompletion) error {
	stub.completion = completion
	return stub.completeErr
}

type clockStub struct{ now time.Time }

func (stub clockStub) Now() time.Time { return stub.now }

type idStub string

func (stub idStub) New() string { return string(stub) }

type handlerStub struct {
	executed, reconciled int
	executeErr           error
	reconcileOutcome     runnercapability.ActionOutcome
	reconcileErr         error
}

func (stub *handlerStub) Execute(_ context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, error) {
	stub.executed++
	if call.ApprovedByUserID == "" || call.Action == nil {
		return nil, errors.New("approval binding missing")
	}
	return json.RawMessage(`{"ok":true}`), stub.executeErr
}
func (stub *handlerStub) Reconcile(context.Context, runnercapability.AuthorizedCall) (json.RawMessage, runnercapability.ActionOutcome, error) {
	stub.reconciled++
	return json.RawMessage(`{"ok":true}`), stub.reconcileOutcome, stub.reconcileErr
}

type definitiveFailure struct{}

func (definitiveFailure) Error() string    { return "provider_rejected" }
func (definitiveFailure) Code() string     { return "provider_rejected" }
func (definitiveFailure) Definitive() bool { return true }

func TestProcessOneExecutesExactApprovedPayload(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	repository := &repositoryStub{found: true, claim: validClaim(now, runnercapability.ActionExecute)}
	handler := &handlerStub{}
	service, err := New(repository, idStub(repository.claim.Lease.AttemptID), clockStub{now}, time.Minute,
		[]Definition{{Capability: repository.claim.Lease.Capability, Timeout: time.Second, Handler: handler}})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.ProcessOne(context.Background())
	if err != nil || !worked || handler.executed != 1 || handler.reconciled != 0 || repository.completion.Outcome != runnercapability.ActionSucceeded {
		t.Fatalf("worked=%v executed=%d reconciled=%d completion=%+v err=%v", worked, handler.executed, handler.reconciled, repository.completion, err)
	}
}

func TestProcessOneNeverReexecutesReconciliationLease(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	repository := &repositoryStub{found: true, claim: validClaim(now, runnercapability.ActionReconcile)}
	handler := &handlerStub{reconcileOutcome: runnercapability.ActionUnknown}
	service, _ := New(repository, idStub(repository.claim.Lease.AttemptID), clockStub{now}, time.Minute,
		[]Definition{{Capability: repository.claim.Lease.Capability, Timeout: time.Second, Handler: handler}})
	worked, err := service.ProcessOne(context.Background())
	if !worked || !errors.Is(err, ErrUnavailable) || handler.executed != 0 || handler.reconciled != 1 ||
		repository.completion.Outcome != runnercapability.ActionUnknown || repository.completion.ErrorCode != "action_unknown" {
		t.Fatalf("worked=%v executed=%d reconciled=%d completion=%+v err=%v", worked, handler.executed, handler.reconciled, repository.completion, err)
	}
}

func TestProcessOneSettlesDefinitiveFailureForPolicyRetry(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	repository := &repositoryStub{found: true, claim: validClaim(now, runnercapability.ActionExecute)}
	handler := &handlerStub{executeErr: definitiveFailure{}}
	service, _ := New(repository, idStub(repository.claim.Lease.AttemptID), clockStub{now}, time.Minute,
		[]Definition{{Capability: repository.claim.Lease.Capability, Timeout: time.Second, Handler: handler}})
	worked, err := service.ProcessOne(context.Background())
	if !worked || err == nil || repository.completion.Outcome != runnercapability.ActionFailed || repository.completion.ErrorCode != "provider_rejected" {
		t.Fatalf("worked=%v completion=%+v err=%v", worked, repository.completion, err)
	}
}

func TestProcessOneCannotSettleReconciliationSuccessWithAnError(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	repository := &repositoryStub{found: true, claim: validClaim(now, runnercapability.ActionReconcile)}
	handler := &handlerStub{reconcileOutcome: runnercapability.ActionSucceeded, reconcileErr: errors.New("uncertain lookup")}
	service, _ := New(repository, idStub(repository.claim.Lease.AttemptID), clockStub{now}, time.Minute,
		[]Definition{{Capability: repository.claim.Lease.Capability, Timeout: time.Second, Handler: handler}})
	worked, err := service.ProcessOne(context.Background())
	if !worked || err == nil || repository.completion.Outcome != runnercapability.ActionUnknown || repository.completion.ErrorCode != "execution_failed" {
		t.Fatalf("worked=%v completion=%+v err=%v", worked, repository.completion, err)
	}
}

func TestProcessOneRejectsPayloadDigestMismatchWithoutExecution(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	claim := validClaim(now, runnercapability.ActionExecute)
	claim.CanonicalPayload = json.RawMessage(`{"changed":true}`)
	repository := &repositoryStub{found: true, claim: claim}
	handler := &handlerStub{}
	service, _ := New(repository, idStub(claim.Lease.AttemptID), clockStub{now}, time.Minute,
		[]Definition{{Capability: claim.Lease.Capability, Timeout: time.Second, Handler: handler}})
	worked, err := service.ProcessOne(context.Background())
	if !worked || !errors.Is(err, ErrInvalidClaim) || handler.executed != 0 || repository.completion.Lease.OperationID != "" {
		t.Fatalf("worked=%v executed=%d completion=%+v err=%v", worked, handler.executed, repository.completion, err)
	}
}

func validClaim(now time.Time, mode runnercapability.ActionMode) Claim {
	payload := json.RawMessage(`{"entry_id":"51000000-0000-4000-8000-000000000001","expected_version":1}`)
	digest := sha256.Sum256(payload)
	return Claim{CanonicalPayload: payload, ApprovedByUserID: ids.UserID("61000000-0000-4000-8000-000000000001"), Lease: runnercapability.ActionLease{
		AccountID: "11000000-0000-4000-8000-000000000001", InvocationID: "21000000-0000-4000-8000-000000000001",
		OperationID: "31000000-0000-4000-8000-000000000001", AttemptID: "41000000-0000-4000-8000-000000000001",
		Capability: "finance.entry.post", InputDigest: digest, Mode: mode,
		IdempotencyKey: "31000000-0000-4000-8000-000000000001", LeaseExpiresAt: now.Add(time.Minute),
	}}
}
