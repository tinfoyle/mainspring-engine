// Package integrationconnectorworker composes one least-privilege connector
// runtime per cell. Provider adapters, content readers and credential brokers
// are injected; the worker never falls back to an app or Agent runtime.
package integrationconnectorworker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	"github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/scheduledreports"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type Config struct {
	ReportSender                       scheduledreports.Sender
	GlobalDatabaseURL, CellDatabaseURL string
	CellID                             ids.CellID
	MaxGlobalConns, MaxCellConns       int32
	PollInterval, Lease                time.Duration
}

type processor interface {
	ProcessOne(context.Context) (bool, error)
}

type combinedProcessor struct {
	mu         sync.Mutex
	processors []processor
	next       int
}

func (processor *combinedProcessor) ProcessOne(ctx context.Context) (bool, error) {
	processor.mu.Lock()
	defer processor.mu.Unlock()
	if len(processor.processors) == 0 {
		return false, errors.New("Integration connector processors are required")
	}
	start := processor.next % len(processor.processors)
	for offset := range processor.processors {
		index := (start + offset) % len(processor.processors)
		worked, err := processor.processors[index].ProcessOne(ctx)
		if worked || err != nil {
			processor.next = (index + 1) % len(processor.processors)
			return worked, err
		}
	}
	processor.next = (start + 1) % len(processor.processors)
	return false, nil
}

type SourceDependencies struct {
	Provider integrationsync.Provider
	Cursors  integrationsync.CursorCipher
	Objects  knowledge.SourceObjectStore
	Timeout  time.Duration
}

type Worker struct {
	global, cell        *pgxpool.Pool
	processor           processor
	sourceObjects       knowledge.SourceObjectStore
	poll                time.Duration
	logger              *slog.Logger
	processed, failures atomic.Uint64
}

type Status struct {
	Processed uint64 `json:"processed"`
	Failures  uint64 `json:"failures"`
}

func New(ctx context.Context, config Config, broker integrationexecution.CredentialBroker, contents integrationexecution.ContentSource,
	definitions []integrationexecution.Definition, healthDefinitions []integrationhealth.Definition, source SourceDependencies, logger *slog.Logger) (*Worker, error) {
	if config.GlobalDatabaseURL == "" || config.CellDatabaseURL == "" || !routecontext.ValidCellID(config.CellID) ||
		broker == nil || contents == nil || len(definitions) == 0 || len(healthDefinitions) == 0 || source.Provider == nil ||
		source.Cursors == nil || source.Objects == nil || source.Timeout < 100*time.Millisecond || source.Timeout > integrationsync.MaximumLease || logger == nil {
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
	accessRepository := postgres.NewAccessRepository(global)
	repository, err := postgres.NewIntegrationExecutionRepository(cell)
	if err != nil {
		return closeOnError(err)
	}
	authority, err := integrationexecution.NewCurrentAuthority(accessRepository, config.CellID)
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
	sourceRepository, err := postgres.NewIntegrationSourceSyncRepository(cell)
	if err != nil {
		return closeOnError(err)
	}
	sourceAuthority, err := integrationsync.NewCurrentAuthority(accessRepository, config.CellID)
	if err != nil {
		return closeOnError(err)
	}
	workloadAuthorizer, err := access.NewWorkloadAuthorizer(accessRepository)
	if err != nil {
		return closeOnError(err)
	}
	cellPool, err := database.NewCellPool(cell)
	if err != nil {
		return closeOnError(err)
	}
	knowledgeRepository, err := postgres.NewKnowledgeRepository(cellPool)
	if err != nil {
		return closeOnError(err)
	}
	documents, err := knowledge.NewDocumentService(workloadAuthorizer, knowledgeRepository, registration.SystemClock{})
	if err != nil {
		return closeOnError(err)
	}
	admission, err := knowledge.NewDocumentAdmissionService(documents, source.Objects)
	if err != nil {
		return closeOnError(err)
	}
	sink, err := integrationsync.NewKnowledgeCaptureSink(documents, admission)
	if err != nil {
		return closeOnError(err)
	}
	sourceApplication, err := integrationsync.New(sourceRepository, sourceAuthority, broker, source.Cursors, source.Provider, sink,
		ids.RandomGenerator{}, registration.SystemClock{}, config.Lease, source.Timeout)
	if err != nil {
		return closeOnError(err)
	}
	processors := []processor{healthApplication, application, sourceApplication}
	if config.ReportSender != nil {
		reports := postgres.NewScheduleReportRepository(cell, global, config.CellID)
		reportProcessor, err := scheduledreports.New(reports, reports, config.ReportSender, ids.RandomGenerator{}, registration.SystemClock{})
		if err != nil {
			return closeOnError(err)
		}
		processors = append(processors, reportProcessor)
	}
	return &Worker{global: global, cell: cell, processor: &combinedProcessor{processors: processors},
		sourceObjects: source.Objects, poll: config.PollInterval, logger: logger}, nil
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
	return errors.Join(worker.global.Ping(ctx), worker.cell.Ping(ctx), worker.sourceObjects.Verify(ctx))
}

func (worker *Worker) Status(context.Context) (any, error) {
	return Status{Processed: worker.processed.Load(), Failures: worker.failures.Load()}, nil
}

func (worker *Worker) Close() {
	worker.cell.Close()
	worker.global.Close()
}
