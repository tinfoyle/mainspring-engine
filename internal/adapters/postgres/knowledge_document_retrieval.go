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

func (r *KnowledgeRepository) RetrieveDocumentChunks(ctx context.Context, accountID ids.AccountID, query knowledgeapp.DocumentRetrievalQuery) ([]knowledgeapp.DocumentCitation, error) {
	if query.DocumentID != "" {
		return r.listPublishedDocumentChunks(ctx, accountID, query)
	}
	results := make([]knowledgeapp.DocumentCitation, 0, query.Limit)
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH requested AS (SELECT websearch_to_tsquery('simple'::regconfig,$2) AS query)
			SELECT c.account_id,d.id,r.id,c.id,d.title,d.sensitivity,r.revision,c.chunk_index,c.start_byte,c.end_byte,
				c.content,c.content_sha256,c.token_count,c.index_generation,c.created_at,
				ts_rank_cd(c.search_vector,requested.query,32)::float8
			FROM requested,spyglass.knowledge_document_chunks c
			JOIN spyglass.knowledge_document_revisions r ON r.account_id=c.account_id AND r.id=c.revision_id
			JOIN spyglass.knowledge_documents d ON d.account_id=r.account_id AND d.id=r.document_id
			WHERE c.account_id=$1 AND d.state='ready' AND d.current_revision_id=r.id AND r.state='ready'
				AND ($3 OR d.sensitivity<>'restricted') AND c.search_vector @@ requested.query
			ORDER BY ts_rank_cd(c.search_vector,requested.query,32) DESC,d.updated_at DESC,d.id,c.chunk_index,c.id
			LIMIT $4`, accountID, query.Text, query.IncludeRestricted, query.Limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			citation, err := scanDocumentCitation(rows)
			if err != nil {
				return err
			}
			results = append(results, citation)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, classifyKnowledge(err)
	}
	return results, nil
}

func (r *KnowledgeRepository) GetDocumentCitation(ctx context.Context, accountID ids.AccountID, documentID ids.KnowledgeDocumentID, revisionID ids.KnowledgeDocumentRevisionID, chunkID ids.KnowledgeDocumentChunkID, includeRestricted bool) (knowledgeapp.DocumentCitation, error) {
	var result knowledgeapp.DocumentCitation
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT c.account_id,d.id,r.id,c.id,d.title,d.sensitivity,r.revision,c.chunk_index,c.start_byte,c.end_byte,
			c.content,c.content_sha256,c.token_count,c.index_generation,c.created_at,0::float8
			FROM spyglass.knowledge_document_chunks c
			JOIN spyglass.knowledge_document_revisions r ON r.account_id=c.account_id AND r.id=c.revision_id
			JOIN spyglass.knowledge_documents d ON d.account_id=r.account_id AND d.id=r.document_id
			WHERE c.account_id=$1 AND d.id=$2 AND r.id=$3 AND c.id=$4
				AND d.state='ready' AND d.current_revision_id=r.id AND r.state='ready'
				AND ($5 OR d.sensitivity<>'restricted')`, accountID, documentID, revisionID, chunkID, includeRestricted)
		value, err := scanDocumentCitation(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return knowledgeapp.ErrNotFound
		}
		result = value
		return err
	})
	return result, classifyKnowledge(err)
}

type documentCitationScanner interface{ Scan(...any) error }

func scanDocumentCitation(scanner documentCitationScanner) (knowledgeapp.DocumentCitation, error) {
	var result knowledgeapp.DocumentCitation
	var digest []byte
	var tokenCount uint32
	var createdAt time.Time
	err := scanner.Scan(&result.AccountID, &result.DocumentID, &result.RevisionID, &result.ChunkID, &result.DocumentTitle, &result.Sensitivity,
		&result.Revision, &result.ChunkIndex, &result.StartByte, &result.EndByte, &result.Content, &digest, &tokenCount,
		&result.IndexGeneration, &createdAt, &result.Rank)
	if err != nil {
		return knowledgeapp.DocumentCitation{}, err
	}
	if len(digest) != sha256.Size || len(result.Content) > knowledgeapp.MaximumDocumentChunkBytes {
		return knowledgeapp.DocumentCitation{}, knowledgeapp.ErrRepository
	}
	copy(result.ContentSHA256[:], digest)
	_, err = knowledgedomain.NewDocumentChunk(knowledgedomain.DocumentChunk{ID: result.ChunkID, AccountID: result.AccountID, RevisionID: result.RevisionID, Index: result.ChunkIndex, StartByte: result.StartByte, EndByte: result.EndByte, Content: result.Content, ContentSHA256: result.ContentSHA256, TokenCount: tokenCount, IndexGeneration: result.IndexGeneration, CreatedAt: createdAt})
	if err != nil {
		return knowledgeapp.DocumentCitation{}, knowledgeapp.ErrRepository
	}
	return result, nil
}

var _ knowledgeapp.DocumentRetrievalRepository = (*KnowledgeRepository)(nil)

// listPublishedDocumentChunks uses the same publication, account and sensitivity
// boundary as search and citation lookup. The revision is pinned by the caller.
func (r *KnowledgeRepository) listPublishedDocumentChunks(ctx context.Context, accountID ids.AccountID, query knowledgeapp.DocumentRetrievalQuery) ([]knowledgeapp.DocumentCitation, error) {
	results := make([]knowledgeapp.DocumentCitation, 0, query.Limit)
	after := int64(-1)
	if query.AfterChunkIndex != nil {
		after = int64(*query.AfterChunkIndex)
	}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.account_id,d.id,r.id,c.id,d.title,d.sensitivity,r.revision,c.chunk_index,c.start_byte,c.end_byte,
		c.content,c.content_sha256,c.token_count,c.index_generation,c.created_at,0::float8
		FROM spyglass.knowledge_document_chunks c
		JOIN spyglass.knowledge_document_revisions r ON r.account_id=c.account_id AND r.id=c.revision_id
		JOIN spyglass.knowledge_documents d ON d.account_id=r.account_id AND d.id=r.document_id
		WHERE c.account_id=$1 AND d.id=$2 AND r.id=$3 AND c.chunk_index>$4
		AND d.state='ready' AND d.current_revision_id=r.id AND r.state='ready'
		AND ($5 OR d.sensitivity<>'restricted')
		ORDER BY c.chunk_index,c.id LIMIT $6`, accountID, query.DocumentID, query.RevisionID, after, query.IncludeRestricted, query.Limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			citation, err := scanDocumentCitation(rows)
			if err != nil {
				return err
			}
			results = append(results, citation)
		}
		return rows.Err()
	})
	return results, classifyKnowledge(err)
}
