package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (r *KnowledgeRepository) AdmitDocument(ctx context.Context, document knowledgedomain.Document, revision knowledgedomain.DocumentRevision, mutation knowledgeapp.Mutation) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
	var admittedDocument knowledgedomain.Document
	var admittedRevision knowledgedomain.DocumentRevision
	err := r.cell.WithAccountTx(ctx, document.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, err := getKnowledgeDocument(ctx, tx, document.AccountID, document.ID, false)
		if err == nil {
			existingRevision, revisionErr := getKnowledgeDocumentRevision(ctx, tx, document.AccountID, revision.ID, false)
			if revisionErr != nil {
				return revisionErr
			}
			if !sameDocumentAdmission(existing, document) || !sameRevisionAdmission(existingRevision, revision) {
				return knowledgeapp.ErrConflict
			}
			admittedDocument, admittedRevision = existing, existingRevision
			return nil
		}
		if !errors.Is(err, knowledgeapp.ErrNotFound) {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.knowledge_documents(account_id,id,title,sensitivity,state,retain_until,legal_hold,version,created_by_kind,created_by_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, document.AccountID, document.ID, document.Title, document.Sensitivity, document.State, document.RetainUntil, document.LegalHold, document.Version, document.CreatedBy.Kind, document.CreatedBy.ID, document.CreatedAt, document.UpdatedAt)
		if err != nil {
			return err
		}
		if err := insertKnowledgeDocumentRevision(ctx, tx, revision); err != nil {
			return err
		}
		if err := insertKnowledgeDocumentEvent(ctx, tx, document.AccountID, document.ID, "", "document_created", 0, document.Version, mutation, map[string]any{"sensitivity": document.Sensitivity}); err != nil {
			return err
		}
		if err := insertKnowledgeDocumentEvent(ctx, tx, document.AccountID, document.ID, revision.ID, "revision_admitted", 0, 0, mutation, map[string]any{"revision": revision.Number, "byte_size": revision.ByteSize, "verified_media_type": revision.VerifiedType}); err != nil {
			return err
		}
		admittedDocument, admittedRevision = document, revision
		return nil
	})
	return admittedDocument, admittedRevision, classifyKnowledge(err)
}

func (r *KnowledgeRepository) GetDocument(ctx context.Context, accountID ids.AccountID, documentID ids.KnowledgeDocumentID) (knowledgedomain.Document, error) {
	var result knowledgedomain.Document
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := getKnowledgeDocument(ctx, tx, accountID, documentID, false)
		result = value
		return err
	})
	return result, classifyKnowledge(err)
}

func (r *KnowledgeRepository) GetDocumentRevision(ctx context.Context, accountID ids.AccountID, revisionID ids.KnowledgeDocumentRevisionID) (knowledgedomain.DocumentRevision, error) {
	var result knowledgedomain.DocumentRevision
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := getKnowledgeDocumentRevision(ctx, tx, accountID, revisionID, false)
		result = value
		return err
	})
	return result, classifyKnowledge(err)
}

func (r *KnowledgeRepository) SaveDocumentRevision(ctx context.Context, value knowledgedomain.DocumentRevision, expectedUpdatedAt time.Time, eventType string, mutation knowledgeapp.Mutation) (knowledgedomain.DocumentRevision, error) {
	err := r.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		if err := updateKnowledgeDocumentRevision(ctx, tx, value, expectedUpdatedAt); err != nil {
			return err
		}
		if value.State == knowledgedomain.RevisionFailed && value.Number == 1 {
			document, err := getKnowledgeDocument(ctx, tx, value.AccountID, value.DocumentID, true)
			if err != nil {
				return err
			}
			if document.State == knowledgedomain.DocumentProcessing {
				failed, err := document.FailInitial(document.Version, mutation.At)
				if err != nil {
					return err
				}
				tag, err := tx.Exec(ctx, `UPDATE spyglass.knowledge_documents SET state=$4,version=$5,updated_at=$6 WHERE account_id=$1 AND id=$2 AND version=$3`, failed.AccountID, failed.ID, document.Version, failed.State, failed.Version, failed.UpdatedAt)
				if err == nil && tag.RowsAffected() != 1 {
					return knowledgeapp.ErrConflict
				}
				if err != nil {
					return err
				}
			}
		}
		return insertKnowledgeDocumentEvent(ctx, tx, value.AccountID, value.DocumentID, value.ID, eventType, 0, 0, mutation, map[string]any{"revision": value.Number, "state": value.State, "scan_state": value.ScanState, "extraction_state": value.Extraction, "index_state": value.Index})
	})
	return value, classifyKnowledge(err)
}

