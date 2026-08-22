package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// PrototypeMigrationDestination admits only the review-first records named in
// a sealed bundle. Documents remain unpublished and claims remain proposed;
// the migration workload never impersonates a human reviewer.
type PrototypeMigrationDestination struct {
	cell      *database.CellPool
	documents *knowledgeapp.DocumentAdmissionService
	knowledge *knowledgeapp.Service
	objects   knowledgeapp.DocumentObjectStore
}

func NewPrototypeMigrationDestination(cell *database.CellPool, documents *knowledgeapp.DocumentAdmissionService, knowledge *knowledgeapp.Service, objects knowledgeapp.DocumentObjectStore) (*PrototypeMigrationDestination, error) {
	if cell == nil || documents == nil || knowledge == nil || objects == nil {
		return nil, prototypemigration.ErrImportDependency
	}
	return &PrototypeMigrationDestination{cell: cell, documents: documents, knowledge: knowledge, objects: objects}, nil
}

func (destination *PrototypeMigrationDestination) ImportDocument(ctx context.Context, accountID ids.AccountID, plan prototypemigration.DocumentPlan, body []byte) error {
	digest, err := migrationDigest(plan.ContentSHA256)
	if err != nil || accountID == "" || ids.Validate(plan.TargetDocumentID) != nil || ids.Validate(plan.TargetRevisionID) != nil || plan.Action != "import_reconstructed_text" || plan.ImportMediaType != "text/plain" || int64(len(body)) != plan.ByteSize || sha256.Sum256(body) != digest {
		return prototypemigration.ErrImportTarget
	}
	correlationID, err := ids.Derive(plan.TargetRevisionID, "prototype-migration-document")
	if err != nil {
		return prototypemigration.ErrImportTarget
	}
	_, _, err = destination.documents.Upload(ctx, knowledgeapp.UploadDocumentCommand{
		Actor: access.Actor{WorkloadID: prototypemigration.MigrationWorkloadID}, AccountID: accountID,
		DocumentID: ids.KnowledgeDocumentID(plan.TargetDocumentID), RevisionID: ids.KnowledgeDocumentRevisionID(plan.TargetRevisionID),
		Title: plan.Title, Sensitivity: knowledgedomain.SensitivityInternal, Filename: plan.Filename, DeclaredType: plan.ImportMediaType,
		Body: bytes.NewReader(body), ChangeSummary: migrationChangeSummary(plan), CorrelationID: correlationID,
	})
	return classifyPrototypeDestinationError(err)
}

func (destination *PrototypeMigrationDestination) ImportFact(ctx context.Context, accountID ids.AccountID, plan prototypemigration.FactPlan) error {
	evidenceDigest, err := migrationDigest(plan.EvidenceSHA256)
	if err != nil || ids.Validate(plan.TargetEvidenceID) != nil || ids.Validate(plan.TargetClaimID) != nil || plan.Scope != "account" || (plan.Action != "import_proposed_claim" && plan.Action != "import_non_authoritative_claim") {
		return prototypemigration.ErrImportTarget
	}
	kind := knowledgedomain.SourceKind(plan.EvidenceKind)
	if !kind.Valid() || (plan.Action == "import_non_authoritative_claim") != (kind == knowledgedomain.SourceAgentDerivation) {
		return prototypemigration.ErrImportTarget
	}
	evidenceCorrelation, err := ids.Derive(plan.TargetEvidenceID, "prototype-migration-evidence")
	if err != nil {
		return prototypemigration.ErrImportTarget
	}
	actor := access.Actor{WorkloadID: prototypemigration.MigrationWorkloadID}
	if _, err := destination.knowledge.RegisterEvidence(ctx, knowledgeapp.RegisterEvidenceCommand{
		Actor: actor, AccountID: accountID, EvidenceID: ids.KnowledgeEvidenceID(plan.TargetEvidenceID), Kind: kind,
		SourceReference: plan.SourceReference, SourceRevision: plan.SourceRevision, ContentSHA256: evidenceDigest,
		CapturedAt: plan.RecordedAt, CorrelationID: evidenceCorrelation,
	}); err != nil {
		return classifyPrototypeDestinationError(err)
	}
	claimCorrelation, err := ids.Derive(plan.TargetClaimID, "prototype-migration-claim")
	if err != nil {
		return prototypemigration.ErrImportTarget
	}
	_, err = destination.knowledge.ProposeClaim(ctx, knowledgeapp.ProposeClaimCommand{
		Actor: actor, AccountID: accountID, ClaimID: ids.KnowledgeClaimID(plan.TargetClaimID),
		Scope: knowledgedomain.Scope{Kind: knowledgedomain.ScopeAccount}, Key: plan.Key, CanonicalValue: plan.CanonicalValue,
		Confidence: plan.Confidence, Sensitivity: knowledgedomain.Sensitivity(plan.Sensitivity),
		Citations:     []knowledgedomain.Citation{{EvidenceID: ids.KnowledgeEvidenceID(plan.TargetEvidenceID), EvidenceKind: kind, Relation: knowledgedomain.EvidenceSupports, Locator: "prototype:" + plan.SourceID}},
		CorrelationID: claimCorrelation,
	})
	return classifyPrototypeDestinationError(err)
}

