package entitlementworker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/entitlementrollout"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL      string
	MaxDatabaseConns int32
	PollInterval     time.Duration
	SeedBatch        int
}

type Worker struct {
	pool      *pgxpool.Pool
	processor interface {
		ProcessOne(context.Context) (bool, error)
	}
	poll   time.Duration
	logger *slog.Logger
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.DatabaseURL == "" || logger == nil {
		return nil, errors.New("entitlement worker database URL and logger are required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.SeedBatch <= 0 {
		config.SeedBatch = 100
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
	processor, err := entitlementrollout.NewProcessor(postgres.NewEntitlementRolloutRepository(pool), ids.RandomGenerator{}, registration.SystemClock{}, 2*time.Minute, config.SeedBatch)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: processor, poll: config.PollInterval, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		worked, err := w.processor.ProcessOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.logger.Error("entitlement rollout processing failed", "error", err)
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

func (w *Worker) Ready(ctx context.Context) error { return w.pool.Ping(ctx) }
func (w *Worker) Close()                          { w.pool.Close() }
