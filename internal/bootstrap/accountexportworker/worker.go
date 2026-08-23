// Package accountexportworker composes the private per-cell artifact builder
// and the global artifact-expiry worker. Neither process exposes a customer
// transport or accepts caller-supplied object keys.
package accountexportworker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/localartifacts"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/s3objects"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type ObjectConfig struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	Secure    bool
	SSE       bool
	Transport http.RoundTripper
}

func (config ObjectConfig) adapter() s3objects.Config {
	return s3objects.Config{Endpoint: config.Endpoint, Region: config.Region, Bucket: config.Bucket, AccessKey: config.AccessKey,
		SecretKey: config.SecretKey, Secure: config.Secure, ServerSideEncryption: config.SSE, Transport: config.Transport}
}

type BuildConfig struct {
	GlobalDatabaseURL string
	CellDatabaseURL   string
	CellID            ids.CellID
	GlobalMaxConns    int32
	CellMaxConns      int32
	PollInterval      time.Duration
	Lease             time.Duration
	RetryDelay        time.Duration
	StagingRoot       string
	SourceObjects     ObjectConfig
	ArtifactObjects   ObjectConfig
}

type ExpiryConfig struct {
	GlobalDatabaseURL string
	GlobalMaxConns    int32
	PollInterval      time.Duration
	Lease             time.Duration
	ArtifactObjects   ObjectConfig
}

type processor interface {
	ProcessOne(context.Context) (bool, error)
}

type objectDependency interface {
	VerifyReadOnly(context.Context) error
}

type Status struct {
	Processed uint64 `json:"processed"`
	Failures  uint64 `json:"failures"`
}

type BuildWorker struct {
	global, cell *pgxpool.Pool
	processor    processor
	source       objectDependency
	artifacts    objectDependency
	poll         time.Duration
	logger       *slog.Logger
	processed    atomic.Uint64
	failures     atomic.Uint64
}

func NewBuild(ctx context.Context, config BuildConfig, logger *slog.Logger) (*BuildWorker, error) {
	if config.GlobalDatabaseURL == "" || config.CellDatabaseURL == "" || !routecontext.ValidCellID(config.CellID) || config.StagingRoot == "" || logger == nil {
		return nil, errors.New("Account export build worker global/cell databases, cell ID, staging root, and logger are required")
	}
	config.PollInterval, config.Lease, config.RetryDelay = buildDefaults(config.PollInterval, config.Lease, config.RetryDelay)
	if err := validatePolling(config.PollInterval, config.Lease); err != nil || config.RetryDelay <= 0 || config.RetryDelay > 24*time.Hour {
		return nil, errors.New("Account export build worker timing is out of bounds")
	}
	global, err := openPool(ctx, config.GlobalDatabaseURL, config.GlobalMaxConns)
	if err != nil {
		return nil, err
	}
	fail := func(cell *pgxpool.Pool, value error) (*BuildWorker, error) {
		if cell != nil {
			cell.Close()
		}
		global.Close()
		return nil, value
	}
	cell, err := openPool(ctx, config.CellDatabaseURL, config.CellMaxConns)
	if err != nil {
		return fail(nil, err)
	}
	source, err := s3objects.New(config.SourceObjects.adapter())
	if err != nil {
		return fail(cell, err)
	}
	artifacts, err := s3objects.NewExport(config.ArtifactObjects.adapter())
	if err != nil {
		return fail(cell, err)
	}
	for _, dependency := range []objectDependency{source, artifacts} {
		if err := dependency.VerifyReadOnly(ctx); err != nil {
			return fail(cell, err)
		}
	}
	if err := localartifacts.Prepare(config.StagingRoot); err != nil {
		return fail(cell, err)
	}
	stager, err := localartifacts.New(config.StagingRoot)
	if err != nil {
		return fail(cell, err)
	}
	factory, err := postgres.NewLaunchAccountExportSourceFactory(source, source)
	if err != nil {
		return fail(cell, err)
	}
	coordinator, err := postgres.NewAccountExportSnapshotCoordinator(global, cell, config.CellID, factory)
	if err != nil {
		return fail(cell, err)
	}
	registry, err := accountexport.LaunchRegistry()
	if err != nil {
		return fail(cell, err)
	}
	producer, err := accountexport.NewPipelineProducer(registry, coordinator, stager, artifacts)
	if err != nil {
		return fail(cell, err)
	}
	repository := postgres.NewAccountExportRepository(global)
	application, err := accountexport.NewBuildProcessor(repository, producer, ids.RandomGenerator{}, registration.SystemClock{}, config.CellID, config.Lease, config.RetryDelay)
	if err != nil {
		return fail(cell, err)
	}
	return &BuildWorker{global: global, cell: cell, processor: application, source: source, artifacts: artifacts, poll: config.PollInterval, logger: logger}, nil
}

