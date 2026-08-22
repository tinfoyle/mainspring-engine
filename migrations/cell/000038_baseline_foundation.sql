BEGIN;

CREATE TABLE spyglass.baseline_assessments (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    catalog_version text NOT NULL CHECK (char_length(catalog_version) BETWEEN 1 AND 200 AND catalog_version=btrim(catalog_version)),
    scope_policy_version text NOT NULL CHECK (char_length(scope_policy_version) BETWEEN 1 AND 200 AND scope_policy_version=btrim(scope_policy_version)),
    state text NOT NULL CHECK (state IN ('interview','inventory','gap_review','plan_approval','active','ready','archived')),
    created_by_user_id uuid NOT NULL,
    superseded_by_assessment_id uuid,
    version bigint NOT NULL CHECK (version>0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,superseded_by_assessment_id) REFERENCES spyglass.baseline_assessments(account_id,id) DEFERRABLE INITIALLY DEFERRED,
    CHECK ((state='archived')=(superseded_by_assessment_id IS NOT NULL)),
    CHECK (superseded_by_assessment_id IS NULL OR superseded_by_assessment_id<>id)
);

CREATE TABLE spyglass.baseline_interview_answers (
    account_id uuid NOT NULL,
    assessment_id uuid NOT NULL,
    question_key text NOT NULL CHECK (question_key ~ '^[a-z][a-z0-9._:/-]{0,127}$'),
    answer_kind text NOT NULL CHECK (answer_kind IN ('fact','unknown')),
    fact_id uuid,
    fact_revision bigint,
    reason text NOT NULL DEFAULT '' CHECK (char_length(reason)<=1000),
    answered_by_user_id uuid NOT NULL,
    answered_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,assessment_id,question_key),
    FOREIGN KEY (account_id,assessment_id) REFERENCES spyglass.baseline_assessments(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,fact_id,fact_revision) REFERENCES spyglass.knowledge_fact_revisions(account_id,fact_id,revision) ON DELETE CASCADE,
    CHECK ((answer_kind='fact' AND fact_id IS NOT NULL AND fact_revision>0 AND reason='') OR
           (answer_kind='unknown' AND fact_id IS NULL AND fact_revision IS NULL AND char_length(btrim(reason)) BETWEEN 3 AND 1000))
);

CREATE TABLE spyglass.baseline_requirements (
    account_id uuid NOT NULL,
    assessment_id uuid NOT NULL,
    id uuid NOT NULL,
    requirement_code text NOT NULL CHECK (requirement_code ~ '^[a-z][a-z0-9._:/-]{0,127}$'),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 300 AND title=btrim(title)),
    responsibility_kind text NOT NULL CHECK (responsibility_kind IN ('account','user','persona')),
    responsible_user_id uuid,
    responsible_persona_id uuid,
    renew_after_days integer NOT NULL CHECK (renew_after_days BETWEEN 0 AND 3650),
    catalog_version text NOT NULL CHECK (char_length(catalog_version) BETWEEN 1 AND 200 AND catalog_version=btrim(catalog_version)),
    scope_policy_version text NOT NULL CHECK (char_length(scope_policy_version) BETWEEN 1 AND 200 AND scope_policy_version=btrim(scope_policy_version)),
    disposition text NOT NULL CHECK (disposition IN ('pending','satisfied','gap','not_applicable')),
    reason text NOT NULL DEFAULT '' CHECK (char_length(reason)<=1000),
    renew_at timestamptz,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,assessment_id,id),
    UNIQUE (account_id,assessment_id,requirement_code),
    FOREIGN KEY (account_id,assessment_id) REFERENCES spyglass.baseline_assessments(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,responsible_persona_id) REFERENCES spyglass.agent_personas(account_id,id) ON DELETE CASCADE,
    CHECK ((responsibility_kind='account' AND responsible_user_id IS NULL AND responsible_persona_id IS NULL) OR
           (responsibility_kind='user' AND responsible_user_id IS NOT NULL AND responsible_persona_id IS NULL) OR
           (responsibility_kind='persona' AND responsible_user_id IS NULL AND responsible_persona_id IS NOT NULL)),
    CHECK ((disposition='pending' AND reason='' AND renew_at IS NULL) OR
           (disposition='satisfied' AND reason='' AND ((renew_after_days=0 AND renew_at IS NULL) OR (renew_after_days>0 AND renew_at IS NOT NULL))) OR
           (disposition IN ('gap','not_applicable') AND char_length(btrim(reason)) BETWEEN 3 AND 1000 AND renew_at IS NULL))
);

