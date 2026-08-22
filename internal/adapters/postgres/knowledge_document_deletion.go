package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type KnowledgeDocumentDeletionQueue struct{ pool *pgxpool.Pool }

func NewKnowledgeDocumentDeletionQueue(pool *pgxpool.Pool) (*KnowledgeDocumentDeletionQueue, error) {
	if pool == nil {
		return nil, errors.New("Knowledge document deletion database pool is required")
	}
	return &KnowledgeDocumentDeletionQueue{pool: pool}, nil
}

func (queue *KnowledgeDocumentDeletionQueue) Claim(ctx context.Context, leaseID string, now time.Time, lease time.Duration) (knowledgeapp.DocumentDeletionClaim, bool, error) {
	var claim knowledgeapp.DocumentDeletionClaim
	err := queue.pool.QueryRow(ctx, `SELECT account_id,document_id,lease_id,attempt_count
		FROM public.spyglass_claim_knowledge_document_deletion($1,$2,$3)`, leaseID, now.UTC(), int(lease/time.Second)).Scan(&claim.AccountID, &claim.DocumentID, &claim.LeaseID, &claim.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return knowledgeapp.DocumentDeletionClaim{}, false, nil
	}
	if err != nil {
		return knowledgeapp.DocumentDeletionClaim{}, false, mapKnowledgeDocumentDeletionError("claim Knowledge document deletion", err)
	}
	return claim, true, nil
}

func (queue *KnowledgeDocumentDeletionQueue) Complete(ctx context.Context, claim knowledgeapp.DocumentDeletionClaim, now time.Time) error {
	var completed bool
	err := queue.pool.QueryRow(ctx, `SELECT public.spyglass_complete_knowledge_document_deletion($1,$2,$3,$4)`, claim.AccountID, claim.DocumentID, claim.LeaseID, now.UTC()).Scan(&completed)
	if err != nil {
		return mapKnowledgeDocumentDeletionError("complete Knowledge document deletion", err)
	}
	_ = completed
	return nil
}

func (queue *KnowledgeDocumentDeletionQueue) Fail(ctx context.Context, claim knowledgeapp.DocumentDeletionClaim, retry bool, next time.Time, code string, now time.Time, maxAttempts int) (string, error) {
	var state string
	err := queue.pool.QueryRow(ctx, `SELECT public.spyglass_fail_knowledge_document_deletion($1,$2,$3,$4,$5,$6,$7,$8)`, claim.AccountID, claim.DocumentID, claim.LeaseID, retry, next.UTC(), code, now.UTC(), maxAttempts).Scan(&state)
	if err != nil {
		return "", mapKnowledgeDocumentDeletionError("fail Knowledge document deletion", err)
	}
	return state, nil
}

func (queue *KnowledgeDocumentDeletionQueue) Stats(ctx context.Context, now time.Time) (knowledgeapp.DocumentDeletionStats, error) {
	var result knowledgeapp.DocumentDeletionStats
	var oldest *time.Time
	err := queue.pool.QueryRow(ctx, `SELECT pending,ready,leased,retrying,completed,dead_letter,oldest_ready_at
		FROM public.spyglass_knowledge_document_deletion_stats($1)`, now.UTC()).Scan(&result.Pending, &result.Ready, &result.Leased, &result.Retrying, &result.Completed, &result.DeadLetter, &oldest)
	if err != nil {
		return knowledgeapp.DocumentDeletionStats{}, mapKnowledgeDocumentDeletionError("read Knowledge document deletion stats", err)
	}
	if oldest != nil && now.After(*oldest) {
		result.OldestReadyAge = now.Sub(*oldest).Round(time.Second)
	}
	return result, nil
}

