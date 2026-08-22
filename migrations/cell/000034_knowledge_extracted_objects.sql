BEGIN;

ALTER TABLE spyglass.knowledge_document_revisions
    ADD COLUMN extracted_object_key text NOT NULL DEFAULT '',
    ADD COLUMN extracted_object_version text NOT NULL DEFAULT '';

ALTER TABLE spyglass.knowledge_document_revisions
    ALTER COLUMN extracted_object_key DROP DEFAULT,
    ALTER COLUMN extracted_object_version DROP DEFAULT,
    ADD CONSTRAINT knowledge_revision_extracted_object_identity CHECK (
        (extraction_state='ready' AND
         extracted_object_key='accounts/'||account_id::text||'/documents/'||document_id::text||'/revisions/'||id::text||'/extracted/text' AND
         char_length(extracted_object_key) BETWEEN 1 AND 1024 AND
         char_length(extracted_object_version) BETWEEN 1 AND 256 AND
         extracted_object_version=btrim(extracted_object_version)) OR
        (extraction_state<>'ready' AND extracted_object_key='' AND extracted_object_version='')
    );

CREATE UNIQUE INDEX knowledge_revision_extracted_object_identity_unique
    ON spyglass.knowledge_document_revisions(account_id,extracted_object_key,extracted_object_version)
    WHERE extracted_object_version<>'';

CREATE OR REPLACE FUNCTION spyglass.protect_knowledge_revision_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.document_id IS DISTINCT FROM OLD.document_id OR NEW.revision IS DISTINCT FROM OLD.revision OR NEW.filename IS DISTINCT FROM OLD.filename OR NEW.declared_media_type IS DISTINCT FROM OLD.declared_media_type OR NEW.verified_media_type IS DISTINCT FROM OLD.verified_media_type OR NEW.byte_size IS DISTINCT FROM OLD.byte_size OR NEW.content_sha256 IS DISTINCT FROM OLD.content_sha256 OR NEW.object_key IS DISTINCT FROM OLD.object_key OR NEW.object_version IS DISTINCT FROM OLD.object_version OR NEW.change_summary IS DISTINCT FROM OLD.change_summary OR NEW.created_by_kind IS DISTINCT FROM OLD.created_by_kind OR NEW.created_by_id IS DISTINCT FROM OLD.created_by_id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Knowledge document revision identity is immutable';
    END IF;
    IF (OLD.extracted_object_key<>'' OR OLD.extracted_object_version<>'') AND (NEW.extracted_object_key IS DISTINCT FROM OLD.extracted_object_key OR NEW.extracted_object_version IS DISTINCT FROM OLD.extracted_object_version) THEN
        RAISE EXCEPTION 'Knowledge extracted object identity is immutable';
    END IF;
    IF OLD.state='quarantined' AND NEW.state NOT IN ('extracting','failed') THEN RAISE EXCEPTION 'invalid Knowledge revision transition'; END IF;
    IF OLD.state='extracting' AND NEW.state NOT IN ('extracting','ready','failed') THEN RAISE EXCEPTION 'invalid Knowledge revision transition'; END IF;
    IF OLD.state IN ('ready','failed') AND NEW.state<>'deleted' THEN RAISE EXCEPTION 'invalid Knowledge revision transition'; END IF;
    IF OLD.state='deleted' THEN RAISE EXCEPTION 'deleted Knowledge revision is immutable'; END IF;
    RETURN NEW;
END;
$$;

COMMIT;
