package knowledge

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type documentDeletionQueue struct {
	claim       DocumentDeletionClaim
	found       bool
	completed   int
	failed      int
	retry       bool
	failureCode string
	state       string
}

func (queue *documentDeletionQueue) Claim(_ context.Context, leaseID string, _ time.Time, _ time.Duration) (DocumentDeletionClaim, bool, error) {
	queue.claim.LeaseID = leaseID
	return queue.claim, queue.found, nil
}
func (queue *documentDeletionQueue) Complete(context.Context, DocumentDeletionClaim, time.Time) error {
	queue.completed++
	return nil
}
func (queue *documentDeletionQueue) Fail(_ context.Context, _ DocumentDeletionClaim, retry bool, _ time.Time, code string, _ time.Time, _ int) (string, error) {
	queue.failed++
	queue.retry, queue.failureCode = retry, code
	if queue.state == "" {
		queue.state = "dead_letter"
	}
	return queue.state, nil
}
func (*documentDeletionQueue) Stats(context.Context, time.Time) (DocumentDeletionStats, error) {
	return DocumentDeletionStats{}, nil
}

type documentDeletionRepository struct {
	manifest       DocumentDeletionManifest
	loadErr        error
	receiptErr     error
	receipts       []DocumentDeletionReceipt
	receiptFailure int
}

func (repository *documentDeletionRepository) LoadDeletionManifest(context.Context, ids.AccountID, ids.KnowledgeDocumentID) (DocumentDeletionManifest, error) {
	return repository.manifest, repository.loadErr
}
func (repository *documentDeletionRepository) RecordDeletionReceipt(_ context.Context, receipt DocumentDeletionReceipt) error {
	if repository.receiptFailure > 0 {
		repository.receiptFailure--
		return repository.receiptErr
	}
	repository.receipts = append(repository.receipts, receipt)
	for index := range repository.manifest.Objects {
		if repository.manifest.Objects[index].RevisionID == receipt.Object.RevisionID && repository.manifest.Objects[index].Kind == receipt.Object.Kind {
			repository.manifest.Objects[index].Deleted = true
		}
	}
	return nil
}

type documentDeletionObjects struct {
	deleted []DocumentObjectIdentity
	err     error
}

func (*documentDeletionObjects) Verify(context.Context) error { return nil }
func (*documentDeletionObjects) PutImmutable(context.Context, SourceObjectWrite) (SourceObjectWriteResult, error) {
	return SourceObjectWriteResult{}, errors.New("not used")
}
func (*documentDeletionObjects) PutExtractedImmutable(context.Context, ExtractedObjectWrite) (ExtractedObjectWriteResult, error) {
	return ExtractedObjectWriteResult{}, errors.New("not used")
}
func (objects *documentDeletionObjects) Delete(_ context.Context, identity DocumentObjectIdentity) error {
	objects.deleted = append(objects.deleted, identity)
	return objects.err
}

type documentDeletionObjectStore struct{ *documentDeletionObjects }

func (store documentDeletionObjectStore) Open(context.Context, DocumentObjectIdentity) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}

type documentDeletionClock struct{ now time.Time }

func (clock *documentDeletionClock) Now() time.Time { return clock.now }

type documentDeletionIDs struct{ next int }

func (generator *documentDeletionIDs) New() string {
	generator.next++
	return "a4000000-0000-4000-8000-" + fmt.Sprintf("%012d", generator.next)
}

func documentDeletionFixture(t *testing.T) (*DocumentDeleter, *documentDeletionQueue, *documentDeletionRepository, *documentDeletionObjects, *documentDeletionClock) {
	t.Helper()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("a1000000-0000-4000-8000-000000000001")
	documentID := ids.KnowledgeDocumentID("a2000000-0000-4000-8000-000000000002")
	revisionID := ids.KnowledgeDocumentRevisionID("a3000000-0000-4000-8000-000000000003")
	requested := now.Add(-time.Minute)
	document, err := knowledgedomain.RestoreDocument(knowledgedomain.Document{ID: documentID, AccountID: accountID, Title: "Deletion fixture", Sensitivity: knowledgedomain.SensitivityInternal, CurrentRevisionID: revisionID, CurrentRevision: 1, State: knowledgedomain.DocumentDeletionPending, Version: 3, CreatedBy: knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: "a5000000-0000-4000-8000-000000000005"}, CreatedAt: now.Add(-time.Hour), UpdatedAt: requested, DeletionRequested: &requested})
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("source")
	extracted := []byte("extracted")
	prefix := "accounts/" + string(accountID) + "/documents/" + string(documentID) + "/revisions/" + string(revisionID)
	repository := &documentDeletionRepository{manifest: DocumentDeletionManifest{Document: document, Objects: []DocumentDeletionObject{
		{RevisionID: revisionID, Kind: "source", Identity: DocumentObjectIdentity{Key: prefix + "/source", Version: "source-version", Size: int64(len(source)), ContentSHA256: sha256.Sum256(source)}},
		{RevisionID: revisionID, Kind: "extracted", Identity: DocumentObjectIdentity{Key: prefix + "/extracted/text", Version: "extracted-version", Size: int64(len(extracted)), ContentSHA256: sha256.Sum256(extracted)}},
	}}}
	queue := &documentDeletionQueue{found: true, claim: DocumentDeletionClaim{AccountID: accountID, DocumentID: documentID, Attempt: 1}}
	objects := &documentDeletionObjects{}
	clock := &documentDeletionClock{now: now}
	deleter, err := NewDocumentDeleter(queue, repository, documentDeletionObjectStore{objects}, clock, &documentDeletionIDs{}, DefaultDocumentDeletionLease, DefaultDocumentDeletionMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	return deleter, queue, repository, objects, clock
}

