package runnercontrol

import (
	"context"
	"errors"
	"testing"
	"time"
)

type queueStub struct {
	item                   Invocation
	found                  bool
	launched, failed, dead bool
	completed              string
	launchedItems          []Invocation
	policy                 AccountPolicy
}

func (q *queueStub) Configure(_ context.Context, p AccountPolicy, _ time.Time) error {
	q.policy = p
	return nil
}
func (q *queueStub) Enqueue(context.Context, Invocation) (bool, error) { return true, nil }
func (q *queueStub) ClaimFair(context.Context, time.Time, time.Duration) (Invocation, bool, error) {
	return q.item, q.found, nil
}
func (q *queueStub) MarkLaunched(_ context.Context, _ Invocation, _ string, _ time.Time) error {
	q.launched = true
	return nil
}
func (q *queueStub) FailLaunch(_ context.Context, _ Invocation, _ time.Time, _ string, dead bool) error {
	q.failed = true
	q.dead = dead
	return nil
}
func (q *queueStub) Complete(_ context.Context, _, _, outcome string, _ time.Time) error {
	q.completed = outcome
	return nil
}
func (q *queueStub) ClaimLaunched(context.Context, time.Time, time.Duration, int) ([]Invocation, error) {
	return q.launchedItems, nil
}
func (q *queueStub) Stats(context.Context, time.Time) (Stats, error) {
	return Stats{Ready: 1}, nil
}

type launcherStub struct {
	err      error
	terminal TerminalStatus
}

func (l launcherStub) Ensure(context.Context, Invocation) (string, error) {
	return "runner-invocation", l.err
}
func (l launcherStub) Inspect(context.Context, Invocation) (TerminalStatus, error) {
	return l.terminal, l.err
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

func TestReconcileLaunchedCompletesOnlyTerminalJobs(t *testing.T) {
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	queue := &queueStub{launchedItems: []Invocation{{ID: "10000000-0000-4000-8000-000000000001", JobName: "runner-1"}}}
	service, _ := NewService(queue, launcherStub{terminal: TerminalStatus{Terminal: true, Outcome: "completed"}}, clockStub{now}, time.Minute, 3)
	if completed, err := service.ReconcileLaunched(context.Background(), 10); err != nil || completed != 1 || queue.completed != "completed" {
		t.Fatalf("completed=%d outcome=%q err=%v", completed, queue.completed, err)
	}
	queue.completed = ""
	service, _ = NewService(queue, launcherStub{}, clockStub{now}, time.Minute, 3)
	if completed, err := service.ReconcileLaunched(context.Background(), 10); err != nil || completed != 0 || queue.completed != "" {
		t.Fatalf("nonterminal completed=%d outcome=%q err=%v", completed, queue.completed, err)
	}
}
