package accountexport

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

// SnapshotCoordinator keeps the global and assigned-cell repeatable-read
// snapshots open for the callback. It must invoke the callback exactly once
// after validating the Work placement and Account version.
type SnapshotCoordinator interface {
	WithSnapshot(context.Context, Work, func(Snapshot, []SectionSource, []ObjectSource) error) error
}

type StagedArtifact interface {
	io.Writer
	Rewind() (io.Reader, error)
	Cleanup() error
}

type ArtifactStager interface {
	Create(context.Context, string) (StagedArtifact, error)
}

type PipelineProducer struct {
	registry    *Registry
	coordinator SnapshotCoordinator
	stager      ArtifactStager
	publisher   ArtifactPublisher
}

func NewPipelineProducer(registry *Registry, coordinator SnapshotCoordinator, stager ArtifactStager, publisher ArtifactPublisher) (*PipelineProducer, error) {
	if registry == nil || coordinator == nil || stager == nil || publisher == nil {
		return nil, ErrInvalid
	}
	return &PipelineProducer{registry: registry, coordinator: coordinator, stager: stager, publisher: publisher}, nil
}

func (producer *PipelineProducer) Produce(ctx context.Context, work Work) (ProducedArtifact, error) {
	if !validWork(work) {
		return ProducedArtifact{}, productionError("invalid_work", true, ErrInvalid)
	}
	callbackCount := 0
	var produced ProducedArtifact
	err := producer.coordinator.WithSnapshot(ctx, work, func(snapshot Snapshot, sections []SectionSource, objects []ObjectSource) error {
		callbackCount++
		if callbackCount != 1 {
			return productionError("snapshot_protocol_invalid", true, ErrInvalid)
		}
		request := BuildRequest{AccountID: work.AccountID, ExportID: work.ID, RequestedBy: work.RequestedBy, RequestedAt: work.RequestedAt,
			ExpiresAt: work.ExpiresAt, Snapshot: snapshot}
		if !validRequest(request) || snapshot.CellID != work.CellID || snapshot.PlacementGeneration != work.PlacementGeneration || snapshot.AccountVersion != work.AccountVersion {
			return productionError("snapshot_identity_invalid", true, ErrInvalid)
		}
		builder, err := NewBuilder(producer.registry, sections, objects)
		if err != nil {
			return productionError("export_registry_invalid", true, err)
		}
		stage, err := producer.stager.Create(ctx, work.ID)
		if err != nil || stage == nil {
			return productionError("staging_unavailable", false, errors.Join(ErrUnavailable, err))
		}
		result, err := builder.Build(ctx, request, stage)
		if err != nil {
			cleanupErr := stage.Cleanup()
			if errors.Is(err, ErrInvalid) {
				return productionError("export_source_invalid", true, errors.Join(err, cleanupErr))
			}
			return productionError("export_source_unavailable", false, errors.Join(err, cleanupErr))
		}
		reader, err := stage.Rewind()
		if err != nil || reader == nil {
			cleanupErr := stage.Cleanup()
			return productionError("staging_unavailable", false, errors.Join(ErrUnavailable, err, cleanupErr))
		}
		artifact, err := producer.publisher.Publish(ctx, ArtifactWrite{ExportID: work.ID, Bytes: result.ArtifactBytes, SHA256: result.ArtifactSHA256, Body: reader})
		cleanupErr := stage.Cleanup()
		if err != nil {
			switch {
			case errors.Is(err, ErrArtifactConflict):
				return productionError("artifact_conflict", true, errors.Join(err, cleanupErr))
			case errors.Is(err, ErrArtifactIntegrity), errors.Is(err, ErrInvalid):
				return productionError("artifact_integrity", true, errors.Join(err, cleanupErr))
			default:
				return productionError("artifact_store_unavailable", false, errors.Join(err, cleanupErr))
			}
		}
		if cleanupErr != nil {
			// Publication may have succeeded. Returning a transient failure makes
			// the next lease reconcile the stable object before completion.
			return productionError("staging_cleanup_failed", false, cleanupErr)
		}
		if artifact.Bytes != result.ArtifactBytes || artifact.SHA256 != result.ArtifactSHA256 || !validArtifact(artifact) {
			return productionError("artifact_integrity", true, ErrArtifactIntegrity)
		}
		produced = ProducedArtifact{Snapshot: snapshot, Artifact: artifact}
		return nil
	})
	if err != nil {
		return ProducedArtifact{}, err
	}
	if callbackCount != 1 || !validArtifact(produced.Artifact) {
		return ProducedArtifact{}, productionError("snapshot_protocol_invalid", true, ErrInvalid)
	}
	return produced, nil
}

func validWork(work Work) bool {
	return idsValidate(work.ID) && idsValidate(string(work.AccountID)) && idsValidate(string(work.RequestedBy)) &&
		routecontext.ValidCellID(work.CellID) && work.PlacementGeneration > 0 && work.AccountVersion > 0 && work.Version > 0 && idsValidate(work.LeaseID) &&
		!work.RequestedAt.IsZero() && work.ExpiresAt.After(work.RequestedAt)
}

func idsValidate(value string) bool {
	return ids.Validate(value) == nil
}

type producerFailure struct {
	code      string
	permanent bool
	cause     error
}

func productionError(code string, permanent bool, cause error) error {
	return producerFailure{code: code, permanent: permanent, cause: cause}
}

func (failure producerFailure) Error() string {
	return fmt.Sprintf("Account export production %s: %v", failure.code, failure.cause)
}

func (failure producerFailure) Unwrap() error   { return failure.cause }
func (failure producerFailure) Code() string    { return failure.code }
func (failure producerFailure) Permanent() bool { return failure.permanent }

var _ Producer = (*PipelineProducer)(nil)
