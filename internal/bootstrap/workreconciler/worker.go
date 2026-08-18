package workreconciler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/workreconciliation"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	CellDatabaseURL, GlobalDatabaseURL           string
	CellMaxDatabaseConns, GlobalMaxDatabaseConns int32
	PollInterval, Lease, CleanupInterval         time.Duration
	CompletedRetention                           time.Duration
	MaxAttempts, PruneBatch                      int
}

type Status struct {
	Pending                 uint64 `json:"pending"`
	Processing              uint64 `json:"processing"`
	DeadLetter              uint64 `json:"dead_letter"`
	OldestPendingAgeSeconds int64  `json:"oldest_pending_age_seconds"`
}

type Worker struct {
	cellPool, globalPool *pgxpool.Pool
	processor            interface {
		ProcessOne(context.Context) (bool, error)
		Stats(context.Context) (workreconciliation.Stats, error)
		PruneCompleted(context.Context, time.Duration, int) (int64, error)
	}
	poll, cleanupInterval, completedRetention time.Duration
	pruneBatch                                int
	logger                                    *slog.Logger
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.CellDatabaseURL == "" || config.GlobalDatabaseURL == "" || logger == nil {
		return nil, errors.New("Work reconciler cell/global database URLs and logger are required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.Lease == 0 {
		config.Lease = workreconciliation.DefaultLease
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = workreconciliation.DefaultMaxAttempts
	}
	if config.CleanupInterval == 0 {
		config.CleanupInterval = time.Hour
	}
	if config.CompletedRetention == 0 {
		config.CompletedRetention = workreconciliation.DefaultRetention
	}
	if config.PruneBatch == 0 {
		config.PruneBatch = workreconciliation.DefaultPruneBatch
	}
	if config.CleanupInterval < time.Minute || config.CleanupInterval > 24*time.Hour || config.CompletedRetention < 24*time.Hour || config.CompletedRetention > 365*24*time.Hour || config.PruneBatch < 1 || config.PruneBatch > workreconciliation.MaximumPruneBatch {
		return nil, errors.New("Work reconciler cleanup configuration is out of bounds")
	}
	cellPool, err := openPool(ctx, config.CellDatabaseURL, config.CellMaxDatabaseConns)
	if err != nil {
		return nil, err
	}
	globalPool, err := openPool(ctx, config.GlobalDatabaseURL, config.GlobalMaxDatabaseConns)
	if err != nil {
		cellPool.Close()
		return nil, err
	}
	cell, err := database.NewCellPool(cellPool)
	if err != nil {
		cellPool.Close()
		globalPool.Close()
		return nil, err
	}
	queue, err := postgres.NewWorkReleaseQueueRepository(cellPool, cell, ids.RandomGenerator{})
	if err != nil {
		cellPool.Close()
		globalPool.Close()
		return nil, err
	}
	processor, err := workreconciliation.NewProcessor(queue, postgres.NewUsageReleaseRepository(globalPool), registration.SystemClock{}, config.Lease, config.MaxAttempts)
	if err != nil {
		cellPool.Close()
		globalPool.Close()
		return nil, err
	}
	return &Worker{cellPool: cellPool, globalPool: globalPool, processor: processor, poll: config.PollInterval, cleanupInterval: config.CleanupInterval, completedRetention: config.CompletedRetention, pruneBatch: config.PruneBatch, logger: logger}, nil
}

func openPool(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if maxConns > 0 {
		config.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func (w *Worker) Run(ctx context.Context) error {
	cleanup := time.NewTicker(w.cleanupInterval)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-cleanup.C:
			w.pruneCompleted(ctx)
		default:
		}
		worked, err := w.processor.ProcessOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.logFailure(ctx, err)
		}
		if worked {
			continue
		}
		timer := time.NewTimer(w.poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-cleanup.C:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			w.pruneCompleted(ctx)
		case <-timer.C:
		}
	}
}

func (w *Worker) pruneCompleted(ctx context.Context) {
	count, err := w.processor.PruneCompleted(ctx, w.completedRetention, w.pruneBatch)
	if err != nil {
		w.logger.Error("Prune completed Work capacity releases", "error", err)
		return
	}
	if count > 0 {
		w.logger.Info("Pruned completed Work capacity releases", "count", count, "retention_seconds", int64(w.completedRetention/time.Second))
	}
}

func (w *Worker) logFailure(ctx context.Context, processErr error) {
	stats, statsErr := w.processor.Stats(ctx)
	if statsErr != nil {
		w.logger.Error("Work capacity reconciliation failed", "error", processErr, "stats_error", statsErr)
		return
	}
	w.logger.Error("Work capacity reconciliation failed", "error", processErr, "pending", stats.Pending, "processing", stats.Processing, "dead_letter", stats.DeadLetter, "oldest_pending_age_seconds", int64(stats.OldestPendingAge/time.Second))
}

func (w *Worker) Ready(ctx context.Context) error {
	return errors.Join(w.cellPool.Ping(ctx), w.globalPool.Ping(ctx))
}

func (w *Worker) Status(ctx context.Context) (any, error) {
	stats, err := w.processor.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return Status{Pending: stats.Pending, Processing: stats.Processing, DeadLetter: stats.DeadLetter, OldestPendingAgeSeconds: int64(stats.OldestPendingAge / time.Second)}, nil
}

func (w *Worker) Close() {
	w.cellPool.Close()
	w.globalPool.Close()
}
