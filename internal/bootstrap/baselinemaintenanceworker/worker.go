// Package baselinemaintenanceworker composes one least-privilege per-cell
// worker for deterministic Baseline renewal and reassessment Work.
package baselinemaintenanceworker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/application/baselinemaintenance"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type Config struct {
	GlobalDatabaseURL, CellDatabaseURL string
	CellID                             ids.CellID
	MaxGlobalConns, MaxCellConns       int32
	PollInterval, Lease                time.Duration
	MaxAttempts                        int
}

type processor interface {
	ProcessOne(context.Context) (baselinemaintenance.Result, error)
	Stats(context.Context) (baselinemaintenance.Stats, error)
}

type Worker struct {
	global, cell                   *pgxpool.Pool
	processor                      processor
	poll                           time.Duration
	logger                         *slog.Logger
	processed, succeeded, failures atomic.Uint64
}

type Status struct {
	Pending, Ready, Leased, Retrying, DeadLetter uint64
	OldestReadyAgeSeconds                        int64
	Processed, Succeeded, Failures               uint64
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.GlobalDatabaseURL == "" || config.CellDatabaseURL == "" || !routecontext.ValidCellID(config.CellID) || logger == nil {
		return nil, errors.New("Baseline maintenance worker database URLs, cell ID, and logger are required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.Lease == 0 {
		config.Lease = baselinemaintenance.DefaultLease
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = baselinemaintenance.DefaultMaxAttempts
	}
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute {
		return nil, errors.New("Baseline maintenance poll interval is out of bounds")
	}
	global, err := openPool(ctx, config.GlobalDatabaseURL, config.MaxGlobalConns)
	if err != nil {
		return nil, err
	}
	cell, err := openPool(ctx, config.CellDatabaseURL, config.MaxCellConns)
	if err != nil {
		global.Close()
		return nil, err
	}
	closeOnError := func(err error) (*Worker, error) {
		cell.Close()
		global.Close()
		return nil, err
	}
	cellDatabase, err := database.NewCellPool(cell)
	if err != nil {
		return closeOnError(err)
	}
	baseAuthorizer, err := access.NewWorkloadAuthorizer(postgres.NewAccessRepository(global))
	if err != nil {
		return closeOnError(err)
	}
	authorizer := cellAuthorizer{inner: baseAuthorizer, cellID: config.CellID}
	clock := registration.SystemClock{}
	capacity, err := usageadmission.NewService(authorizer, postgres.NewUsageAdmissionRepository(global), ids.RandomGenerator{}, clock)
	if err != nil {
		return closeOnError(err)
	}
	workRepository, err := postgres.NewWorkRepository(cellDatabase, ids.RandomGenerator{})
	if err != nil {
		return closeOnError(err)
	}
	work, err := workapp.NewService(authorizer, capacity, workRepository, clock)
	if err != nil {
		return closeOnError(err)
	}
	baselineRepository, err := postgres.NewBaselineRepository(cellDatabase)
	if err != nil {
		return closeOnError(err)
	}
	baseline, err := baselineapp.New(authorizer, baselineRepository, clock, baselineapp.WithWorkCreator(work))
	if err != nil {
		return closeOnError(err)
	}
	queue, err := postgres.NewBaselineMaintenanceQueue(cell)
	if err != nil {
		return closeOnError(err)
	}
	application, err := baselinemaintenance.NewProcessor(queue, baseline, clock, ids.RandomGenerator{}, config.Lease, config.MaxAttempts)
	if err != nil {
		return closeOnError(err)
	}
	return &Worker{global: global, cell: cell, processor: application, poll: config.PollInterval, logger: logger}, nil
}

func openPool(ctx context.Context, databaseURL string, maximum int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if maximum > 0 {
		config.MaxConns = maximum
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

func (worker *Worker) Run(ctx context.Context) error {
	for {
		result, err := worker.processor.ProcessOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if result.Worked {
			worker.processed.Add(1)
		}
		if result.Completed {
			worker.succeeded.Add(1)
		}
		if err != nil {
			worker.failures.Add(1)
			worker.logFailure(ctx, err)
		}
		if result.Worked {
			continue
		}
		timer := time.NewTimer(worker.poll)
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

func (worker *Worker) logFailure(ctx context.Context, processErr error) {
	stats, err := worker.processor.Stats(ctx)
	worker.logger.Error("Baseline maintenance materialization failed", "error", processErr, "ready", stats.Ready, "leased", stats.Leased, "retrying", stats.Retrying, "dead_letter", stats.DeadLetter, "oldest_ready_age_seconds", int64(stats.OldestReadyAge/time.Second), "stats_error", err)
}

func (worker *Worker) Ready(ctx context.Context) error {
	return errors.Join(worker.global.Ping(ctx), worker.cell.Ping(ctx))
}

func (worker *Worker) Status(ctx context.Context) (any, error) {
	stats, err := worker.processor.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return Status{Pending: stats.Pending, Ready: stats.Ready, Leased: stats.Leased, Retrying: stats.Retrying, DeadLetter: stats.DeadLetter, OldestReadyAgeSeconds: int64(stats.OldestReadyAge / time.Second), Processed: worker.processed.Load(), Succeeded: worker.succeeded.Load(), Failures: worker.failures.Load()}, nil
}

func (worker *Worker) Close() {
	worker.cell.Close()
	worker.global.Close()
}

type cellAuthorizer struct {
	inner  *access.WorkloadAuthorizer
	cellID ids.CellID
}

func (authorizer cellAuthorizer) Authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	value, err := authorizer.inner.Authorize(ctx, actor, accountID, requirement)
	if err != nil {
		return access.AccountContext{}, err
	}
	if value.CellID != authorizer.cellID {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialCorruptContext}
	}
	return value, nil
}
