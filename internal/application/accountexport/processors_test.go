package accountexport

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type processorIDs struct{ next uint64 }

func (generator *processorIDs) New() string {
	generator.next++
	return fmt.Sprintf("fa000000-0000-4000-8000-%012d", generator.next)
}

type processorStore struct {
	work          Work
	found         bool
	completion    CompleteMutation
	failure       FailureMutation
	deletion      DeletionWork
	deletedAt     time.Time
	deleteEventID string
}

func (*processorStore) Create(context.Context, CreateMutation) (Status, error) { return Status{}, nil }
func (*processorStore) Get(context.Context, ids.AccountID, string) (Status, error) {
	return Status{}, nil
}
func (*processorStore) List(context.Context, ids.AccountID, uint64) ([]Status, error) {
	return nil, nil
}
func (*processorStore) Cancel(context.Context, CancelMutation) (Status, error) { return Status{}, nil }
func (store *processorStore) ClaimBuild(_ context.Context, cellID ids.CellID, _ time.Time, _ time.Duration, leaseID, _ string) (Work, bool, error) {
	work := store.work
	if work.CellID != "" && work.CellID != cellID {
		return Work{}, false, nil
	}
	work.LeaseID = leaseID
	return work, store.found, nil
}
func (store *processorStore) Complete(_ context.Context, mutation CompleteMutation) (Status, error) {
	store.completion = mutation
	return Status{State: StateAvailable}, nil
}
func (store *processorStore) RecordFailure(_ context.Context, mutation FailureMutation) (Status, error) {
	store.failure = mutation
	return Status{State: StateFailed}, nil
}
func (store *processorStore) ClaimDeletion(_ context.Context, _ time.Time, _ time.Duration, leaseID, _ string) (DeletionWork, bool, error) {
	work := store.deletion
	work.LeaseID = leaseID
	return work, store.found, nil
}
func (store *processorStore) CompleteDeletion(_ context.Context, _ DeletionWork, at time.Time, eventID string) (Status, error) {
	store.deletedAt, store.deleteEventID = at, eventID
	return Status{State: StateDeleted}, nil
}

type producerStub struct {
	result ProducedArtifact
	err    error
}

func (producer producerStub) Produce(context.Context, Work) (ProducedArtifact, error) {
	return producer.result, producer.err
}

type classifiedBuildError struct {
	code      string
	permanent bool
}

func (failure classifiedBuildError) Error() string   { return "classified export failure" }
func (failure classifiedBuildError) Code() string    { return failure.code }
func (failure classifiedBuildError) Permanent() bool { return failure.permanent }

type deleterStub struct {
	artifact Artifact
	err      error
}

func (deleter *deleterStub) Delete(_ context.Context, artifact Artifact) error {
	deleter.artifact = artifact
	return deleter.err
}

func processorWork(now time.Time) Work {
	return Work{ID: "fb000000-0000-4000-8000-000000000001", AccountID: exportAccount, RequestedBy: exportOwner,
		CellID: "cell-us-east-01", PlacementGeneration: 2, AccountVersion: 7, AttemptCount: 1, Version: 2,
		RequestedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
}

func processorArtifact() Artifact {
	digest := sha256.Sum256([]byte("artifact"))
	return Artifact{Reference: "account-exports/fb/export.zip", SHA256: digest, Bytes: 2048}
}

func TestBuildProcessorCompletesOnlyExactProducedArtifact(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store := &processorStore{work: processorWork(now), found: true}
	produced := ProducedArtifact{Snapshot: Snapshot{CellID: "cell-us-east-01", PlacementGeneration: 2, AccountVersion: 7,
		GlobalAt: now.Add(time.Minute), CellAt: now.Add(2 * time.Minute)}, Artifact: processorArtifact()}
	processor, err := NewBuildProcessor(store, producerStub{result: produced}, &processorIDs{}, exportClock{now.Add(3 * time.Minute)}, "cell-us-east-01", 5*time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || store.completion.Artifact != produced.Artifact || store.completion.Snapshot != produced.Snapshot || store.completion.EventID == "" {
		t.Fatalf("worked=%v completion=%+v err=%v", worked, store.completion, err)
	}
}

func TestBuildProcessorDurablyClassifiesFailureAndInvalidOutput(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store := &processorStore{work: processorWork(now), found: true}
	producerErr := classifiedBuildError{code: "object_digest_mismatch", permanent: true}
	processor, _ := NewBuildProcessor(store, producerStub{err: producerErr}, &processorIDs{}, exportClock{now.Add(time.Minute)}, "cell-us-east-01", 5*time.Minute, 10*time.Minute)
	worked, err := processor.ProcessOne(context.Background())
	if !worked || !errors.Is(err, producerErr) || store.failure.ErrorCode != producerErr.code || !store.failure.Permanent || store.failure.NextAttemptAt != now.Add(11*time.Minute) {
		t.Fatalf("worked=%v failure=%+v err=%v", worked, store.failure, err)
	}
	store.failure = FailureMutation{}
	invalid := ProducedArtifact{Snapshot: Snapshot{CellID: "different-cell", PlacementGeneration: 2, AccountVersion: 7}, Artifact: processorArtifact()}
	processor, _ = NewBuildProcessor(store, producerStub{result: invalid}, &processorIDs{}, exportClock{now.Add(time.Minute)}, "cell-us-east-01", 5*time.Minute, 10*time.Minute)
	if worked, err := processor.ProcessOne(context.Background()); !worked || !errors.Is(err, ErrInvalid) || store.failure.ErrorCode != "invalid_artifact" || !store.failure.Permanent {
		t.Fatalf("invalid worked=%v failure=%+v err=%v", worked, store.failure, err)
	}
}

func TestExpiryProcessorDeletesExactArtifactBeforeEvidenceTransition(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	artifact := processorArtifact()
	store := &processorStore{found: true, deletion: DeletionWork{ID: "fc000000-0000-4000-8000-000000000001", AccountID: exportAccount, Version: 4, Artifact: artifact}}
	deleter := &deleterStub{}
	processor, err := NewExpiryProcessor(store, deleter, &processorIDs{}, exportClock{now}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || deleter.artifact != artifact || store.deletedAt != now || store.deleteEventID == "" {
		t.Fatalf("worked=%v deleted=%+v at=%v event=%q err=%v", worked, deleter.artifact, store.deletedAt, store.deleteEventID, err)
	}
	deleteErr := errors.New("object store unavailable")
	store.deletedAt, deleter.err = time.Time{}, deleteErr
	if worked, err := processor.ProcessOne(context.Background()); !worked || !errors.Is(err, deleteErr) || !store.deletedAt.IsZero() {
		t.Fatalf("failed delete worked=%v completed_at=%v err=%v", worked, store.deletedAt, err)
	}
}
