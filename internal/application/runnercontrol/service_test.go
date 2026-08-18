package runnercontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type queueStub struct {
	item                                                   Invocation
	found                                                  bool
	launched, uncertain, confirmed, resolved, failed, dead bool
	completed                                              string
	launchedItems                                          []Invocation
	policy                                                 AccountPolicy
	cancellationState                                      string
	canceledAccount                                        string
	canceledInvocation                                     string
}

func (q *queueStub) Configure(_ context.Context, p AccountPolicy, _ time.Time) error {
	q.policy = p
	return nil
}
func (q *queueStub) Enqueue(context.Context, Invocation) (bool, error) { return true, nil }
func (q *queueStub) RequestCancellation(_ context.Context, accountID ids.AccountID, invocationID string, _ time.Time) (string, error) {
	q.canceledAccount, q.canceledInvocation = string(accountID), invocationID
	return q.cancellationState, nil
}
func (q *queueStub) ClaimFair(context.Context, time.Time, time.Duration) (Invocation, bool, error) {
	return q.item, q.found, nil
}
func (q *queueStub) MarkLaunched(_ context.Context, _ Invocation, _ string, _ time.Time) error {
	q.launched = true
	return nil
}
func (q *queueStub) MarkLaunchUncertain(_ context.Context, _ Invocation, _ string, _ time.Time, _ string) error {
	q.uncertain = true
	return nil
}
func (q *queueStub) FailLaunch(_ context.Context, _ Invocation, _, _ time.Time, _ string, dead bool) error {
	q.failed = true
	q.dead = dead
	return nil
}
func (q *queueStub) Complete(_ context.Context, _, _, outcome string, _ time.Time) error {
	q.completed = outcome
	return nil
}
func (q *queueStub) ClaimReconciliationCandidates(context.Context, time.Time, time.Duration, int) ([]Invocation, error) {
	return q.launchedItems, nil
}
func (q *queueStub) ConfirmLaunch(context.Context, Invocation, time.Time) error {
	q.confirmed = true
	return nil
}
func (q *queueStub) ResolveLaunchAbsent(_ context.Context, _ Invocation, _, _ time.Time, _ string, dead bool) error {
	q.resolved, q.dead = true, dead
	return nil
}
func (q *queueStub) Stats(context.Context, time.Time) (Stats, error) {
	return Stats{Ready: 1}, nil
}

type launcherStub struct {
	err, cancelErr error
	terminal       TerminalStatus
	cancelCalls    *int
}

func (l launcherStub) Ensure(context.Context, Invocation) (string, error) {
	return "runner-invocation", l.err
}
func (l launcherStub) Cancel(context.Context, Invocation) error {
	if l.cancelCalls != nil {
		(*l.cancelCalls)++
	}
	return l.cancelErr
}
func (l launcherStub) Inspect(context.Context, Invocation) (TerminalStatus, error) {
	return l.terminal, l.err
}

func TestCancellationIsAccountBoundAndValidated(t *testing.T) {
	queue := &queueStub{cancellationState: "canceling"}
	service, _ := NewService(queue, launcherStub{}, clockStub{time.Now()}, time.Minute, 3)
	const accountID = "20000000-0000-4000-8000-000000000002"
	const invocationID = "10000000-0000-4000-8000-000000000001"
	state, err := service.RequestCancellation(context.Background(), accountID, invocationID)
	if err != nil || state != "canceling" || queue.canceledAccount != accountID || queue.canceledInvocation != invocationID {
		t.Fatalf("state=%q account=%q invocation=%q err=%v", state, queue.canceledAccount, queue.canceledInvocation, err)
	}
	if _, err := service.RequestCancellation(context.Background(), "bad", invocationID); !errors.Is(err, ErrInvalidInvocation) {
		t.Fatalf("invalid cancellation err=%v", err)
	}
}

type clockStub struct{ now time.Time }

func (c clockStub) Now() time.Time { return c.now }

func TestControllerLaunchesOrDurablyRetriesOneClaim(t *testing.T) {
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	item := Invocation{ID: "10000000-0000-4000-8000-000000000001", AccountID: "20000000-0000-4000-8000-000000000002", Profile: "agent-small", AttemptCount: 1}
	queue := &queueStub{item: item, found: true}
	service, _ := NewService(queue, launcherStub{}, clockStub{now}, time.Minute, 3)
	if worked, err := service.ProcessOne(context.Background()); err != nil || !worked || !queue.launched {
		t.Fatalf("worked=%v launched=%v err=%v", worked, queue.launched, err)
	}
	queue = &queueStub{item: Invocation{ID: item.ID, AccountID: item.AccountID, Profile: item.Profile, AttemptCount: 3}, found: true}
	service, _ = NewService(queue, launcherStub{err: errors.New("cluster unavailable")}, clockStub{now}, time.Minute, 3)
	if worked, err := service.ProcessOne(context.Background()); !worked || err == nil || !queue.failed || !queue.dead {
		t.Fatalf("worked=%v failed=%v dead=%v err=%v", worked, queue.failed, queue.dead, err)
	}
}

func TestControllerRetainsCapacityForUncertainLaunch(t *testing.T) {
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	item := Invocation{ID: "10000000-0000-4000-8000-000000000001", AccountID: "20000000-0000-4000-8000-000000000002", Profile: "agent-small", AttemptCount: 1}
	queue := &queueStub{item: item, found: true}
	service, _ := NewService(queue, launcherStub{err: ErrLaunchUncertain}, clockStub{now}, time.Minute, 3)
	if worked, err := service.ProcessOne(context.Background()); !worked || !errors.Is(err, ErrLaunchUncertain) || !queue.uncertain || queue.failed {
		t.Fatalf("worked=%v uncertain=%v failed=%v err=%v", worked, queue.uncertain, queue.failed, err)
	}
}

