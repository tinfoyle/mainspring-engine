BEGIN;

ALTER TABLE catalog_publications DROP CONSTRAINT catalog_publications_state_check;
ALTER TABLE catalog_publications
    ADD CONSTRAINT catalog_publications_state_check
    CHECK (state IN ('draft','in_review','approved','published','retired'));

ALTER TABLE catalog_publications
    ADD COLUMN created_by text,
    ADD COLUMN change_reason text,
    ADD COLUMN review_requested_at timestamptz,
    ADD COLUMN review_requested_by text,
    ADD COLUMN reviewed_at timestamptz,
    ADD COLUMN reviewed_by text,
    ADD COLUMN published_by text,
    ADD COLUMN retired_at timestamptz,
    ADD COLUMN retired_by text;

UPDATE catalog_publications
SET created_by='migration',change_reason='seeded by reviewed migration',
    reviewed_at=COALESCE(published_at,created_at),reviewed_by='migration',
    published_by=CASE WHEN published_at IS NOT NULL THEN 'migration' ELSE NULL END
WHERE created_by IS NULL;

ALTER TABLE catalog_publications ALTER COLUMN created_by SET NOT NULL;
ALTER TABLE catalog_publications ALTER COLUMN change_reason SET NOT NULL;

CREATE TABLE catalog_operator_events (
    id uuid PRIMARY KEY,
    catalog_version bigint NOT NULL REFERENCES catalog_publications (version),
    action text NOT NULL CHECK (action IN ('draft_created','price_mapped','review_requested','approved','published','retired','republished')),
    actor text NOT NULL,
    reason text NOT NULL,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL
);
CREATE INDEX catalog_operator_events_version_time ON catalog_operator_events (catalog_version,created_at,id);

CREATE FUNCTION reject_catalog_content_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.content IS DISTINCT FROM NEW.content OR OLD.content_hash IS DISTINCT FROM NEW.content_hash OR OLD.version IS DISTINCT FROM NEW.version THEN
        RAISE EXCEPTION 'catalog publication content is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER catalog_content_immutable
BEFORE UPDATE ON catalog_publications
FOR EACH ROW EXECUTE FUNCTION reject_catalog_content_mutation();

CREATE FUNCTION enforce_catalog_state_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.state = NEW.state THEN
        RETURN NEW;
    ELSIF OLD.state = 'draft' AND NEW.state = 'in_review' AND NEW.review_requested_at IS NOT NULL AND NEW.review_requested_by IS NOT NULL THEN
        RETURN NEW;
    ELSIF OLD.state = 'in_review' AND NEW.state = 'approved' AND NEW.reviewed_at IS NOT NULL AND NEW.reviewed_by IS NOT NULL AND NEW.reviewed_by <> OLD.created_by THEN
        RETURN NEW;
    ELSIF OLD.state = 'approved' AND NEW.state = 'published' AND NEW.published_at IS NOT NULL AND NEW.published_by IS NOT NULL THEN
        RETURN NEW;
    ELSIF OLD.state = 'published' AND NEW.state = 'retired' AND NEW.retired_at IS NOT NULL AND NEW.retired_by IS NOT NULL THEN
        RETURN NEW;
    ELSIF OLD.state = 'retired' AND NEW.state = 'published' AND NEW.published_at IS NOT NULL AND NEW.published_by IS NOT NULL THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invalid catalog state transition from % to %', OLD.state, NEW.state;
END;
$$;
CREATE TRIGGER catalog_state_transition_guard
BEFORE UPDATE ON catalog_publications
FOR EACH ROW EXECUTE FUNCTION enforce_catalog_state_transition();

CREATE FUNCTION reject_catalog_publication_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'catalog publications are immutable and cannot be deleted';
END;
$$;
CREATE TRIGGER catalog_publication_delete_forbidden
BEFORE DELETE ON catalog_publications
FOR EACH ROW EXECUTE FUNCTION reject_catalog_publication_delete();

CREATE FUNCTION require_draft_catalog_price_mapping() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    target_version bigint;
    target_state text;
BEGIN
    target_version := CASE WHEN TG_OP = 'DELETE' THEN OLD.catalog_version ELSE NEW.catalog_version END;
    SELECT state INTO target_state FROM catalog_publications WHERE version=target_version;
    IF target_state IS DISTINCT FROM 'draft' THEN
        RAISE EXCEPTION 'provider price mappings may change only while the catalog is draft';
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;
CREATE TRIGGER catalog_price_mapping_draft_only
BEFORE INSERT OR UPDATE OR DELETE ON offer_provider_prices
FOR EACH ROW EXECUTE FUNCTION require_draft_catalog_price_mapping();

COMMIT;
