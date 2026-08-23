package accountexport

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ProducedArtifact struct {
	Snapshot Snapshot
	Artifact Artifact
}

// Producer owns coordinated global/cell repeatable-read snapshots, exact
// object reads, private staging, digest verification and create-if-absent
// publication. Retrying the same Work must reconcile the stable artifact key.
type Producer interface {
	Produce(context.Context, Work) (ProducedArtifact, error)
}

type BuildFailure interface {
	error
	Code() string
	Permanent() bool
}

type BuildProcessor struct {
	store      Store
	producer   Producer
	ids        ids.Generator
	clock      Clock
	lease      time.Duration
	retryDelay time.Duration
}

func NewBuildProcessor(store Store, producer Producer, generator ids.Generator, clock Clock, lease, retryDelay time.Duration) (*BuildProcessor, error) {
	if store == nil || producer == nil || generator == nil || clock == nil || lease <= 0 || lease > 30*time.Minute || retryDelay <= 0 || retryDelay > 24*time.Hour {
		return nil, ErrInvalid
	}
	return &BuildProcessor{store: store, producer: producer, ids: generator, clock: clock, lease: lease, retryDelay: retryDelay}, nil
}

func (processor *BuildProcessor) ProcessOne(ctx context.Context) (bool, error) {
	now := processor.clock.Now().UTC()
	work, found, err := processor.store.ClaimBuild(ctx, now, processor.lease, processor.ids.New(), processor.ids.New())
	if err != nil || !found {
		return found, err
	}
	produced, produceErr := processor.producer.Produce(ctx, work)
	if produceErr != nil {
		if ctx.Err() != nil {
			// The canceled context cannot durably record a classification. The
			// expired lease is the recovery boundary.
			return true, produceErr
		}
		code, permanent := "export_unavailable", false
		var classified BuildFailure
		if errors.As(produceErr, &classified) && validErrorCode(classified.Code()) {
			code, permanent = classified.Code(), classified.Permanent()
		}
		failedAt := processor.clock.Now().UTC()
		_, recordErr := processor.store.RecordFailure(ctx, FailureMutation{Work: work, EventID: processor.ids.New(), ErrorCode: code,
			NextAttemptAt: failedAt.Add(processor.retryDelay), Permanent: permanent, At: failedAt})
		return true, errors.Join(produceErr, recordErr)
	}
	buildRequest := BuildRequest{AccountID: work.AccountID, ExportID: work.ID, RequestedBy: work.RequestedBy, RequestedAt: work.RequestedAt,
		ExpiresAt: work.ExpiresAt, Snapshot: produced.Snapshot}
	if !validArtifact(produced.Artifact) || !validRequest(buildRequest) || produced.Snapshot.CellID != work.CellID ||
		produced.Snapshot.PlacementGeneration != work.PlacementGeneration || produced.Snapshot.AccountVersion != work.AccountVersion {
		_, recordErr := processor.store.RecordFailure(ctx, FailureMutation{Work: work, EventID: processor.ids.New(), ErrorCode: "invalid_artifact",
			Permanent: true, At: processor.clock.Now().UTC()})
		return true, errors.Join(ErrInvalid, recordErr)
	}
	_, err = processor.store.Complete(ctx, CompleteMutation{Work: work, EventID: processor.ids.New(), Snapshot: produced.Snapshot,
		Artifact: produced.Artifact, AvailableAt: processor.clock.Now().UTC()})
	return true, err
}

type ArtifactDeleter interface {
	Delete(context.Context, Artifact) error
}

type ExpiryProcessor struct {
	store   Store
	deleter ArtifactDeleter
	ids     ids.Generator
	clock   Clock
	lease   time.Duration
}

func NewExpiryProcessor(store Store, deleter ArtifactDeleter, generator ids.Generator, clock Clock, lease time.Duration) (*ExpiryProcessor, error) {
	if store == nil || deleter == nil || generator == nil || clock == nil || lease <= 0 || lease > 30*time.Minute {
		return nil, ErrInvalid
	}
	return &ExpiryProcessor{store: store, deleter: deleter, ids: generator, clock: clock, lease: lease}, nil
}

func (processor *ExpiryProcessor) ProcessOne(ctx context.Context) (bool, error) {
	work, found, err := processor.store.ClaimDeletion(ctx, processor.clock.Now().UTC(), processor.lease, processor.ids.New(), processor.ids.New())
	if err != nil || !found {
		return found, err
	}
	if err := processor.deleter.Delete(ctx, work.Artifact); err != nil {
		return true, err
	}
	_, err = processor.store.CompleteDeletion(ctx, work, processor.clock.Now().UTC(), processor.ids.New())
	return true, err
}