type ExpiryWorker struct {
	global    *pgxpool.Pool
	processor processor
	artifacts objectDependency
	poll      time.Duration
	logger    *slog.Logger
	processed atomic.Uint64
	failures  atomic.Uint64
}

func NewExpiry(ctx context.Context, config ExpiryConfig, logger *slog.Logger) (*ExpiryWorker, error) {
	if config.GlobalDatabaseURL == "" || logger == nil {
		return nil, errors.New("Account export expiry worker global database and logger are required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.Lease <= 0 {
		config.Lease = 5 * time.Minute
	}
	if err := validatePolling(config.PollInterval, config.Lease); err != nil {
		return nil, err
	}
	global, err := openPool(ctx, config.GlobalDatabaseURL, config.GlobalMaxConns)
	if err != nil {
		return nil, err
	}
	fail := func(value error) (*ExpiryWorker, error) {
		global.Close()
		return nil, value
	}
	artifacts, err := s3objects.NewExport(config.ArtifactObjects.adapter())
	if err != nil {
		return fail(err)
	}
	if err := artifacts.VerifyReadOnly(ctx); err != nil {
		return fail(err)
	}
	repository := postgres.NewAccountExportRepository(global)
	application, err := accountexport.NewExpiryProcessor(repository, artifacts, ids.RandomGenerator{}, registration.SystemClock{}, config.Lease)
	if err != nil {
		return fail(err)
	}
	return &ExpiryWorker{global: global, processor: application, artifacts: artifacts, poll: config.PollInterval, logger: logger}, nil
}

func buildDefaults(poll, lease, retry time.Duration) (time.Duration, time.Duration, time.Duration) {
	if poll <= 0 {
		poll = time.Second
	}
	if lease <= 0 {
		lease = 20 * time.Minute
	}
	if retry <= 0 {
		retry = 5 * time.Minute
	}
	return poll, lease, retry
}

func validatePolling(poll, lease time.Duration) error {
	if poll < 100*time.Millisecond || poll > time.Minute || lease < time.Second || lease > 30*time.Minute {
		return errors.New("Account export worker poll interval or lease is out of bounds")
	}
	return nil
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

func run(ctx context.Context, application processor, poll time.Duration, logger *slog.Logger, processed, failures *atomic.Uint64, kind string) error {
	for {
		worked, err := application.ProcessOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			failures.Add(1)
			logger.Error("Account export work failed", "kind", kind, "error", err)
		}
		if worked {
			processed.Add(1)
			continue
		}
		timer := time.NewTimer(poll)
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

func (worker *BuildWorker) Run(ctx context.Context) error {
	return run(ctx, worker.processor, worker.poll, worker.logger, &worker.processed, &worker.failures, "build")
}

func (worker *BuildWorker) Ready(ctx context.Context) error {
	if err := worker.global.Ping(ctx); err != nil {
		return err
	}
	if err := worker.cell.Ping(ctx); err != nil {
		return err
	}
	if err := worker.source.VerifyReadOnly(ctx); err != nil {
		return err
	}
	return worker.artifacts.VerifyReadOnly(ctx)
}

func (worker *BuildWorker) Status(context.Context) (any, error) {
	return Status{Processed: worker.processed.Load(), Failures: worker.failures.Load()}, nil
}

func (worker *BuildWorker) Close() {
	worker.cell.Close()
	worker.global.Close()
}

func (worker *ExpiryWorker) Run(ctx context.Context) error {
	return run(ctx, worker.processor, worker.poll, worker.logger, &worker.processed, &worker.failures, "expiry")
}

func (worker *ExpiryWorker) Ready(ctx context.Context) error {
	if err := worker.global.Ping(ctx); err != nil {
		return err
	}
	return worker.artifacts.VerifyReadOnly(ctx)
}

func (worker *ExpiryWorker) Status(context.Context) (any, error) {
	return Status{Processed: worker.processed.Load(), Failures: worker.failures.Load()}, nil
}

func (worker *ExpiryWorker) Close() { worker.global.Close() }
