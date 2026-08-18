package workreconciliation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	reconcileAccount     = "10000000-0000-4000-8000-000000000001"
	reconcileItem        = "20000000-0000-4000-8000-000000000002"
	reconcileReservation = "30000000-0000-4000-8000-000000000003"
	reconcileLease       = "40000000-0000-4000-8000-000000000004"
)

func TestProcessorReleasesAndCheckpointsJob(t *testing.T) {
	now := time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC)
	queue := &queueStub{job: validJob(now), found: true}
	capacity := &capacityStub{}
	processor, err := NewProcessor(queue, capacity, clockStub{now}, time.Minute, 12)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || !queue.completed || capacity.accountID != reconcileAccount || capacity.requestID != reconcileReservation || !capacity.at.Equal(now) {
		t.Fatalf("worked=%v err=%v queue=%+v capacity=%+v", worked, err, queue, capacity)
	}
}

func TestProcessorRetriesTransientReleaseAndDeadLettersCorruption(t *testing.T) {
	now := time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC)
	for name, testCase := range map[string]struct {
		err     error
		attempt int
		code    string
		dead    bool
	}{
		"transient": {errors.New("global database unavailable"), 2, "global_release_unavailable", false},
		"exhausted": {errors.New("global database unavailable"), 12, "global_release_unavailable", true},
		"missing":   {usageadmission.ErrInvalidRequest, 1, "reservation_not_found", true},
	} {
		t.Run(name, func(t *testing.T) {
			job := validJob(now)
			job.Attempt = testCase.attempt
			queue := &queueStub{job: job, found: true}
			processor, _ := NewProcessor(queue, &capacityStub{err: testCase.err}, clockStub{now}, time.Minute, 12)
			worked, err := processor.ProcessOne(context.Background())
			if !worked || err == nil || queue.failureCode != testCase.code || queue.dead != testCase.dead || !queue.next.After(now) {
				t.Fatalf("worked=%v err=%v queue=%+v", worked, err, queue)
			}
		})
	}
}

func TestProcessorRejectsInvalidBoundsAndReportsQueueStats(t *testing.T) {
	if _, err := NewProcessor(nil, &capacityStub{}, clockStub{}, time.Minute, 12); err == nil {
		t.Fatal("expected missing queue rejection")
	}
	now := time.Now().UTC()
	want := Stats{Pending: 3, DeadLetter: 1, OldestPendingAge: 5 * time.Minute}
	queue := &queueStub{stats: want}
	processor, _ := NewProcessor(queue, &capacityStub{}, clockStub{now}, time.Minute, 12)
	got, err := processor.Stats(context.Background())
	if err != nil || got != want {
		t.Fatalf("stats=%+v err=%v", got, err)
	}
}

func validJob(now time.Time) Job {
	return Job{AccountID: ids.AccountID(reconcileAccount), WorkItemID: ids.WorkItemID(reconcileItem), ReservationID: reconcileReservation, LeaseID: reconcileLease, Attempt: 1, QueuedAt: now.Add(-time.Minute)}
}

type queueStub struct {
	job                    Job
	found, completed, dead bool
	err                    error
	failureCode            string
	next                   time.Time
	stats                  Stats
}

func (q *queueStub) Claim(context.Context, time.Time, time.Duration) (Job, bool, error) {
	return q.job, q.found, q.err
}
func (q *queueStub) Complete(context.Context, Job, time.Time) error {
	q.completed = true
	return q.err
}
func (q *queueStub) Fail(_ context.Context, _ Job, next time.Time, code string, dead bool) error {
	q.next, q.failureCode, q.dead = next, code, dead
	return nil
}
func (q *queueStub) Stats(context.Context, time.Time) (Stats, error) { return q.stats, q.err }

type capacityStub struct {
	accountID ids.AccountID
	requestID string
	at        time.Time
	err       error
}

func (c *capacityStub) Release(_ context.Context, accountID ids.AccountID, requestID string, at time.Time) (usageadmission.Reservation, error) {
	c.accountID, c.requestID, c.at = accountID, requestID, at
	return usageadmission.Reservation{}, c.err
}

type clockStub struct{ now time.Time }

func (c clockStub) Now() time.Time { return c.now }