func TestDocumentDeleterDeletesExactManifestAndCompletes(t *testing.T) {
	deleter, queue, repository, objects, _ := documentDeletionFixture(t)
	result, err := deleter.ProcessOne(context.Background())
	if err != nil || !result.Completed || queue.completed != 1 || len(objects.deleted) != 2 || len(repository.receipts) != 2 {
		t.Fatalf("result=%+v completed=%d deleted=%d receipts=%d err=%v", result, queue.completed, len(objects.deleted), len(repository.receipts), err)
	}
	if objects.deleted[0].Version != "source-version" || objects.deleted[1].Version != "extracted-version" {
		t.Fatalf("deleted identities=%+v", objects.deleted)
	}
}

func TestDocumentDeleterReplaysUnknownObjectCommit(t *testing.T) {
	deleter, queue, repository, objects, _ := documentDeletionFixture(t)
	repository.receiptFailure = 1
	repository.receiptErr = errors.New("database unavailable after object delete")
	queue.state = "retry"
	result, err := deleter.ProcessOne(context.Background())
	if err == nil || !result.Worked || queue.failureCode != "receipt_write_failed" || len(objects.deleted) != 1 || len(repository.receipts) != 0 {
		t.Fatalf("first result=%+v failure=%q deleted=%d receipts=%d err=%v", result, queue.failureCode, len(objects.deleted), len(repository.receipts), err)
	}
	queue.claim.Attempt = 2
	result, err = deleter.ProcessOne(context.Background())
	if err != nil || !result.Completed || queue.completed != 1 || len(objects.deleted) != 3 || len(repository.receipts) != 2 {
		t.Fatalf("replay result=%+v completed=%d deleted=%d receipts=%d err=%v", result, queue.completed, len(objects.deleted), len(repository.receipts), err)
	}
	if objects.deleted[0] != objects.deleted[1] {
		t.Fatalf("unknown-commit replay changed exact identity: first=%+v replay=%+v", objects.deleted[0], objects.deleted[1])
	}
}

func TestDocumentDeleterRetriesObjectFailureWithoutReceipt(t *testing.T) {
	deleter, queue, repository, objects, _ := documentDeletionFixture(t)
	objects.err = errors.New("object store unavailable")
	queue.state = "retry"
	result, err := deleter.ProcessOne(context.Background())
	if err == nil || !result.Worked || result.DeadLetter || queue.failureCode != "object_delete_failed" || !queue.retry || len(repository.receipts) != 0 {
		t.Fatalf("result=%+v failure=%q retry=%v receipts=%d err=%v", result, queue.failureCode, queue.retry, len(repository.receipts), err)
	}
}

func TestDocumentDeleterRejectsObjectOutsideClaimedAccountPath(t *testing.T) {
	deleter, queue, repository, objects, _ := documentDeletionFixture(t)
	repository.manifest.Objects[0].Identity.Key = "accounts/b1000000-0000-4000-8000-000000000001/documents/" + string(queue.claim.DocumentID) + "/revisions/" + string(repository.manifest.Objects[0].RevisionID) + "/source"
	result, err := deleter.ProcessOne(context.Background())
	if err == nil || !result.DeadLetter || queue.failureCode != "object_identity_invalid" || len(objects.deleted) != 0 || len(repository.receipts) != 0 {
		t.Fatalf("result=%+v failure=%q deleted=%d receipts=%d err=%v", result, queue.failureCode, len(objects.deleted), len(repository.receipts), err)
	}
}
