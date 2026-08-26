// Package entitlementrollout applies effective Catalog free-plan changes to
// existing Accounts while preserving independently sourced grants.
package entitlementrollout

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrInvalidCatalog = errors.New("entitlement rollout catalog is invalid")

type Work struct {
	RolloutID      string
	AccountID      ids.AccountID
	CatalogVersion uint64
	AttemptCount   int
}

type Input struct {
	AccountID      ids.AccountID
	CurrentVersion uint64
	Publication    catalog.PublishedCatalog
	OtherGrants    []entitlements.Grant
}

type Output struct {
	FreeGrants []entitlements.Grant
	Snapshot   entitlements.Snapshot
}

type Store interface {
	EnsureRepairRollout(context.Context, string, time.Time) (bool, error)
	SeedBatch(context.Context, time.Time, int) (bool, error)
	Claim(context.Context, time.Time, time.Duration) (Work, bool, error)
	Apply(context.Context, Work, time.Time, func(Input) (Output, error)) error
	MarkFailed(context.Context, Work, time.Time, time.Time, string, bool) error
}

type Clock interface{ Now() time.Time }

type Processor struct {
	store Store
	ids   ids.Generator
	clock Clock
	lease time.Duration
	batch int
}

func NewProcessor(store Store, generator ids.Generator, clock Clock, lease time.Duration, batch int) (*Processor, error) {
	if store == nil || generator == nil || clock == nil || lease <= 0 || batch <= 0 || batch > 1000 {
		return nil, errors.New("entitlement rollout dependencies and bounds are required")
	}
	return &Processor{store: store, ids: generator, clock: clock, lease: lease, batch: batch}, nil
}

func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	now := p.clock.Now().UTC()
	ensured, err := p.store.EnsureRepairRollout(ctx, p.ids.New(), now)
	if err != nil {
		return false, err
	}
	seeded, err := p.store.SeedBatch(ctx, now, p.batch)
	if err != nil {
		return ensured, err
	}
	work, claimed, err := p.store.Claim(ctx, now, p.lease)
	if err != nil || !claimed {
		return ensured || seeded, err
	}
	err = p.store.Apply(ctx, work, now, func(input Input) (Output, error) {
		if err := input.Publication.Validate(); err != nil {
			return Output{}, errors.Join(ErrInvalidCatalog, err)
		}
		free := []entitlements.Grant{}
		if plan, ok := input.Publication.Plan("free"); ok {
			var err error
			free, err = entitlements.FreePlanGrants(input.AccountID, plan, input.Publication.Packages, p.ids, now)
			if err != nil {
				return Output{}, errors.Join(ErrInvalidCatalog, err)
			}
		}
		grants := append(append([]entitlements.Grant(nil), input.OtherGrants...), free...)
		snapshot, err := entitlements.Evaluate(input.AccountID, input.CurrentVersion+1, input.Publication, grants, now)
		if err != nil {
			return Output{}, err
		}
		return Output{FreeGrants: free, Snapshot: snapshot}, nil
	})
	if err == nil {
		return true, nil
	}
	terminal := errors.Is(err, ErrInvalidCatalog) || work.AttemptCount >= 12
	next := now.Add(retryDelay(work.AttemptCount))
	if markErr := p.store.MarkFailed(ctx, work, now, next, "recompute_failed", terminal); markErr != nil {
		return true, errors.Join(err, markErr)
	}
	return true, err
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 15 * time.Second
	for index := 1; index < attempt && delay < time.Hour; index++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}