func (r *KnowledgeRepository) IndexDocumentRevision(ctx context.Context, value knowledgedomain.DocumentRevision, expectedUpdatedAt time.Time, chunks []knowledgedomain.DocumentChunk, mutation knowledgeapp.Mutation) (knowledgedomain.DocumentRevision, error) {
	err := r.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		for _, chunk := range chunks {
			_, err := tx.Exec(ctx, `INSERT INTO spyglass.knowledge_document_chunks(account_id,id,revision_id,chunk_index,start_byte,end_byte,content,content_sha256,token_count,index_generation,created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, chunk.AccountID, chunk.ID, chunk.RevisionID, chunk.Index, chunk.StartByte, chunk.EndByte, chunk.Content, chunk.ContentSHA256[:], chunk.TokenCount, chunk.IndexGeneration, chunk.CreatedAt)
			if err != nil {
				return err
			}
		}
		if err := updateKnowledgeDocumentRevision(ctx, tx, value, expectedUpdatedAt); err != nil {
			return err
		}
		return insertKnowledgeDocumentEvent(ctx, tx, value.AccountID, value.DocumentID, value.ID, "index_completed", 0, 0, mutation, map[string]any{"revision": value.Number, "chunk_count": value.ChunkCount, "index_generation": value.IndexGeneration})
	})
	return value, classifyKnowledge(err)
}

func (r *KnowledgeRepository) PublishDocumentRevision(ctx context.Context, accountID ids.AccountID, documentID ids.KnowledgeDocumentID, revisionID ids.KnowledgeDocumentRevisionID, expectedVersion uint64, mutation knowledgeapp.Mutation) (knowledgedomain.Document, error) {
	var result knowledgedomain.Document
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		document, err := getKnowledgeDocument(ctx, tx, accountID, documentID, true)
		if err != nil {
			return err
		}
		if document.CurrentRevisionID == revisionID && document.Version == expectedVersion+1 {
			result = document
			return nil
		}
		revision, err := getKnowledgeDocumentRevision(ctx, tx, accountID, revisionID, true)
		if err != nil {
			return err
		}
		result, err = document.Publish(revision, expectedVersion, mutation.At)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE spyglass.knowledge_documents SET current_revision_id=$4,current_revision=$5,state=$6,version=$7,updated_at=$8 WHERE account_id=$1 AND id=$2 AND version=$3`, accountID, documentID, expectedVersion, result.CurrentRevisionID, result.CurrentRevision, result.State, result.Version, result.UpdatedAt)
		if err == nil && tag.RowsAffected() != 1 {
			return knowledgeapp.ErrConflict
		}
		if err != nil {
			return err
		}
		return insertKnowledgeDocumentEvent(ctx, tx, accountID, documentID, "", "revision_published", expectedVersion, result.Version, mutation, map[string]any{"revision": result.CurrentRevision, "revision_id": result.CurrentRevisionID})
	})
	return result, classifyKnowledge(err)
}

