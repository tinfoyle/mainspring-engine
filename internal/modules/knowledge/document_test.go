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

func TestDocumentPublicationCanRecoverAcrossFailedRevisionNumbers(t *testing.T) {
	now := time.Date(2026, 8, 23, 18, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("f1000000-0000-4000-8000-000000000001")
	documentID := ids.KnowledgeDocumentID("f2000000-0000-4000-8000-000000000002")
	actor := Actor{Kind: ActorWorkload, ID: "integration-source-sync"}
	document, err := RestoreDocument(Document{ID: documentID, AccountID: accountID, Title: "Synced plan", Sensitivity: SensitivityInternal,
		CurrentRevisionID: "f3000000-0000-4000-8000-000000000003", CurrentRevision: 1, State: DocumentReady, Version: 2,
		CreatedBy: actor, CreatedAt: now, UpdatedAt: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := NewDocumentRevision(DocumentRevisionDraft{ID: "f5000000-0000-4000-8000-000000000005", DocumentID: documentID,
		AccountID: accountID, Number: 3, Filename: "plan.txt", DeclaredType: "text/plain", VerifiedType: "text/plain", ByteSize: 7,
		ContentSHA256: sha256.Sum256([]byte("updated")), ObjectKey: "accounts/" + string(accountID) + "/documents/" + string(documentID) + "/revisions/f5000000-0000-4000-8000-000000000005/source",
		ObjectVersion: "version-3", ChangeSummary: "Recovered after rejected provider version", CreatedBy: actor}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.RecordScan(ScanClean, "clamav", "daily.cvd", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.RecordExtraction(sha256.Sum256([]byte("updated")), 7, "text-v1", "accounts/"+string(accountID)+"/documents/"+string(documentID)+"/revisions/"+string(revision.ID)+"/extracted/text", "extracted-3", now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.RecordIndex("knowledge-v1", 1, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	document, err = document.Publish(revision, 2, now.Add(6*time.Second))
	if err != nil || document.CurrentRevision != 3 || document.CurrentRevisionID != revision.ID || document.State != DocumentReady {
		t.Fatalf("recovered document=%+v err=%v", document, err)
	}
	document.State, document.CurrentRevisionID, document.CurrentRevision, document.Version = DocumentFailed, "", 0, 2
	if restored, restoreErr := RestoreDocument(document); restoreErr != nil {
		t.Fatal(restoreErr)
	} else if recovered, publishErr := restored.Publish(revision, restored.Version, now.Add(7*time.Second)); publishErr != nil || recovered.State != DocumentReady || recovered.CurrentRevision != 3 {
		t.Fatalf("initial-failure recovery=%+v err=%v", recovered, publishErr)
	}
}

func TestDocumentPublicationCanResurrectDeletedSourceWithNewRevision(t *testing.T) {
	now := time.Date(2026, 8, 23, 19, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("fa000000-0000-4000-8000-00000000000a")
	documentID := ids.KnowledgeDocumentID("fb000000-0000-4000-8000-00000000000b")
	actor := Actor{Kind: ActorWorkload, ID: "integration-source-sync"}
	requested, deleted := now.Add(time.Second), now.Add(2*time.Second)
	document, err := RestoreDocument(Document{ID: documentID, AccountID: accountID, Title: "Restored source",
		Sensitivity: SensitivityInternal, CurrentRevisionID: "fc000000-0000-4000-8000-00000000000c", CurrentRevision: 2,
		State: DocumentDeleted, Version: 5, CreatedBy: actor, CreatedAt: now, UpdatedAt: deleted,
		DeletionRequested: &requested, DeletedAt: &deleted})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := NewDocumentRevision(DocumentRevisionDraft{ID: "fd000000-0000-4000-8000-00000000000d", DocumentID: documentID,
		AccountID: accountID, Number: 3, Filename: "restored.txt", DeclaredType: "text/plain", VerifiedType: "text/plain",
		ByteSize: 8, ContentSHA256: sha256.Sum256([]byte("restored")), ObjectKey: "accounts/" + string(accountID) +
			"/documents/" + string(documentID) + "/revisions/fd000000-0000-4000-8000-00000000000d/source",
		ObjectVersion: "version-restored", ChangeSummary: "Provider source restored", CreatedBy: actor}, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.RecordScan(ScanClean, "clamav", "daily.cvd", now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.RecordExtraction(sha256.Sum256([]byte("restored")), 8, "text-v1", "accounts/"+string(accountID)+
		"/documents/"+string(documentID)+"/revisions/"+string(revision.ID)+"/extracted/text", "extracted-restored", now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision, err = revision.RecordIndex("knowledge-v1", 1, now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	resurrected, err := document.Publish(revision, document.Version, now.Add(7*time.Second))
	if err != nil || resurrected.State != DocumentReady || resurrected.CurrentRevisionID != revision.ID ||
		resurrected.CurrentRevision != 3 || resurrected.Version != 6 || resurrected.DeletionRequested != nil || resurrected.DeletedAt != nil {
		t.Fatalf("resurrected=%+v err=%v", resurrected, err)
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
