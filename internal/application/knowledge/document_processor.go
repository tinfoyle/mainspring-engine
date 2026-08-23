package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultDocumentProcessingLease       = 10 * time.Minute
	DefaultDocumentProcessingMaxAttempts = 12
	MaximumDocumentProcessingMaxAttempts = 100
	documentProcessingWorkloadID         = "knowledge-document-processor"
	documentProcessingMaximumRetryDelay  = 15 * time.Minute
)

var (
	ErrDocumentProcessingClaim = errors.New("Knowledge document processing claim is invalid")
	ErrDocumentProcessingLease = errors.New("Knowledge document processing lease was lost")
)

type DocumentProcessingClaim struct {
	AccountID  ids.AccountID
	RevisionID ids.KnowledgeDocumentRevisionID
	LeaseID    string
	Attempt    int
}

func (claim DocumentProcessingClaim) Valid() bool {
	return ids.Validate(string(claim.AccountID)) == nil && ids.Validate(string(claim.RevisionID)) == nil && ids.Validate(claim.LeaseID) == nil && claim.Attempt > 0
}

type DocumentProcessingStats struct {
	Pending        uint64
	Ready          uint64
	Leased         uint64
	Retrying       uint64
	Completed      uint64
	DeadLetter     uint64
	OldestReadyAge time.Duration
}

type DocumentProcessingQueue interface {
	Claim(context.Context, string, time.Time, time.Duration) (DocumentProcessingClaim, bool, error)
	Complete(context.Context, DocumentProcessingClaim, time.Time) error
	Fail(context.Context, DocumentProcessingClaim, bool, time.Time, string, time.Time, int) (string, error)
	Stats(context.Context, time.Time) (DocumentProcessingStats, error)
}

type DocumentProcessingResult struct {
	Worked     bool
	Completed  bool
	DeadLetter bool
}

type DocumentProcessor struct {
	queue       DocumentProcessingQueue
	documents   *DocumentService
	objects     DocumentObjectStore
	scanner     MalwareScanner
	extractor   TextExtractor
	clock       Clock
	ids         ids.Generator
	lease       time.Duration
	maxAttempts int
	actor       knowledgedomain.Actor
}

const IntegrationSourceSyncWorkloadID = "integration-source-sync"

func NewDocumentProcessor(queue DocumentProcessingQueue, documents *DocumentService, objects DocumentObjectStore, scanner MalwareScanner, extractor TextExtractor, clock Clock, generator ids.Generator, lease time.Duration, maxAttempts int) (*DocumentProcessor, error) {
	if queue == nil || documents == nil || objects == nil || scanner == nil || extractor == nil || clock == nil || generator == nil || lease < time.Second || lease > 30*time.Minute || maxAttempts < 1 || maxAttempts > MaximumDocumentProcessingMaxAttempts {
		return nil, errors.New("Knowledge document processor dependencies or bounds are invalid")
	}
	return &DocumentProcessor{queue: queue, documents: documents, objects: objects, scanner: scanner, extractor: extractor, clock: clock, ids: generator, lease: lease, maxAttempts: maxAttempts, actor: knowledgedomain.Actor{Kind: knowledgedomain.ActorWorkload, ID: documentProcessingWorkloadID}}, nil
}

