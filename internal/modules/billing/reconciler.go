package billing

import (
	"context"
	"errors"
	"time"
)

type ReconciliationQueue interface {
	ClaimReconciliation(context.Context, time.Time, time.Duration) (string, bool, error)
	CompleteReconciliation(context.Context, string, time.Time) error
	FailReconciliation(context.Context, string, time.Time, string) error
}

type SubscriptionRefresher interface {
	Refresh(context.Context, string) error
}

type Reconciler struct {
	queue   ReconciliationQueue
	refresh SubscriptionRefresher
	clock   Clock
	lease   time.Duration
}

func NewReconciler(queue ReconciliationQueue, refresh SubscriptionRefresher, clock Clock, lease time.Duration) (*Reconciler, error) {
	if queue == nil || refresh == nil || clock == nil || lease <= 0 {
		return nil, errors.New("billing reconciler dependencies and positive lease are required")
	}
	return &Reconciler{queue: queue, refresh: refresh, clock: clock, lease: lease}, nil
}

func (r *Reconciler) ProcessOne(ctx context.Context) (bool, error) {
	now := r.clock.Now().UTC()
	id, found, err := r.queue.ClaimReconciliation(ctx, now, r.lease)
	if err != nil || !found {
		return found, err
	}
	if err := r.refresh.Refresh(ctx, id); err != nil {
		if markErr := r.queue.FailReconciliation(ctx, id, now.Add(5*time.Minute), "refresh_failed"); markErr != nil {
			return true, errors.Join(err, markErr)
		}
		return true, err
	}
	return true, r.queue.CompleteReconciliation(ctx, id, now)
}