CREATE TABLE spyglass.baseline_evidence_decisions (
    account_id uuid NOT NULL,
    assessment_id uuid NOT NULL,
    requirement_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    decision text NOT NULL CHECK (decision IN ('accepted','rejected')),
    reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 3 AND 1000),
    decided_by_user_id uuid NOT NULL,
    decided_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,requirement_id,evidence_id),
    FOREIGN KEY (account_id,assessment_id,requirement_id) REFERENCES spyglass.baseline_requirements(account_id,assessment_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,evidence_id) REFERENCES spyglass.knowledge_evidence(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.baseline_plans (
    account_id uuid NOT NULL,
    assessment_id uuid NOT NULL,
    id uuid NOT NULL,
    assessment_version bigint NOT NULL CHECK (assessment_version>0),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32),
    proposed_work_count integer NOT NULL CHECK (proposed_work_count BETWEEN 0 AND 256),
    approved_by_user_id uuid,
    approved_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,assessment_id),
    FOREIGN KEY (account_id,assessment_id) REFERENCES spyglass.baseline_assessments(account_id,id) ON DELETE CASCADE,
    CHECK ((approved_by_user_id IS NULL)=(approved_at IS NULL)),
    CHECK (approved_at IS NULL OR approved_at>=created_at)
);

CREATE TABLE spyglass.baseline_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    assessment_id uuid NOT NULL,
    requirement_id uuid,
    plan_id uuid,
    event_type text NOT NULL CHECK (event_type IN ('assessment_started','interview_answered','inventory_started','inventory_completed','evidence_decided','requirement_dispositioned','plan_submitted','plan_approved','assessment_ready','assessment_archived')),
    from_version bigint NOT NULL CHECK (from_version>=0),
    to_version bigint NOT NULL CHECK (to_version=from_version+1),
    actor_user_id uuid NOT NULL,
    reason_code text NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    correlation_id uuid NOT NULL,
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=8192),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,assessment_id) REFERENCES spyglass.baseline_assessments(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,requirement_id) REFERENCES spyglass.baseline_requirements(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,plan_id) REFERENCES spyglass.baseline_plans(account_id,id) ON DELETE CASCADE
);

CREATE INDEX baseline_assessments_queue ON spyglass.baseline_assessments(account_id,state,updated_at,id);
CREATE UNIQUE INDEX baseline_one_current_assessment ON spyglass.baseline_assessments(account_id) WHERE state<>'archived';
CREATE INDEX baseline_requirements_review ON spyglass.baseline_requirements(account_id,assessment_id,disposition,requirement_code,id);
CREATE INDEX baseline_requirements_renewal ON spyglass.baseline_requirements(account_id,renew_at,id) WHERE disposition='satisfied' AND renew_at IS NOT NULL;
CREATE INDEX baseline_events_assessment ON spyglass.baseline_events(account_id,assessment_id,occurred_at,id);

ALTER TABLE spyglass.baseline_assessments ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_assessments FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_interview_answers ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_interview_answers FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_requirements ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_requirements FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_evidence_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_evidence_decisions FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_plans FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_events FORCE ROW LEVEL SECURITY;

CREATE POLICY baseline_assessments_isolation ON spyglass.baseline_assessments USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY baseline_interview_answers_isolation ON spyglass.baseline_interview_answers USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY baseline_requirements_isolation ON spyglass.baseline_requirements USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY baseline_evidence_decisions_isolation ON spyglass.baseline_evidence_decisions USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY baseline_plans_isolation ON spyglass.baseline_plans USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY baseline_events_isolation ON spyglass.baseline_events USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.reject_baseline_history_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        IF pg_trigger_depth()>1 THEN RETURN OLD; END IF;
        IF current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE') THEN RETURN OLD; END IF;
    END IF;
    RAISE EXCEPTION 'Baseline history is immutable';
END;
$$;

CREATE FUNCTION spyglass.protect_baseline_assessment_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR
       NEW.catalog_version IS DISTINCT FROM OLD.catalog_version OR NEW.scope_policy_version IS DISTINCT FROM OLD.scope_policy_version OR
       NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Baseline assessment identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_baseline_interview_answer() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE assessment_state text;
BEGIN
    IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
    IF TG_OP='DELETE' AND current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE') THEN RETURN OLD; END IF;
    IF TG_OP='UPDATE' AND (NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.assessment_id IS DISTINCT FROM OLD.assessment_id OR NEW.question_key IS DISTINCT FROM OLD.question_key) THEN
        RAISE EXCEPTION 'Baseline interview answer identity is immutable';
    END IF;
    SELECT state INTO assessment_state FROM spyglass.baseline_assessments
      WHERE account_id=OLD.account_id AND id=OLD.assessment_id;
    IF assessment_state IS DISTINCT FROM 'interview' THEN
        RAISE EXCEPTION 'Baseline interview answers are frozen after inventory begins';
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;