type prototypeDocumentReconciliation struct {
	SourceRevisionID, DocumentID, RevisionID    string
	SourceObjectVersion, ExtractedObjectVersion string
	ChunkSHA256                                 []string
}

type prototypeFactReconciliation struct {
	SourceID, EvidenceID, ClaimID string
}

type prototypeReconciliationSeal struct {
	Version, ManifestSHA256, RunID, RunState string
	Documents                                []prototypeDocumentReconciliation
	Facts                                    []prototypeFactReconciliation
	Unresolved                               uint64
}

func (destination *PrototypeMigrationDestination) Reconcile(ctx context.Context, bundle prototypemigration.Bundle) (prototypemigration.Reconciliation, error) {
	if err := prototypemigration.Verify(bundle); err != nil {
		return prototypemigration.Reconciliation{}, err
	}
	runID, err := ids.Derive(string(bundle.Manifest.AccountID), "prototype-migration:"+bundle.Manifest.ContentSHA256)
	if err != nil {
		return prototypemigration.Reconciliation{}, prototypemigration.ErrImportTarget
	}
	seal := prototypeReconciliationSeal{Version: prototypemigration.ManifestVersion, ManifestSHA256: bundle.Manifest.ContentSHA256, RunID: runID, Documents: []prototypeDocumentReconciliation{}, Facts: []prototypeFactReconciliation{}}
	result := prototypemigration.Reconciliation{}
	err = destination.cell.WithAccountTx(ctx, bundle.Manifest.AccountID, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(ctx context.Context, tx pgx.Tx) error {
		if err := reconcilePrototypeRun(ctx, tx, bundle, runID, &seal, &result); err != nil {
			return err
		}
		documents := append([]prototypemigration.DocumentPlan(nil), bundle.Manifest.Documents...)
		sort.Slice(documents, func(left, right int) bool {
			return documents[left].SourceRevisionID < documents[right].SourceRevisionID
		})
		for _, plan := range documents {
			if plan.Action != "import_reconstructed_text" {
				continue
			}
			body := bundle.Objects[plan.ObjectPath]
			reconciled, err := destination.reconcileDocument(ctx, tx, bundle.Manifest.AccountID, plan, body)
			if err != nil {
				return err
			}
			seal.Documents = append(seal.Documents, reconciled)
			result.Documents++
			result.DocumentObjects += 2
			result.Chunks += uint64(len(reconciled.ChunkSHA256))
		}
		facts := append([]prototypemigration.FactPlan(nil), bundle.Manifest.Facts...)
		sort.Slice(facts, func(left, right int) bool { return facts[left].SourceID < facts[right].SourceID })
		for _, plan := range facts {
			if !strings.HasPrefix(plan.Action, "import_") {
				continue
			}
			if err := reconcilePrototypeFact(ctx, tx, bundle.Manifest.AccountID, plan); err != nil {
				return err
			}
			seal.Facts = append(seal.Facts, prototypeFactReconciliation{SourceID: plan.SourceID, EvidenceID: plan.TargetEvidenceID, ClaimID: plan.TargetClaimID})
			result.Evidence++
			result.Claims++
			result.Citations++
		}
		return reconcilePrototypeReceipts(ctx, tx, bundle, runID)
	})
	if err != nil {
		return result, classifyPrototypeDestinationError(err)
	}
	encoded, err := json.Marshal(seal)
	if err != nil {
		return result, prototypemigration.ErrReconciliation
	}
	result.ContentSHA256 = sha256.Sum256(encoded)
	return result, nil
}

