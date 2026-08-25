BEGIN;

CREATE TABLE affiliate_enrollment_events (
    event_id uuid PRIMARY KEY,
    affiliate_id uuid NOT NULL REFERENCES affiliate_enrollments (affiliate_id),
    version bigint NOT NULL CHECK (version > 0),
    action text NOT NULL CHECK (action IN ('enrolled','inspected','activated','suspended','closed')),
    state text NOT NULL CHECK (state IN ('active','suspended','closed')),
    actor text NOT NULL CHECK (length(actor) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (length(reason) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    occurred_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX affiliate_enrollment_events_transition_version
    ON affiliate_enrollment_events (affiliate_id,version)
    WHERE action <> 'inspected';
CREATE INDEX affiliate_enrollment_events_history
    ON affiliate_enrollment_events (affiliate_id,occurred_at,event_id);

INSERT INTO affiliate_enrollment_events (event_id,affiliate_id,version,action,state,actor,reason,environment,occurred_at)
SELECT gen_random_uuid(),affiliate_id,version,
       CASE state WHEN 'active' THEN 'enrolled' WHEN 'suspended' THEN 'suspended' ELSE 'closed' END,
       state,'system:migration','Enrollment state imported into the lifecycle ledger','migration',updated_at
FROM affiliate_enrollments;

CREATE FUNCTION spyglass_record_affiliate_enrollment() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    INSERT INTO public.affiliate_enrollment_events
        (event_id,affiliate_id,version,action,state,actor,reason,environment,occurred_at)
    VALUES (gen_random_uuid(),NEW.affiliate_id,NEW.version,'enrolled',NEW.state,
            'user:' || NEW.user_id::text,'Accepted the current Affiliate terms','application',NEW.created_at);
    RETURN NEW;
END;
$$;
CREATE TRIGGER affiliate_enrollments_record_initial_state
AFTER INSERT ON affiliate_enrollments
FOR EACH ROW EXECUTE FUNCTION spyglass_record_affiliate_enrollment();

CREATE TRIGGER affiliate_enrollment_events_immutable
BEFORE UPDATE OR DELETE ON affiliate_enrollment_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();

CREATE FUNCTION spyglass_inspect_affiliate_enrollment(
    p_event_id uuid,
    p_affiliate_id uuid,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(
    affiliate_id uuid,user_id uuid,settlement_account_id text,public_code text,
    terms_version bigint,rule_version bigint,state text,version bigint,created_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    current_row public.affiliate_enrollments%ROWTYPE;
BEGIN
    IF p_event_id IS NULL OR p_affiliate_id IS NULL OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION 'invalid Affiliate inspection' USING ERRCODE='22023';
    END IF;
    SELECT * INTO current_row FROM public.affiliate_enrollments e WHERE e.affiliate_id=p_affiliate_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Affiliate enrollment not found' USING ERRCODE='P0002';
    END IF;
    INSERT INTO public.affiliate_enrollment_events
        (event_id,affiliate_id,version,action,state,actor,reason,environment,occurred_at)
    VALUES (p_event_id,current_row.affiliate_id,current_row.version,'inspected',current_row.state,
            btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    RETURN QUERY SELECT current_row.affiliate_id,current_row.user_id,current_row.settlement_account_id::text,
        current_row.public_code,current_row.terms_version,current_row.rule_version,current_row.state,
        current_row.version,current_row.created_at;
END;
$$;

CREATE FUNCTION spyglass_transition_affiliate_enrollment(
    p_event_id uuid,
    p_affiliate_id uuid,
    p_expected_version bigint,
    p_state text,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(
    affiliate_id uuid,user_id uuid,settlement_account_id text,public_code text,
    terms_version bigint,rule_version bigint,state text,version bigint,created_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    current_row public.affiliate_enrollments%ROWTYPE;
    action_name text;
BEGIN
    IF p_event_id IS NULL OR p_affiliate_id IS NULL OR p_expected_version < 1 OR
       p_state NOT IN ('active','suspended','closed') OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION 'invalid Affiliate transition' USING ERRCODE='22023';
    END IF;
    SELECT * INTO current_row FROM public.affiliate_enrollments e WHERE e.affiliate_id=p_affiliate_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Affiliate enrollment not found' USING ERRCODE='P0002';
    END IF;
    IF current_row.version <> p_expected_version OR current_row.state=p_state OR current_row.state='closed' OR
       (current_row.state='active' AND p_state NOT IN ('suspended','closed')) OR
       (current_row.state='suspended' AND p_state NOT IN ('active','closed')) THEN
        RAISE EXCEPTION 'Affiliate enrollment state changed' USING ERRCODE='P0001';
    END IF;
    action_name := CASE p_state WHEN 'active' THEN 'activated' WHEN 'suspended' THEN 'suspended' ELSE 'closed' END;
    UPDATE public.affiliate_enrollments e
       SET state=p_state,version=e.version+1,updated_at=statement_timestamp()
     WHERE e.affiliate_id=p_affiliate_id
     RETURNING e.* INTO current_row;
    INSERT INTO public.affiliate_enrollment_events
        (event_id,affiliate_id,version,action,state,actor,reason,environment,occurred_at)
    VALUES (p_event_id,current_row.affiliate_id,current_row.version,action_name,current_row.state,
            btrim(p_actor),btrim(p_reason),p_environment,current_row.updated_at);
    RETURN QUERY SELECT current_row.affiliate_id,current_row.user_id,current_row.settlement_account_id::text,
        current_row.public_code,current_row.terms_version,current_row.rule_version,current_row.state,
        current_row.version,current_row.created_at;
END;
$$;

REVOKE ALL ON TABLE affiliate_enrollment_events FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_record_affiliate_enrollment() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_inspect_affiliate_enrollment(uuid,uuid,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_transition_affiliate_enrollment(uuid,uuid,bigint,text,text,text,text) FROM PUBLIC;

COMMIT;
