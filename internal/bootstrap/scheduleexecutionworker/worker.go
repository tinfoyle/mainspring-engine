// Package scheduleexecutionworker composes the least-privilege cell Schedule
// occurrence worker.
package scheduleexecutionworker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/admissionhttp"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/cellexecutionhttp"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	schedulingapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type Config struct {
	CellDatabaseURL     string
	CellID              ids.CellID
	AdmissionOrigin     string
	CellExecutionOrigin string
	WorkloadTransport   http.RoundTripper
	AllowHTTP           bool
	MaxDatabaseConns    int32
	PollInterval        time.Duration
	Lease               time.Duration
	MaxAttempts         int
}

type Status struct {
	Pending               uint64 `json:"pending"`
	Ready                 uint64 `json:"ready"`
	Leased                uint64 `json:"leased"`
	Retrying              uint64 `json:"retrying"`
	DeadLetter            uint64 `json:"dead_letter"`
	OldestReadyAgeSeconds int64  `json:"oldest_ready_age_seconds"`
	Processed             uint64 `json:"processed"`
	Failures              uint64 `json:"failures"`
}

type processor interface {
	ProcessOne(context.Context) (schedulingapp.ExecutionResult, error)
}

type queueStats interface {
	Stats(context.Context, time.Time) (schedulingapp.ExecutionStats, error)
}

type Worker struct {
	pool      *pgxpool.Pool
	processor processor
	queue     queueStats
	poll      time.Duration
	logger    *slog.Logger
	processed atomic.Uint64
	failures  atomic.Uint64
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.CellDatabaseURL == "" || !routecontext.ValidCellID(config.CellID) || config.AdmissionOrigin == "" || config.CellExecutionOrigin == "" || logger == nil {
		return nil, errors.New("Schedule execution worker configuration is required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.Lease == 0 {
		config.Lease = schedulingapp.DefaultExecutionLease
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = schedulingapp.DefaultExecutionMaxAttempts
	}
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute {
		return nil, errors.New("Schedule execution worker poll interval is out of bounds")
	}
	poolConfig, err := pgxpool.ParseConfig(config.CellDatabaseURL)
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
	queue, err := postgres.NewScheduleExecutionQueueRepository(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	executor, err := cellexecutionhttp.New(config.CellExecutionOrigin, config.AllowHTTP, config.WorkloadTransport)
	if err != nil {
		pool.Close()
		return nil, err
	}
	store, err := schedulingapp.NewExecutionStore(queue, executor)
	if err != nil {
		pool.Close()
		return nil, err
	}
	authorizer, err := admissionhttp.NewScheduleExecutionAuthorizer(config.AdmissionOrigin, config.CellID, config.AllowHTTP, config.WorkloadTransport)
	if err != nil {
		pool.Close()
		return nil, err
	}
	application, err := schedulingapp.NewExecutionProcessor(store, authorizer, registration.SystemClock{}, ids.RandomGenerator{}, config.Lease, config.MaxAttempts)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: application, queue: queue, poll: config.PollInterval, logger: logger}, nil
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
	stats, err := worker.queue.Stats(ctx, time.Now().UTC())
	if err != nil {
		worker.logger.Error("Schedule execution failed", "error", processErr, "stats_error", err)
		return
	}
	worker.logger.Error("Schedule execution failed", "error", processErr, "ready", stats.Ready, "leased", stats.Leased,
		"retrying", stats.Retrying, "dead_letter", stats.DeadLetter, "oldest_ready_age_seconds", int64(stats.OldestReadyAge/time.Second))
}

func (worker *Worker) Ready(ctx context.Context) error { return worker.pool.Ping(ctx) }

func (worker *Worker) Status(ctx context.Context) (any, error) {
	stats, err := worker.queue.Stats(ctx, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return Status{Pending: stats.Pending, Ready: stats.Ready, Leased: stats.Leased, Retrying: stats.Retrying,
		DeadLetter: stats.DeadLetter, OldestReadyAgeSeconds: int64(stats.OldestReadyAge / time.Second),
		Processed: worker.processed.Load(), Failures: worker.failures.Load()}, nil
}

func (worker *Worker) Close() { worker.pool.Close() }
