package runnercontroller

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/kubernetes"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	CellDatabaseURL                          string
	MaxDatabaseConns                         int32
	PollInterval, Lease, CleanupInterval     time.Duration
	PayloadRetention                         time.Duration
	MaxAttempts, InspectionBatch, PruneBatch int
	Kubernetes                               kubernetes.Config
	Launcher                                 runnercontrol.Launcher
}

type Status struct {
	Ready                 uint64 `json:"ready"`
	Launching             uint64 `json:"launching"`
	LaunchUncertain       uint64 `json:"launch_uncertain"`
	Launched              uint64 `json:"launched"`
	Canceling             uint64 `json:"canceling"`
	RetryableFailed       uint64 `json:"retryable_failed"`
	DeadLetter            uint64 `json:"dead_letter"`
	OldestReadyAgeSeconds int64  `json:"oldest_ready_age_seconds"`
}

type processor interface {
	ProcessOne(context.Context) (bool, error)
	ReconcileJobs(context.Context, int) (int, error)
	PruneTerminalPayloads(context.Context, time.Duration, int) (int64, error)
	Stats(context.Context) (runnercontrol.Stats, error)
}

type Worker struct {
	pool                                    *pgxpool.Pool
	processor                               processor
	poll, cleanupInterval, payloadRetention time.Duration
	inspectionBatch, pruneBatch             int
	logger                                  *slog.Logger
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.CellDatabaseURL == "" || logger == nil {
		return nil, errors.New("runner controller cell database URL and logger are required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.Lease == 0 {
		config.Lease = runnercontrol.DefaultLease
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = runnercontrol.DefaultMaxAttempts
	}
	if config.InspectionBatch == 0 {
		config.InspectionBatch = 100
	}
	if config.CleanupInterval == 0 {
		config.CleanupInterval = time.Hour
	}
	if config.PayloadRetention == 0 {
		config.PayloadRetention = runnercontrol.DefaultPayloadRetention
	}
	if config.PruneBatch == 0 {
		config.PruneBatch = runnercontrol.DefaultPruneBatch
	}
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute || config.Lease < time.Second || config.Lease > 30*time.Minute || config.MaxAttempts < 1 || config.MaxAttempts > 100 || config.InspectionBatch < 1 || config.InspectionBatch > 1000 || config.CleanupInterval < time.Minute || config.CleanupInterval > 24*time.Hour || config.PayloadRetention < time.Hour || config.PayloadRetention > 30*24*time.Hour || config.PruneBatch < 1 || config.PruneBatch > runnercontrol.MaximumPruneBatch {
		return nil, errors.New("runner controller scheduling configuration is out of bounds")
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
	launcher := config.Launcher
	if launcher == nil {
		launcher, err = kubernetes.NewInClusterRunnerJobs(config.Kubernetes)
		if err != nil {
			pool.Close()
			return nil, err
		}
	}
	queue, err := postgres.NewRunnerControlQueue(pool, ids.RandomGenerator{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	service, err := runnercontrol.NewService(queue, launcher, registration.SystemClock{}, config.Lease, config.MaxAttempts)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: service, poll: config.PollInterval, cleanupInterval: config.CleanupInterval, payloadRetention: config.PayloadRetention, inspectionBatch: config.InspectionBatch, pruneBatch: config.PruneBatch, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	cleanup := time.NewTicker(w.cleanupInterval)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-cleanup.C:
			w.pruneTerminalPayloads(ctx)
		default:
		}
		worked, err := w.cycle(ctx)
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
			w.pruneTerminalPayloads(ctx)
		case <-timer.C:
		}
	}
}

func (w *Worker) pruneTerminalPayloads(ctx context.Context) {
	count, err := w.processor.PruneTerminalPayloads(ctx, w.payloadRetention, w.pruneBatch)
	if err != nil {
		w.logger.Error("Prune terminal runner payloads", "error", err)
		return
	}
	if count > 0 {
		w.logger.Info("Pruned terminal runner payloads", "count", count, "retention_seconds", int64(w.payloadRetention/time.Second))
	}
}

func (w *Worker) cycle(ctx context.Context) (bool, error) {
	reconciled, reconcileErr := w.processor.ReconcileJobs(ctx, w.inspectionBatch)
	launched, launchErr := w.processor.ProcessOne(ctx)
	return reconciled > 0 || launched, errors.Join(reconcileErr, launchErr)
}

func (w *Worker) logFailure(ctx context.Context, processErr error) {
	stats, statsErr := w.processor.Stats(ctx)
	if statsErr != nil {
		w.logger.Error("Runner control cycle failed", "error", processErr, "stats_error", statsErr)
		return
	}
	w.logger.Error("Runner control cycle failed", "error", processErr, "ready", stats.Ready, "launching", stats.Launching, "launch_uncertain", stats.LaunchUncertain, "launched", stats.Launched, "canceling", stats.Canceling, "retryable_failed", stats.Failed, "dead_letter", stats.DeadLetter, "oldest_ready_age_seconds", int64(stats.OldestReadyAge/time.Second))
}

func (w *Worker) Ready(ctx context.Context) error { return w.pool.Ping(ctx) }

func (w *Worker) Status(ctx context.Context) (any, error) {
	stats, err := w.processor.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return Status{Ready: stats.Ready, Launching: stats.Launching, LaunchUncertain: stats.LaunchUncertain, Launched: stats.Launched, Canceling: stats.Canceling, RetryableFailed: stats.Failed, DeadLetter: stats.DeadLetter, OldestReadyAgeSeconds: int64(stats.OldestReadyAge / time.Second)}, nil
}

func (w *Worker) Close() { w.pool.Close() }