func reconcilePrototypeRun(ctx context.Context, tx pgx.Tx, bundle prototypemigration.Bundle, runID string, seal *prototypeReconciliationSeal, result *prototypemigration.Reconciliation) error {
	var manifestDigest []byte
	var unresolved uint64
	if err := tx.QueryRow(ctx, `SELECT state,manifest_sha256,unresolved_records FROM spyglass.prototype_migration_runs WHERE account_id=$1 AND id=$2`, bundle.Manifest.AccountID, runID).Scan(&seal.RunState, &manifestDigest, &unresolved); err != nil {
		return err
	}
	want, err := migrationDigest(bundle.Manifest.ContentSHA256)
	if err != nil || !bytes.Equal(manifestDigest, want[:]) || unresolved != bundle.Manifest.Totals.UnresolvedRecords || (seal.RunState != string(prototypemigration.RunImported) && seal.RunState != string(prototypemigration.RunReconciled)) {
		return prototypemigration.ErrReconciliation
	}
	seal.Unresolved, result.Unresolved = unresolved, unresolved
	return nil
}

func (destination *PrototypeMigrationDestination) reconcileDocument(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, plan prototypemigration.DocumentPlan, body []byte) (prototypeDocumentReconciliation, error) {
	var documentState, revisionState, createdByKind, createdByID, filename, title, sensitivity string
	var currentRevisionID *string
	var contentDigest, textDigest []byte
	var byteSize, textBytes int64
	var objectKey, objectVersion, extractedKey, extractedVersion, generation string
	var chunkCount uint32
	err := tx.QueryRow(ctx, `SELECT document.state,document.current_revision_id::text,document.title,document.sensitivity,document.created_by_kind,document.created_by_id,
		revision.state,revision.filename,revision.byte_size,revision.content_sha256,revision.object_key,revision.object_version,
		revision.text_bytes,revision.text_sha256,revision.extracted_object_key,revision.extracted_object_version,revision.index_generation,revision.chunk_count
		FROM spyglass.knowledge_documents document JOIN spyglass.knowledge_document_revisions revision
		ON revision.account_id=document.account_id AND revision.document_id=document.id
		WHERE document.account_id=$1 AND document.id=$2 AND revision.id=$3`, accountID, plan.TargetDocumentID, plan.TargetRevisionID).Scan(
		&documentState, &currentRevisionID, &title, &sensitivity, &createdByKind, &createdByID,
		&revisionState, &filename, &byteSize, &contentDigest, &objectKey, &objectVersion,
		&textBytes, &textDigest, &extractedKey, &extractedVersion, &generation, &chunkCount)
	want, digestErr := migrationDigest(plan.ContentSHA256)
	if err != nil || digestErr != nil || documentState != string(knowledgedomain.DocumentProcessing) || currentRevisionID != nil || revisionState != string(knowledgedomain.RevisionReady) || title != plan.Title || filename != plan.Filename || sensitivity != string(knowledgedomain.SensitivityInternal) || createdByKind != string(knowledgedomain.ActorWorkload) || createdByID != prototypemigration.MigrationWorkloadID || byteSize != plan.ByteSize || textBytes != plan.ByteSize || !bytes.Equal(contentDigest, want[:]) || !bytes.Equal(textDigest, want[:]) || generation != knowledgeapp.DocumentChunkGeneration || chunkCount != plan.ExpectedChunks {
		return prototypeDocumentReconciliation{}, errors.Join(err, prototypemigration.ErrReconciliation)
	}
	identities := []knowledgeapp.DocumentObjectIdentity{{Key: objectKey, Version: objectVersion, Size: byteSize, ContentSHA256: want}, {Key: extractedKey, Version: extractedVersion, Size: textBytes, ContentSHA256: want}}
	for _, identity := range identities {
		stored, err := destination.readObject(ctx, identity)
		if err != nil || !bytes.Equal(stored, body) {
			return prototypeDocumentReconciliation{}, errors.Join(err, prototypemigration.ErrReconciliation)
		}
	}
	drafts, err := knowledgeapp.ChunkExtractedText(body)
	if err != nil || len(drafts) != int(plan.ExpectedChunks) {
		return prototypeDocumentReconciliation{}, prototypemigration.ErrReconciliation
	}
	rows, err := tx.Query(ctx, `SELECT chunk_index,start_byte,end_byte,content,content_sha256,token_count,index_generation
		FROM spyglass.knowledge_document_chunks WHERE account_id=$1 AND revision_id=$2 ORDER BY chunk_index`, accountID, plan.TargetRevisionID)
	if err != nil {
		return prototypeDocumentReconciliation{}, err
	}
	defer rows.Close()
	chunkDigests := make([]string, 0, len(drafts))
	index := 0
	for rows.Next() {
		var chunkIndex uint32
		var start, end int64
		var content string
		var digest []byte
		var tokens uint32
		var storedGeneration string
		if err := rows.Scan(&chunkIndex, &start, &end, &content, &digest, &tokens, &storedGeneration); err != nil {
			return prototypeDocumentReconciliation{}, err
		}
		if index >= len(drafts) {
			return prototypeDocumentReconciliation{}, prototypemigration.ErrReconciliation
		}
		draft, expectedDigest := drafts[index], sha256.Sum256([]byte(drafts[index].Content))
		if chunkIndex != uint32(index) || start != draft.StartByte || end != draft.EndByte || content != draft.Content || tokens != draft.TokenCount || storedGeneration != knowledgeapp.DocumentChunkGeneration || !bytes.Equal(digest, expectedDigest[:]) {
			return prototypeDocumentReconciliation{}, prototypemigration.ErrReconciliation
		}
		chunkDigests = append(chunkDigests, hex.EncodeToString(expectedDigest[:]))
		index++
	}
	if err := rows.Err(); err != nil || index != len(drafts) {
		return prototypeDocumentReconciliation{}, errors.Join(err, prototypemigration.ErrReconciliation)
	}
	return prototypeDocumentReconciliation{SourceRevisionID: plan.SourceRevisionID, DocumentID: plan.TargetDocumentID, RevisionID: plan.TargetRevisionID, SourceObjectVersion: objectVersion, ExtractedObjectVersion: extractedVersion, ChunkSHA256: chunkDigests}, nil
}