CREATE FUNCTION spyglass.protect_baseline_requirement_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.assessment_id IS DISTINCT FROM OLD.assessment_id OR NEW.id IS DISTINCT FROM OLD.id OR
       NEW.requirement_code IS DISTINCT FROM OLD.requirement_code OR NEW.title IS DISTINCT FROM OLD.title OR
       NEW.responsibility_kind IS DISTINCT FROM OLD.responsibility_kind OR NEW.responsible_user_id IS DISTINCT FROM OLD.responsible_user_id OR
       NEW.responsible_persona_id IS DISTINCT FROM OLD.responsible_persona_id OR NEW.renew_after_days IS DISTINCT FROM OLD.renew_after_days OR
       NEW.catalog_version IS DISTINCT FROM OLD.catalog_version OR NEW.scope_policy_version IS DISTINCT FROM OLD.scope_policy_version THEN
        RAISE EXCEPTION 'Baseline requirement identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_baseline_plan_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.assessment_id IS DISTINCT FROM OLD.assessment_id OR NEW.id IS DISTINCT FROM OLD.id OR
       NEW.assessment_version IS DISTINCT FROM OLD.assessment_version OR NEW.content_sha256 IS DISTINCT FROM OLD.content_sha256 OR
       NEW.proposed_work_count IS DISTINCT FROM OLD.proposed_work_count OR NEW.created_at IS DISTINCT FROM OLD.created_at OR
       (OLD.approved_at IS NOT NULL AND (NEW.approved_at IS DISTINCT FROM OLD.approved_at OR NEW.approved_by_user_id IS DISTINCT FROM OLD.approved_by_user_id)) THEN
        RAISE EXCEPTION 'Baseline plan binding is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.capture_baseline_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        counts:=COALESCE(NULLIF(current_setting('spyglass.baseline_erasure_counts',true),'')::jsonb,'{}'::jsonb);
        current_count:=COALESCE((counts->>TG_TABLE_NAME)::bigint,0)+1;
        PERFORM set_config('spyglass.baseline_erasure_counts',(counts||jsonb_build_object(TG_TABLE_NAME,current_count))::text,true);
    END IF;
    RETURN OLD;
END;
$$;

CREATE FUNCTION spyglass.add_baseline_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.baseline_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER baseline_assessment_identity_guard BEFORE UPDATE ON spyglass.baseline_assessments FOR EACH ROW EXECUTE FUNCTION spyglass.protect_baseline_assessment_identity();
CREATE TRIGGER baseline_assessments_immutable_delete BEFORE DELETE ON spyglass.baseline_assessments FOR EACH ROW EXECUTE FUNCTION spyglass.reject_baseline_history_change();
CREATE TRIGGER baseline_interview_answer_guard BEFORE UPDATE OR DELETE ON spyglass.baseline_interview_answers FOR EACH ROW EXECUTE FUNCTION spyglass.protect_baseline_interview_answer();
CREATE TRIGGER baseline_requirement_identity_guard BEFORE UPDATE ON spyglass.baseline_requirements FOR EACH ROW EXECUTE FUNCTION spyglass.protect_baseline_requirement_identity();
CREATE TRIGGER baseline_requirements_immutable_delete BEFORE DELETE ON spyglass.baseline_requirements FOR EACH ROW EXECUTE FUNCTION spyglass.reject_baseline_history_change();
CREATE TRIGGER baseline_plan_identity_guard BEFORE UPDATE ON spyglass.baseline_plans FOR EACH ROW EXECUTE FUNCTION spyglass.protect_baseline_plan_identity();
CREATE TRIGGER baseline_evidence_decisions_immutable BEFORE UPDATE OR DELETE ON spyglass.baseline_evidence_decisions FOR EACH ROW EXECUTE FUNCTION spyglass.reject_baseline_history_change();
CREATE TRIGGER baseline_events_immutable BEFORE UPDATE OR DELETE ON spyglass.baseline_events FOR EACH ROW EXECUTE FUNCTION spyglass.reject_baseline_history_change();
CREATE TRIGGER baseline_plans_immutable_delete BEFORE DELETE ON spyglass.baseline_plans FOR EACH ROW EXECUTE FUNCTION spyglass.reject_baseline_history_change();

CREATE TRIGGER baseline_assessments_erasure_count BEFORE DELETE ON spyglass.baseline_assessments FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER baseline_interview_answers_erasure_count BEFORE DELETE ON spyglass.baseline_interview_answers FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER baseline_requirements_erasure_count BEFORE DELETE ON spyglass.baseline_requirements FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER baseline_evidence_decisions_erasure_count BEFORE DELETE ON spyglass.baseline_evidence_decisions FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER baseline_plans_erasure_count BEFORE DELETE ON spyglass.baseline_plans FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER baseline_events_erasure_count BEFORE DELETE ON spyglass.baseline_events FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER account_erasure_baseline_counts BEFORE INSERT ON spyglass.account_erasure_tombstones FOR EACH ROW EXECUTE FUNCTION spyglass.add_baseline_erasure_counts();

CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_assessments FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_interview_answers FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_requirements FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_evidence_decisions FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_plans FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_events FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.reject_baseline_history_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_baseline_assessment_identity() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_baseline_interview_answer() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_baseline_requirement_identity() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_baseline_plan_identity() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.capture_baseline_erasure_count() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.add_baseline_erasure_counts() FROM PUBLIC;

COMMIT;
