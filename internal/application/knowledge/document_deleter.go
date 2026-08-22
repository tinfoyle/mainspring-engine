package knowledge

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultDocumentDeletionLease       = 10 * time.Minute
	DefaultDocumentDeletionMaxAttempts = 12
	documentDeletionMaximumRetryDelay  = 15 * time.Minute
)

var (
	ErrDocumentDeletionClaim = errors.New("Knowledge document deletion claim is invalid")
	ErrDocumentDeletionLease = errors.New("Knowledge document deletion lease was lost")
)

type DocumentDeletionClaim struct {
	AccountID  ids.AccountID
	DocumentID ids.KnowledgeDocumentID
	LeaseID    string
	Attempt    int
}

func (claim DocumentDeletionClaim) Valid() bool {
	return ids.Validate(string(claim.AccountID)) == nil && ids.Validate(string(claim.DocumentID)) == nil && ids.Validate(claim.LeaseID) == nil && claim.Attempt > 0
}

type DocumentDeletionStats struct {
	Pending        uint64
	Ready          uint64
	Leased         uint64
	Retrying       uint64
	Completed      uint64
	DeadLetter     uint64
	OldestReadyAge time.Duration
}

type DocumentDeletionQueue interface {
	Claim(context.Context, string, time.Time, time.Duration) (DocumentDeletionClaim, bool, error)
	Complete(context.Context, DocumentDeletionClaim, time.Time) error
	Fail(context.Context, DocumentDeletionClaim, bool, time.Time, string, time.Time, int) (string, error)
	Stats(context.Context, time.Time) (DocumentDeletionStats, error)
}

type DocumentDeletionObject struct {
	RevisionID ids.KnowledgeDocumentRevisionID
	Kind       string
	Identity   DocumentObjectIdentity
	Deleted    bool
}

func (value DocumentDeletionObject) Valid(accountID ids.AccountID, documentID ids.KnowledgeDocumentID) bool {
	if ids.Validate(string(value.RevisionID)) != nil || value.Identity.Version == "" || value.Identity.Size <= 0 || value.Identity.ContentSHA256 == ([sha256.Size]byte{}) {
		return false
	}
	var expected string
	var err error
	switch value.Kind {
	case "source":
		expected, err = knowledgedomain.SourceObjectKey(accountID, documentID, value.RevisionID)
	case "extracted":
		expected, err = knowledgedomain.ExtractedObjectKey(accountID, documentID, value.RevisionID)
	default:
		return false
	}
	return err == nil && value.Identity.Key == expected
}

type DocumentDeletionManifest struct {
	Document knowledgedomain.Document
	Objects  []DocumentDeletionObject
}

type DocumentDeletionReceipt struct {
	AccountID  ids.AccountID
	DocumentID ids.KnowledgeDocumentID
	Object     DocumentDeletionObject
	DeletedAt  time.Time
}

type DocumentDeletionRepository interface {
	LoadDeletionManifest(context.Context, ids.AccountID, ids.KnowledgeDocumentID) (DocumentDeletionManifest, error)
	RecordDeletionReceipt(context.Context, DocumentDeletionReceipt) error
}

type DocumentDeletionResult struct {
	Worked     bool
	Completed  bool
	DeadLetter bool
}

type DocumentDeleter struct {
	queue       DocumentDeletionQueue
	repository  DocumentDeletionRepository
	objects     DocumentObjectStore
	clock       Clock
	ids         ids.Generator
	lease       time.Duration
	maxAttempts int
}

func NewDocumentDeleter(queue DocumentDeletionQueue, repository DocumentDeletionRepository, objects DocumentObjectStore, clock Clock, generator ids.Generator, lease time.Duration, maxAttempts int) (*DocumentDeleter, error) {
	if queue == nil || repository == nil || objects == nil || clock == nil || generator == nil || lease < time.Second || lease > 30*time.Minute || maxAttempts < 1 || maxAttempts > MaximumDocumentProcessingMaxAttempts {
		return nil, errors.New("Knowledge document deleter dependencies or bounds are invalid")
	}
	return &DocumentDeleter{queue: queue, repository: repository, objects: objects, clock: clock, ids: generator, lease: lease, maxAttempts: maxAttempts}, nil
}

func (deleter *DocumentDeleter) ProcessOne(ctx context.Context) (DocumentDeletionResult, error) {
	now := deleter.clock.Now().UTC()
	claim, found, err := deleter.queue.Claim(ctx, deleter.ids.New(), now, deleter.lease)
	if err != nil || !found {
		return DocumentDeletionResult{}, err
	}
	if !claim.Valid() {
		return deleter.reject(ctx, claim, "claim_invalid", ErrDocumentDeletionClaim)
	}
	manifest, err := deleter.repository.LoadDeletionManifest(ctx, claim.AccountID, claim.DocumentID)
	if err != nil {
		return deleter.retry(ctx, claim, "manifest_load_failed", err)
	}
	if manifest.Document.AccountID != claim.AccountID || manifest.Document.ID != claim.DocumentID || manifest.Document.State != knowledgedomain.DocumentDeletionPending || manifest.Document.LegalHold || len(manifest.Objects) == 0 {
		return deleter.reject(ctx, claim, "manifest_invalid", ErrDocumentDeletionClaim)
	}
	if manifest.Document.RetainUntil != nil && now.Before(*manifest.Document.RetainUntil) {
		return deleter.reject(ctx, claim, "retention_active", knowledgedomain.ErrDocumentRetention)
	}
	for _, object := range manifest.Objects {
		if !object.Valid(claim.AccountID, claim.DocumentID) {
			return deleter.reject(ctx, claim, "object_identity_invalid", ErrDocumentDeletionClaim)
		}
		if object.Deleted {
			continue
		}
		if err := deleter.objects.Delete(ctx, object.Identity); err != nil {
			return deleter.retry(ctx, claim, "object_delete_failed", err)
		}
		if err := deleter.repository.RecordDeletionReceipt(ctx, DocumentDeletionReceipt{AccountID: claim.AccountID, DocumentID: claim.DocumentID, Object: object, DeletedAt: deleter.clock.Now().UTC()}); err != nil {
			return deleter.retry(ctx, claim, "receipt_write_failed", err)
		}
	}
	err = deleter.queue.Complete(ctx, claim, deleter.clock.Now().UTC())
	return DocumentDeletionResult{Worked: true, Completed: err == nil}, err
}

func (deleter *DocumentDeleter) Stats(ctx context.Context) (DocumentDeletionStats, error) {
	return deleter.queue.Stats(ctx, deleter.clock.Now().UTC())
}

func (deleter *DocumentDeleter) reject(ctx context.Context, claim DocumentDeletionClaim, code string, cause error) (DocumentDeletionResult, error) {
	now := deleter.clock.Now().UTC()
	state, failErr := deleter.queue.Fail(ctx, claim, false, now, code, now, deleter.maxAttempts)
	return DocumentDeletionResult{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func (deleter *DocumentDeleter) retry(ctx context.Context, claim DocumentDeletionClaim, code string, cause error) (DocumentDeletionResult, error) {
	now := deleter.clock.Now().UTC()
	next := now.Add(documentDeletionRetryDelay(claim.Attempt))
	state, failErr := deleter.queue.Fail(ctx, claim, true, next, code, now, deleter.maxAttempts)
	return DocumentDeletionResult{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func documentDeletionRetryDelay(attempt int) time.Duration {
	delay := documentProcessingRetryDelay(attempt)
	if delay > documentDeletionMaximumRetryDelay {
		return documentDeletionMaximumRetryDelay
	}
	return delay
}
