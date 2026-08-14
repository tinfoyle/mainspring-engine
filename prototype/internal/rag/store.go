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
	Revision       int       `json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type DocumentDetail struct {
	Document
	Content string `json:"content"`
}

type DocumentProvenance struct {
	CreatedBy     string `json:"created_by,omitempty"`
	PersonaID     string `json:"persona_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	InvocationID  string `json:"invocation_id,omitempty"`
	ChangeSummary string `json:"change_summary,omitempty"`
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
	return s.ingestText(ctx, name, mediaType, content, DocumentProvenance{CreatedBy: createdBy, ChangeSummary: "Initial document revision"})
}

func (s *Store) IngestTextByAgent(ctx context.Context, name, mediaType, content string, provenance DocumentProvenance) (Document, error) {
	if strings.TrimSpace(provenance.PersonaID) == "" || strings.TrimSpace(provenance.RunID) == "" || strings.TrimSpace(provenance.InvocationID) == "" {
		return Document{}, errors.New("agent document provenance is required")
	}
	if strings.TrimSpace(provenance.ChangeSummary) == "" {
		provenance.ChangeSummary = "Created by agent"
	}
	return s.ingestText(ctx, name, mediaType, content, provenance)
}

func (s *Store) ingestText(ctx context.Context, name, mediaType, content string, provenance DocumentProvenance) (Document, error) {
	name = strings.TrimSpace(name)
	mediaType = strings.TrimSpace(mediaType)
	if name == "" || len(name) > 255 {
		return Document{}, errors.New("document name must contain between 1 and 255 characters")
	}
	if mediaType == "" {
		mediaType = "text/plain"
	}
	if !supportedTextMediaType(mediaType) {
		return Document{}, errors.New("only extracted PDF, Word, and supported text documents are accepted")
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
		       COALESCE(length(d.content), 0), COALESCE(p.name, u.display_name, 'System'), d.revision, d.created_at, d.updated_at
		FROM documents d LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN users u ON u.id = d.created_by
		LEFT JOIN personas p ON p.id = d.created_by_persona
		WHERE d.storage_key = $1 AND d.deleted_at IS NULL
		GROUP BY d.id, u.display_name, p.name
	`, storageKey).Scan(&existing.ID, &existing.Name, &existing.MediaType, &existing.StorageKey, &existing.Status,
		&existing.ChunkCount, &existing.CharacterCount, &existing.UploadedBy, &existing.Revision, &existing.CreatedAt, &existing.UpdatedAt)
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
	var creator, personaID, runID, invocationID any
	if strings.TrimSpace(provenance.CreatedBy) != "" {
		creator = strings.TrimSpace(provenance.CreatedBy)
	}
	if strings.TrimSpace(provenance.PersonaID) != "" {
		personaID = strings.TrimSpace(provenance.PersonaID)
	}
	if strings.TrimSpace(provenance.RunID) != "" {
		runID = strings.TrimSpace(provenance.RunID)
	}
	if strings.TrimSpace(provenance.InvocationID) != "" {
		invocationID = strings.TrimSpace(provenance.InvocationID)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO documents (
			id, name, media_type, storage_key, sha256, status, content, created_by,
			created_by_persona, last_updated_by_persona, source_run_id, source_invocation_id
		)
		VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7, $8, $8, $9, $10)
	`, document.ID, name, mediaType, storageKey, digest[:], content, creator, personaID, runID, invocationID); err != nil {
		return Document{}, fmt.Errorf("create document: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO document_revisions (
			document_id, revision, name, media_type, content, sha256, change_summary,
			created_by, created_by_persona, source_run_id, source_invocation_id
		) VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, document.ID, name, mediaType, content, digest[:], strings.TrimSpace(provenance.ChangeSummary), creator, personaID, runID, invocationID); err != nil {
		return Document{}, fmt.Errorf("create document revision: %w", err)
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
	document.Revision = 1
	document.UpdatedAt = document.CreatedAt
	if personaID != nil {
		_ = s.pool.QueryRow(ctx, `SELECT name FROM personas WHERE id = $1`, personaID).Scan(&document.UploadedBy)
	} else if creator != nil {
		_ = s.pool.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1`, creator).Scan(&document.UploadedBy)
	}
	if document.UploadedBy == "" {
		document.UploadedBy = "System"
	}
	return document, nil
}

func (s *Store) UpdateTextByAgent(ctx context.Context, documentID, name, mediaType, content string, provenance DocumentProvenance) (Document, error) {
	if _, err := uuid.Parse(documentID); err != nil {
		return Document{}, ErrDocumentNotFound
	}
	if strings.TrimSpace(provenance.PersonaID) == "" || strings.TrimSpace(provenance.RunID) == "" || strings.TrimSpace(provenance.InvocationID) == "" {
		return Document{}, errors.New("agent document provenance is required")
	}
	name = strings.TrimSpace(name)
	mediaType = strings.TrimSpace(mediaType)
	if name == "" || len(name) > 255 {
		return Document{}, errors.New("document name must contain between 1 and 255 characters")
	}
	if mediaType == "" {
		mediaType = "text/markdown"
	}
	if !supportedTextMediaType(mediaType) || strings.TrimSpace(content) == "" || len([]byte(content)) > TextDocumentLimit || !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return Document{}, errors.New("document update must contain valid supported text no larger than 2 MB")
	}
	digest := sha256.Sum256([]byte(content))
	storageKey := fmt.Sprintf("inline:sha256:%x", digest[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var revision int
	var currentDigest []byte
	if err := tx.QueryRow(ctx, `SELECT revision, sha256 FROM documents WHERE id=$1 AND deleted_at IS NULL AND status <> 'deleted' FOR UPDATE`, documentID).Scan(&revision, &currentDigest); errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrDocumentNotFound
	} else if err != nil {
		return Document{}, err
	}
	if string(currentDigest) == string(digest[:]) {
		if err := tx.Commit(ctx); err != nil {
			return Document{}, err
		}
		detail, err := s.GetDocument(ctx, documentID)
		return detail.Document, err
	}
	revision++
	if _, err := tx.Exec(ctx, `DELETE FROM document_chunks WHERE document_id=$1`, documentID); err != nil {
		return Document{}, err
	}
	chunks := chunkText(content, 1500, 200)
	for index, chunk := range chunks {
		if _, err := tx.Exec(ctx, `INSERT INTO document_chunks (document_id, chunk_index, content) VALUES ($1,$2,$3)`, documentID, index, chunk); err != nil {
			return Document{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE documents SET name=$2, media_type=$3, storage_key=$4, sha256=$5, content=$6,
			revision=$7, last_updated_by_persona=$8, source_run_id=$9, source_invocation_id=$10,
			status='ready', updated_at=now()
		WHERE id=$1
	`, documentID, name, mediaType, storageKey, digest[:], content, revision, provenance.PersonaID, provenance.RunID, provenance.InvocationID); err != nil {
		return Document{}, fmt.Errorf("update document: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO document_revisions (
			document_id, revision, name, media_type, content, sha256, change_summary,
			created_by_persona, source_run_id, source_invocation_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, documentID, revision, name, mediaType, content, digest[:], strings.TrimSpace(provenance.ChangeSummary), provenance.PersonaID, provenance.RunID, provenance.InvocationID); err != nil {
		return Document{}, fmt.Errorf("create document revision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	detail, err := s.GetDocument(ctx, documentID)
	return detail.Document, err
}

func (s *Store) ListDocuments(ctx context.Context) ([]Document, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id::text, d.name, d.media_type, d.storage_key, d.status, count(c.id),
		       COALESCE(length(d.content), 0), COALESCE(p.name, u.display_name, 'System'), d.revision, d.created_at, d.updated_at
		FROM documents d
		LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN users u ON u.id = d.created_by
		LEFT JOIN personas p ON p.id = d.created_by_persona
		WHERE d.deleted_at IS NULL AND d.status <> 'deleted'
		GROUP BY d.id, u.display_name, p.name
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
			&document.ChunkCount, &document.CharacterCount, &document.UploadedBy, &document.Revision, &document.CreatedAt, &document.UpdatedAt); err != nil {
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
		       COALESCE(length(d.content), 0), COALESCE(p.name, u.display_name, 'System'), d.revision, d.created_at, d.updated_at,
		       COALESCE(d.content, '')
		FROM documents d
		LEFT JOIN users u ON u.id = d.created_by
		LEFT JOIN personas p ON p.id = d.created_by_persona
		WHERE d.id = $1 AND d.deleted_at IS NULL AND d.status <> 'deleted'
	`, documentID).Scan(&document.ID, &document.Name, &document.MediaType, &document.StorageKey, &document.Status,
		&document.ChunkCount, &document.CharacterCount, &document.UploadedBy, &document.Revision, &document.CreatedAt, &document.UpdatedAt, &document.Content)
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
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || mediaType == "application/xml" || mediaType == "application/xhtml+xml" ||
		mediaType == "application/pdf" || mediaType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
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
	if query == "*" {
		if len(documentIDs) == 0 {
			return nil, errors.New("wildcard document retrieval requires document filters")
		}
		rows, err := s.pool.Query(ctx, `
			SELECT c.document_id::text, d.name, c.chunk_index, c.content, 0::real
			FROM document_chunks c JOIN documents d ON d.id = c.document_id
			WHERE d.status = 'ready' AND d.deleted_at IS NULL
			  AND c.document_id::text = ANY($2::text[])
			ORDER BY c.document_id, c.chunk_index
			LIMIT $1
		`, limit, documentIDs)
		if err != nil {
			return nil, fmt.Errorf("retrieve document chunks: %w", err)
		}
		return scanSearchResults(rows)
	}
	rows, err := s.pool.Query(ctx, `
		WITH search_query AS (
			SELECT to_tsquery('english', string_agg(quote_literal(term), ' | ')) AS value
			FROM unnest(tsvector_to_array(to_tsvector('english', $1))) AS term
		)
		SELECT c.document_id::text, d.name, c.chunk_index, c.content,
		       ts_rank_cd(c.search_vector, search_query.value) AS rank
		FROM document_chunks c
		JOIN documents d ON d.id = c.document_id
		CROSS JOIN search_query
		WHERE d.status = 'ready' AND d.deleted_at IS NULL
		  AND search_query.value IS NOT NULL
		  AND c.search_vector @@ search_query.value
		  AND ($3::text[] IS NULL OR c.document_id::text = ANY($3::text[]))
		ORDER BY rank DESC, c.document_id, c.chunk_index
		LIMIT $2
	`, query, limit, filters)
	if err != nil {
		return nil, fmt.Errorf("search documents: %w", err)
	}
	return scanSearchResults(rows)
}

func scanSearchResults(rows pgx.Rows) ([]SearchResult, error) {
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
