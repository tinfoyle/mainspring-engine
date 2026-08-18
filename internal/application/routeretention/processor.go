// Package routeretention bounds cell replay-receipt storage without granting
// a cross-Account worker access to customer-scoped rows.
package routeretention

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultLease      = 30 * time.Second
	DefaultRetention  = 5 * time.Minute
	DefaultPruneBatch = 500
	MaximumPruneBatch = 1000
	minimumRetention  = time.Minute
	maximumRetention  = 24 * time.Hour
	maximumRetryDelay = 15 * time.Minute
)

var (
	ErrInvalidJob = errors.New("route receipt cleanup job is invalid")
	ErrLeaseLost  = errors.New("route receipt cleanup lease was lost")
)

type Job struct {
	AccountID       ids.AccountID
	LeaseID         string
	Attempt         int
	ScheduleVersion uint64
	DueAt           time.Time
}

func (j Job) Valid() bool {
	return ids.Validate(string(j.AccountID)) == nil && ids.Validate(j.LeaseID) == nil && j.Attempt > 0 && j.ScheduleVersion > 0 && !j.DueAt.IsZero()
}

type Stats struct {
	Scheduled    uint64
	Ready        uint64
	Leased       uint64
	Retrying     uint64
	OldestDueAge time.Duration
}

type Result struct {
	Worked bool
	Pruned int64
}

type Queue interface {
	Claim(context.Context, time.Time, time.Duration) (Job, bool, error)
	Prune(context.Context, Job, time.Time, time.Duration, int) (int64, error)
	Fail(context.Context, Job, time.Time, string) error
	Stats(context.Context, time.Time) (Stats, error)
}

type Clock interface{ Now() time.Time }

type Processor struct {
	queue     Queue
	clock     Clock
	lease     time.Duration
	retention time.Duration
	batch     int
}

func NewProcessor(queue Queue, clock Clock, lease, retention time.Duration, batch int) (*Processor, error) {
	if queue == nil || clock == nil {
		return nil, errors.New("route receipt retention dependencies are invalid")
	}
	if err := ValidateBounds(lease, retention, batch); err != nil {
		return nil, err
	}
	return &Processor{queue: queue, clock: clock, lease: lease, retention: retention, batch: batch}, nil
}

func ValidateBounds(lease, retention time.Duration, batch int) error {
	if lease < time.Second || lease > 30*time.Minute || retention < minimumRetention || retention > maximumRetention || batch < 1 || batch > MaximumPruneBatch {
		return errors.New("route receipt retention bounds are invalid")
	}
	return nil
}

func (p *Processor) ProcessOne(ctx context.Context) (Result, error) {
	now := p.clock.Now().UTC()
	job, found, err := p.queue.Claim(ctx, now, p.lease)
	if err != nil || !found {
		return Result{}, err
	}
	if !job.Valid() {
		failErr := p.queue.Fail(ctx, job, now.Add(retryDelay(job.Attempt)), "invalid_job")
		return Result{Worked: true}, errors.Join(ErrInvalidJob, failErr)
	}
	pruned, err := p.queue.Prune(ctx, job, now, p.retention, p.batch)
	if err != nil {
		failErr := p.queue.Fail(ctx, job, now.Add(retryDelay(job.Attempt)), "prune_failed")
		return Result{Worked: true}, errors.Join(fmt.Errorf("prune route receipts: %w", err), failErr)
	}
	return Result{Worked: true, Pruned: pruned}, nil
}

func (p *Processor) Stats(ctx context.Context) (Stats, error) {
	return p.queue.Stats(ctx, p.clock.Now().UTC())
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	for index := 1; index < attempt && delay < maximumRetryDelay; index++ {
		delay *= 2
	}
	if delay > maximumRetryDelay {
		return maximumRetryDelay
	}
	return delay
}
