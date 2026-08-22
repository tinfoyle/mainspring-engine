package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type documentProcessingQueue struct {
	claim       DocumentProcessingClaim
	found       bool
	claimErr    error
	completeErr error
	completed   int
	failed      int
	retry       bool
	failureCode string
	next        time.Time
	state       string
}

func (queue *documentProcessingQueue) Claim(_ context.Context, leaseID string, _ time.Time, _ time.Duration) (DocumentProcessingClaim, bool, error) {
	queue.claim.LeaseID = leaseID
	return queue.claim, queue.found, queue.claimErr
}
func (queue *documentProcessingQueue) Complete(context.Context, DocumentProcessingClaim, time.Time) error {
	queue.completed++
	return queue.completeErr
}
func (queue *documentProcessingQueue) Fail(_ context.Context, _ DocumentProcessingClaim, retry bool, next time.Time, code string, _ time.Time, _ int) (string, error) {
	queue.failed++
	queue.retry, queue.next, queue.failureCode = retry, next, code
	if queue.state == "" {
		queue.state = "dead_letter"
	}
	return queue.state, nil
}
func (*documentProcessingQueue) Stats(context.Context, time.Time) (DocumentProcessingStats, error) {
	return DocumentProcessingStats{}, nil
}

type documentProcessingObjects struct {
	source       []byte
	extracted    []byte
	extractedPut int
	deleted      int
}

func (*documentProcessingObjects) Verify(context.Context) error { return nil }
func (store *documentProcessingObjects) PutImmutable(context.Context, SourceObjectWrite) (SourceObjectWriteResult, error) {
	return SourceObjectWriteResult{}, errors.New("not used")
}
func (store *documentProcessingObjects) PutExtractedImmutable(_ context.Context, write ExtractedObjectWrite) (ExtractedObjectWriteResult, error) {
	value, err := io.ReadAll(write.Body)
	if err != nil {
		return ExtractedObjectWriteResult{}, err
	}
	store.extractedPut++
	store.extracted = value
	key, err := write.Key()
	return ExtractedObjectWriteResult{Identity: DocumentObjectIdentity{Key: key, Version: "extracted-version-1", Size: int64(len(value)), ContentSHA256: sha256.Sum256(value)}, Created: true}, err
}
func (store *documentProcessingObjects) Open(_ context.Context, identity DocumentObjectIdentity) (io.ReadCloser, error) {
	if bytes.HasSuffix([]byte(identity.Key), []byte("/source")) {
		return io.NopCloser(bytes.NewReader(store.source)), nil
	}
	return io.NopCloser(bytes.NewReader(store.extracted)), nil
}
func (store *documentProcessingObjects) Delete(context.Context, DocumentObjectIdentity) error {
	store.deleted++
	return nil
}

type documentProcessingScanner struct {
	result MalwareScanResult
	err    error
	calls  int
}

func (*documentProcessingScanner) Verify(context.Context) error { return nil }
func (scanner *documentProcessingScanner) Scan(_ context.Context, request MalwareScanRequest) (MalwareScanResult, error) {
	scanner.calls++
	_, readErr := io.ReadAll(request.Body)
	return scanner.result, errors.Join(scanner.err, readErr)
}

type documentProcessingExtractor struct {
	text  []byte
	err   error
	calls int
}

func (*documentProcessingExtractor) Verify(context.Context) error { return nil }
func (extractor *documentProcessingExtractor) Extract(_ context.Context, request TextExtractionRequest) (TextExtractionResult, error) {
	extractor.calls++
	_, readErr := io.ReadAll(request.Body)
	if extractor.err != nil || readErr != nil {
		return TextExtractionResult{}, errors.Join(extractor.err, readErr)
	}
	return TextExtractionResult{Text: extractor.text, TextSHA256: sha256.Sum256(extractor.text), Extractor: "Apache Tika 3.2.3"}, nil
}

type documentProcessingIDs struct{ value string }

func (generator documentProcessingIDs) New() string { return generator.value }

