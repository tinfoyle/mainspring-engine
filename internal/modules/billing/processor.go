package billing

import (
	"context"
	"errors"
	"time"
)

type WorkItem struct {
	Entry   InboxEntry
	Payload []byte
}

type WorkQueue interface {
	Claim(context.Context, time.Time, time.Duration) (WorkItem, bool, error)
	MarkProcessed(context.Context, string, time.Time) error
	MarkFailed(context.Context, string, time.Time, time.Time, string) error
}

type EventHandler interface {
	Project(context.Context, WorkItem) error
}

type SequenceHandler []EventHandler

func (handlers SequenceHandler) Project(ctx context.Context, item WorkItem) error {
	for _, handler := range handlers {
		if handler == nil {
			return errors.New("billing event handler sequence contains a nil handler")
		}
		if err := handler.Project(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

type Processor struct {
	queue   WorkQueue
	handler EventHandler
	clock   Clock
	lease   time.Duration
}

func NewProcessor(queue WorkQueue, handler EventHandler, clock Clock, lease time.Duration) (*Processor, error) {
	if queue == nil || handler == nil || clock == nil || lease <= 0 {
		return nil, errors.New("billing processor dependencies and positive lease are required")
	}
	return &Processor{queue: queue, handler: handler, clock: clock, lease: lease}, nil
}

// ProcessOne deliberately handles one event so the runtime owns concurrency,
// fairness, shutdown, and polling policy rather than hiding goroutines here.
func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	now := p.clock.Now().UTC()
	item, found, err := p.queue.Claim(ctx, now, p.lease)
	if err != nil || !found {
		return found, err
	}
	if err := p.handler.Project(ctx, item); err != nil {
		next := now.Add(retryDelay(item.Entry.AttemptCount))
		if markErr := p.queue.MarkFailed(ctx, item.Entry.ProviderEventID, now, next, billingFailureCode(err, "projection_failed")); markErr != nil {
			return true, errors.Join(err, markErr)
		}
		return true, err
	}
	return true, p.queue.MarkProcessed(ctx, item.Entry.ProviderEventID, now)
}

func billingFailureCode(err error, fallback string) string {
	switch {
	case errors.Is(err, ErrSubscriptionMismatch):
		return "subscription_mapping_mismatch"
	case errors.Is(err, ErrAffiliateAttributionMismatch):
		return "affiliate_attribution_mismatch"
	case errors.Is(err, ErrUnmappedSubscription):
		return "subscription_unmapped"
	default:
		return fallback
	}
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<(attempt-1)) * time.Minute
}
