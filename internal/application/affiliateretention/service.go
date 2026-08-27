// Package affiliateretention owns the policy-fixed Affiliate minimization
// processor. PostgreSQL determines seven-calendar-year eligibility and blocks
// legal holds, open cases, unsettled value, and in-flight provider recovery.
package affiliateretention

import (
	"context"
	"errors"
	"time"
)

const DefaultBatch = 100

var ErrInvalidRetention = errors.New("Affiliate retention state is invalid")

type Clock interface{ Now() time.Time }

type Store interface {
	Minimize(context.Context, time.Time, int) (int64, error)
	Stats(context.Context, time.Time) (Stats, error)
}

type Stats struct {
	Total             uint64
	Eligible          uint64
	OldestEligibleAge time.Duration
}

func (s Stats) Validate() error {
	if s.Eligible > s.Total || s.OldestEligibleAge < 0 || (s.Eligible == 0 && s.OldestEligibleAge != 0) {
		return ErrInvalidRetention
	}
	return nil
}

type Processor struct {
	store Store
	clock Clock
	batch int
}

func NewProcessor(store Store, clock Clock, batch int) (*Processor, error) {
	if store == nil || clock == nil || batch < 1 || batch > 1000 {
		return nil, ErrInvalidRetention
	}
	return &Processor{store: store, clock: clock, batch: batch}, nil
}

func (p *Processor) Process(ctx context.Context) (int64, error) {
	count, err := p.store.Minimize(ctx, p.clock.Now().UTC(), p.batch)
	if err != nil {
		return 0, err
	}
	if count < 0 || count > int64(p.batch) {
		return 0, ErrInvalidRetention
	}
	return count, nil
}

func (p *Processor) Stats(ctx context.Context) (Stats, error) {
	value, err := p.store.Stats(ctx, p.clock.Now().UTC())
	if err != nil {
		return Stats{}, err
	}
	if err := value.Validate(); err != nil {
		return Stats{}, err
	}
	return value, nil
}
