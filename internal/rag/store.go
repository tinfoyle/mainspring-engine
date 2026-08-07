package rag

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Document struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	MediaType      string    `json:"media_type"`
	StorageKey     string    `json:"storage_key"`
	Status         string    `json:"status"`
	ChunkCount     int       `json:"chunk_count"`
	CharacterCount int       `json:"character_count"`
	UploadedBy     string    `json:"uploaded_by"`
	CreatedAt      time.Time `json:"created_at"`
}

type DocumentDetail struct {
	Document
	Content string `json:"content"`
}

var ErrDocumentNotFound = errors.New("document was not found")

const TextDocumentLimit = 2 << 20

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
	return s.IngestTextBy(ctx, name, mediaType, content, "")
}

func (s *Store) IngestTextBy(ctx context.Context, name, mediaType, content, createdBy string) (Document, error) {
	name = strings.TrimSpace(name)
	mediaType = strings.TrimSpace(mediaType)
	if name == "" || len(name) > 255 {
		return Document{}, errors.New("document name must contain between 1 and 255 characters")
	}
	if mediaType == "" {
		mediaType = "text/plain"
	}
	if !supportedTextMediaType(mediaType) {
		return Document{}, errors.New("only plain text, Markdown, CSV, JSON, XML, HTML, and log files are supported")
	}
	if strings.TrimSpace(content) == "" {
		return Document{}, errors.New("document content is required")
	}
	if len([]byte(content)) > TextDocumentLimit {
		return Document{}, errors.New("document content must be no larger than 2 MB")
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return Document{}, errors.New("document content must be valid text")
	}
	digest := sha256.Sum256([]byte(content))
	storageKey := fmt.Sprintf("inline:sha256:%x", digest[:])

	var existing Document
	err := s.pool.QueryRow(ctx, `
		SELECT d.id::text, d.name, d.media_type, d.storage_key, d.status, count(c.id),
		       COALESCE(length(d.content), 0), COALESCE(u.display_name, 'System'), d.created_at
		FROM documents d LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN users u ON u.id = d.created_by
		WHERE d.storage_key = $1 AND d.deleted_at IS NULL
		GROUP BY d.id, u.display_name
	`, storageKey).Scan(&existing.ID, &existing.Name, &existing.MediaType, &existing.StorageKey, &existing.Status,
		&existing.ChunkCount, &existing.CharacterCount, &existing.UploadedBy, &existing.CreatedAt)
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
	document := Document{ID: uuid.NewString(), Name: name, MediaType: mediaType, StorageKey: storageKey, Status: "ready", CharacterCount: len([]rune(content)), CreatedAt: time.Now().UTC()}
	var creator any
	if strings.TrimSpace(createdBy) != "" {
		creator = strings.TrimSpace(createdBy)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO documents (id, name, media_type, storage_key, sha256, status, content, created_by)
		VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7)
	`, document.ID, name, mediaType, storageKey, digest[:], content, creator); err != nil {
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
	if creator != nil {
		_ = s.pool.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1`, creator).Scan(&document.UploadedBy)
	}
	if document.UploadedBy == "" {
		document.UploadedBy = "System"
	}
	return document, nil
}

func (s *Store) ListDocuments(ctx context.Context) ([]Document, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id::text, d.name, d.media_type, d.storage_key, d.status, count(c.id),
		       COALESCE(length(d.content), 0), COALESCE(u.display_name, 'System'), d.created_at
		FROM documents d
		LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN users u ON u.id = d.created_by
		WHERE d.deleted_at IS NULL AND d.status <> 'deleted'
		GROUP BY d.id, u.display_name
		ORDER BY d.created_at DESC, d.id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()
	var documents []Document
	for rows.Next() {
		var document Document
		if err := rows.Scan(&document.ID, &document.Name, &document.MediaType, &document.StorageKey, &document.Status,
			&document.ChunkCount, &document.CharacterCount, &document.UploadedBy, &document.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan document: %w", err)
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func (s *Store) GetDocument(ctx context.Context, documentID string) (DocumentDetail, error) {
	var document DocumentDetail
	err := s.pool.QueryRow(ctx, `
		SELECT d.id::text, d.name, d.media_type, d.storage_key, d.status,
		       (SELECT count(*) FROM document_chunks c WHERE c.document_id = d.id),
		       COALESCE(length(d.content), 0), COALESCE(u.display_name, 'System'), d.created_at,
		       COALESCE(d.content, '')
		FROM documents d
		LEFT JOIN users u ON u.id = d.created_by
		WHERE d.id = $1 AND d.deleted_at IS NULL AND d.status <> 'deleted'
	`, documentID).Scan(&document.ID, &document.Name, &document.MediaType, &document.StorageKey, &document.Status,
		&document.ChunkCount, &document.CharacterCount, &document.UploadedBy, &document.CreatedAt, &document.Content)
	if errors.Is(err, pgx.ErrNoRows) {
		return DocumentDetail{}, ErrDocumentNotFound
	}
	if err != nil {
		return DocumentDetail{}, fmt.Errorf("get document: %w", err)
	}
	return document, nil
}

func supportedTextMediaType(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || mediaType == "application/xml" || mediaType == "application/xhtml+xml"
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	return s.SearchDocuments(ctx, query, limit, nil)
}

func (s *Store) SearchDocuments(ctx context.Context, query string, limit int, documentIDs []string) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is required")
	}
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	for _, documentID := range documentIDs {
		if _, err := uuid.Parse(documentID); err != nil {
			return nil, errors.New("document filters must be valid IDs")
		}
	}
	var filters any
	if len(documentIDs) > 0 {
		filters = documentIDs
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.document_id::text, d.name, c.chunk_index, c.content,
		       ts_rank_cd(c.search_vector, websearch_to_tsquery('english', $1)) AS rank
		FROM document_chunks c JOIN documents d ON d.id = c.document_id
		WHERE d.status = 'ready' AND d.deleted_at IS NULL
		  AND c.search_vector @@ websearch_to_tsquery('english', $1)
		  AND ($3::text[] IS NULL OR c.document_id::text = ANY($3::text[]))
		ORDER BY rank DESC, c.document_id, c.chunk_index
		LIMIT $2
	`, query, limit, filters)
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
