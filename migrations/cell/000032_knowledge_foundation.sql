BEGIN;

CREATE TABLE spyglass.knowledge_evidence (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    source_kind text NOT NULL CHECK (source_kind IN ('owner_statement','document_revision','integration_record','public_web_capture','agent_derivation')),
    source_reference text NOT NULL CHECK (char_length(source_reference) BETWEEN 1 AND 2048 AND source_reference=btrim(source_reference)),
    source_revision text NOT NULL CHECK (char_length(source_revision) BETWEEN 1 AND 256 AND source_revision=btrim(source_revision)),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32),
    captured_at timestamptz NOT NULL,
    created_by_kind text NOT NULL CHECK (created_by_kind IN ('user','workload')),
    created_by_id text NOT NULL CHECK (char_length(created_by_id) BETWEEN 1 AND 256 AND created_by_id=btrim(created_by_id)),
    created_at timestamptz NOT NULL CHECK (created_at>=captured_at),
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,id,source_kind),
    UNIQUE (account_id,source_kind,source_reference,source_revision,content_sha256),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE
);

CREATE TABLE spyglass.knowledge_claims (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    scope_kind text NOT NULL CHECK (scope_kind IN ('account','work_item','conversation')),
    scope_work_item_id uuid,
    scope_conversation_id uuid,
    fact_key text NOT NULL CHECK (fact_key ~ '^[a-z][a-z0-9._:/-]{0,127}$'),
    canonical_value bytea NOT NULL CHECK (octet_length(canonical_value) BETWEEN 1 AND 65536),
    value_sha256 bytea NOT NULL CHECK (octet_length(value_sha256)=32),
    hash_version smallint NOT NULL CHECK (hash_version=1),
    confidence smallint NOT NULL CHECK (confidence BETWEEN 0 AND 1000),
    sensitivity text NOT NULL CHECK (sensitivity IN ('public','internal','confidential','restricted')),
    proposed_by_kind text NOT NULL CHECK (proposed_by_kind IN ('user','workload')),
    proposed_by_id text NOT NULL CHECK (char_length(proposed_by_id) BETWEEN 1 AND 256 AND proposed_by_id=btrim(proposed_by_id)),
    state text NOT NULL CHECK (state IN ('proposed','accepted','rejected','superseded','stale')),
    decision_reason text NOT NULL DEFAULT '' CHECK (char_length(decision_reason)<=1000),
    decided_by_user_id uuid,
    decided_at timestamptz,
    version bigint NOT NULL CHECK (version>0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,id,fact_key),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,scope_work_item_id) REFERENCES spyglass.work_items(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,scope_conversation_id) REFERENCES spyglass.agent_conversations(account_id,id) ON DELETE CASCADE,
    CHECK ((scope_kind='account' AND scope_work_item_id IS NULL AND scope_conversation_id IS NULL) OR
           (scope_kind='work_item' AND scope_work_item_id IS NOT NULL AND scope_conversation_id IS NULL) OR
           (scope_kind='conversation' AND scope_work_item_id IS NULL AND scope_conversation_id IS NOT NULL)),
    CHECK ((state='proposed' AND decision_reason='' AND decided_by_user_id IS NULL AND decided_at IS NULL) OR
           (state IN ('accepted','rejected','superseded','stale') AND char_length(btrim(decision_reason)) BETWEEN 3 AND 1000 AND decided_by_user_id IS NOT NULL AND decided_at>=created_at AND updated_at>=decided_at))
);

CREATE TABLE spyglass.knowledge_claim_citations (
    account_id uuid NOT NULL,
    claim_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    evidence_kind text NOT NULL CHECK (evidence_kind IN ('owner_statement','document_revision','integration_record','public_web_capture','agent_derivation')),
    relation text NOT NULL CHECK (relation IN ('supports','refutes')),
    locator text NOT NULL CHECK (char_length(locator) BETWEEN 1 AND 512 AND locator=btrim(locator)),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,claim_id,evidence_id),
    FOREIGN KEY (account_id,claim_id) REFERENCES spyglass.knowledge_claims(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,evidence_id,evidence_kind) REFERENCES spyglass.knowledge_evidence(account_id,id,source_kind) ON DELETE CASCADE
);

CREATE TABLE spyglass.knowledge_facts (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    scope_kind text NOT NULL CHECK (scope_kind IN ('account','work_item','conversation')),
    scope_work_item_id uuid,
    scope_conversation_id uuid,
    fact_key text NOT NULL CHECK (fact_key ~ '^[a-z][a-z0-9._:/-]{0,127}$'),
    current_claim_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('active','stale')),
    revision bigint NOT NULL CHECK (revision>0),
    accepted_by_user_id uuid NOT NULL,
    accepted_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at AND updated_at>=accepted_at),
    PRIMARY KEY (account_id,id),
    UNIQUE NULLS NOT DISTINCT (account_id,scope_kind,scope_work_item_id,scope_conversation_id,fact_key),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,current_claim_id,fact_key) REFERENCES spyglass.knowledge_claims(account_id,id,fact_key) ON DELETE CASCADE,
    FOREIGN KEY (account_id,scope_work_item_id) REFERENCES spyglass.work_items(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,scope_conversation_id) REFERENCES spyglass.agent_conversations(account_id,id) ON DELETE CASCADE,
    CHECK ((scope_kind='account' AND scope_work_item_id IS NULL AND scope_conversation_id IS NULL) OR
           (scope_kind='work_item' AND scope_work_item_id IS NOT NULL AND scope_conversation_id IS NULL) OR
           (scope_kind='conversation' AND scope_work_item_id IS NULL AND scope_conversation_id IS NOT NULL))
);

