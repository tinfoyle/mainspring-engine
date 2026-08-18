// Package workreconciliation closes the cross-database capacity-release
// window after a Work item becomes terminal.
package workreconciliation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultLease       = 2 * time.Minute
	DefaultMaxAttempts = 12
	DefaultRetention   = 30 * 24 * time.Hour
	DefaultPruneBatch  = 500
	MaximumPruneBatch  = 1000
	maximumRetryDelay  = 15 * time.Minute
)

var (
	ErrInvalidJob = errors.New("work capacity release job is invalid")
	ErrLeaseLost  = errors.New("work capacity release lease was lost")
)

type Job struct {
	AccountID     ids.AccountID
	WorkItemID    ids.WorkItemID
	ReservationID string
	LeaseID       string
	Attempt       int
	QueuedAt      time.Time
}

func (j Job) Valid() bool {
	return ids.Validate(string(j.AccountID)) == nil && ids.Validate(string(j.WorkItemID)) == nil &&
		ids.Validate(j.ReservationID) == nil && ids.Validate(j.LeaseID) == nil && j.Attempt > 0 && !j.QueuedAt.IsZero()
}

type Stats struct {
	Pending          uint64        `json:"pending"`
	Processing       uint64        `json:"processing"`
	DeadLetter       uint64        `json:"dead_letter"`
	OldestPendingAge time.Duration `json:"oldest_pending_age"`
}

type Queue interface {
	Claim(context.Context, time.Time, time.Duration) (Job, bool, error)
	Complete(context.Context, Job, time.Time) error
	Fail(context.Context, Job, time.Time, string, bool) error
	Stats(context.Context, time.Time) (Stats, error)
	PruneCompleted(context.Context, time.Time, int) (int64, error)
}

// Capacity is deliberately release-only. The worker credential should have
// access only to Account existence, usage counters, and usage reservations.
type Capacity interface {
	Release(context.Context, ids.AccountID, string, time.Time) (usageadmission.Reservation, error)
}

type Clock interface{ Now() time.Time }

type Processor struct {
	queue       Queue
	capacity    Capacity
	clock       Clock
	lease       time.Duration
	maxAttempts int
}

func NewProcessor(queue Queue, capacity Capacity, clock Clock, lease time.Duration, maxAttempts int) (*Processor, error) {
	if queue == nil || capacity == nil || clock == nil || lease < time.Second || lease > 30*time.Minute || maxAttempts < 1 || maxAttempts > 100 {
		return nil, errors.New("work reconciliation dependencies or bounds are invalid")
	}
	return &Processor{queue: queue, capacity: capacity, clock: clock, lease: lease, maxAttempts: maxAttempts}, nil
}

func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	now := p.clock.Now().UTC()
	job, found, err := p.queue.Claim(ctx, now, p.lease)
	if err != nil || !found {
		return false, err
	}
	if !job.Valid() {
		failErr := p.queue.Fail(ctx, job, now, "invalid_job", true)
		return true, errors.Join(ErrInvalidJob, failErr)
	}
	if _, err := p.capacity.Release(ctx, job.AccountID, job.ReservationID, now); err != nil {
		code, terminal := releaseFailure(err)
		terminal = terminal || job.Attempt >= p.maxAttempts
		failErr := p.queue.Fail(ctx, job, now.Add(retryDelay(job.Attempt)), code, terminal)
		return true, errors.Join(fmt.Errorf("release Work capacity (%s): %w", code, err), failErr)
	}
	if err := p.queue.Complete(ctx, job, now); err != nil {
		failErr := p.queue.Fail(ctx, job, now.Add(retryDelay(job.Attempt)), "cell_checkpoint_failed", job.Attempt >= p.maxAttempts)
		return true, errors.Join(fmt.Errorf("checkpoint Work capacity release: %w", err), failErr)
	}
	return true, nil
}

func (p *Processor) Stats(ctx context.Context) (Stats, error) {
	return p.queue.Stats(ctx, p.clock.Now().UTC())
}

func (p *Processor) PruneCompleted(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	if retention < 24*time.Hour || retention > 365*24*time.Hour || limit < 1 || limit > MaximumPruneBatch {
		return 0, errors.New("Work release retention bounds are invalid")
	}
	return p.queue.PruneCompleted(ctx, p.clock.Now().UTC().Add(-retention), limit)
}

func releaseFailure(err error) (string, bool) {
	switch {
	case errors.Is(err, usageadmission.ErrInvalidRequest):
		return "reservation_not_found", true
	case errors.Is(err, usageadmission.ErrCorruptUsage):
		return "usage_corrupt", true
	case errors.Is(err, usageadmission.ErrReservationConflict):
		return "reservation_conflict", true
	default:
		return "global_release_unavailable", false
	}
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