func TestAccountPolicyIsBounded(t *testing.T) {
	queue := &queueStub{}
	service, _ := NewService(queue, launcherStub{}, clockStub{time.Now()}, time.Minute, 3)
	if err := service.Configure(context.Background(), AccountPolicy{AccountID: "20000000-0000-4000-8000-000000000002", Weight: 2, ConcurrencyLimit: 4}); err != nil || queue.policy.Weight != 2 {
		t.Fatalf("policy=%+v err=%v", queue.policy, err)
	}
	if err := service.Configure(context.Background(), AccountPolicy{AccountID: "bad", Weight: 1, ConcurrencyLimit: 1}); err == nil {
		t.Fatal("invalid Account accepted")
	}
}

func TestCompletionValidatesTerminalOutcome(t *testing.T) {
	queue := &queueStub{}
	service, _ := NewService(queue, launcherStub{}, clockStub{time.Now()}, time.Minute, 3)
	const invocationID = "10000000-0000-4000-8000-000000000001"
	if err := service.Complete(context.Background(), invocationID, "runner-1", "completed"); err != nil || queue.completed != "completed" {
		t.Fatalf("completed=%q err=%v", queue.completed, err)
	}
	if err := service.Complete(context.Background(), invocationID, "runner-1", "failed"); !errors.Is(err, ErrInvalidInvocation) {
		t.Fatalf("invalid outcome err=%v", err)
	}
}

func TestReconcileJobsCompletesOnlyTerminalJobs(t *testing.T) {
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	queue := &queueStub{launchedItems: []Invocation{{ID: "10000000-0000-4000-8000-000000000001", JobName: "runner-1"}}}
	service, _ := NewService(queue, launcherStub{terminal: TerminalStatus{Terminal: true, Outcome: "completed"}}, clockStub{now}, time.Minute, 3)
	if completed, err := service.ReconcileJobs(context.Background(), 10); err != nil || completed != 1 || queue.completed != "completed" {
		t.Fatalf("completed=%d outcome=%q err=%v", completed, queue.completed, err)
	}
	queue.completed = ""
	service, _ = NewService(queue, launcherStub{}, clockStub{now}, time.Minute, 3)
	if completed, err := service.ReconcileJobs(context.Background(), 10); err != nil || completed != 0 || queue.completed != "" {
		t.Fatalf("nonterminal completed=%d outcome=%q err=%v", completed, queue.completed, err)
	}
}

func TestReconcileJobsDeletesCancelingJobBeforeCapacityRelease(t *testing.T) {
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	queue := &queueStub{launchedItems: []Invocation{{ID: "10000000-0000-4000-8000-000000000001", State: "canceling", JobName: "runner-1"}}}
	cancelCalls := 0
	service, _ := NewService(queue, launcherStub{cancelCalls: &cancelCalls, terminal: TerminalStatus{Terminal: true, Outcome: "canceled"}}, clockStub{now}, time.Minute, 3)
	if completed, err := service.ReconcileJobs(context.Background(), 10); err != nil || completed != 1 || queue.completed != "canceled" || cancelCalls != 1 {
		t.Fatalf("completed=%d outcome=%q cancel_calls=%d err=%v", completed, queue.completed, cancelCalls, err)
	}
}

func TestReconcileJobsRetainsCapacityWhenCancellationFails(t *testing.T) {
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	queue := &queueStub{launchedItems: []Invocation{{ID: "10000000-0000-4000-8000-000000000001", State: "canceling", JobName: "runner-1"}}}
	cancelCalls := 0
	cancelErr := errors.New("Kubernetes delete unavailable")
	service, _ := NewService(queue, launcherStub{cancelCalls: &cancelCalls, cancelErr: cancelErr, terminal: TerminalStatus{Terminal: true, Outcome: "canceled"}}, clockStub{now}, time.Minute, 3)
	if completed, err := service.ReconcileJobs(context.Background(), 10); completed != 0 || !errors.Is(err, cancelErr) || queue.completed != "" || cancelCalls != 1 {
		t.Fatalf("completed=%d outcome=%q cancel_calls=%d err=%v", completed, queue.completed, cancelCalls, err)
	}
}

func TestReconcileJobsResolvesUncertainLaunchByObservation(t *testing.T) {
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	uncertain := Invocation{ID: "10000000-0000-4000-8000-000000000001", State: "launch_uncertain", JobName: "runner-1", AttemptCount: 1}
	queue := &queueStub{launchedItems: []Invocation{uncertain}}
	service, _ := NewService(queue, launcherStub{}, clockStub{now}, time.Minute, 3)
	if reconciled, err := service.ReconcileJobs(context.Background(), 10); err != nil || reconciled != 1 || !queue.resolved || queue.confirmed {
		t.Fatalf("absent reconciled=%d resolved=%v confirmed=%v err=%v", reconciled, queue.resolved, queue.confirmed, err)
	}

	queue = &queueStub{launchedItems: []Invocation{uncertain}}
	service, _ = NewService(queue, launcherStub{terminal: TerminalStatus{Observed: true}}, clockStub{now}, time.Minute, 3)
	if reconciled, err := service.ReconcileJobs(context.Background(), 10); err != nil || reconciled != 1 || queue.resolved || !queue.confirmed {
		t.Fatalf("observed reconciled=%d resolved=%v confirmed=%v err=%v", reconciled, queue.resolved, queue.confirmed, err)
	}
}
