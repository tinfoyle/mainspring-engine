ALTER TABLE documents
    ADD COLUMN content TEXT;

-- Earlier MVP ingestion stored only overlapping search chunks. Preserve a
-- readable fallback for those rows; new uploads store the exact source text.
UPDATE documents d
SET content = source.reconstructed
FROM (
    SELECT document_id, string_agg(content, E'\n\n' ORDER BY chunk_index) AS reconstructed
    FROM document_chunks
    GROUP BY document_id
) source
WHERE source.document_id = d.id AND d.content IS NULL;
