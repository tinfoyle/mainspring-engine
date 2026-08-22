package schedulequeueadmin

import (
	"context"
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const testBatch = "10000000-0000-4000-8000-000000000001"

type storeStub struct {
	queue  string
	limit  int
	change Change
	target Target
}

func (s *storeStub) Inspect(_ context.Context, queue string, limit int, change Change) ([]DeadLetter, error) {
	s.queue, s.limit, s.change = queue, limit, change
	return []DeadLetter{{Target: validTarget(queue)}}, nil
}

func (s *storeStub) Requeue(_ context.Context, target Target, change Change) (DeadLetter, error) {
	s.target, s.change = target, change
	return DeadLetter{Target: target, AttemptCount: 12, LastErrorCode: "dispatch_failed"}, nil
}

type fixedGenerator struct{}

func (fixedGenerator) New() string { return testBatch }

func validTarget(queue string) Target {
	target := Target{Queue: queue, AccountID: ids.AccountID("20000000-0000-4000-8000-000000000002"), ScheduleID: "30000000-0000-4000-8000-000000000003"}
	if queue == QueueTrigger {
		target.TriggerID = "40000000-0000-4000-8000-000000000004"
	}
	return target
}

func TestInspectIsQueueSpecificBoundedAndAudited(t *testing.T) {
	store := &storeStub{}
	service, _ := NewService(store, fixedGenerator{})
	result, err := service.Inspect(context.Background(), QueueTrigger, 0, " operator@example.com ", " investigate trigger failure ", "production")
	if err != nil || len(result.DeadLetters) != 1 || result.AuditBatchID != testBatch || store.queue != QueueTrigger || store.limit != DefaultInspectLimit || store.change.Actor != "operator@example.com" {
		t.Fatalf("result=%+v store=%+v err=%v", result, store, err)
	}
}

func TestRequeueRequiresExactQueueAndTarget(t *testing.T) {
	service, _ := NewService(&storeStub{}, fixedGenerator{})
	for name, target := range map[string]Target{
		"queue":             validTarget("other"),
		"account":           {Queue: QueueRecurring},
		"schedule":          {Queue: QueueRecurring, AccountID: ids.AccountID("20000000-0000-4000-8000-000000000002")},
		"recurring-trigger": {Queue: QueueRecurring, AccountID: ids.AccountID("20000000-0000-4000-8000-000000000002"), ScheduleID: "30000000-0000-4000-8000-000000000003", TriggerID: "40000000-0000-4000-8000-000000000004"},
		"trigger-id":        {Queue: QueueTrigger, AccountID: ids.AccountID("20000000-0000-4000-8000-000000000002"), ScheduleID: "30000000-0000-4000-8000-000000000003"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Requeue(context.Background(), target, "operator", "reviewed recovery", "production"); !errors.Is(err, ErrInvalidChange) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := service.Inspect(context.Background(), QueueRecurring, MaximumInspectLimit+1, "operator", "reviewed inspection", "production"); !errors.Is(err, ErrInvalidChange) {
		t.Fatalf("limit error=%v", err)
	}
}