func (processor *DocumentProcessor) ProcessOne(ctx context.Context) (DocumentProcessingResult, error) {
	now := processor.clock.Now().UTC()
	claim, found, err := processor.queue.Claim(ctx, processor.ids.New(), now, processor.lease)
	if err != nil || !found {
		return DocumentProcessingResult{}, err
	}
	if !claim.Valid() {
		return processor.reject(ctx, claim, "claim_invalid", ErrDocumentProcessingClaim)
	}
	revision, err := processor.documents.repository.GetDocumentRevision(ctx, claim.AccountID, claim.RevisionID)
	if err != nil {
		return processor.retry(ctx, claim, "revision_load_failed", err)
	}
	correlationID := string(revision.ID)

	if revision.State == knowledgedomain.RevisionQuarantined {
		revision, err = processor.scan(ctx, claim, revision, correlationID)
		if err != nil {
			return processor.retry(ctx, claim, "malware_scan_failed", err)
		}
		if revision.State == knowledgedomain.RevisionFailed {
			return processor.complete(ctx, claim)
		}
	}
	if revision.State == knowledgedomain.RevisionExtracting && revision.Extraction == knowledgedomain.ExtractionPending {
		revision, err = processor.extract(ctx, claim, revision, correlationID)
		if err != nil {
			return processor.retry(ctx, claim, "extraction_failed", err)
		}
	}
	if revision.State == knowledgedomain.RevisionExtracting && revision.Extraction == knowledgedomain.ExtractionReady {
		revision, err = processor.index(ctx, claim, revision, correlationID)
		if err != nil {
			if errors.Is(err, ErrInvalid) {
				return processor.reject(ctx, claim, "chunking_invalid", err)
			}
			return processor.retry(ctx, claim, "indexing_failed", err)
		}
	}
	if revision.State == knowledgedomain.RevisionReady {
		if revision.CreatedBy.Kind == knowledgedomain.ActorWorkload && revision.CreatedBy.ID == IntegrationSourceSyncWorkloadID {
			if _, err := processor.documents.publishSourceRevision(ctx, processor.actor, claim.AccountID, revision.DocumentID, revision.ID); err != nil {
				return processor.retry(ctx, claim, "source_publication_failed", err)
			}
		}
		return processor.complete(ctx, claim)
	}
	if revision.State == knowledgedomain.RevisionFailed || revision.State == knowledgedomain.RevisionDeleted {
		return processor.complete(ctx, claim)
	}
	return processor.reject(ctx, claim, "revision_state_invalid", ErrDocumentProcessingClaim)
}

func (processor *DocumentProcessor) Stats(ctx context.Context) (DocumentProcessingStats, error) {
	return processor.queue.Stats(ctx, processor.clock.Now().UTC())
}

func (processor *DocumentProcessor) scan(ctx context.Context, claim DocumentProcessingClaim, revision knowledgedomain.DocumentRevision, correlationID string) (knowledgedomain.DocumentRevision, error) {
	body, err := processor.objects.Open(ctx, DocumentObjectIdentity{Key: revision.ObjectKey, Version: revision.ObjectVersion, Size: revision.ByteSize, ContentSHA256: revision.ContentSHA256})
	if err != nil {
		return revision, err
	}
	result, scanErr := processor.scanner.Scan(ctx, MalwareScanRequest{Body: body, Size: revision.ByteSize})
	closeErr := body.Close()
	if scanErr != nil || closeErr != nil {
		return revision, errors.Join(scanErr, closeErr)
	}
	return processor.documents.recordScan(ctx, processor.actor, claim.AccountID, claim.RevisionID, result.State, result.Engine, result.Signature, correlationID, processor.clock.Now().UTC())
}

func (processor *DocumentProcessor) extract(ctx context.Context, claim DocumentProcessingClaim, revision knowledgedomain.DocumentRevision, correlationID string) (knowledgedomain.DocumentRevision, error) {
	body, err := processor.objects.Open(ctx, DocumentObjectIdentity{Key: revision.ObjectKey, Version: revision.ObjectVersion, Size: revision.ByteSize, ContentSHA256: revision.ContentSHA256})
	if err != nil {
		return revision, err
	}
	var result TextExtractionResult
	var extractErr error
	if revision.VerifiedType == "text/plain" {
		result, extractErr = extractPlainTextIdentity(body, revision.ByteSize, revision.ContentSHA256)
	} else {
		result, extractErr = processor.extractor.Extract(ctx, TextExtractionRequest{Body: body, Size: revision.ByteSize, MediaType: revision.VerifiedType})
	}
	closeErr := body.Close()
	if extractErr != nil || closeErr != nil {
		return revision, errors.Join(extractErr, closeErr)
	}
	write, err := processor.objects.PutExtractedImmutable(ctx, ExtractedObjectWrite{AccountID: claim.AccountID, DocumentID: revision.DocumentID, RevisionID: revision.ID, Size: int64(len(result.Text)), ContentSHA256: result.TextSHA256, Body: bytes.NewReader(result.Text)})
	if err != nil {
		return revision, err
	}
	updated, err := processor.documents.recordExtraction(ctx, processor.actor, claim.AccountID, claim.RevisionID, result.TextSHA256, int64(len(result.Text)), result.Extractor, write.Identity.Key, write.Identity.Version, correlationID, processor.clock.Now().UTC())
	if err == nil || !write.Created {
		return updated, err
	}
	if cleanupErr := processor.objects.Delete(ctx, write.Identity); cleanupErr != nil {
		return revision, errors.Join(err, fmt.Errorf("extracted-object cleanup failed: %w", cleanupErr))
	}
	return revision, err
}