CREATE TABLE spyglass.knowledge_fact_revisions (
    account_id uuid NOT NULL,
    fact_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    claim_id uuid NOT NULL,
    accepted_by_user_id uuid NOT NULL,
    accepted_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,fact_id,revision),
    UNIQUE (account_id,fact_id,claim_id),
    FOREIGN KEY (account_id,fact_id) REFERENCES spyglass.knowledge_facts(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,claim_id) REFERENCES spyglass.knowledge_claims(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.knowledge_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    aggregate_kind text NOT NULL CHECK (aggregate_kind IN ('evidence','claim','fact')),
    evidence_id uuid,
    claim_id uuid,
    fact_id uuid,
    event_type text NOT NULL CHECK (event_type IN ('evidence_registered','claim_proposed','claim_accepted','claim_rejected','claim_superseded','claim_stale','fact_created','fact_revised','fact_stale')),
    from_version bigint NOT NULL CHECK (from_version>=0),
    to_version bigint NOT NULL CHECK (to_version=from_version+1),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 256 AND actor_id=btrim(actor_id)),
    reason_code text NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    correlation_id uuid NOT NULL,
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=8192),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,evidence_id) REFERENCES spyglass.knowledge_evidence(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,claim_id) REFERENCES spyglass.knowledge_claims(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,fact_id) REFERENCES spyglass.knowledge_facts(account_id,id) ON DELETE CASCADE,
    CHECK ((aggregate_kind='evidence' AND evidence_id IS NOT NULL AND claim_id IS NULL AND fact_id IS NULL AND event_type='evidence_registered' AND from_version=0 AND to_version=1) OR
           (aggregate_kind='claim' AND evidence_id IS NULL AND claim_id IS NOT NULL AND fact_id IS NULL AND event_type IN ('claim_proposed','claim_accepted','claim_rejected','claim_superseded','claim_stale')) OR
           (aggregate_kind='fact' AND evidence_id IS NULL AND claim_id IS NULL AND fact_id IS NOT NULL AND event_type IN ('fact_created','fact_revised','fact_stale')))
);

CREATE INDEX knowledge_claims_review ON spyglass.knowledge_claims(account_id,state,updated_at,id);
CREATE INDEX knowledge_claims_key ON spyglass.knowledge_claims(account_id,scope_kind,scope_work_item_id,scope_conversation_id,fact_key,created_at,id);
CREATE INDEX knowledge_facts_list ON spyglass.knowledge_facts(account_id,state,updated_at,id);
CREATE INDEX knowledge_evidence_source ON spyglass.knowledge_evidence(account_id,source_kind,captured_at,id);
CREATE INDEX knowledge_events_aggregate ON spyglass.knowledge_events(account_id,aggregate_kind,evidence_id,claim_id,fact_id,occurred_at,id);

