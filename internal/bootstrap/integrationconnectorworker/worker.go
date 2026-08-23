// Package integrationconnectorworker composes one least-privilege connector
// runtime per cell. Provider adapters, content readers and credential brokers
// are injected; the worker never falls back to an app or Agent runtime.
package integrationconnectorworker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type Config struct {
	GlobalDatabaseURL, CellDatabaseURL string
	CellID                             ids.CellID
	MaxGlobalConns, MaxCellConns       int32
	PollInterval, Lease                time.Duration
}

type processor interface {
	ProcessOne(context.Context) (bool, error)
}

type combinedProcessor struct {
	health, execution processor
}

func (processor *combinedProcessor) ProcessOne(ctx context.Context) (bool, error) {
	worked, err := processor.health.ProcessOne(ctx)
	if worked || err != nil {
		return worked, err
	}
	return processor.execution.ProcessOne(ctx)
}

type Worker struct {
	global, cell        *pgxpool.Pool
	processor           processor
	poll                time.Duration
	logger              *slog.Logger
	processed, failures atomic.Uint64
}

type Status struct {
	Processed uint64 `json:"processed"`
	Failures  uint64 `json:"failures"`
}

func New(ctx context.Context, config Config, broker integrationexecution.CredentialBroker, contents integrationexecution.ContentSource,
	definitions []integrationexecution.Definition, healthDefinitions []integrationhealth.Definition, logger *slog.Logger) (*Worker, error) {
	if config.GlobalDatabaseURL == "" || config.CellDatabaseURL == "" || !routecontext.ValidCellID(config.CellID) ||
		broker == nil || contents == nil || len(definitions) == 0 || len(healthDefinitions) == 0 || logger == nil {
		return nil, errors.New("Integration connector worker configuration is required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.Lease == 0 {
		config.Lease = integrationexecution.DefaultLease
	}
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute {
		return nil, errors.New("Integration connector worker poll interval is out of bounds")
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
	repository, err := postgres.NewIntegrationExecutionRepository(cell)
	if err != nil {
		return closeOnError(err)
	}
	authority, err := integrationexecution.NewCurrentAuthority(postgres.NewAccessRepository(global), config.CellID)
	if err != nil {
		return closeOnError(err)
	}
	payloads, err := integrationexecution.NewPayloadAssembler(repository, contents)
	if err != nil {
		return closeOnError(err)
	}
	application, err := integrationexecution.New(repository, authority, payloads, broker, ids.RandomGenerator{}, registration.SystemClock{}, config.Lease, definitions)
	if err != nil {
		return closeOnError(err)
	}
	healthRepository, err := postgres.NewIntegrationHealthRepository(cell)
	if err != nil {
		return closeOnError(err)
	}
	healthApplication, err := integrationhealth.New(healthRepository, authority, broker, ids.RandomGenerator{}, registration.SystemClock{}, config.Lease, healthDefinitions)
	if err != nil {
		return closeOnError(err)
	}
	return &Worker{global: global, cell: cell, processor: &combinedProcessor{health: healthApplication, execution: application}, poll: config.PollInterval, logger: logger}, nil
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
		worked, err := worker.processor.ProcessOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if worked {
			worker.processed.Add(1)
		}
		if err != nil {
			worker.failures.Add(1)
			worker.logger.Error("Integration connector execution failed", "error", err)
		}
		if worked {
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

func (worker *Worker) Ready(ctx context.Context) error {
	return errors.Join(worker.global.Ping(ctx), worker.cell.Ping(ctx))
}

func (worker *Worker) Status(context.Context) (any, error) {
	return Status{Processed: worker.processed.Load(), Failures: worker.failures.Load()}, nil
}

func (worker *Worker) Close() {
	worker.cell.Close()
	worker.global.Close()
}
