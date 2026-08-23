package integrationsync

import (
	"crypto/sha256"
	"testing"
	"time"
)

func TestClaimAndCompletionAreBoundedAndExact(t *testing.T) {
	now := time.Date(2026, 8, 23, 18, 0, 0, 0, time.UTC)
	digest := sha256.Sum256([]byte("value"))
	claim := Claim{
		AccountID: "a1000000-0000-4000-8000-000000000001", SyncID: "a2000000-0000-4000-8000-000000000002",
		GrantID: "a3000000-0000-4000-8000-000000000003", ConnectionID: "a4000000-0000-4000-8000-000000000004",
		ConnectionRevisionID: "a5000000-0000-4000-8000-000000000005", ConnectionRevision: 2,
		CredentialID: "a6000000-0000-4000-8000-000000000006", CredentialGeneration: 3,
		CredentialProvider: "google_drive", CredentialReferenceSHA256: digest, FolderIDs: []string{"folder-a", "folder-b"},
		LeaseExpiresAt: now.Add(time.Minute),
	}
	if !claim.Valid(now) {
		t.Fatal("valid first-page claim was rejected")
	}
	receipt := CaptureReceipt{ID: "a7000000-0000-4000-8000-000000000007", FolderID: "folder-a",
		ProviderObjectSHA256: digest, ProviderRevisionSHA256: sha256.Sum256([]byte("revision")), Operation: CaptureAdmitted,
		DocumentID: "a8000000-0000-4000-8000-000000000008", DocumentRevisionID: "a9000000-0000-4000-8000-000000000009",
		ContentSHA256: sha256.Sum256([]byte("content"))}
	completion := Completion{Claim: claim, CursorCiphertext: []byte("sealed-cursor"), CursorSHA256: sha256.Sum256([]byte("cursor")),
		Captures: []CaptureReceipt{receipt}, CompletedAt: now.Add(30 * time.Second)}
	if !completion.Valid() {
		t.Fatal("valid completion was rejected")
	}
	outside := completion
	outside.Captures = append([]CaptureReceipt(nil), receipt)
	outside.Captures[0].FolderID = "folder-c"
	if outside.Valid() {
		t.Fatal("capture outside the frozen grant was accepted")
	}
	duplicate := completion
	duplicate.Captures = []CaptureReceipt{receipt, receipt}
	if duplicate.Valid() {
		t.Fatal("duplicate capture receipt was accepted")
	}
	stale := completion
	stale.CompletedAt = claim.LeaseExpiresAt.Add(time.Nanosecond)
	if stale.Valid() {
		t.Fatal("completion beyond its lease was accepted")
	}
	unsorted := claim
	unsorted.FolderIDs = []string{"folder-b", "folder-a"}
	if unsorted.Valid(now) {
		t.Fatal("noncanonical grant scope was accepted")
	}
}
