CREATE TABLE business_knowledge_fact_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fact_key TEXT NOT NULL,
    label TEXT NOT NULL,
    value TEXT NOT NULL,
    scope TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_ref TEXT NOT NULL,
    confidence NUMERIC(4,3) NOT NULL,
    sensitivity TEXT NOT NULL,
    status TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX business_knowledge_fact_history_key_idx
    ON business_knowledge_fact_history(fact_key, recorded_at DESC);

CREATE OR REPLACE FUNCTION record_business_knowledge_fact_version()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT'
       OR OLD.value IS DISTINCT FROM NEW.value
       OR OLD.scope IS DISTINCT FROM NEW.scope
       OR OLD.status IS DISTINCT FROM NEW.status
       OR OLD.source_type IS DISTINCT FROM NEW.source_type
       OR OLD.source_ref IS DISTINCT FROM NEW.source_ref THEN
        INSERT INTO business_knowledge_fact_history(
            fact_key,label,value,scope,source_type,source_ref,confidence,sensitivity,status,recorded_at
        ) VALUES (
            NEW.fact_key,NEW.label,NEW.value,NEW.scope,NEW.source_type,NEW.source_ref,
            NEW.confidence,NEW.sensitivity,NEW.status,now()
        );
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER business_knowledge_fact_version_trigger
AFTER INSERT OR UPDATE ON business_knowledge_facts
FOR EACH ROW EXECUTE FUNCTION record_business_knowledge_fact_version();

-- Establish an initial version for facts imported before this migration.
INSERT INTO business_knowledge_fact_history(
    fact_key,label,value,scope,source_type,source_ref,confidence,sensitivity,status,recorded_at
)
SELECT fact_key,label,value,scope,source_type,source_ref,confidence,sensitivity,status,updated_at
FROM business_knowledge_facts;
