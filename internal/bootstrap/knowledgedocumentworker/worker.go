// Package knowledgedocumentworker composes the private per-cell document
// processing worker. It is the only process that combines queue authority,
// object credentials, malware scanning, extraction and indexing.
package knowledgedocumentworker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/clamav"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/s3objects"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/tika"
	"github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	CellDatabaseURL    string
	MaxDatabaseConns   int32
	PollInterval       time.Duration
	Lease              time.Duration
	MaxAttempts        int
	ObjectEndpoint     string
	ObjectRegion       string
	ObjectBucket       string
	ObjectAccessKey    string
	ObjectSecretKey    string
	ObjectSecure       bool
	ObjectSSE          bool
	ObjectTransport    http.RoundTripper
	MalwareAddress     string
	MalwareTimeout     time.Duration
	ExtractorEndpoint  string
	ExtractorTimeout   time.Duration
	ExtractorTransport http.RoundTripper
}

type Status struct {
	Pending               uint64 `json:"pending"`
	Ready                 uint64 `json:"ready"`
	Leased                uint64 `json:"leased"`
	Retrying              uint64 `json:"retrying"`
	Completed             uint64 `json:"completed"`
	DeadLetter            uint64 `json:"dead_letter"`
	OldestReadyAgeSeconds int64  `json:"oldest_ready_age_seconds"`
	Processed             uint64 `json:"processed"`
	Succeeded             uint64 `json:"succeeded"`
	Failures              uint64 `json:"failures"`
	DeletionPending       uint64 `json:"deletion_pending"`
	DeletionReady         uint64 `json:"deletion_ready"`
	DeletionLeased        uint64 `json:"deletion_leased"`
	DeletionRetrying      uint64 `json:"deletion_retrying"`
	DeletionCompleted     uint64 `json:"deletion_completed"`
	DeletionDeadLetter    uint64 `json:"deletion_dead_letter"`
	DeletionOldestAge     int64  `json:"deletion_oldest_ready_age_seconds"`
}

type processor interface {
	ProcessOne(context.Context) (knowledge.DocumentProcessingResult, error)
	Stats(context.Context) (knowledge.DocumentProcessingStats, error)
}

type deleter interface {
	ProcessOne(context.Context) (knowledge.DocumentDeletionResult, error)
	Stats(context.Context) (knowledge.DocumentDeletionStats, error)
}

type dependency interface{ Verify(context.Context) error }

