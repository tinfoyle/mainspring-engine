// Package affiliateretentionworker composes the execute-only Affiliate
// retention worker and exposes only content-free operational status.
package affiliateretentionworker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateretention"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
)

const overdueAlertAge = 8 * 365 * 24 * time.Hour

type Config struct {
	DatabaseURL      string
	MaxDatabaseConns int32
	Interval         time.Duration
	Batch            int
	AlertBacklog     uint64
}

type processor interface {
	Process(context.Context) (int64, error)
	Stats(context.Context) (affiliateretention.Stats, error)
}

type Worker struct {
	pool         *pgxpool.Pool
	processor    processor
	interval     time.Duration
	alertBacklog uint64
	logger       *slog.Logger
	minimized    atomic.Int64
	failures     atomic.Uint64
}

type Status struct {
	Total                    uint64 `json:"total_closed_affiliates"`
	Eligible                 uint64 `json:"eligible_affiliates"`
	OldestEligibleAgeSeconds int64  `json:"oldest_eligible_age_seconds"`
	Minimized                int64  `json:"minimized_affiliates"`
	Failures                 uint64 `json:"failures"`
	Alerting                 bool   `json:"alerting"`
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.DatabaseURL == "" || logger == nil {
		return nil, errors.New("Affiliate retention database URL and logger are required")
	}
	if config.Interval == 0 {
		config.Interval = 24 * time.Hour
	}
	if config.Batch == 0 {
		config.Batch = affiliateretention.DefaultBatch
	}
	if config.AlertBacklog == 0 {
		config.AlertBacklog = 100
	}
	if config.Interval < time.Minute || config.Interval > 7*24*time.Hour ||
		config.Batch < 1 || config.Batch > 1000 || config.AlertBacklog > 1_000_000 {
		return nil, errors.New("Affiliate retention schedule or backlog threshold is out of bounds")
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
	processor, err := affiliateretention.NewProcessor(postgres.NewAffiliateRetentionRepository(pool), registration.SystemClock{}, config.Batch)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: processor, interval: config.Interval,
		alertBacklog: config.AlertBacklog, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	w.runOnce(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	count, err := w.processor.Process(ctx)
	if err != nil {
		w.failures.Add(1)
		w.logger.Error("Affiliate retention minimization failed", "error", err)
		return
	}
	w.minimized.Add(count)
	stats, err := w.processor.Stats(ctx)
	if err != nil {
		w.failures.Add(1)
		w.logger.Error("Affiliate retention stats failed", "error", err)
		return
	}
	if alerting(stats, w.alertBacklog) {
		w.logger.Warn("Affiliate retention backlog is abnormal", "eligible", stats.Eligible,
			"oldest_eligible_age_seconds", int64(stats.OldestEligibleAge/time.Second))
	} else if count > 0 {
		w.logger.Info("Minimized expired Affiliate records", "count", count)
	}
}

func alerting(stats affiliateretention.Stats, threshold uint64) bool {
	return stats.Eligible >= threshold || stats.OldestEligibleAge > overdueAlertAge
}

func (w *Worker) Ready(ctx context.Context) error { return w.pool.Ping(ctx) }

func (w *Worker) Status(ctx context.Context) (any, error) {
	stats, err := w.processor.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return Status{Total: stats.Total, Eligible: stats.Eligible,
		OldestEligibleAgeSeconds: int64(stats.OldestEligibleAge / time.Second),
		Minimized:                w.minimized.Load(), Failures: w.failures.Load(),
		Alerting: alerting(stats, w.alertBacklog)}, nil
}

func (w *Worker) Close() { w.pool.Close() }