func (destination *PrototypeMigrationDestination) readObject(ctx context.Context, identity knowledgeapp.DocumentObjectIdentity) ([]byte, error) {
	body, err := destination.objects.Open(ctx, identity)
	if err != nil {
		return nil, err
	}
	value, readErr := io.ReadAll(io.LimitReader(body, identity.Size+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || int64(len(value)) != identity.Size || sha256.Sum256(value) != identity.ContentSHA256 {
		return nil, errors.Join(readErr, closeErr, prototypemigration.ErrReconciliation)
	}
	return value, nil
}

func reconcilePrototypeFact(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, plan prototypemigration.FactPlan) error {
	var evidenceKind, sourceReference, sourceRevision, evidenceActorKind, evidenceActorID string
	var evidenceDigest []byte
	var capturedAtEqual bool
	if err := tx.QueryRow(ctx, `SELECT source_kind,source_reference,source_revision,content_sha256,created_by_kind,created_by_id,captured_at=$3
		FROM spyglass.knowledge_evidence WHERE account_id=$1 AND id=$2`, accountID, plan.TargetEvidenceID, plan.RecordedAt).Scan(&evidenceKind, &sourceReference, &sourceRevision, &evidenceDigest, &evidenceActorKind, &evidenceActorID, &capturedAtEqual); err != nil {
		return err
	}
	wantEvidence, err := migrationDigest(plan.EvidenceSHA256)
	if err != nil || evidenceKind != plan.EvidenceKind || sourceReference != plan.SourceReference || sourceRevision != plan.SourceRevision || evidenceActorKind != string(knowledgedomain.ActorWorkload) || evidenceActorID != prototypemigration.MigrationWorkloadID || !capturedAtEqual || !bytes.Equal(evidenceDigest, wantEvidence[:]) {
		return prototypemigration.ErrReconciliation
	}
	var scope, key, sensitivity, state, actorKind, actorID string
	var canonical, valueDigest []byte
	var confidence uint16
	var version uint64
	if err := tx.QueryRow(ctx, `SELECT scope_kind,fact_key,canonical_value,value_sha256,confidence,sensitivity,state,version,proposed_by_kind,proposed_by_id
		FROM spyglass.knowledge_claims WHERE account_id=$1 AND id=$2`, accountID, plan.TargetClaimID).Scan(&scope, &key, &canonical, &valueDigest, &confidence, &sensitivity, &state, &version, &actorKind, &actorID); err != nil {
		return err
	}
	wantValue, err := migrationDigest(plan.ContentSHA256)
	if err != nil || scope != string(knowledgedomain.ScopeAccount) || key != plan.Key || !bytes.Equal(canonical, plan.CanonicalValue) || !bytes.Equal(valueDigest, wantValue[:]) || confidence != plan.Confidence || sensitivity != plan.Sensitivity || state != string(knowledgedomain.ClaimProposed) || version != 1 || actorKind != string(knowledgedomain.ActorWorkload) || actorID != prototypemigration.MigrationWorkloadID {
		return prototypemigration.ErrReconciliation
	}
	var citationCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM spyglass.knowledge_claim_citations WHERE account_id=$1 AND claim_id=$2 AND evidence_id=$3 AND evidence_kind=$4 AND relation='supports' AND locator=$5`, accountID, plan.TargetClaimID, plan.TargetEvidenceID, plan.EvidenceKind, "prototype:"+plan.SourceID).Scan(&citationCount); err != nil || citationCount != 1 {
		return errors.Join(err, prototypemigration.ErrReconciliation)
	}
	return nil
}

func reconcilePrototypeReceipts(ctx context.Context, tx pgx.Tx, bundle prototypemigration.Bundle, runID string) error {
	expected := make(map[string]struct{})
	for _, plan := range bundle.Manifest.Documents {
		if plan.Action == "import_reconstructed_text" {
			expected["document_revision\x00"+plan.SourceRevisionID+"\x00document\x00"+plan.TargetDocumentID] = struct{}{}
			expected["document_revision\x00"+plan.SourceRevisionID+"\x00document_revision\x00"+plan.TargetRevisionID] = struct{}{}
		}
	}
	for _, plan := range bundle.Manifest.Facts {
		if strings.HasPrefix(plan.Action, "import_") {
			expected["fact\x00"+plan.SourceID+"\x00evidence\x00"+plan.TargetEvidenceID] = struct{}{}
			expected["fact\x00"+plan.SourceID+"\x00claim\x00"+plan.TargetClaimID] = struct{}{}
		}
	}
	rows, err := tx.Query(ctx, `SELECT source_kind,source_id,target_kind,target_id::text FROM spyglass.prototype_migration_receipts WHERE account_id=$1 AND run_id=$2`, bundle.Manifest.AccountID, runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := make(map[string]struct{}, len(expected))
	for rows.Next() {
		var sourceKind, sourceID, targetKind, targetID string
		if err := rows.Scan(&sourceKind, &sourceID, &targetKind, &targetID); err != nil {
			return err
		}
		key := sourceKind + "\x00" + sourceID + "\x00" + targetKind + "\x00" + targetID
		if _, exists := expected[key]; !exists {
			return prototypemigration.ErrReconciliation
		}
		seen[key] = struct{}{}
	}
	if err := rows.Err(); err != nil || len(seen) != len(expected) {
		return errors.Join(err, prototypemigration.ErrReconciliation)
	}
	return nil
}

func migrationDigest(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return result, prototypemigration.ErrImportTarget
	}
	copy(result[:], decoded)
	return result, nil
}

func migrationChangeSummary(plan prototypemigration.DocumentPlan) string {
	return fmt.Sprintf("Reconstructed from prototype revision %s; original media type %s", plan.SourceRevisionID, plan.OriginalMediaType)
}

func classifyPrototypeDestinationError(err error) error {
	if err == nil || errors.Is(err, prototypemigration.ErrImportTarget) || errors.Is(err, prototypemigration.ErrReconciliation) {
		return err
	}
	if errors.Is(err, knowledgeapp.ErrInvalid) || errors.Is(err, knowledgeapp.ErrConflict) || errors.Is(err, knowledgeapp.ErrConstraint) || errors.Is(err, knowledgeapp.ErrNotFound) {
		return errors.Join(prototypemigration.ErrImportTarget, err)
	}
	return fmt.Errorf("prototype migration destination: %w", err)
}

var _ prototypemigration.ImportDestination = (*PrototypeMigrationDestination)(nil)