func (r *KnowledgeRepository) LoadDeletionManifest(ctx context.Context, accountID ids.AccountID, documentID ids.KnowledgeDocumentID) (knowledgeapp.DocumentDeletionManifest, error) {
	var result knowledgeapp.DocumentDeletionManifest
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		document, err := getKnowledgeDocument(ctx, tx, accountID, documentID, false)
		if err != nil {
			return err
		}
		if document.State != knowledgedomain.DocumentDeletionPending || document.LegalHold {
			return knowledgedomain.ErrState
		}
		var hasNonterminalRevision bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM spyglass.knowledge_document_revisions
			WHERE account_id=$1 AND document_id=$2 AND state NOT IN ('ready','failed','deleted'))`, accountID, documentID).Scan(&hasNonterminalRevision); err != nil {
			return err
		}
		if hasNonterminalRevision {
			return knowledgedomain.ErrState
		}
		rows, err := tx.Query(ctx, `SELECT r.id,r.object_key,r.object_version,r.byte_size,r.content_sha256,
			r.extracted_object_key,r.extracted_object_version,r.text_bytes,r.text_sha256,
			EXISTS (SELECT 1 FROM spyglass.knowledge_document_deletion_receipts receipt
				WHERE receipt.account_id=r.account_id AND receipt.document_id=r.document_id AND receipt.revision_id=r.id
				AND receipt.object_kind='source' AND receipt.object_key=r.object_key AND receipt.object_version=r.object_version
				AND receipt.byte_size=r.byte_size AND receipt.content_sha256=r.content_sha256),
			CASE WHEN r.extracted_object_version='' THEN false ELSE EXISTS (
				SELECT 1 FROM spyglass.knowledge_document_deletion_receipts receipt
				WHERE receipt.account_id=r.account_id AND receipt.document_id=r.document_id AND receipt.revision_id=r.id
				AND receipt.object_kind='extracted' AND receipt.object_key=r.extracted_object_key
				AND receipt.object_version=r.extracted_object_version AND receipt.byte_size=r.text_bytes
				AND receipt.content_sha256=r.text_sha256) END
			FROM spyglass.knowledge_document_revisions r
			WHERE r.account_id=$1 AND r.document_id=$2 AND r.state IN ('ready','failed','deleted')
			ORDER BY r.revision,r.id`, accountID, documentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var revisionID ids.KnowledgeDocumentRevisionID
			var sourceKey, sourceVersion, extractedKey, extractedVersion string
			var sourceSize, extractedSize int64
			var sourceDigest, extractedDigest []byte
			var sourceDeleted, extractedDeleted bool
			if err := rows.Scan(&revisionID, &sourceKey, &sourceVersion, &sourceSize, &sourceDigest, &extractedKey, &extractedVersion, &extractedSize, &extractedDigest, &sourceDeleted, &extractedDeleted); err != nil {
				return err
			}
			if len(sourceDigest) != sha256.Size || (extractedVersion != "" && len(extractedDigest) != sha256.Size) {
				return knowledgeapp.ErrRepository
			}
			var sourceSHA [sha256.Size]byte
			copy(sourceSHA[:], sourceDigest)
			result.Objects = append(result.Objects, knowledgeapp.DocumentDeletionObject{RevisionID: revisionID, Kind: "source", Identity: knowledgeapp.DocumentObjectIdentity{Key: sourceKey, Version: sourceVersion, Size: sourceSize, ContentSHA256: sourceSHA}, Deleted: sourceDeleted})
			if extractedVersion != "" {
				var extractedSHA [sha256.Size]byte
				copy(extractedSHA[:], extractedDigest)
				result.Objects = append(result.Objects, knowledgeapp.DocumentDeletionObject{RevisionID: revisionID, Kind: "extracted", Identity: knowledgeapp.DocumentObjectIdentity{Key: extractedKey, Version: extractedVersion, Size: extractedSize, ContentSHA256: extractedSHA}, Deleted: extractedDeleted})
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(result.Objects) == 0 {
			return knowledgedomain.ErrState
		}
		result.Document = document
		return nil
	})
	return result, classifyKnowledge(err)
}

func (r *KnowledgeRepository) RecordDeletionReceipt(ctx context.Context, receipt knowledgeapp.DocumentDeletionReceipt) error {
	if ids.Validate(string(receipt.AccountID)) != nil || ids.Validate(string(receipt.DocumentID)) != nil || !receipt.Object.Valid(receipt.AccountID, receipt.DocumentID) || receipt.DeletedAt.IsZero() {
		return knowledgeapp.ErrInvalid
	}
	err := r.cell.WithAccountTx(ctx, receipt.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO spyglass.knowledge_document_deletion_receipts
			(account_id,document_id,revision_id,object_kind,object_key,object_version,byte_size,content_sha256,deleted_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (account_id,document_id,revision_id,object_kind) DO NOTHING`,
			receipt.AccountID, receipt.DocumentID, receipt.Object.RevisionID, receipt.Object.Kind, receipt.Object.Identity.Key,
			receipt.Object.Identity.Version, receipt.Object.Identity.Size, receipt.Object.Identity.ContentSHA256[:], receipt.DeletedAt.UTC())
		if err != nil {
			return err
		}
		var exact bool
		err = tx.QueryRow(ctx, `SELECT object_key=$5 AND object_version=$6 AND byte_size=$7 AND content_sha256=$8
			FROM spyglass.knowledge_document_deletion_receipts
			WHERE account_id=$1 AND document_id=$2 AND revision_id=$3 AND object_kind=$4`,
			receipt.AccountID, receipt.DocumentID, receipt.Object.RevisionID, receipt.Object.Kind, receipt.Object.Identity.Key,
			receipt.Object.Identity.Version, receipt.Object.Identity.Size, receipt.Object.Identity.ContentSHA256[:]).Scan(&exact)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !exact) {
			return knowledgeapp.ErrConflict
		}
		return err
	})
	return classifyKnowledge(err)
}

func mapKnowledgeDocumentDeletionError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch {
		case postgresError.Code == "P0001" && postgresError.Message == "Knowledge document deletion lease lost":
			return knowledgeapp.ErrDocumentDeletionLease
		case postgresError.Code == "22023" || postgresError.Code == "P0002" || postgresError.Code == "P0003":
			return knowledgeapp.ErrDocumentDeletionClaim
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ knowledgeapp.DocumentDeletionQueue = (*KnowledgeDocumentDeletionQueue)(nil)
var _ knowledgeapp.DocumentDeletionRepository = (*KnowledgeRepository)(nil)
