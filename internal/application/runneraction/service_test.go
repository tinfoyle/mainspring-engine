package runneraction

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fixedIDs struct{ value string }

func (g fixedIDs) New() string { return g.value }

type repository struct {
	begin      BeginCommand
	lease      runnercapability.ActionLease
	beginErr   error
	completion runnercapability.ActionCompletion
	finishErr  error
}

func (r *repository) Begin(_ context.Context, command BeginCommand) (runnercapability.ActionLease, error) {
	r.begin = command
	if r.beginErr != nil {
		return runnercapability.ActionLease{}, r.beginErr
	}
	lease := r.lease
	if lease.AttemptID == "" {
		request := command.Request
		lease = runnercapability.ActionLease{AccountID: request.AccountID, InvocationID: request.InvocationID, OperationID: request.OperationID, AttemptID: command.AttemptID, Capability: request.Capability, InputDigest: request.InputDigest, Mode: runnercapability.ActionExecute, IdempotencyKey: request.OperationID, LeaseExpiresAt: command.LeaseExpiresAt}
	}
	return lease, nil
}

func (r *repository) Complete(_ context.Context, completion runnercapability.ActionCompletion) error {
	r.completion = completion
	return r.finishErr
}

func fixture(t *testing.T) (*Service, *repository, fixedClock, runnercapability.ActionRequest) {
	t.Helper()
	clock := fixedClock{now: time.Date(2026, 8, 18, 17, 0, 0, 0, time.UTC)}
	repository := &repository{}
	service, err := New(repository, fixedIDs{value: "50000000-0000-4000-8000-000000000005"}, clock, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	request := runnercapability.ActionRequest{
		AccountID: "10000000-0000-4000-8000-000000000001", InvocationID: "20000000-0000-4000-8000-000000000002",
		PodUID: "30000000-0000-4000-8000-000000000003", OperationID: "40000000-0000-4000-8000-000000000004",
		Capability: "email.send", InputDigest: sha256.Sum256([]byte(`{"to":"redacted"}`)), ExpiresAt: clock.now.Add(time.Hour),
	}
	return service, repository, clock, request
}

func TestBeginCreatesBoundExecuteLease(t *testing.T) {
	service, repository, clock, request := fixture(t)
	lease, err := service.BeginAction(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Mode != runnercapability.ActionExecute || lease.IdempotencyKey != request.OperationID || lease.AttemptID != repository.begin.AttemptID || !lease.LeaseExpiresAt.Equal(clock.now.Add(2*time.Minute)) {
		t.Fatalf("unexpected lease: %#v", lease)
	}
	if repository.begin.Request.InputDigest != request.InputDigest || !repository.begin.Now.Equal(clock.now) {
		t.Fatalf("unexpected command: %#v", repository.begin)
	}
}

func TestBeginCapsLeaseAtInvocationExpiryAndRejectsMismatchedRepositoryLease(t *testing.T) {
	service, repository, clock, request := fixture(t)
	request.ExpiresAt = clock.now.Add(time.Minute)
	lease, err := service.BeginAction(context.Background(), request)
	if err != nil || !lease.LeaseExpiresAt.Equal(request.ExpiresAt) {
		t.Fatalf("lease=%#v err=%v", lease, err)
	}
	repository.lease = lease
	repository.lease.OperationID = "90000000-0000-4000-8000-000000000009"
	if _, err := service.BeginAction(context.Background(), request); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("expected state conflict, got %v", err)
	}
}

func TestDecisionErrorsHaveContentFreeStableCodes(t *testing.T) {
	for _, test := range []struct {
		cause error
		code  string
	}{
		{ErrApprovalRequired, "approval_required"}, {ErrApprovalExpired, "approval_expired"}, {ErrActionBusy, "action_in_progress"},
		{ErrActionDenied, "action_denied"}, {ErrStateConflict, "action_state_conflict"}, {errors.New("private database detail"), "action_repository_unavailable"},
	} {
		service, repository, _, request := fixture(t)
		repository.beginErr = test.cause
		_, err := service.BeginAction(context.Background(), request)
		var coded interface{ Code() string }
		if !errors.As(err, &coded) || coded.Code() != test.code || (test.cause != nil && test.cause != ErrRepository && test.code != "action_repository_unavailable" && !errors.Is(err, test.cause)) {
			t.Fatalf("cause=%v error=%v code=%v", test.cause, err, coded)
		}
	}
}

func TestCompleteValidatesOutcomeAndPreservesStableLease(t *testing.T) {
	service, repository, clock, request := fixture(t)
	lease, _ := service.BeginAction(context.Background(), request)
	completion := runnercapability.ActionCompletion{Lease: lease, Outcome: runnercapability.ActionUnknown, ErrorCode: "provider_timeout", At: clock.now.Add(time.Second)}
	if err := service.CompleteAction(context.Background(), completion); err != nil {
		t.Fatal(err)
	}
	if repository.completion.Outcome != runnercapability.ActionUnknown || repository.completion.Lease.IdempotencyKey != request.OperationID {
		t.Fatalf("completion=%#v", repository.completion)
	}
	completion.Outcome, completion.ErrorCode = runnercapability.ActionSucceeded, "provider_timeout"
	if err := service.CompleteAction(context.Background(), completion); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("accepted succeeded completion with error: %v", err)
	}
}

func TestInvalidConstructionAndRequestFailClosed(t *testing.T) {
	clock := fixedClock{now: time.Now().UTC()}
	if _, err := New(nil, fixedIDs{}, clock, time.Minute); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("missing repository=%v", err)
	}
	service, _, _, request := fixture(t)
	request.InputDigest = [sha256.Size]byte{}
	if _, err := service.BeginAction(context.Background(), request); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("zero digest=%v", err)
	}
	request.InputDigest = sha256.Sum256([]byte(`{}`))
	request.AccountID = ids.AccountID("cross-account")
	if _, err := service.BeginAction(context.Background(), request); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("invalid Account=%v", err)
	}
}
