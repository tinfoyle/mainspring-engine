package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
)

// KnowledgeDocumentProcessingQueue has queue-function authority plus the
// narrowly scoped document-table grants needed by the processing repository.
type KnowledgeDocumentProcessingQueue struct{ pool *pgxpool.Pool }

func NewKnowledgeDocumentProcessingQueue(pool *pgxpool.Pool) (*KnowledgeDocumentProcessingQueue, error) {
	if pool == nil {
		return nil, errors.New("Knowledge document processing database pool is required")
	}
	return &KnowledgeDocumentProcessingQueue{pool: pool}, nil
}

func (queue *KnowledgeDocumentProcessingQueue) Claim(ctx context.Context, leaseID string, now time.Time, lease time.Duration) (knowledgeapp.DocumentProcessingClaim, bool, error) {
	var claim knowledgeapp.DocumentProcessingClaim
	err := queue.pool.QueryRow(ctx, `SELECT account_id,revision_id,lease_id,attempt_count
		FROM public.spyglass_claim_knowledge_document_processing($1,$2,$3)`, leaseID, now.UTC(), int(lease/time.Second)).Scan(&claim.AccountID, &claim.RevisionID, &claim.LeaseID, &claim.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return knowledgeapp.DocumentProcessingClaim{}, false, nil
	}
	if err != nil {
		return knowledgeapp.DocumentProcessingClaim{}, false, mapKnowledgeDocumentProcessingError("claim Knowledge document processing", err)
	}
	return claim, true, nil
}

func (queue *KnowledgeDocumentProcessingQueue) Complete(ctx context.Context, claim knowledgeapp.DocumentProcessingClaim, now time.Time) error {
	var completed bool
	err := queue.pool.QueryRow(ctx, `SELECT public.spyglass_complete_knowledge_document_processing($1,$2,$3,$4)`, claim.AccountID, claim.RevisionID, claim.LeaseID, now.UTC()).Scan(&completed)
	if err != nil {
		return mapKnowledgeDocumentProcessingError("complete Knowledge document processing", err)
	}
	_ = completed
	return nil
}

func (queue *KnowledgeDocumentProcessingQueue) Fail(ctx context.Context, claim knowledgeapp.DocumentProcessingClaim, retry bool, next time.Time, code string, now time.Time, maxAttempts int) (string, error) {
	var state string
	err := queue.pool.QueryRow(ctx, `SELECT public.spyglass_fail_knowledge_document_processing($1,$2,$3,$4,$5,$6,$7,$8)`, claim.AccountID, claim.RevisionID, claim.LeaseID, retry, next.UTC(), code, now.UTC(), maxAttempts).Scan(&state)
	if err != nil {
		return "", mapKnowledgeDocumentProcessingError("fail Knowledge document processing", err)
	}
	return state, nil
}

func (queue *KnowledgeDocumentProcessingQueue) Stats(ctx context.Context, now time.Time) (knowledgeapp.DocumentProcessingStats, error) {
	var result knowledgeapp.DocumentProcessingStats
	var oldest *time.Time
	err := queue.pool.QueryRow(ctx, `SELECT pending,ready,leased,retrying,completed,dead_letter,oldest_ready_at
		FROM public.spyglass_knowledge_document_processing_stats($1)`, now.UTC()).Scan(&result.Pending, &result.Ready, &result.Leased, &result.Retrying, &result.Completed, &result.DeadLetter, &oldest)
	if err != nil {
		return knowledgeapp.DocumentProcessingStats{}, mapKnowledgeDocumentProcessingError("read Knowledge document processing stats", err)
	}
	if oldest != nil && now.After(*oldest) {
		result.OldestReadyAge = now.Sub(*oldest).Round(time.Second)
	}
	return result, nil
}

func mapKnowledgeDocumentProcessingError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch {
		case postgresError.Code == "P0001" && postgresError.Message == "Knowledge document processing lease lost":
			return knowledgeapp.ErrDocumentProcessingLease
		case postgresError.Code == "22023" || postgresError.Code == "P0002":
			return knowledgeapp.ErrDocumentProcessingClaim
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ knowledgeapp.DocumentProcessingQueue = (*KnowledgeDocumentProcessingQueue)(nil)