func documentProcessorFixture(t *testing.T, source, extracted []byte) (*DocumentProcessor, *documentRepository, *documentProcessingQueue, *documentProcessingObjects, *documentProcessingScanner, *documentProcessingExtractor, *knowledgeClock) {
	t.Helper()
	documents, _, repository, clock := documentServiceFixture(t)
	_, revision, err := documents.Admit(context.Background(), AdmitDocumentCommand{
		Actor: knowledgedomainActorUser(), AccountID: appKnowledgeAccount, DocumentID: appKnowledgeDocument, RevisionID: appKnowledgeRevision,
		Title: "Processor fixture", Sensitivity: knowledgedomain.SensitivityInternal, Filename: "fixture.pdf", DeclaredType: "application/pdf", VerifiedType: "application/pdf",
		ByteSize: int64(len(source)), ContentSHA256: sha256.Sum256(source), ObjectKey: "accounts/" + string(appKnowledgeAccount) + "/documents/" + string(appKnowledgeDocument) + "/revisions/" + string(appKnowledgeRevision) + "/source", ObjectVersion: "source-version-1", ChangeSummary: "Initial", CorrelationID: appKnowledgeOperation,
	})
	if err != nil {
		t.Fatal(err)
	}
	queue := &documentProcessingQueue{found: true, claim: DocumentProcessingClaim{AccountID: appKnowledgeAccount, RevisionID: revision.ID, Attempt: 1}}
	objects := &documentProcessingObjects{source: source}
	scanner := &documentProcessingScanner{result: MalwareScanResult{State: knowledgedomain.ScanClean, Engine: "ClamAV 1.4.6"}}
	extractor := &documentProcessingExtractor{text: extracted}
	processor, err := NewDocumentProcessor(queue, documents, objects, scanner, extractor, clock, documentProcessingIDs{value: "a4000000-0000-4000-8000-000000000004"}, DefaultDocumentProcessingLease, DefaultDocumentProcessingMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	return processor, repository, queue, objects, scanner, extractor, clock
}

func knowledgedomainActorUser() access.Actor {
	return access.Actor{UserID: appKnowledgeUser}
}

func TestDocumentProcessorCompletesCleanRestartSafePipeline(t *testing.T) {
	source := []byte("source document")
	extracted := []byte(bytes.Repeat([]byte("alpha beta gamma delta. "), 260))
	processor, repository, queue, objects, scanner, extractor, clock := documentProcessorFixture(t, source, extracted)
	clock.now = clock.now.Add(time.Second)
	result, err := processor.ProcessOne(context.Background())
	if err != nil || !result.Completed || queue.completed != 1 || repository.revision.State != knowledgedomain.RevisionReady || len(repository.chunks) < 2 {
		t.Fatalf("result=%+v completed=%d revision=%+v chunks=%d err=%v", result, queue.completed, repository.revision, len(repository.chunks), err)
	}
	if scanner.calls != 1 || extractor.calls != 1 || objects.extractedPut != 1 || repository.revision.IndexGeneration != DocumentChunkGeneration {
		t.Fatalf("scanner=%d extractor=%d puts=%d generation=%q", scanner.calls, extractor.calls, objects.extractedPut, repository.revision.IndexGeneration)
	}

	queue.completeErr = errors.New("queue unavailable")
	result, err = processor.ProcessOne(context.Background())
	if err == nil || result.Completed || scanner.calls != 1 || extractor.calls != 1 || objects.extractedPut != 1 {
		t.Fatalf("ready replay result=%+v scanner=%d extractor=%d puts=%d err=%v", result, scanner.calls, extractor.calls, objects.extractedPut, err)
	}
}

func TestDocumentProcessorPreservesScannedPlainTextBytesWithoutTikaNormalization(t *testing.T) {
	source := []byte("first line\r\nsecond line\r\n")
	processor, repository, queue, objects, scanner, extractor, clock := documentProcessorFixture(t, source, []byte("normalized by external extractor"))
	repository.revision.Filename = "fixture.txt"
	repository.revision.DeclaredType = "text/plain"
	repository.revision.VerifiedType = "text/plain"
	clock.now = clock.now.Add(time.Second)
	result, err := processor.ProcessOne(context.Background())
	if err != nil || !result.Completed || queue.completed != 1 || repository.revision.State != knowledgedomain.RevisionReady {
		t.Fatalf("result=%+v completed=%d revision=%+v err=%v", result, queue.completed, repository.revision, err)
	}
	if scanner.calls != 1 || extractor.calls != 0 || objects.extractedPut != 1 || !bytes.Equal(objects.extracted, source) || repository.revision.TextBytes != int64(len(source)) || repository.revision.TextSHA256 != sha256.Sum256(source) || repository.revision.Extractor != "Spyglass text/plain identity v1" {
		t.Fatalf("scanner=%d extractor=%d puts=%d extracted=%q revision=%+v", scanner.calls, extractor.calls, objects.extractedPut, objects.extracted, repository.revision)
	}
}

func TestDocumentProcessorCompletesInfectedRevisionWithoutExtraction(t *testing.T) {
	processor, repository, queue, objects, scanner, extractor, clock := documentProcessorFixture(t, []byte("infected fixture"), []byte("unused"))
	scanner.result = MalwareScanResult{State: knowledgedomain.ScanInfected, Engine: "ClamAV 1.4.6", Signature: "Eicar-Signature"}
	clock.now = clock.now.Add(time.Second)
	result, err := processor.ProcessOne(context.Background())
	if err != nil || !result.Completed || repository.revision.State != knowledgedomain.RevisionFailed || repository.document.State != knowledgedomain.DocumentFailed || repository.revision.ScanState != knowledgedomain.ScanInfected || extractor.calls != 0 || objects.extractedPut != 0 || queue.completed != 1 {
		t.Fatalf("result=%+v document=%+v revision=%+v extractor=%d puts=%d completed=%d err=%v", result, repository.document, repository.revision, extractor.calls, objects.extractedPut, queue.completed, err)
	}
}

func TestDocumentProcessorRetriesTransientScanFailure(t *testing.T) {
	processor, repository, queue, _, scanner, _, clock := documentProcessorFixture(t, []byte("source"), []byte("unused"))
	scanner.err = errors.New("scanner unavailable")
	queue.state = "retry"
	clock.now = clock.now.Add(time.Second)
	result, err := processor.ProcessOne(context.Background())
	if err == nil || !result.Worked || result.DeadLetter || queue.failed != 1 || !queue.retry || queue.failureCode != "malware_scan_failed" || repository.revision.State != knowledgedomain.RevisionQuarantined {
		t.Fatalf("result=%+v failed=%d retry=%v code=%q revision=%+v err=%v", result, queue.failed, queue.retry, queue.failureCode, repository.revision, err)
	}
}

var _ ids.Generator = documentProcessingIDs{}