func (r *KnowledgeRepository) RequestDocumentDeletion(ctx context.Context, accountID ids.AccountID, documentID ids.KnowledgeDocumentID, expectedVersion uint64, mutation knowledgeapp.Mutation) (knowledgedomain.Document, error) {
	var result knowledgedomain.Document
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := getKnowledgeDocument(ctx, tx, accountID, documentID, true)
		if err != nil {
			return err
		}
		if current.State == knowledgedomain.DocumentDeletionPending && current.Version == expectedVersion+1 {
			result = current
			return nil
		}
		result, err = current.RequestDeletion(expectedVersion, mutation.At)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE spyglass.knowledge_documents SET state=$4,version=$5,updated_at=$6,deletion_requested_at=$7 WHERE account_id=$1 AND id=$2 AND version=$3`, accountID, documentID, expectedVersion, result.State, result.Version, result.UpdatedAt, result.DeletionRequested)
		if err == nil && tag.RowsAffected() != 1 {
			return knowledgeapp.ErrConflict
		}
		if err != nil {
			return err
		}
		return insertKnowledgeDocumentEvent(ctx, tx, accountID, documentID, "", "deletion_requested", expectedVersion, result.Version, mutation, map[string]any{"current_revision": result.CurrentRevision})
	})
	return result, classifyKnowledge(err)
}

func insertKnowledgeDocumentRevision(ctx context.Context, tx pgx.Tx, value knowledgedomain.DocumentRevision) error {
	_, err := tx.Exec(ctx, `INSERT INTO spyglass.knowledge_document_revisions(account_id,id,document_id,revision,filename,declared_media_type,verified_media_type,byte_size,content_sha256,object_key,object_version,change_summary,state,scan_state,scan_engine,scan_signature,scanned_at,extraction_state,extractor,text_sha256,text_bytes,extracted_object_key,extracted_object_version,extracted_at,index_state,index_generation,chunk_count,indexed_at,failure_code,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33)`, value.AccountID, value.ID, value.DocumentID, value.Number, value.Filename, value.DeclaredType, value.VerifiedType, value.ByteSize, value.ContentSHA256[:], value.ObjectKey, value.ObjectVersion, value.ChangeSummary, value.State, value.ScanState, value.ScanEngine, value.ScanSignature, value.ScannedAt, value.Extraction, value.Extractor, nullableDigest(value.TextSHA256), value.TextBytes, value.ExtractedObjectKey, value.ExtractedObjectVersion, value.ExtractedAt, value.Index, value.IndexGeneration, value.ChunkCount, value.IndexedAt, value.FailureCode, value.CreatedBy.Kind, value.CreatedBy.ID, value.CreatedAt, value.UpdatedAt)
	return err
}

func updateKnowledgeDocumentRevision(ctx context.Context, tx pgx.Tx, value knowledgedomain.DocumentRevision, expectedUpdatedAt time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE spyglass.knowledge_document_revisions SET state=$4,scan_state=$5,scan_engine=$6,scan_signature=$7,scanned_at=$8,extraction_state=$9,extractor=$10,text_sha256=$11,text_bytes=$12,extracted_object_key=$13,extracted_object_version=$14,extracted_at=$15,index_state=$16,index_generation=$17,chunk_count=$18,indexed_at=$19,failure_code=$20,updated_at=$21 WHERE account_id=$1 AND id=$2 AND updated_at=$3`, value.AccountID, value.ID, expectedUpdatedAt, value.State, value.ScanState, value.ScanEngine, value.ScanSignature, value.ScannedAt, value.Extraction, value.Extractor, nullableDigest(value.TextSHA256), value.TextBytes, value.ExtractedObjectKey, value.ExtractedObjectVersion, value.ExtractedAt, value.Index, value.IndexGeneration, value.ChunkCount, value.IndexedAt, value.FailureCode, value.UpdatedAt)
	if err == nil && tag.RowsAffected() != 1 {
		return knowledgeapp.ErrConflict
	}
	return err
}

func getKnowledgeDocument(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, documentID ids.KnowledgeDocumentID, lock bool) (knowledgedomain.Document, error) {
	var value knowledgedomain.Document
	var currentRevisionID *string
	var currentRevision *uint64
	statement := `SELECT id,account_id,title,sensitivity,current_revision_id,current_revision,state,retain_until,legal_hold,version,created_by_kind,created_by_id,created_at,updated_at,deletion_requested_at,deleted_at FROM spyglass.knowledge_documents WHERE account_id=$1 AND id=$2`
	if lock {
		statement += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, statement, accountID, documentID).Scan(&value.ID, &value.AccountID, &value.Title, &value.Sensitivity, &currentRevisionID, &currentRevision, &value.State, &value.RetainUntil, &value.LegalHold, &value.Version, &value.CreatedBy.Kind, &value.CreatedBy.ID, &value.CreatedAt, &value.UpdatedAt, &value.DeletionRequested, &value.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, knowledgeapp.ErrNotFound
	}
	if err != nil {
		return value, err
	}
	if currentRevisionID != nil {
		value.CurrentRevisionID = ids.KnowledgeDocumentRevisionID(*currentRevisionID)
	}
	if currentRevision != nil {
		value.CurrentRevision = *currentRevision
	}
	value, err = knowledgedomain.RestoreDocument(value)
	if err != nil {
		return value, knowledgeapp.ErrRepository
	}
	return value, nil
}

