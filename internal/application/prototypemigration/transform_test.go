package prototypemigration

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testAccount = "10000000-0000-4000-8000-000000000001"
	testTenant  = "20000000-0000-4000-8000-000000000002"
	testDoc     = "30000000-0000-4000-8000-000000000003"
	testDocRev  = "40000000-0000-4000-8000-000000000004"
)

func TestTransformProducesDeterministicReviewFirstBundle(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	confirmed := now.Add(-time.Hour)
	content := []byte("Formation record\nVerified in the prototype library.\n")
	digest := sha256.Sum256(content)
	snapshot := Snapshot{
		TenantID:       testTenant,
		Checkpoint:     "00000016/B374D848",
		SchemaVersions: []string{"032_agent_managed_documents.sql", "029_business_knowledge_fact_history.sql"},
		Inventory:      Inventory{FactVersions: 4, CurrentFacts: 1, DocumentRevisions: 1, Assessments: 1, AssessmentFacts: 1, Requirements: 1, EvidenceLinks: 1, DocumentChunks: 2},
		Documents:      []LegacyDocumentRevision{{DocumentID: testDoc, RevisionID: testDocRev, Revision: 1, Name: "Formation.pdf", MediaType: "application/pdf", Content: content, StoredSHA256: digest[:], ChangeSummary: "Initial", Status: "ready", CreatedAt: now.Add(-3 * time.Hour)}},
		FactVersions: []LegacyFactVersion{
			{ID: "51000000-0000-4000-8000-000000000005", Key: "organization.legal_name", Label: "Legal name", Value: "Infinite Ocean LLC", Scope: "company", SourceType: "owner", Confidence: "1.000", Sensitivity: "internal", Status: "active", RecordedAt: now.Add(-2 * time.Hour)},
			{ID: "52000000-0000-4000-8000-000000000005", Key: "legal.formation", Label: "Formation", Value: "verified", Scope: "company", SourceType: "document", SourceRef: testDoc, Confidence: "0.900", Sensitivity: "confidential", Status: "active", RecordedAt: now.Add(-90 * time.Minute)},
			{ID: "53000000-0000-4000-8000-000000000005", Key: "operations.summary", Label: "Summary", Value: "draft", Scope: "company", SourceType: "agent", Confidence: "0.500", Sensitivity: "internal", Status: "active", RecordedAt: now.Add(-80 * time.Minute)},
			{ID: "54000000-0000-4000-8000-000000000005", Key: "mail.customer_count", Label: "Customers", Value: "14", Scope: "company", SourceType: "email", SourceRef: "INBOX", Confidence: "0.800", Sensitivity: "internal", Status: "active", RecordedAt: now.Add(-70 * time.Minute)},
		},
		CurrentFacts: []LegacyCurrentFact{{Key: "organization.legal_name", Label: "Legal name", Value: "Infinite Ocean LLC", Scope: "company", SourceType: "owner", Confidence: "1.000", Sensitivity: "internal", Status: "active", ConfirmedAt: &confirmed, UpdatedAt: now.Add(-2 * time.Hour)}},
		Assessments: []LegacyAssessment{{
			ID: "60000000-0000-4000-8000-000000000006", Status: "ready", Phase: "baseline_ready", BaselineVersion: 2, CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: now,
			Facts:        []LegacyAssessmentFact{{ID: "61000000-0000-4000-8000-000000000006", Key: "team_size", Value: "1", SourceType: "owner", Confidence: "1.000", ConfirmedAt: &confirmed, UpdatedAt: now}},
			Requirements: []LegacyRequirement{{ID: "62000000-0000-4000-8000-000000000006", Key: "identity_registration", Status: "confirmed", Disposition: "have_it", Responsibility: "owner", Evidence: []LegacyEvidenceLink{{ID: "63000000-0000-4000-8000-000000000006", DocumentID: testDoc}}}},
		}},
	}

	bundle, err := NewTransformer().Transform(ids.AccountID(testAccount), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Manifest.Totals.FactRecords != 4 || bundle.Manifest.Totals.ImportableClaims != 2 || bundle.Manifest.Totals.ImportableDocuments != 1 || bundle.Manifest.Totals.BaselineAnswers != 1 || bundle.Manifest.Totals.RequirementReviews != 1 {
		t.Fatalf("totals=%+v", bundle.Manifest.Totals)
	}
	if len(bundle.Objects) != 1 || string(bundle.Objects[bundle.Manifest.Documents[0].ObjectPath]) != string(content) || bundle.Manifest.Documents[0].OriginalMediaType != "application/pdf" || bundle.Manifest.Documents[0].ImportMediaType != "text/plain" {
		t.Fatalf("documents=%+v objects=%d", bundle.Manifest.Documents, len(bundle.Objects))
	}
	facts := factsBySourceID(bundle.Manifest.Facts)
	if facts["51000000-0000-4000-8000-000000000005"].Action != "report_only" || !facts["51000000-0000-4000-8000-000000000005"].SourceCurrent || !hasIssue(bundle.Manifest.Unresolved, "fact", "51000000-0000-4000-8000-000000000005", "owner_actor_missing") {
		t.Fatalf("owner fact=%+v", facts["51000000-0000-4000-8000-000000000005"])
	}
	if facts["52000000-0000-4000-8000-000000000005"].Action != "import_proposed_claim" || facts["52000000-0000-4000-8000-000000000005"].SourceReference != bundle.Manifest.Documents[0].TargetRevisionID {
		t.Fatalf("document fact=%+v", facts["52000000-0000-4000-8000-000000000005"])
	}
	if facts["53000000-0000-4000-8000-000000000005"].Action != "import_non_authoritative_claim" || facts["54000000-0000-4000-8000-000000000005"].Action != "report_only" {
		t.Fatalf("agent=%+v email=%+v", facts["53000000-0000-4000-8000-000000000005"], facts["54000000-0000-4000-8000-000000000005"])
	}
	if got := bundle.Manifest.Baselines[0].Answers[0].QuestionKey; got != "organization.team_size" {
		t.Fatalf("question key=%q", got)
	}
	if !hasIssue(bundle.Manifest.Unresolved, "fact", "54000000-0000-4000-8000-000000000005", "integration_grant_unbound") || !hasIssue(bundle.Manifest.Unresolved, "document", testDocRev, "original_binary_unavailable") || !hasIssue(bundle.Manifest.Unresolved, "baseline_evidence", "63000000-0000-4000-8000-000000000006", "evidence_not_human_verified") {
		t.Fatalf("unresolved=%+v", bundle.Manifest.Unresolved)
	}

	reversed := snapshot
	reversed.FactVersions = slices.Clone(snapshot.FactVersions)
	slices.Reverse(reversed.FactVersions)
	reversed.SchemaVersions = slices.Clone(snapshot.SchemaVersions)
	slices.Reverse(reversed.SchemaVersions)
	again, err := NewTransformer().Transform(ids.AccountID(testAccount), reversed)
	if err != nil || again.Manifest.ContentSHA256 != bundle.Manifest.ContentSHA256 {
		t.Fatalf("determinism digest=%q want=%q err=%v", again.Manifest.ContentSHA256, bundle.Manifest.ContentSHA256, err)
	}
}

func TestTransformRejectsCorruptDocumentAndManifestObject(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	content := []byte("verified text")
	digest := sha256.Sum256(content)
	snapshot := Snapshot{TenantID: testTenant, Checkpoint: "checkpoint", Inventory: Inventory{DocumentRevisions: 1}, Documents: []LegacyDocumentRevision{{DocumentID: testDoc, RevisionID: testDocRev, Revision: 1, Name: "record.txt", MediaType: "text/plain", Content: content, StoredSHA256: digest[:], Status: "ready", CreatedAt: now}}}
	corrupt := snapshot
	corrupt.Documents = slices.Clone(snapshot.Documents)
	corrupt.Documents[0].StoredSHA256 = make([]byte, sha256.Size)
	if _, err := NewTransformer().Transform(testAccount, corrupt); !errors.Is(err, ErrCorruptSource) {
		t.Fatalf("checksum error=%v", err)
	}
	bundle, err := NewTransformer().Transform(testAccount, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for key := range bundle.Objects {
		bundle.Objects[key][0] ^= 0xff
	}
	if err := Verify(bundle); !errors.Is(err, ErrManifest) {
		t.Fatalf("verification error=%v", err)
	}
}

func TestTransformDoesNotResurrectDeletedDocumentOrUnconfirmedOwnerHistory(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	content := []byte("deleted")
	digest := sha256.Sum256(content)
	snapshot := Snapshot{
		TenantID: testTenant, Checkpoint: "checkpoint",
		Inventory:    Inventory{FactVersions: 1, DocumentRevisions: 1},
		FactVersions: []LegacyFactVersion{{ID: "70000000-0000-4000-8000-000000000007", Key: "organization.industry", Value: "software", Scope: "company", SourceType: "owner", Confidence: "1", Sensitivity: "internal", Status: "superseded", RecordedAt: now}},
		Documents:    []LegacyDocumentRevision{{DocumentID: testDoc, RevisionID: testDocRev, Revision: 1, Name: "deleted.txt", MediaType: "text/plain", Content: content, StoredSHA256: digest[:], Status: "deleted", DeletedAt: &now, CreatedAt: now.Add(-time.Hour)}},
	}
	bundle, err := NewTransformer().Transform(testAccount, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Objects) != 0 || bundle.Manifest.Documents[0].Action != "report_only" || bundle.Manifest.Facts[0].Action != "report_only" {
		t.Fatalf("bundle=%+v objects=%d", bundle.Manifest, len(bundle.Objects))
	}
}

func TestConfidencePermilleIsExact(t *testing.T) {
	for input, expected := range map[string]uint16{"0": 0, "0.1": 100, "0.125": 125, "1": 1000, "1.000": 1000} {
		value, err := confidencePermille(input)
		if err != nil || value != expected {
			t.Fatalf("input=%q value=%d err=%v", input, value, err)
		}
	}
	for _, input := range []string{"", "-1", "1.001", "0.0001", "thing"} {
		if _, err := confidencePermille(input); err == nil {
			t.Fatalf("input %q accepted", input)
		}
	}
}

func TestManifestDigestCoversCustomerValues(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	confirmed := now
	snapshot := Snapshot{TenantID: testTenant, Checkpoint: "checkpoint", Inventory: Inventory{CurrentFacts: 1}, CurrentFacts: []LegacyCurrentFact{{Key: "organization.industry", Value: "software", Scope: "company", SourceType: "owner", Confidence: "1", Sensitivity: "internal", Status: "active", ConfirmedAt: &confirmed, UpdatedAt: now}}}
	bundle, err := NewTransformer().Transform(testAccount, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := json.Unmarshal(bundle.Manifest.Facts[0].CanonicalValue, &value); err != nil || value != "software" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	bundle.Manifest.Facts[0].CanonicalValue = json.RawMessage(`"retail"`)
	if err := Verify(bundle); !errors.Is(err, ErrManifest) {
		t.Fatalf("verification error=%v", err)
	}
}

func factsBySourceID(values []FactPlan) map[string]FactPlan {
	result := make(map[string]FactPlan, len(values))
	for _, value := range values {
		result[value.SourceID] = value
	}
	return result
}

func hasIssue(values []UnresolvedRecord, kind, sourceID, code string) bool {
	for _, value := range values {
		if value.Kind == kind && value.SourceID == sourceID && value.Code == code {
			return true
		}
	}
	return false
}
