package rag

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Document struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	MediaType  string `json:"media_type"`
	StorageKey string `json:"storage_key"`
	ChunkCount int    `json:"chunk_count"`
}

type SearchResult struct {
	DocumentID   string  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	ChunkIndex   int     `json:"chunk_index"`
	Content      string  `json:"content"`
	Rank         float32 `json:"rank"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) IngestText(ctx context.Context, name, mediaType, content string) (Document, error) {
	name = strings.TrimSpace(name)
	mediaType = strings.TrimSpace(mediaType)
	content = strings.TrimSpace(content)
	if name == "" || len(name) > 255 {
		return Document{}, errors.New("document name must contain between 1 and 255 characters")
	}
	if mediaType == "" {
		mediaType = "text/plain"
	}
	if !strings.HasPrefix(mediaType, "text/") {
		return Document{}, errors.New("only text media types are supported by the MVP ingester")
	}
	if content == "" {
		return Document{}, errors.New("document content is required")
	}
	digest := sha256.Sum256([]byte(content))
	storageKey := fmt.Sprintf("inline:sha256:%x", digest[:])

	var existing Document
	err := s.pool.QueryRow(ctx, `
		SELECT d.id::text, d.name, d.media_type, d.storage_key, count(c.id)
		FROM documents d LEFT JOIN document_chunks c ON c.document_id = d.id
		WHERE d.storage_key = $1 AND d.deleted_at IS NULL
		GROUP BY d.id
	`, storageKey).Scan(&existing.ID, &existing.Name, &existing.MediaType, &existing.StorageKey, &existing.ChunkCount)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Document{}, fmt.Errorf("find existing document: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	document := Document{ID: uuid.NewString(), Name: name, MediaType: mediaType, StorageKey: storageKey}
	if _, err := tx.Exec(ctx, `
		INSERT INTO documents (id, name, media_type, storage_key, sha256, status)
		VALUES ($1, $2, $3, $4, $5, 'processing')
	`, document.ID, name, mediaType, storageKey, digest[:]); err != nil {
		return Document{}, fmt.Errorf("create document: %w", err)
	}
	chunks := chunkText(content, 1500, 200)
	for index, chunk := range chunks {
		if _, err := tx.Exec(ctx, `INSERT INTO document_chunks (document_id, chunk_index, content) VALUES ($1, $2, $3)`, document.ID, index, chunk); err != nil {
			return Document{}, fmt.Errorf("create document chunk: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE documents SET status = 'ready', updated_at = now() WHERE id = $1`, document.ID); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	document.ChunkCount = len(chunks)
	return document, nil
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is required")
	}
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.document_id::text, d.name, c.chunk_index, c.content,
		       ts_rank_cd(c.search_vector, websearch_to_tsquery('english', $1)) AS rank
		FROM document_chunks c JOIN documents d ON d.id = c.document_id
		WHERE d.status = 'ready' AND d.deleted_at IS NULL
		  AND c.search_vector @@ websearch_to_tsquery('english', $1)
		ORDER BY rank DESC, c.document_id, c.chunk_index
		LIMIT $2
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search documents: %w", err)
	}
	defer rows.Close()
	var results []SearchResult
	for rows.Next() {
		var item SearchResult
		if err := rows.Scan(&item.DocumentID, &item.DocumentName, &item.ChunkIndex, &item.Content, &item.Rank); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func chunkText(content string, size, overlap int) []string {
	if size <= 0 {
		size = 1500
	}
	if overlap < 0 || overlap >= size {
		overlap = 0
	}
	runes := []rune(content)
	var chunks []string
	for start := 0; start < len(runes); {
		end := min(start+size, len(runes))
		if end < len(runes) {
			for candidate := end; candidate > start+size/2; candidate-- {
				if runes[candidate-1] == '\n' || runes[candidate-1] == ' ' {
					end = candidate
					break
				}
			}
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	return chunks
}
