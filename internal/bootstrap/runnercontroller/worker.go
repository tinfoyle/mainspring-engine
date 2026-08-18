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
	CellDatabaseURL     string
	MaxDatabaseConns    int32
	PollInterval, Lease time.Duration
	MaxAttempts         int
	InspectionBatch     int
	Kubernetes          kubernetes.Config
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
	Stats(context.Context) (runnercontrol.Stats, error)
}

type Worker struct {
	pool            *pgxpool.Pool
	processor       processor
	poll            time.Duration
	inspectionBatch int
	logger          *slog.Logger
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
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute || config.Lease < time.Second || config.Lease > 30*time.Minute || config.MaxAttempts < 1 || config.MaxAttempts > 100 || config.InspectionBatch < 1 || config.InspectionBatch > 1000 {
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
	launcher, err := kubernetes.NewInClusterRunnerJobs(config.Kubernetes)
	if err != nil {
		pool.Close()
		return nil, err
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
	return &Worker{pool: pool, processor: service, poll: config.PollInterval, inspectionBatch: config.InspectionBatch, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
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
		case <-timer.C:
		}
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
