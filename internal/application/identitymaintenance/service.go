// Package identitymaintenance owns bounded retention of transient global
// identity records. It exposes only aggregate, content-free backlog evidence.
package identitymaintenance

import (
	"context"
	"errors"
	"time"
)

const (
	DefaultRetention = 24 * time.Hour
	DefaultBatch     = 500
	MaximumBatch     = 5000
)

type Clock interface{ Now() time.Time }

type Stats struct {
	Total             uint64
	Eligible          uint64
	OldestEligibleAge time.Duration
}

type Repository interface {
	Prune(context.Context, time.Time, time.Duration, int) (int64, error)
	Stats(context.Context, time.Time, time.Duration) (Stats, error)
}

type Processor struct {
	repository Repository
	clock      Clock
	retention  time.Duration
	batch      int
}

func NewProcessor(repository Repository, clock Clock, retention time.Duration, batch int) (*Processor, error) {
	if repository == nil || clock == nil {
		return nil, errors.New("identity maintenance repository and clock are required")
	}
	if err := ValidateBounds(retention, batch); err != nil {
		return nil, err
	}
	return &Processor{repository: repository, clock: clock, retention: retention, batch: batch}, nil
}

func ValidateBounds(retention time.Duration, batch int) error {
	if retention < time.Hour || retention > 30*24*time.Hour || retention%time.Second != 0 {
		return errors.New("identity maintenance retention must be whole seconds between 1h and 30d")
	}
	if batch < 1 || batch > MaximumBatch {
		return errors.New("identity maintenance batch is out of bounds")
	}
	return nil
}

func (p *Processor) Process(ctx context.Context) (int64, error) {
	return p.repository.Prune(ctx, p.clock.Now().UTC(), p.retention, p.batch)
}

func (p *Processor) Stats(ctx context.Context) (Stats, error) {
	return p.repository.Stats(ctx, p.clock.Now().UTC(), p.retention)
}
