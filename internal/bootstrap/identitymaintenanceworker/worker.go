// Package identitymaintenanceworker composes the global transient-identity
// retention worker and its content-free operational status.
package identitymaintenanceworker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsretention"
	"github.com/tinfoyle/spyglass-engine/internal/application/identitymaintenance"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
)

type Config struct {
	DatabaseURL              string
	MaxDatabaseConns         int32
	Interval                 time.Duration
	Retention                time.Duration
	PruneBatch               int
	AlertBacklog             uint64
	AnalyticsRetention       time.Duration
	AnalyticsPruneBatch      int
	AnalyticsAlertBacklog    uint64
	NetworkLimitRetention    time.Duration
	NetworkLimitPruneBatch   int
	NetworkLimitAlertBacklog uint64
}

type Status struct {
	Total                                uint64 `json:"total_ceremonies"`
	Eligible                             uint64 `json:"eligible_ceremonies"`
	OldestEligibleAgeSeconds             int64  `json:"oldest_eligible_age_seconds"`
	Pruned                               int64  `json:"pruned_ceremonies"`
	Failures                             uint64 `json:"failures"`
	Alerting                             bool   `json:"alerting"`
	TotalAnalyticsEvents                 uint64 `json:"total_analytics_events"`
	EligibleAnalyticsEvents              uint64 `json:"eligible_analytics_events"`
	OldestEligibleAnalyticsAgeSeconds    int64  `json:"oldest_eligible_analytics_age_seconds"`
	PrunedAnalyticsEvents                int64  `json:"pruned_analytics_events"`
	AnalyticsFailures                    uint64 `json:"analytics_failures"`
	AnalyticsAlerting                    bool   `json:"analytics_alerting"`
	TotalNetworkActorLimits              uint64 `json:"total_network_actor_limits"`
	EligibleNetworkActorLimits           uint64 `json:"eligible_network_actor_limits"`
	OldestEligibleNetworkLimitAgeSeconds int64  `json:"oldest_eligible_network_limit_age_seconds"`
	PrunedNetworkActorLimits             int64  `json:"pruned_network_actor_limits"`
	NetworkLimitFailures                 uint64 `json:"network_limit_failures"`
	NetworkLimitAlerting                 bool   `json:"network_limit_alerting"`
}

type processor interface {
	Process(context.Context) (int64, error)
	Stats(context.Context) (identitymaintenance.Stats, error)
	ProcessNetworkLimits(context.Context, time.Duration, int) (int64, error)
	NetworkLimitStats(context.Context, time.Duration) (identitymaintenance.Stats, error)
}

type analyticsProcessor interface {
	Process(context.Context) (int64, error)
	Stats(context.Context) (analyticsretention.Stats, error)
}

type Worker struct {
	pool                     *pgxpool.Pool
	processor                processor
	analyticsProcessor       analyticsProcessor
	interval                 time.Duration
	retention                time.Duration
	alertBacklog             uint64
	analyticsRetention       time.Duration
	analyticsAlertBacklog    uint64
	networkLimitRetention    time.Duration
	networkLimitPruneBatch   int
	networkLimitAlertBacklog uint64
	logger                   *slog.Logger
	pruned                   atomic.Int64
	failures                 atomic.Uint64
	analyticsPruned          atomic.Int64
	analyticsFailures        atomic.Uint64
	networkLimitsPruned      atomic.Int64
	networkLimitFailures     atomic.Uint64
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.DatabaseURL == "" || logger == nil {
		return nil, errors.New("identity maintenance database URL and logger are required")
	}
	if config.Interval == 0 {
		config.Interval = time.Hour
	}
	if config.Retention == 0 {
		config.Retention = identitymaintenance.DefaultRetention
	}
	if config.PruneBatch == 0 {
		config.PruneBatch = identitymaintenance.DefaultBatch
	}
	if config.AlertBacklog == 0 {
		config.AlertBacklog = 10000
	}
	if config.AnalyticsRetention == 0 {
		config.AnalyticsRetention = analyticsretention.DefaultRetention
	}
	if config.AnalyticsPruneBatch == 0 {
		config.AnalyticsPruneBatch = analyticsretention.DefaultBatch
	}
	if config.AnalyticsAlertBacklog == 0 {
		config.AnalyticsAlertBacklog = 100000
	}
	if config.NetworkLimitRetention == 0 {
		config.NetworkLimitRetention = identitymaintenance.DefaultNetworkLimitRetention
	}
	if config.NetworkLimitPruneBatch == 0 {
		config.NetworkLimitPruneBatch = identitymaintenance.DefaultBatch
	}
	if config.NetworkLimitAlertBacklog == 0 {
		config.NetworkLimitAlertBacklog = 10000
	}
	if config.Interval < time.Minute || config.Interval > 24*time.Hour || config.AlertBacklog > 10000000 || config.AnalyticsAlertBacklog > 10000000 || config.NetworkLimitAlertBacklog > 10000000 {
		return nil, errors.New("identity maintenance schedule or alert threshold is out of bounds")
	}
	if err := identitymaintenance.ValidateBounds(config.Retention, config.PruneBatch); err != nil {
		return nil, err
	}
	if err := analyticsretention.ValidateBounds(config.AnalyticsRetention, config.AnalyticsPruneBatch); err != nil {
		return nil, err
	}
	if err := identitymaintenance.ValidateBounds(config.NetworkLimitRetention, config.NetworkLimitPruneBatch); err != nil {
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
	processor, err := identitymaintenance.NewProcessor(postgres.NewIdentityMaintenanceRepository(pool), registration.SystemClock{}, config.Retention, config.PruneBatch)
	if err != nil {
		pool.Close()
		return nil, err
	}
	analyticsProcessor, err := analyticsretention.NewProcessor(postgres.NewAnalyticsRetentionRepository(pool), registration.SystemClock{}, config.AnalyticsRetention, config.AnalyticsPruneBatch)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: processor, analyticsProcessor: analyticsProcessor, interval: config.Interval,
		retention: config.Retention, alertBacklog: config.AlertBacklog, analyticsRetention: config.AnalyticsRetention,
		analyticsAlertBacklog: config.AnalyticsAlertBacklog, networkLimitRetention: config.NetworkLimitRetention,
		networkLimitPruneBatch: config.NetworkLimitPruneBatch, networkLimitAlertBacklog: config.NetworkLimitAlertBacklog, logger: logger}, nil
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
		w.logger.Error("Identity maintenance failed", "error", err)
		return
	}
	w.pruned.Add(count)
	stats, err := w.processor.Stats(ctx)
	if err != nil {
		w.failures.Add(1)
		w.logger.Error("Identity maintenance stats failed", "error", err)
		return
	}
	if stats.Eligible >= w.alertBacklog || stats.OldestEligibleAge > 2*w.retention {
		w.logger.Warn("Identity ceremony retention backlog is abnormal", "eligible", stats.Eligible, "oldest_eligible_age_seconds", int64(stats.OldestEligibleAge/time.Second))
	} else if count > 0 {
		w.logger.Info("Pruned passkey ceremonies", "count", count)
	}
	w.runNetworkLimitsOnce(ctx)
	w.runAnalyticsOnce(ctx)
}

