// Package routereceiptworker composes the cell-wide replay-receipt retention
// worker. One shared workload serves every Account assigned to the cell.
package routereceiptworker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/routeretention"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL      string
	MaxDatabaseConns int32
	PollInterval     time.Duration
	Lease            time.Duration
	Retention        time.Duration
	PruneBatch       int
}

type Status struct {
	Scheduled           uint64 `json:"scheduled_accounts"`
	Ready               uint64 `json:"ready_accounts"`
	Leased              uint64 `json:"leased_accounts"`
	Retrying            uint64 `json:"retrying_accounts"`
	OldestDueAgeSeconds int64  `json:"oldest_due_age_seconds"`
	Processed           uint64 `json:"processed_batches"`
	Pruned              int64  `json:"pruned_receipts"`
	Failures            uint64 `json:"failures"`
}

type processor interface {
	ProcessOne(context.Context) (routeretention.Result, error)
	Stats(context.Context) (routeretention.Stats, error)
}

type Worker struct {
	pool      *pgxpool.Pool
	processor processor
	poll      time.Duration
	logger    *slog.Logger
	processed atomic.Uint64
	pruned    atomic.Int64
	failures  atomic.Uint64
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.DatabaseURL == "" || logger == nil {
		return nil, errors.New("route receipt worker database URL and logger are required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.Lease == 0 {
		config.Lease = routeretention.DefaultLease
	}
	if config.Retention == 0 {
		config.Retention = routeretention.DefaultRetention
	}
	if config.PruneBatch == 0 {
		config.PruneBatch = routeretention.DefaultPruneBatch
	}
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute {
		return nil, errors.New("route receipt worker poll interval is out of bounds")
	}
	if err := routeretention.ValidateBounds(config.Lease, config.Retention, config.PruneBatch); err != nil {
		return nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if config.MaxDatabaseConns > 0 {
		poolConfig.MaxConns = config.MaxDatabaseConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	cell, err := database.NewCellPool(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	queue, err := postgres.NewRouteReceiptCleanupRepository(pool, cell, ids.RandomGenerator{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	processor, err := routeretention.NewProcessor(queue, registration.SystemClock{}, config.Lease, config.Retention, config.PruneBatch)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: processor, poll: config.PollInterval, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		result, err := w.processor.ProcessOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if result.Worked {
			w.processed.Add(1)
			w.pruned.Add(result.Pruned)
		}
		if err != nil {
			w.failures.Add(1)
			w.logFailure(ctx, err)
		}
		if result.Worked {
			continue
		}
		timer := time.NewTimer(w.poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (w *Worker) logFailure(ctx context.Context, processErr error) {
	stats, err := w.processor.Stats(ctx)
	if err != nil {
		w.logger.Error("Route receipt cleanup failed", "error", processErr, "stats_error", err)
		return
	}
	w.logger.Error("Route receipt cleanup failed", "error", processErr, "scheduled_accounts", stats.Scheduled, "ready_accounts", stats.Ready, "leased_accounts", stats.Leased, "retrying_accounts", stats.Retrying, "oldest_due_age_seconds", int64(stats.OldestDueAge/time.Second))
}

func (w *Worker) Ready(ctx context.Context) error { return w.pool.Ping(ctx) }

func (w *Worker) Status(ctx context.Context) (any, error) {
	stats, err := w.processor.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return Status{Scheduled: stats.Scheduled, Ready: stats.Ready, Leased: stats.Leased, Retrying: stats.Retrying, OldestDueAgeSeconds: int64(stats.OldestDueAge / time.Second), Processed: w.processed.Load(), Pruned: w.pruned.Load(), Failures: w.failures.Load()}, nil
}

func (w *Worker) Close() { w.pool.Close() }