func getKnowledgeDocumentRevision(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, revisionID ids.KnowledgeDocumentRevisionID, lock bool) (knowledgedomain.DocumentRevision, error) {
	var value knowledgedomain.DocumentRevision
	var sourceDigest []byte
	var textDigest []byte
	statement := `SELECT id,document_id,account_id,revision,filename,declared_media_type,verified_media_type,byte_size,content_sha256,object_key,object_version,change_summary,state,scan_state,scan_engine,scan_signature,scanned_at,extraction_state,extractor,text_sha256,text_bytes,extracted_object_key,extracted_object_version,extracted_at,index_state,index_generation,chunk_count,indexed_at,failure_code,created_by_kind,created_by_id,created_at,updated_at FROM spyglass.knowledge_document_revisions WHERE account_id=$1 AND id=$2`
	if lock {
		statement += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, statement, accountID, revisionID).Scan(&value.ID, &value.DocumentID, &value.AccountID, &value.Number, &value.Filename, &value.DeclaredType, &value.VerifiedType, &value.ByteSize, &sourceDigest, &value.ObjectKey, &value.ObjectVersion, &value.ChangeSummary, &value.State, &value.ScanState, &value.ScanEngine, &value.ScanSignature, &value.ScannedAt, &value.Extraction, &value.Extractor, &textDigest, &value.TextBytes, &value.ExtractedObjectKey, &value.ExtractedObjectVersion, &value.ExtractedAt, &value.Index, &value.IndexGeneration, &value.ChunkCount, &value.IndexedAt, &value.FailureCode, &value.CreatedBy.Kind, &value.CreatedBy.ID, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, knowledgeapp.ErrNotFound
	}
	if err != nil || len(sourceDigest) != sha256.Size || (textDigest != nil && len(textDigest) != sha256.Size) {
		return value, err
	}
	copy(value.ContentSHA256[:], sourceDigest)
	copy(value.TextSHA256[:], textDigest)
	value, err = knowledgedomain.RestoreDocumentRevision(value)
	if err != nil {
		return value, knowledgeapp.ErrRepository
	}
	return value, nil
}

func insertKnowledgeDocumentEvent(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, documentID ids.KnowledgeDocumentID, revisionID ids.KnowledgeDocumentRevisionID, eventType string, from, to uint64, mutation knowledgeapp.Mutation, payload map[string]any) error {
	eventID, err := ids.Derive(mutation.CorrelationID, "knowledge-document-"+eventType+"-"+string(documentID)+"-"+string(revisionID))
	if err != nil {
		return knowledgeapp.ErrInvalid
	}
	kind := "document"
	var storedRevision any
	if revisionID != "" {
		kind, storedRevision = "revision", revisionID
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.knowledge_document_events(account_id,id,aggregate_kind,document_id,revision_id,event_type,from_version,to_version,actor_kind,actor_id,reason_code,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT (account_id,id) DO NOTHING`, accountID, eventID, kind, documentID, storedRevision, eventType, from, to, mutation.Actor.Kind, mutation.Actor.ID, mutation.ReasonCode, mutation.CorrelationID, payload, mutation.At)
	return err
}

func sameDocumentAdmission(left, right knowledgedomain.Document) bool {
	return left.ID == right.ID && left.AccountID == right.AccountID && left.Title == right.Title && left.Sensitivity == right.Sensitivity && equalOptionalTime(left.RetainUntil, right.RetainUntil) && left.CreatedBy == right.CreatedBy
}

func sameRevisionAdmission(left, right knowledgedomain.DocumentRevision) bool {
	return left.ID == right.ID && left.DocumentID == right.DocumentID && left.AccountID == right.AccountID && left.Number == right.Number && left.Filename == right.Filename && left.DeclaredType == right.DeclaredType && left.VerifiedType == right.VerifiedType && left.ByteSize == right.ByteSize && left.ContentSHA256 == right.ContentSHA256 && left.ObjectKey == right.ObjectKey && left.ObjectVersion == right.ObjectVersion && left.ChangeSummary == right.ChangeSummary && left.CreatedBy == right.CreatedBy
}

func equalOptionalTime(left, right *time.Time) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && left.Equal(*right))
}

func nullableDigest(value [sha256.Size]byte) any {
	if value == ([sha256.Size]byte{}) {
		return nil
	}
	return value[:]
}

var _ knowledgeapp.DocumentRepository = (*KnowledgeRepository)(nil)
