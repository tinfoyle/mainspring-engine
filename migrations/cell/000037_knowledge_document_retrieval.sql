BEGIN;

ALTER TABLE spyglass.knowledge_document_chunks
    ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig,content)) STORED;

CREATE INDEX knowledge_document_chunks_search
    ON spyglass.knowledge_document_chunks USING gin(search_vector);

COMMIT;
