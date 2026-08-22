BEGIN;

ALTER TABLE spyglass.baseline_assessments
    ADD COLUMN reassess_at timestamptz;

UPDATE spyglass.baseline_assessments
SET reassess_at=updated_at+interval '90 days'
WHERE state='ready';

ALTER TABLE spyglass.baseline_assessments
    ADD CONSTRAINT baseline_assessments_reassessment_shape CHECK (
        (state='ready' AND reassess_at IS NOT NULL AND reassess_at>=created_at) OR
        (state IN ('interview','inventory','gap_review','plan_approval','active') AND reassess_at IS NULL) OR
        (state='archived' AND (reassess_at IS NULL OR reassess_at>=created_at))
    );

CREATE INDEX baseline_assessments_reassessment
    ON spyglass.baseline_assessments(account_id,reassess_at,id)
    WHERE state='ready';

CREATE TABLE spyglass.baseline_plan_work (
    account_id uuid NOT NULL,
    plan_id uuid NOT NULL,
    sequence integer NOT NULL CHECK (sequence BETWEEN 0 AND 255),
    assessment_id uuid NOT NULL,
    requirement_id uuid NOT NULL,
    title text NOT NULL CHECK (char_length(btrim(title)) BETWEEN 1 AND 300),
    description text NOT NULL CHECK (char_length(btrim(description)) BETWEEN 3 AND 1000),
    responsibility_kind text NOT NULL CHECK (responsibility_kind IN ('account','user','persona')),
    responsible_user_id uuid,
    responsible_persona_id uuid,
    PRIMARY KEY (account_id,plan_id,sequence),
    UNIQUE (account_id,plan_id,requirement_id),
    FOREIGN KEY (account_id,plan_id) REFERENCES spyglass.baseline_plans(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,assessment_id,requirement_id) REFERENCES spyglass.baseline_requirements(account_id,assessment_id,id) ON DELETE CASCADE,
    CHECK ((responsibility_kind='account' AND responsible_user_id IS NULL AND responsible_persona_id IS NULL) OR
           (responsibility_kind='user' AND responsible_user_id IS NOT NULL AND responsible_persona_id IS NULL) OR
           (responsibility_kind='persona' AND responsible_user_id IS NULL AND responsible_persona_id IS NOT NULL))
);

INSERT INTO spyglass.baseline_plan_work(account_id,plan_id,sequence,assessment_id,requirement_id,title,description,responsibility_kind,responsible_user_id,responsible_persona_id)
SELECT plan.account_id,plan.id,row_number() OVER (PARTITION BY plan.account_id,plan.id ORDER BY requirement.id)-1,
       plan.assessment_id,requirement.id,requirement.title,requirement.reason,requirement.responsibility_kind,
       requirement.responsible_user_id,requirement.responsible_persona_id
FROM spyglass.baseline_plans plan
JOIN spyglass.baseline_requirements requirement
  ON requirement.account_id=plan.account_id AND requirement.assessment_id=plan.assessment_id
WHERE requirement.disposition='gap';

ALTER TABLE spyglass.baseline_plan_work ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_plan_work FORCE ROW LEVEL SECURITY;
CREATE POLICY baseline_plan_work_isolation ON spyglass.baseline_plan_work
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE TRIGGER baseline_plan_work_immutable BEFORE UPDATE OR DELETE ON spyglass.baseline_plan_work
    FOR EACH ROW EXECUTE FUNCTION spyglass.reject_baseline_history_change();
CREATE TRIGGER baseline_plan_work_erasure_count BEFORE DELETE ON spyglass.baseline_plan_work
    FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_plan_work
    FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

ALTER TABLE spyglass.baseline_events
    DROP CONSTRAINT baseline_events_event_type_check;
ALTER TABLE spyglass.baseline_events
    ADD CONSTRAINT baseline_events_event_type_check CHECK (event_type IN (
        'assessment_started','interview_answered','inventory_started','inventory_completed','evidence_decided',
        'requirement_dispositioned','plan_submitted','plan_approved','assessment_ready','assessment_archived',
        'work_evidence_confirmed'
    ));

CREATE OR REPLACE FUNCTION spyglass.protect_baseline_assessment_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR
       NEW.catalog_version IS DISTINCT FROM OLD.catalog_version OR NEW.scope_policy_version IS DISTINCT FROM OLD.scope_policy_version OR
       NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at OR
       (NEW.reassess_at IS DISTINCT FROM OLD.reassess_at AND NOT (
           OLD.state='active' AND NEW.state='ready' AND OLD.reassess_at IS NULL AND NEW.reassess_at IS NOT NULL
       )) THEN
        RAISE EXCEPTION 'Baseline assessment identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;

COMMIT;
