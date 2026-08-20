package billing

import (
	"context"
	"errors"
	"testing"
	"time"
)

type reconciliationQueue struct {
	id                       string
	found, completed, failed bool
	next                     time.Time
	code                     string
}

func (q *reconciliationQueue) ClaimReconciliation(context.Context, time.Time, time.Duration) (string, bool, error) {
	found := q.found
	q.found = false
	return q.id, found, nil
}
func (q *reconciliationQueue) CompleteReconciliation(context.Context, string, time.Time) error {
	q.completed = true
	return nil
}
func (q *reconciliationQueue) FailReconciliation(_ context.Context, _ string, next time.Time, code string) error {
	q.failed = true
	q.next = next
	q.code = code
	return nil
}

func TestReconcilerClassifiesMappingFailure(t *testing.T) {
	queue := &reconciliationQueue{id: "sub_1", found: true}
	reconciler, _ := NewReconciler(queue, &refresher{err: ErrSubscriptionMismatch}, projectionClock{time.Now()}, time.Minute)
	_, _ = reconciler.ProcessOne(context.Background())
	if queue.code != "subscription_mapping_mismatch" {
		t.Fatalf("code=%q", queue.code)
	}
}

type refresher struct {
	err error
	ids []string
}

func (r *refresher) Refresh(_ context.Context, id string) error {
	r.ids = append(r.ids, id)
	return r.err
}

func TestReconcilerRefreshesCurrentSubscription(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	queue := &reconciliationQueue{id: "sub_1", found: true}
	refresh := &refresher{}
	reconciler, err := NewReconciler(queue, refresh, projectionClock{now}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := reconciler.ProcessOne(context.Background())
	if err != nil || !processed || !queue.completed || len(refresh.ids) != 1 {
		t.Fatalf("processed=%v completed=%v ids=%v err=%v", processed, queue.completed, refresh.ids, err)
	}
}

func TestReconcilerSchedulesFailedRefresh(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	queue := &reconciliationQueue{id: "sub_1", found: true}
	refresh := &refresher{err: errors.New("provider unavailable")}
	reconciler, _ := NewReconciler(queue, refresh, projectionClock{now}, time.Minute)
	if _, err := reconciler.ProcessOne(context.Background()); err == nil {
		t.Fatal("expected refresh error")
	}
	if !queue.failed || queue.next != now.Add(5*time.Minute) {
		t.Fatalf("unexpected retry: failed=%v next=%v", queue.failed, queue.next)
	}
}
