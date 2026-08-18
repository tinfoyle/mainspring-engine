package routeretention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestProcessorClaimsAndPrunesBoundedReceiptBatch(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	queue := &queueStub{job: validJob(), found: true, pruned: 23}
	processor, err := NewProcessor(queue, fixedClock{now}, time.Minute, 5*time.Minute, 200)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessOne(context.Background())
	if err != nil || !result.Worked || result.Pruned != 23 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if queue.pruneNow != now || queue.retention != 5*time.Minute || queue.batch != 200 {
		t.Fatalf("prune now=%v retention=%v batch=%d", queue.pruneNow, queue.retention, queue.batch)
	}
}

func TestProcessorFailsAndReschedulesInvalidOrFailedJobs(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	for name, queue := range map[string]*queueStub{
		"invalid": {job: Job{}, found: true},
		"failed":  {job: validJob(), found: true, pruneErr: errors.New("database unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			processor, _ := NewProcessor(queue, fixedClock{now}, time.Minute, 5*time.Minute, 200)
			result, err := processor.ProcessOne(context.Background())
			if err == nil || !result.Worked || queue.failCode == "" || !queue.failAt.After(now) {
				t.Fatalf("result=%+v err=%v fail=%s at=%v", result, err, queue.failCode, queue.failAt)
			}
		})
	}
}

func TestProcessorRejectsUnsafeRetentionBounds(t *testing.T) {
	for name, test := range map[string]struct {
		retention time.Duration
		batch     int
	}{
		"short retention": {time.Second, 100},
		"long retention":  {25 * time.Hour, 100},
		"empty batch":     {time.Minute, 0},
		"large batch":     {time.Minute, MaximumPruneBatch + 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewProcessor(&queueStub{}, fixedClock{time.Now()}, time.Minute, test.retention, test.batch); err == nil {
				t.Fatal("expected invalid retention configuration")
			}
		})
	}
}

func validJob() Job {
	return Job{AccountID: ids.AccountID("10000000-0000-4000-8000-000000000001"), LeaseID: "20000000-0000-4000-8000-000000000002", Attempt: 1, ScheduleVersion: 1, DueAt: time.Now()}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type queueStub struct {
	job       Job
	found     bool
	claimErr  error
	pruned    int64
	pruneErr  error
	pruneNow  time.Time
	retention time.Duration
	batch     int
	failAt    time.Time
	failCode  string
}

func (q *queueStub) Claim(context.Context, time.Time, time.Duration) (Job, bool, error) {
	return q.job, q.found, q.claimErr
}
func (q *queueStub) Prune(_ context.Context, _ Job, now time.Time, retention time.Duration, batch int) (int64, error) {
	q.pruneNow, q.retention, q.batch = now, retention, batch
	return q.pruned, q.pruneErr
}
func (q *queueStub) Fail(_ context.Context, _ Job, next time.Time, code string) error {
	q.failAt, q.failCode = next, code
	return nil
}
func (q *queueStub) Stats(context.Context, time.Time) (Stats, error) { return Stats{}, nil }
