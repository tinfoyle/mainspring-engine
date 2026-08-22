package knowledge

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestDocumentRevisionFailClosedLifecycleAndPublication(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("a1000000-0000-4000-8000-000000000001")
	documentID := ids.KnowledgeDocumentID("a2000000-0000-4000-8000-000000000002")
	revisionID := ids.KnowledgeDocumentRevisionID("a3000000-0000-4000-8000-000000000003")
	actor := Actor{Kind: ActorUser, ID: "a4000000-0000-4000-8000-000000000004"}
	document, err := NewDocument(documentID, accountID, "Employee Handbook", SensitivityInternal, nil, actor, now)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := NewDocumentRevision(DocumentRevisionDraft{
		ID: revisionID, DocumentID: documentID, AccountID: accountID, Number: 1,
		Filename: "employee-handbook.pdf", DeclaredType: "application/pdf", VerifiedType: "application/pdf",
		ByteSize: 4096, ContentSHA256: sha256.Sum256([]byte("source")),
		ObjectKey:     "accounts/a1000000-0000-4000-8000-000000000001/documents/a2000000-0000-4000-8000-000000000002/revisions/a3000000-0000-4000-8000-000000000003/source",
		ObjectVersion: "opaque-version-1", ChangeSummary: "Initial revision", CreatedBy: actor,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := document.Publish(revision, 1, now.Add(time.Second)); !errors.Is(err, ErrState) {
		t.Fatalf("quarantined revision published: %v", err)
	}
	revision, err = revision.RecordScan(ScanClean, "clamav/1.4.3", "daily.cvd:27810", now.Add(time.Second))
	if err != nil || revision.State != RevisionExtracting {
		t.Fatalf("scan=%+v err=%v", revision, err)
	}
	if _, err := revision.RecordIndex("knowledge-v1", 1, now.Add(2*time.Second)); !errors.Is(err, ErrState) {
		t.Fatalf("revision indexed before extraction: %v", err)
	}
	revision, err = revision.RecordExtraction(sha256.Sum256([]byte("extracted text")), 14, "spyglass/pdf-v1", "accounts/a1000000-0000-4000-8000-000000000001/documents/a2000000-0000-4000-8000-000000000002/revisions/a3000000-0000-4000-8000-000000000003/extracted/text", "extracted-version-1", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.RecordIndex("knowledge-v1", 2, now.Add(3*time.Second))
	if err != nil || revision.State != RevisionReady {
		t.Fatalf("index=%+v err=%v", revision, err)
	}
	document, err = document.Publish(revision, 1, now.Add(4*time.Second))
	if err != nil || document.State != DocumentReady || document.CurrentRevisionID != revisionID || document.CurrentRevision != 1 || document.Version != 2 {
		t.Fatalf("published=%+v err=%v", document, err)
	}
}

func TestDocumentRevisionRejectsMediaObjectAndMalwareFailures(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("b1000000-0000-4000-8000-000000000001")
	documentID := ids.KnowledgeDocumentID("b2000000-0000-4000-8000-000000000002")
	revisionID := ids.KnowledgeDocumentRevisionID("b3000000-0000-4000-8000-000000000003")
	actor := Actor{Kind: ActorWorkload, ID: "worker:document-admission"}
	draft := DocumentRevisionDraft{
		ID: revisionID, DocumentID: documentID, AccountID: accountID, Number: 1, Filename: "plan.docx",
		DeclaredType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		VerifiedType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		ByteSize:     1024, ContentSHA256: sha256.Sum256([]byte("source")),
		ObjectKey:     "accounts/b1000000-0000-4000-8000-000000000001/documents/b2000000-0000-4000-8000-000000000002/revisions/b3000000-0000-4000-8000-000000000003/source",
		ObjectVersion: "v1", CreatedBy: actor,
	}
	wrongType := draft
	wrongType.VerifiedType = "application/pdf"
	if _, err := NewDocumentRevision(wrongType, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("media mismatch err=%v", err)
	}
	wrongKey := draft
	wrongKey.ObjectKey = "accounts/other/source"
	if _, err := NewDocumentRevision(wrongKey, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("object key mismatch err=%v", err)
	}
	revision, err := NewDocumentRevision(draft, now)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.RecordScan(ScanInfected, "clamav/1.4.3", "Eicar-Signature", now.Add(time.Second))
	if err != nil || revision.State != RevisionFailed || revision.FailureCode != "malware_scan_infected" {
		t.Fatalf("infected=%+v err=%v", revision, err)
	}
	if _, err := revision.RecordExtraction(sha256.Sum256([]byte("never")), 5, "extractor", "invalid", "version", now.Add(2*time.Second)); !errors.Is(err, ErrState) {
		t.Fatalf("infected revision extracted: %v", err)
	}
}

func TestDocumentRevisionCanFailAfterSuccessfulExtraction(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	revision, err := NewDocumentRevision(DocumentRevisionDraft{
		ID: "e3000000-0000-4000-8000-000000000003", DocumentID: "e2000000-0000-4000-8000-000000000002", AccountID: "e1000000-0000-4000-8000-000000000001", Number: 1,
		Filename: "plan.txt", DeclaredType: "text/plain", VerifiedType: "text/plain", ByteSize: 8, ContentSHA256: sha256.Sum256([]byte("source")),
		ObjectKey: "accounts/e1000000-0000-4000-8000-000000000001/documents/e2000000-0000-4000-8000-000000000002/revisions/e3000000-0000-4000-8000-000000000003/source", ObjectVersion: "v1", CreatedBy: Actor{Kind: ActorWorkload, ID: "worker:documents"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.RecordScan(ScanClean, "clamav", "daily.cvd", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, invalidErr := revision.RecordExtraction(sha256.Sum256([]byte("text")), 4, "text-v1", "accounts/other/extracted/text", "v1", now.Add(2*time.Second)); !errors.Is(invalidErr, ErrInvalid) {
		t.Fatalf("wrong extracted object identity err=%v", invalidErr)
	}
	revision, err = revision.RecordExtraction(sha256.Sum256([]byte("text")), 4, "text-v1", "accounts/e1000000-0000-4000-8000-000000000001/documents/e2000000-0000-4000-8000-000000000002/revisions/e3000000-0000-4000-8000-000000000003/extracted/text", "extracted-v1", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.FailProcessing("index_unavailable", now.Add(3*time.Second))
	if err != nil || revision.State != RevisionFailed || revision.Extraction != ExtractionReady || revision.Index != IndexFailed {
		t.Fatalf("failed revision=%+v err=%v", revision, err)
	}
}

func TestDocumentRetentionAndLegalHoldBlockDeletion(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	retainUntil := now.Add(24 * time.Hour)
	document, err := NewDocument("c2000000-0000-4000-8000-000000000002", "c1000000-0000-4000-8000-000000000001", "Policy", SensitivityRestricted, &retainUntil, Actor{Kind: ActorUser, ID: "c4000000-0000-4000-8000-000000000004"}, now)
	if err != nil {
		t.Fatal(err)
	}
	document, err = document.FailInitial(1, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := document.RequestDeletion(2, now.Add(time.Hour)); !errors.Is(err, ErrDocumentRetention) {
		t.Fatalf("retention err=%v", err)
	}
	document.LegalHold = true
	if _, err := document.RequestDeletion(2, retainUntil.Add(time.Second)); !errors.Is(err, ErrDocumentHold) {
		t.Fatalf("hold err=%v", err)
	}
	document.LegalHold = false
	document, err = document.RequestDeletion(2, retainUntil.Add(time.Second))
	if err != nil || document.State != DocumentDeletionPending {
		t.Fatalf("request deletion=%+v err=%v", document, err)
	}
	document, err = document.CompleteDeletion(3, retainUntil.Add(2*time.Second))
	if err != nil || document.State != DocumentDeleted || document.DeletedAt == nil {
		t.Fatalf("complete deletion=%+v err=%v", document, err)
	}
}
