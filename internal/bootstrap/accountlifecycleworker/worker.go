package accountlifecycleworker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/subscriptionlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL      string
	MaxDatabaseConns int32
	PollInterval     time.Duration
	Lease            time.Duration
	Retention        time.Duration
	BlockedRetry     time.Duration
}

type Worker struct {
	pool      *pgxpool.Pool
	processor interface {
		ProcessOne(context.Context) (bool, error)
	}
	subscriptionProcessor interface {
		ProcessOne(context.Context) (bool, error)
	}
	poll                                       time.Duration
	logger                                     *slog.Logger
	processed, subscriptionProcessed, failures atomic.Uint64
}

type Status struct {
	Processed             uint64 `json:"processed"`
	SubscriptionProcessed uint64 `json:"subscription_processed"`
	Failures              uint64 `json:"failures"`
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.DatabaseURL == "" || logger == nil {
		return nil, errors.New("Account lifecycle worker database URL and logger are required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.Lease <= 0 {
		config.Lease = 2 * time.Minute
	}
	if config.Retention <= 0 {
		config.Retention = 30 * 24 * time.Hour
	}
	if config.BlockedRetry <= 0 {
		config.BlockedRetry = 24 * time.Hour
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
	processor, err := accountlifecycle.NewProcessor(postgres.NewAccountLifecycleRepository(pool), ids.RandomGenerator{}, registration.SystemClock{}, config.Lease, config.Retention, config.BlockedRetry)
	if err != nil {
		pool.Close()
		return nil, err
	}
	subscriptionProcessor, err := subscriptionlifecycle.NewProcessor(postgres.NewSubscriptionLifecycleRepository(pool), registration.SystemClock{}, config.Lease, config.BlockedRetry)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: processor, subscriptionProcessor: subscriptionProcessor, poll: config.PollInterval, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		worked, err := w.processor.ProcessOne(ctx)
		subscriptionWorked, subscriptionErr := false, error(nil)
		if w.subscriptionProcessor != nil {
			subscriptionWorked, subscriptionErr = w.subscriptionProcessor.ProcessOne(ctx)
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.failures.Add(1)
			w.logger.Error("Account lifecycle processing failed", "error", err)
		}
		if subscriptionErr != nil {
			w.failures.Add(1)
			w.logger.Error("subscription lifecycle processing failed", "error", subscriptionErr)
		}
		if worked {
			w.processed.Add(1)
		}
		if subscriptionWorked {
			w.subscriptionProcessed.Add(1)
		}
		if worked || subscriptionWorked {
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

func (w *Worker) Ready(ctx context.Context) error { return w.pool.Ping(ctx) }
func (w *Worker) Status(context.Context) (any, error) {
	return Status{Processed: w.processed.Load(), SubscriptionProcessed: w.subscriptionProcessed.Load(), Failures: w.failures.Load()}, nil
}
func (w *Worker) Close() { w.pool.Close() }
