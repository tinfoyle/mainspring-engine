// Package agentprojectionworker composes the shared cell Agent result
// projection worker. The database role is execute-only and the envelope key
// is supplied at runtime, so neither boundary can recover plaintext alone.
package agentprojectionworker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/admissionhttp"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/agentprojection"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type Config struct {
	CellDatabaseURL    string
	CellID             ids.CellID
	AdmissionOrigin    string
	AdmissionTransport http.RoundTripper
	AllowHTTPAdmission bool
	MaxDatabaseConns   int32
	PollInterval       time.Duration
	Lease              time.Duration
	MaxAttempts        int
	EncryptionKeys     map[int][]byte
	ActiveKeyVersion   int
}

type Status struct {
	Pending               uint64 `json:"pending"`
	Ready                 uint64 `json:"ready"`
	Leased                uint64 `json:"leased"`
	Retrying              uint64 `json:"retrying"`
	DeadLetter            uint64 `json:"dead_letter"`
	OldestReadyAgeSeconds int64  `json:"oldest_ready_age_seconds"`
	Processed             uint64 `json:"processed"`
	Projected             uint64 `json:"projected"`
	Failures              uint64 `json:"failures"`
}

type processor interface {
	ProcessOne(context.Context) (agentprojection.Result, error)
	Stats(context.Context) (agentprojection.Stats, error)
}

type Worker struct {
	pool      *pgxpool.Pool
	processor processor
	poll      time.Duration
	logger    *slog.Logger
	processed atomic.Uint64
	projected atomic.Uint64
	failures  atomic.Uint64
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.CellDatabaseURL == "" || config.AdmissionOrigin == "" || !routecontext.ValidCellID(config.CellID) || logger == nil {
		return nil, errors.New("agent projection worker database URL and logger are required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.Lease == 0 {
		config.Lease = agentprojection.DefaultLease
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = agentprojection.DefaultMaxAttempts
	}
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute {
		return nil, errors.New("agent projection worker poll interval is out of bounds")
	}
	cipher, err := runnerbroker.NewCipher(config.EncryptionKeys, config.ActiveKeyVersion)
	if err != nil {
		return nil, err
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
	repository, err := postgres.NewAgentProjectionRepository(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	tokens, err := admissionhttp.New(config.AdmissionOrigin, config.AllowHTTPAdmission, config.AdmissionTransport)
	if err != nil {
		pool.Close()
		return nil, err
	}
	application, err := agentprojection.New(repository, cipher, tokens, config.CellID, registration.SystemClock{}, ids.RandomGenerator{}, config.Lease, config.MaxAttempts)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: application, poll: config.PollInterval, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		result, err := w.processor.ProcessOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if result.Worked {
			w.processed.Add(1)
		}
		if result.Projected {
			w.projected.Add(1)
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
		w.logger.Error("Agent result projection failed", "error", processErr, "stats_error", err)
		return
	}
	w.logger.Error("Agent result projection failed", "error", processErr, "ready", stats.Ready,
		"leased", stats.Leased, "retrying", stats.Retrying, "dead_letter", stats.DeadLetter,
		"oldest_ready_age_seconds", int64(stats.OldestReadyAge/time.Second))
}

func (w *Worker) Ready(ctx context.Context) error { return w.pool.Ping(ctx) }

func (w *Worker) Status(ctx context.Context) (any, error) {
	stats, err := w.processor.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return Status{Pending: stats.Pending, Ready: stats.Ready, Leased: stats.Leased, Retrying: stats.Retrying,
		DeadLetter: stats.DeadLetter, OldestReadyAgeSeconds: int64(stats.OldestReadyAge / time.Second),
		Processed: w.processed.Load(), Projected: w.projected.Load(), Failures: w.failures.Load()}, nil
}

func (w *Worker) Close() { w.pool.Close() }
