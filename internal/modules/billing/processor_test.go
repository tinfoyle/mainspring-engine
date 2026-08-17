package billing

import (
	"context"
	"errors"
	"testing"
	"time"
)

type queue struct {
	item      WorkItem
	found     bool
	processed bool
	failed    bool
	next      time.Time
}

func (q *queue) Claim(context.Context, time.Time, time.Duration) (WorkItem, bool, error) {
	q.found = false
	return q.item, true, nil
}
func (q *queue) MarkProcessed(context.Context, string, time.Time) error {
	q.processed = true
	return nil
}
func (q *queue) MarkFailed(_ context.Context, _ string, _ time.Time, next time.Time, _ string) error {
	q.failed = true
	q.next = next
	return nil
}

type handler struct{ err error }

func (h handler) Project(context.Context, WorkItem) error { return h.err }

type processorClock struct{ now time.Time }

func (c processorClock) Now() time.Time { return c.now }

func TestProcessorMarksSuccess(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	q := &queue{item: WorkItem{Entry: InboxEntry{ProviderEventID: "evt_1", AttemptCount: 1}}, found: true}
	p, err := NewProcessor(q, handler{}, processorClock{now}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := p.ProcessOne(context.Background())
	if err != nil || !processed || !q.processed {
		t.Fatalf("unexpected result: processed=%v marked=%v err=%v", processed, q.processed, err)
	}
}

func TestProcessorSchedulesBoundedRetry(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	q := &queue{item: WorkItem{Entry: InboxEntry{ProviderEventID: "evt_1", AttemptCount: 3}}, found: true}
	p, err := NewProcessor(q, handler{err: errors.New("provider unavailable")}, processorClock{now}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessOne(context.Background()); err == nil {
		t.Fatal("expected projection failure")
	}
	if !q.failed || q.next != now.Add(4*time.Minute) {
		t.Fatalf("unexpected retry: failed=%v next=%v", q.failed, q.next)
	}
}
