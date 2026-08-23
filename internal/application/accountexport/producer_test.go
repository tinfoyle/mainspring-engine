package accountexport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

type coordinatorStub struct {
	snapshot Snapshot
	sections []SectionSource
	objects  []ObjectSource
	err      error
	skip     bool
	twice    bool
}

func (stub coordinatorStub) WithSnapshot(ctx context.Context, _ Work, callback func(Snapshot, []SectionSource, []ObjectSource) error) error {
	if stub.err != nil || stub.skip {
		return stub.err
	}
	if err := callback(stub.snapshot, stub.sections, stub.objects); err != nil {
		return err
	}
	if stub.twice {
		return callback(stub.snapshot, stub.sections, stub.objects)
	}
	return nil
}

type memoryStage struct {
	bytes.Buffer
	cleanupErr error
	cleanups   int
}

func (stage *memoryStage) Rewind() (io.Reader, error) {
	return bytes.NewReader(append([]byte(nil), stage.Bytes()...)), nil
}
func (stage *memoryStage) Cleanup() error { stage.cleanups++; return stage.cleanupErr }

type stagerStub struct {
	stage *memoryStage
	err   error
}

func (stub stagerStub) Create(context.Context, string) (StagedArtifact, error) {
	return stub.stage, stub.err
}

type publisherStub struct {
	artifact Artifact
	body     []byte
	write    ArtifactWrite
	err      error
}

func (stub *publisherStub) Publish(_ context.Context, write ArtifactWrite) (Artifact, error) {
	stub.write = write
	if write.Body != nil {
		stub.body, _ = io.ReadAll(write.Body)
	}
	if stub.err != nil {
		return Artifact{}, stub.err
	}
	artifact := stub.artifact
	if artifact.Reference == "" {
		artifact = Artifact{Reference: "opaque-private-version", Bytes: write.Bytes, SHA256: write.SHA256}
	}
	return artifact, nil
}

func pipelineFixture(t *testing.T) (Work, coordinatorStub) {
	t.Helper()
	request := exportRequest()
	work := Work{ID: request.ExportID, AccountID: request.AccountID, RequestedBy: request.RequestedBy, CellID: request.Snapshot.CellID,
		PlacementGeneration: request.Snapshot.PlacementGeneration, AccountVersion: request.Snapshot.AccountVersion, Version: 2,
		LeaseID: "e4000000-0000-4000-8000-000000000004", RequestedAt: request.RequestedAt, ExpiresAt: request.ExpiresAt}
	coordinator := coordinatorStub{snapshot: request.Snapshot,
		sections: []SectionSource{
			testSection{descriptor: Descriptor{Code: "account", SchemaVersion: 1, Stores: []string{"global-postgresql"}}, records: []json.RawMessage{json.RawMessage(`{"key":"accounts/one","state":"active"}`)}},
			testSection{descriptor: Descriptor{Code: "work", SchemaVersion: 1, Stores: []string{"cell-postgresql"}}, records: []json.RawMessage{json.RawMessage(`{"key":"work/one","state":"complete"}`)}},
		},
		objects: []ObjectSource{testObjects{descriptor: Descriptor{Code: "documents", SchemaVersion: 1, Stores: []string{"versioned-object-store"}}, values: []testObject{{path: "document/revision/source.txt", media: "text/plain", body: []byte("source")}}}},
	}
	return work, coordinator
}

func TestPipelineProducerBuildsStagesPublishesAndCleans(t *testing.T) {
	work, coordinator := pipelineFixture(t)
	stage := &memoryStage{}
	publisher := &publisherStub{}
	producer, err := NewPipelineProducer(testRegistry(t), coordinator, stagerStub{stage: stage}, publisher)
	if err != nil {
		t.Fatal(err)
	}
	produced, err := producer.Produce(context.Background(), work)
	if err != nil || produced.Snapshot != coordinator.snapshot || !validArtifact(produced.Artifact) || stage.cleanups != 1 {
		t.Fatalf("produced=%+v cleanups=%d err=%v", produced, stage.cleanups, err)
	}
	if publisher.write.ExportID != work.ID || publisher.write.Bytes != int64(len(publisher.body)) || publisher.write.SHA256 != sha256.Sum256(publisher.body) || !bytes.HasPrefix(publisher.body, []byte("PK")) {
		t.Fatalf("write=%+v body=%d", publisher.write, len(publisher.body))
	}
}

func TestPipelineProducerFailsClosedOnSnapshotAndCallbackProtocol(t *testing.T) {
	work, coordinator := pipelineFixture(t)
	coordinator.snapshot.PlacementGeneration++
	stage := &memoryStage{}
	producer, _ := NewPipelineProducer(testRegistry(t), coordinator, stagerStub{stage: stage}, &publisherStub{})
	_, err := producer.Produce(context.Background(), work)
	assertBuildFailure(t, err, "snapshot_identity_invalid", true)
	if stage.cleanups != 0 {
		t.Fatalf("invalid snapshot created stage")
	}
	coordinator.snapshot.PlacementGeneration--
	coordinator.skip = true
	producer, _ = NewPipelineProducer(testRegistry(t), coordinator, stagerStub{stage: stage}, &publisherStub{})
	_, err = producer.Produce(context.Background(), work)
	assertBuildFailure(t, err, "snapshot_protocol_invalid", true)
}

func TestPipelineProducerClassifiesPublicationAndCleanupFailure(t *testing.T) {
	work, coordinator := pipelineFixture(t)
	stage := &memoryStage{}
	publisher := &publisherStub{err: ErrArtifactConflict}
	producer, _ := NewPipelineProducer(testRegistry(t), coordinator, stagerStub{stage: stage}, publisher)
	_, err := producer.Produce(context.Background(), work)
	assertBuildFailure(t, err, "artifact_conflict", true)
	if stage.cleanups != 1 {
		t.Fatalf("publication failure cleanups=%d", stage.cleanups)
	}
	stage = &memoryStage{cleanupErr: errors.New("disk cleanup failed")}
	publisher = &publisherStub{}
	producer, _ = NewPipelineProducer(testRegistry(t), coordinator, stagerStub{stage: stage}, publisher)
	_, err = producer.Produce(context.Background(), work)
	assertBuildFailure(t, err, "staging_cleanup_failed", false)
	if len(publisher.body) == 0 {
		t.Fatalf("cleanup failure occurred before publication")
	}
}

func TestPipelineProducerRejectsInvalidSourcePermanently(t *testing.T) {
	work, coordinator := pipelineFixture(t)
	coordinator.sections[0] = testSection{descriptor: Descriptor{Code: "account", SchemaVersion: 1, Stores: []string{"global-postgresql"}}, records: []json.RawMessage{json.RawMessage(`{ "key": "not-canonical" }`)}}
	stage := &memoryStage{}
	producer, _ := NewPipelineProducer(testRegistry(t), coordinator, stagerStub{stage: stage}, &publisherStub{})
	_, err := producer.Produce(context.Background(), work)
	assertBuildFailure(t, err, "export_source_invalid", true)
	if stage.cleanups != 1 {
		t.Fatalf("invalid source cleanups=%d", stage.cleanups)
	}
}

func assertBuildFailure(t *testing.T, err error, code string, permanent bool) {
	t.Helper()
	var failure BuildFailure
	if !errors.As(err, &failure) || failure.Code() != code || failure.Permanent() != permanent {
		t.Fatalf("failure=%v code=%q permanent=%v", err, code, permanent)
	}
}