type Worker struct {
	pool       *pgxpool.Pool
	processor  processor
	deleter    deleter
	objects    dependency
	scanner    dependency
	extractor  dependency
	poll       time.Duration
	logger     *slog.Logger
	processed  atomic.Uint64
	succeeded  atomic.Uint64
	failures   atomic.Uint64
	nextDelete atomic.Bool
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.CellDatabaseURL == "" || logger == nil {
		return nil, errors.New("Knowledge document worker database URL and logger are required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.Lease == 0 {
		config.Lease = knowledge.DefaultDocumentProcessingLease
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = knowledge.DefaultDocumentProcessingMaxAttempts
	}
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute {
		return nil, errors.New("Knowledge document worker poll interval is out of bounds")
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
	closeOnError := func(err error) (*Worker, error) {
		pool.Close()
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return closeOnError(err)
	}
	cell, err := database.NewCellPool(pool)
	if err != nil {
		return closeOnError(err)
	}
	repository, err := postgres.NewKnowledgeRepository(cell)
	if err != nil {
		return closeOnError(err)
	}
	queue, err := postgres.NewKnowledgeDocumentProcessingQueue(pool)
	if err != nil {
		return closeOnError(err)
	}
	deletionQueue, err := postgres.NewKnowledgeDocumentDeletionQueue(pool)
	if err != nil {
		return closeOnError(err)
	}
	documents, err := knowledge.NewDocumentService(denyExternalAuthorization{}, repository, registration.SystemClock{})
	if err != nil {
		return closeOnError(err)
	}
	objects, err := s3objects.New(s3objects.Config{Endpoint: config.ObjectEndpoint, Region: config.ObjectRegion, Bucket: config.ObjectBucket, AccessKey: config.ObjectAccessKey, SecretKey: config.ObjectSecretKey, Secure: config.ObjectSecure, ServerSideEncryption: config.ObjectSSE, Transport: config.ObjectTransport})
	if err != nil {
		return closeOnError(err)
	}
	scanner, err := clamav.New(clamav.Config{Address: config.MalwareAddress, OperationTimeout: config.MalwareTimeout})
	if err != nil {
		return closeOnError(err)
	}
	extractor, err := tika.New(tika.Config{Endpoint: config.ExtractorEndpoint, Timeout: config.ExtractorTimeout, Transport: config.ExtractorTransport})
	if err != nil {
		return closeOnError(err)
	}
	for _, value := range []dependency{objects, scanner, extractor} {
		if err := value.Verify(ctx); err != nil {
			return closeOnError(err)
		}
	}
	application, err := knowledge.NewDocumentProcessor(queue, documents, objects, scanner, extractor, registration.SystemClock{}, ids.RandomGenerator{}, config.Lease, config.MaxAttempts)
	if err != nil {
		return closeOnError(err)
	}
	deletion, err := knowledge.NewDocumentDeleter(deletionQueue, repository, objects, registration.SystemClock{}, ids.RandomGenerator{}, config.Lease, config.MaxAttempts)
	if err != nil {
		return closeOnError(err)
	}
	return &Worker{pool: pool, processor: application, deleter: deletion, objects: objects, scanner: scanner, extractor: extractor, poll: config.PollInterval, logger: logger}, nil
}

func (worker *Worker) Run(ctx context.Context) error {
	for {
		worked, completed, err := worker.processOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if worked {
			worker.processed.Add(1)
		}
		if completed {
			worker.succeeded.Add(1)
		}
		if err != nil {
			worker.failures.Add(1)
			worker.logFailure(ctx, err)
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

func (worker *Worker) processOne(ctx context.Context) (bool, bool, error) {
	triedDeletion := worker.nextDelete.Swap(false)
	if triedDeletion {
		result, err := worker.deleter.ProcessOne(ctx)
		if result.Worked || err != nil {
			return result.Worked, result.Completed, err
		}
	}
	result, err := worker.processor.ProcessOne(ctx)
	if result.Worked || err != nil {
		worker.nextDelete.Store(true)
		return result.Worked, result.Completed, err
	}
	if triedDeletion {
		return false, false, nil
	}
	deleted, deleteErr := worker.deleter.ProcessOne(ctx)
	return deleted.Worked, deleted.Completed, deleteErr
}

func (worker *Worker) logFailure(ctx context.Context, processErr error) {
	stats, err := worker.processor.Stats(ctx)
	if err != nil {
		worker.logger.Error("Knowledge document processing failed", "error", processErr, "stats_error", err)
		return
	}
	deletion, deletionErr := worker.deleter.Stats(ctx)
	worker.logger.Error("Knowledge document lifecycle work failed", "error", processErr, "ready", stats.Ready, "leased", stats.Leased, "retrying", stats.Retrying, "dead_letter", stats.DeadLetter, "oldest_ready_age_seconds", int64(stats.OldestReadyAge/time.Second), "deletion_ready", deletion.Ready, "deletion_leased", deletion.Leased, "deletion_retrying", deletion.Retrying, "deletion_dead_letter", deletion.DeadLetter, "deletion_stats_error", deletionErr)
}

func (worker *Worker) Ready(ctx context.Context) error {
	if err := worker.pool.Ping(ctx); err != nil {
		return err
	}
	for _, value := range []dependency{worker.objects, worker.scanner, worker.extractor} {
		if err := value.Verify(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (worker *Worker) Status(ctx context.Context) (any, error) {
	stats, err := worker.processor.Stats(ctx)
	if err != nil {
		return nil, err
	}
	deletion, err := worker.deleter.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return Status{Pending: stats.Pending, Ready: stats.Ready, Leased: stats.Leased, Retrying: stats.Retrying, Completed: stats.Completed, DeadLetter: stats.DeadLetter, OldestReadyAgeSeconds: int64(stats.OldestReadyAge / time.Second), Processed: worker.processed.Load(), Succeeded: worker.succeeded.Load(), Failures: worker.failures.Load(), DeletionPending: deletion.Pending, DeletionReady: deletion.Ready, DeletionLeased: deletion.Leased, DeletionRetrying: deletion.Retrying, DeletionCompleted: deletion.Completed, DeletionDeadLetter: deletion.DeadLetter, DeletionOldestAge: int64(deletion.OldestReadyAge / time.Second)}, nil
}

func (worker *Worker) Close() { worker.pool.Close() }

// Processing transitions never enter the routed command surface. This guard
// makes an accidental call to an externally authorized DocumentService method
// fail closed. The processor uses only queue-claimed internal methods; its
// source-publication method additionally requires the exact processor and
// source-sync workload identities plus a ready captured revision.
type denyExternalAuthorization struct{}

func (denyExternalAuthorization) Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error) {
	return access.AccountContext{}, errors.New("routed authorization is unavailable in the Knowledge document worker")
}