ALTER TABLE spyglass.knowledge_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_evidence FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_claims ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_claims FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_claim_citations ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_claim_citations FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_facts ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_facts FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_fact_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_fact_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_events FORCE ROW LEVEL SECURITY;
CREATE POLICY knowledge_evidence_isolation ON spyglass.knowledge_evidence USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY knowledge_claims_isolation ON spyglass.knowledge_claims USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY knowledge_claim_citations_isolation ON spyglass.knowledge_claim_citations USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY knowledge_facts_isolation ON spyglass.knowledge_facts USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY knowledge_fact_revisions_isolation ON spyglass.knowledge_fact_revisions USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY knowledge_events_isolation ON spyglass.knowledge_events USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.validate_knowledge_claim_acceptance() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state='accepted' AND (TG_OP='INSERT' OR OLD.state IS DISTINCT FROM 'accepted') AND NOT EXISTS (
        SELECT 1 FROM spyglass.knowledge_claim_citations citation
        WHERE citation.account_id=NEW.account_id AND citation.claim_id=NEW.id
          AND citation.relation='supports' AND citation.evidence_kind<>'agent_derivation'
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='accepted Knowledge claim requires independent supporting evidence';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.validate_knowledge_fact_claim() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM spyglass.knowledge_claims claim
        WHERE claim.account_id=NEW.account_id AND claim.id=NEW.current_claim_id AND claim.state='accepted'
          AND claim.fact_key=NEW.fact_key AND claim.scope_kind=NEW.scope_kind
          AND claim.scope_work_item_id IS NOT DISTINCT FROM NEW.scope_work_item_id
          AND claim.scope_conversation_id IS NOT DISTINCT FROM NEW.scope_conversation_id
          AND claim.decided_by_user_id=NEW.accepted_by_user_id AND claim.decided_at=NEW.accepted_at
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='Knowledge fact must project the exact accepted claim';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.reject_knowledge_history_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        IF pg_trigger_depth()>1 THEN RETURN OLD; END IF;
        IF current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE') THEN RETURN OLD; END IF;
    END IF;
    RAISE EXCEPTION 'Knowledge history is immutable';
END;
$$;

CREATE FUNCTION spyglass.protect_knowledge_claim_content() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR
       NEW.scope_kind IS DISTINCT FROM OLD.scope_kind OR NEW.scope_work_item_id IS DISTINCT FROM OLD.scope_work_item_id OR
       NEW.scope_conversation_id IS DISTINCT FROM OLD.scope_conversation_id OR NEW.fact_key IS DISTINCT FROM OLD.fact_key OR
       NEW.canonical_value IS DISTINCT FROM OLD.canonical_value OR NEW.value_sha256 IS DISTINCT FROM OLD.value_sha256 OR
       NEW.hash_version IS DISTINCT FROM OLD.hash_version OR NEW.confidence IS DISTINCT FROM OLD.confidence OR
       NEW.sensitivity IS DISTINCT FROM OLD.sensitivity OR NEW.proposed_by_kind IS DISTINCT FROM OLD.proposed_by_kind OR
       NEW.proposed_by_id IS DISTINCT FROM OLD.proposed_by_id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Knowledge claim proposition is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.capture_knowledge_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        counts:=COALESCE(NULLIF(current_setting('spyglass.knowledge_erasure_counts',true),'')::jsonb,'{}'::jsonb);
        current_count:=COALESCE((counts->>TG_TABLE_NAME)::bigint,0)+1;
        PERFORM set_config('spyglass.knowledge_erasure_counts',(counts||jsonb_build_object(TG_TABLE_NAME,current_count))::text,true);
    END IF;
    RETURN OLD;
END;
$$;

CREATE FUNCTION spyglass.add_knowledge_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.knowledge_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER knowledge_fact_revisions_immutable BEFORE UPDATE OR DELETE ON spyglass.knowledge_fact_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_events_immutable BEFORE UPDATE OR DELETE ON spyglass.knowledge_events FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_evidence_immutable BEFORE UPDATE OR DELETE ON spyglass.knowledge_evidence FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_claim_citations_immutable BEFORE UPDATE OR DELETE ON spyglass.knowledge_claim_citations FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_claims_immutable_delete BEFORE DELETE ON spyglass.knowledge_claims FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_facts_immutable_delete BEFORE DELETE ON spyglass.knowledge_facts FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_claim_content_guard BEFORE UPDATE ON spyglass.knowledge_claims FOR EACH ROW EXECUTE FUNCTION spyglass.protect_knowledge_claim_content();
CREATE TRIGGER knowledge_claim_acceptance_guard BEFORE INSERT OR UPDATE OF state ON spyglass.knowledge_claims FOR EACH ROW EXECUTE FUNCTION spyglass.validate_knowledge_claim_acceptance();
CREATE TRIGGER knowledge_fact_claim_guard BEFORE INSERT OR UPDATE OF current_claim_id,scope_kind,scope_work_item_id,scope_conversation_id,fact_key,accepted_by_user_id,accepted_at ON spyglass.knowledge_facts FOR EACH ROW EXECUTE FUNCTION spyglass.validate_knowledge_fact_claim();

CREATE TRIGGER knowledge_evidence_erasure_count BEFORE DELETE ON spyglass.knowledge_evidence FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER knowledge_claims_erasure_count BEFORE DELETE ON spyglass.knowledge_claims FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER knowledge_claim_citations_erasure_count BEFORE DELETE ON spyglass.knowledge_claim_citations FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER knowledge_facts_erasure_count BEFORE DELETE ON spyglass.knowledge_facts FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER knowledge_fact_revisions_erasure_count BEFORE DELETE ON spyglass.knowledge_fact_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER knowledge_events_erasure_count BEFORE DELETE ON spyglass.knowledge_events FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER account_erasure_knowledge_counts BEFORE INSERT ON spyglass.account_erasure_tombstones FOR EACH ROW EXECUTE FUNCTION spyglass.add_knowledge_erasure_counts();

CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_evidence FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_claims FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_claim_citations FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_facts FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_fact_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_events FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.reject_knowledge_history_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_knowledge_claim_content() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_knowledge_claim_acceptance() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_knowledge_fact_claim() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.capture_knowledge_erasure_count() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.add_knowledge_erasure_counts() FROM PUBLIC;

COMMIT;
