// Package accountprovisioning closes the asynchronous boundary between a
// globally committed Account directory row and its cell-local RLS namespace.
package accountprovisioning

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const DefaultLease = 30 * time.Second

type Work struct {
	AccountID           ids.AccountID
	CellID              ids.CellID
	PlacementGeneration uint64
	AttemptCount        int
}

type Queue interface {
	Claim(context.Context, ids.CellID, time.Time, time.Duration) (Work, bool, error)
	Complete(context.Context, Work, time.Time) error
	Retry(context.Context, Work, string, time.Time) error
}

type Cell interface {
	Provision(context.Context, ids.AccountID, uint64, time.Time) error
}

type Clock interface{ Now() time.Time }

type Processor struct {
	queue Queue
	cell  Cell
	clock Clock
	lease time.Duration
}

func NewProcessor(queue Queue, cell Cell, clock Clock, lease time.Duration) (*Processor, error) {
	if queue == nil || cell == nil || clock == nil || lease < 5*time.Second || lease > 5*time.Minute {
		return nil, errors.New("Account provisioning processor configuration is invalid")
	}
	return &Processor{queue: queue, cell: cell, clock: clock, lease: lease}, nil
}

func (p *Processor) ProcessOne(ctx context.Context, cellID ids.CellID) (bool, error) {
	if cellID == "" {
		return false, errors.New("cell ID is required")
	}
	now := p.clock.Now().UTC()
	work, found, err := p.queue.Claim(ctx, cellID, now, p.lease)
	if err != nil || !found {
		return found, err
	}
	if err := p.cell.Provision(ctx, work.AccountID, work.PlacementGeneration, now); err != nil {
		if retryErr := p.queue.Retry(ctx, work, "cell_provision_failed", now); retryErr != nil {
			return true, errors.Join(err, retryErr)
		}
		return true, err
	}
	return true, p.queue.Complete(ctx, work, now)
}