func extractPlainTextIdentity(body io.Reader, size int64, digest [sha256.Size]byte) (TextExtractionResult, error) {
	if body == nil || size < 1 || size > knowledgedomain.MaximumExtractedTextBytes || digest == ([sha256.Size]byte{}) {
		return TextExtractionResult{}, ErrInvalid
	}
	text, err := io.ReadAll(io.LimitReader(body, size+1))
	if err != nil || int64(len(text)) != size || sha256.Sum256(text) != digest {
		return TextExtractionResult{}, errors.Join(err, errors.New("plain-text source failed its integrity check"))
	}
	if _, err := ChunkExtractedText(text); err != nil {
		return TextExtractionResult{}, err
	}
	return TextExtractionResult{Text: text, TextSHA256: digest, Extractor: "Spyglass text/plain identity v1"}, nil
}

func (processor *DocumentProcessor) index(ctx context.Context, claim DocumentProcessingClaim, revision knowledgedomain.DocumentRevision, correlationID string) (knowledgedomain.DocumentRevision, error) {
	text, err := processor.readExtracted(ctx, revision)
	if err != nil {
		return revision, err
	}
	chunks, err := ChunkExtractedText(text)
	if err != nil {
		return revision, err
	}
	return processor.documents.index(ctx, processor.actor, claim.AccountID, claim.RevisionID, DocumentChunkGeneration, chunks, correlationID, processor.clock.Now().UTC())
}

func (processor *DocumentProcessor) readExtracted(ctx context.Context, revision knowledgedomain.DocumentRevision) ([]byte, error) {
	body, err := processor.objects.Open(ctx, DocumentObjectIdentity{Key: revision.ExtractedObjectKey, Version: revision.ExtractedObjectVersion, Size: revision.TextBytes, ContentSHA256: revision.TextSHA256})
	if err != nil {
		return nil, err
	}
	text, readErr := io.ReadAll(io.LimitReader(body, revision.TextBytes+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if int64(len(text)) != revision.TextBytes || sha256.Sum256(text) != revision.TextSHA256 {
		return nil, errors.New("extracted document object failed its integrity check")
	}
	return text, nil
}

func (processor *DocumentProcessor) complete(ctx context.Context, claim DocumentProcessingClaim) (DocumentProcessingResult, error) {
	err := processor.queue.Complete(ctx, claim, processor.clock.Now().UTC())
	return DocumentProcessingResult{Worked: true, Completed: err == nil}, err
}

func (processor *DocumentProcessor) reject(ctx context.Context, claim DocumentProcessingClaim, code string, cause error) (DocumentProcessingResult, error) {
	now := processor.clock.Now().UTC()
	state, failErr := processor.queue.Fail(ctx, claim, false, now, code, now, processor.maxAttempts)
	return DocumentProcessingResult{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func (processor *DocumentProcessor) retry(ctx context.Context, claim DocumentProcessingClaim, code string, cause error) (DocumentProcessingResult, error) {
	now := processor.clock.Now().UTC()
	next := now.Add(documentProcessingRetryDelay(claim.Attempt))
	state, failErr := processor.queue.Fail(ctx, claim, true, next, code, now, processor.maxAttempts)
	return DocumentProcessingResult{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func documentProcessingRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	for index := 1; index < attempt && delay < documentProcessingMaximumRetryDelay; index++ {
		delay *= 2
	}
	if delay > documentProcessingMaximumRetryDelay {
		return documentProcessingMaximumRetryDelay
	}
	return delay
}