func (w *Worker) runNetworkLimitsOnce(ctx context.Context) {
	count, err := w.processor.ProcessNetworkLimits(ctx, w.networkLimitRetention, w.networkLimitPruneBatch)
	if err != nil {
		w.networkLimitFailures.Add(1)
		w.logger.Error("Network actor limit retention failed", "error", err)
		return
	}
	w.networkLimitsPruned.Add(count)
	stats, err := w.processor.NetworkLimitStats(ctx, w.networkLimitRetention)
	if err != nil {
		w.networkLimitFailures.Add(1)
		w.logger.Error("Network actor limit retention stats failed", "error", err)
		return
	}
	if stats.Eligible >= w.networkLimitAlertBacklog || stats.OldestEligibleAge > 2*w.networkLimitRetention {
		w.logger.Warn("Network actor limit retention backlog is abnormal", "eligible", stats.Eligible,
			"oldest_eligible_age_seconds", int64(stats.OldestEligibleAge/time.Second))
	} else if count > 0 {
		w.logger.Info("Pruned network actor limits", "count", count)
	}
}

func (w *Worker) runAnalyticsOnce(ctx context.Context) {
	if w.analyticsProcessor == nil {
		return
	}
	count, err := w.analyticsProcessor.Process(ctx)
	if err != nil {
		w.analyticsFailures.Add(1)
		w.logger.Error("Analytics retention failed", "error", err)
		return
	}
	w.analyticsPruned.Add(count)
	stats, err := w.analyticsProcessor.Stats(ctx)
	if err != nil {
		w.analyticsFailures.Add(1)
		w.logger.Error("Analytics retention stats failed", "error", err)
		return
	}
	if stats.Eligible >= w.analyticsAlertBacklog || stats.OldestEligibleAge > 2*w.analyticsRetention {
		w.logger.Warn("Analytics retention backlog is abnormal", "eligible", stats.Eligible, "oldest_eligible_age_seconds", int64(stats.OldestEligibleAge/time.Second))
	} else if count > 0 {
		w.logger.Info("Pruned analytics events", "count", count)
	}
}

func (w *Worker) Ready(ctx context.Context) error { return w.pool.Ping(ctx) }

func (w *Worker) Status(ctx context.Context) (any, error) {
	stats, err := w.processor.Stats(ctx)
	if err != nil {
		return nil, err
	}
	analyticsStats, err := w.analyticsProcessor.Stats(ctx)
	if err != nil {
		return nil, err
	}
	networkStats, err := w.processor.NetworkLimitStats(ctx, w.networkLimitRetention)
	if err != nil {
		return nil, err
	}
	return Status{
		Total: stats.Total, Eligible: stats.Eligible, OldestEligibleAgeSeconds: int64(stats.OldestEligibleAge / time.Second), Pruned: w.pruned.Load(), Failures: w.failures.Load(), Alerting: stats.Eligible >= w.alertBacklog || stats.OldestEligibleAge > 2*w.retention,
		TotalAnalyticsEvents: analyticsStats.Total, EligibleAnalyticsEvents: analyticsStats.Eligible, OldestEligibleAnalyticsAgeSeconds: int64(analyticsStats.OldestEligibleAge / time.Second), PrunedAnalyticsEvents: w.analyticsPruned.Load(), AnalyticsFailures: w.analyticsFailures.Load(), AnalyticsAlerting: analyticsStats.Eligible >= w.analyticsAlertBacklog || analyticsStats.OldestEligibleAge > 2*w.analyticsRetention,
		TotalNetworkActorLimits: networkStats.Total, EligibleNetworkActorLimits: networkStats.Eligible,
		OldestEligibleNetworkLimitAgeSeconds: int64(networkStats.OldestEligibleAge / time.Second),
		PrunedNetworkActorLimits:             w.networkLimitsPruned.Load(), NetworkLimitFailures: w.networkLimitFailures.Load(),
		NetworkLimitAlerting: networkStats.Eligible >= w.networkLimitAlertBacklog || networkStats.OldestEligibleAge > 2*w.networkLimitRetention,
	}, nil
}

func (w *Worker) Close() { w.pool.Close() }
