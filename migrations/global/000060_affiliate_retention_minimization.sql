BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE affiliate_retention_controls (
    affiliate_id uuid PRIMARY KEY REFERENCES affiliate_enrollments (affiliate_id),
    legal_hold boolean NOT NULL DEFAULT false,
    restricted_at timestamptz,
    version bigint NOT NULL CHECK (version > 0),
    updated_at timestamptz NOT NULL,
    CONSTRAINT affiliate_retention_controls_time_order CHECK (restricted_at IS NULL OR updated_at>=restricted_at)
);

CREATE TABLE affiliate_retention_control_events (
    event_id uuid PRIMARY KEY,
    affiliate_id uuid NOT NULL REFERENCES affiliate_retention_controls (affiliate_id),
    version bigint NOT NULL CHECK (version > 0),
    action text NOT NULL CHECK (action IN ('hold_set','hold_released','restricted')),
    legal_hold boolean NOT NULL,
    restricted_at timestamptz,
    actor text NOT NULL CHECK (length(btrim(actor)) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    occurred_at timestamptz NOT NULL,
    UNIQUE (affiliate_id,version),
    CONSTRAINT affiliate_retention_control_events_action_shape CHECK (
        (action='hold_set' AND legal_hold) OR
        (action='hold_released' AND NOT legal_hold) OR
        (action='restricted' AND restricted_at IS NOT NULL)
    )
);

INSERT INTO affiliate_retention_controls (affiliate_id,legal_hold,restricted_at,version,updated_at)
SELECT affiliate_id,false,NULL,1,updated_at FROM affiliate_enrollments;

CREATE FUNCTION spyglass_record_affiliate_retention_control() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    INSERT INTO public.affiliate_retention_controls (affiliate_id,legal_hold,restricted_at,version,updated_at)
    VALUES (NEW.affiliate_id,false,NULL,1,NEW.created_at);
    RETURN NEW;
END;
$$;

CREATE TRIGGER affiliate_enrollments_record_retention_control
AFTER INSERT ON affiliate_enrollments
FOR EACH ROW EXECUTE FUNCTION spyglass_record_affiliate_retention_control();

CREATE FUNCTION spyglass_set_affiliate_retention_hold(
    p_event_id uuid,
    p_affiliate_id uuid,
    p_expected_version bigint,
    p_legal_hold boolean,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(affiliate_id uuid,legal_hold boolean,restricted_at timestamptz,version bigint,updated_at timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    current_row public.affiliate_retention_controls%ROWTYPE;
BEGIN
    IF p_event_id IS NULL OR p_affiliate_id IS NULL OR p_expected_version<1 OR p_legal_hold IS NULL OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION 'invalid Affiliate retention hold' USING ERRCODE='22023';
    END IF;
    SELECT * INTO current_row FROM public.affiliate_retention_controls control
     WHERE control.affiliate_id=p_affiliate_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Affiliate retention control not found' USING ERRCODE='P0002';
    END IF;
    IF current_row.version<>p_expected_version OR current_row.legal_hold=p_legal_hold THEN
        RAISE EXCEPTION 'Affiliate retention control changed' USING ERRCODE='P0001';
    END IF;
    UPDATE public.affiliate_retention_controls control
       SET legal_hold=p_legal_hold,version=control.version+1,updated_at=statement_timestamp()
     WHERE control.affiliate_id=p_affiliate_id RETURNING * INTO current_row;
    INSERT INTO public.affiliate_retention_control_events
        (event_id,affiliate_id,version,action,legal_hold,restricted_at,actor,reason,environment,occurred_at)
    VALUES (p_event_id,p_affiliate_id,current_row.version,
            CASE WHEN p_legal_hold THEN 'hold_set' ELSE 'hold_released' END,
            p_legal_hold,current_row.restricted_at,btrim(p_actor),btrim(p_reason),
            p_environment,current_row.updated_at);
    RETURN QUERY SELECT current_row.affiliate_id,current_row.legal_hold,current_row.restricted_at,
                        current_row.version,current_row.updated_at;
END;
$$;

CREATE FUNCTION spyglass_restrict_affiliate_retention(
    p_event_id uuid,
    p_affiliate_id uuid,
    p_expected_version bigint,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(affiliate_id uuid,legal_hold boolean,restricted_at timestamptz,version bigint,updated_at timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    current_row public.affiliate_retention_controls%ROWTYPE;
    enrollment_state text;
BEGIN
    IF p_event_id IS NULL OR p_affiliate_id IS NULL OR p_expected_version<1 OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION 'invalid Affiliate retention restriction' USING ERRCODE='22023';
    END IF;
    SELECT enrollment.state INTO enrollment_state
      FROM public.affiliate_retention_controls control
      JOIN public.affiliate_enrollments enrollment ON enrollment.affiliate_id=control.affiliate_id
     WHERE control.affiliate_id=p_affiliate_id
     FOR UPDATE OF control,enrollment;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Affiliate retention control not found' USING ERRCODE='P0002';
    END IF;
    SELECT * INTO current_row FROM public.affiliate_retention_controls control
     WHERE control.affiliate_id=p_affiliate_id;
    IF current_row.version<>p_expected_version OR current_row.restricted_at IS NOT NULL OR enrollment_state<>'closed' THEN
        RAISE EXCEPTION 'Affiliate retention control changed' USING ERRCODE='P0001';
    END IF;
    UPDATE public.affiliate_retention_controls control
       SET restricted_at=statement_timestamp(),version=control.version+1,updated_at=statement_timestamp()
     WHERE control.affiliate_id=p_affiliate_id RETURNING * INTO current_row;
    INSERT INTO public.affiliate_retention_control_events
        (event_id,affiliate_id,version,action,legal_hold,restricted_at,actor,reason,environment,occurred_at)
    VALUES (p_event_id,p_affiliate_id,current_row.version,'restricted',current_row.legal_hold,
            current_row.restricted_at,btrim(p_actor),btrim(p_reason),p_environment,current_row.updated_at);
    RETURN QUERY SELECT current_row.affiliate_id,current_row.legal_hold,current_row.restricted_at,
                        current_row.version,current_row.updated_at;
END;
$$;

CREATE TABLE affiliate_minimization_execution_context (
    backend_pid integer NOT NULL,
    transaction_id bigint NOT NULL,
    PRIMARY KEY (backend_pid,transaction_id)
);

CREATE TABLE affiliate_minimization_tombstones (
    tombstone_id uuid PRIMARY KEY,
    policy_version bigint NOT NULL CHECK (policy_version=1),
    closed_at timestamptz NOT NULL,
    final_activity_at timestamptz NOT NULL,
    minimized_at timestamptz NOT NULL,
    attribution_count bigint NOT NULL CHECK (attribution_count>=0),
    commission_entry_count bigint NOT NULL CHECK (commission_entry_count>=0),
    credit_reservation_count bigint NOT NULL CHECK (credit_reservation_count>=0),
    support_request_count bigint NOT NULL CHECK (support_request_count>=0),
    code_count bigint NOT NULL CHECK (code_count>0),
    CONSTRAINT affiliate_minimization_tombstone_time_order CHECK (
        final_activity_at>=closed_at AND minimized_at>=final_activity_at
    )
);

CREATE TABLE affiliate_minimization_financial_totals (
    tombstone_id uuid NOT NULL REFERENCES affiliate_minimization_tombstones (tombstone_id),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    earned_minor bigint NOT NULL CHECK (earned_minor>=0),
    reversed_minor bigint NOT NULL CHECK (reversed_minor>=0),
    settled_minor bigint NOT NULL CHECK (settled_minor>=0),
    recovery_minor bigint NOT NULL CHECK (recovery_minor>=0),
    PRIMARY KEY (tombstone_id,currency)
);

CREATE TABLE affiliate_retired_code_fingerprints (
    code_fingerprint bytea PRIMARY KEY CHECK (octet_length(code_fingerprint)=32),
    fingerprint_version bigint NOT NULL CHECK (fingerprint_version=1),
    minimized_at timestamptz NOT NULL
);

CREATE FUNCTION spyglass_reject_retired_affiliate_public_code() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM public.affiliate_retired_code_fingerprints retired
         WHERE retired.code_fingerprint=digest(
             convert_to('spyglass:affiliate-code:v1:'||NEW.public_code,'UTF8'),'sha256')
    ) THEN
        RAISE EXCEPTION 'Affiliate public code is permanently retired'
            USING ERRCODE='23505',CONSTRAINT='affiliate_retired_code_fingerprints_pkey';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER affiliate_public_code_history_reject_retired
BEFORE INSERT ON affiliate_public_code_history
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_retired_affiliate_public_code();

CREATE FUNCTION spyglass_affiliate_retention_context_active() RETURNS boolean
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog, public
AS $$
    SELECT EXISTS (
        SELECT 1 FROM public.affiliate_minimization_execution_context context
         WHERE context.backend_pid=pg_backend_pid() AND context.transaction_id=txid_current()
    )
$$;

CREATE OR REPLACE FUNCTION spyglass_reject_affiliate_ledger_mutation() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    IF public.spyglass_affiliate_retention_context_active() THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'affiliate commission rules and entries are immutable';
END;
$$;

CREATE OR REPLACE FUNCTION spyglass_protect_affiliate_public_code_history() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    IF public.spyglass_affiliate_retention_context_active() THEN
        RETURN OLD;
    END IF;
    IF TG_OP = 'DELETE' OR
       OLD.public_code IS DISTINCT FROM NEW.public_code OR
       OLD.affiliate_id IS DISTINCT FROM NEW.affiliate_id OR
       OLD.enrollment_version IS DISTINCT FROM NEW.enrollment_version OR
       OLD.activated_at IS DISTINCT FROM NEW.activated_at OR
       OLD.replaced_at IS NOT NULL OR NEW.replaced_at IS NULL OR
       NEW.replaced_at < OLD.activated_at THEN
        RAISE EXCEPTION 'Affiliate public code history is immutable' USING ERRCODE='P0001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass_affiliate_minimization_candidates(p_now timestamptz)
RETURNS TABLE(candidate_affiliate_id uuid,candidate_closed_at timestamptz,candidate_final_activity_at timestamptz)
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog, public
AS $$
    WITH lifecycle AS (
        SELECT enrollment.affiliate_id,
               max(event.occurred_at) FILTER (WHERE event.action='closed') AS closed_at,
               GREATEST(
                   max(event.occurred_at) FILTER (WHERE event.action='closed'),
                   COALESCE((SELECT max(entry.created_at) FROM public.affiliate_commission_entries entry
                              WHERE entry.affiliate_id=enrollment.affiliate_id),'-infinity'::timestamptz),
                   COALESCE((SELECT max(reservation.updated_at) FROM public.affiliate_credit_reservations reservation
                              WHERE reservation.affiliate_id=enrollment.affiliate_id),'-infinity'::timestamptz),
                   COALESCE((SELECT max(adjustment.updated_at) FROM public.affiliate_credit_reversal_adjustments adjustment
                              WHERE adjustment.affiliate_id=enrollment.affiliate_id),'-infinity'::timestamptz),
                   COALESCE((SELECT max(request.updated_at) FROM public.affiliate_support_requests request
                              WHERE request.affiliate_id=enrollment.affiliate_id),'-infinity'::timestamptz)
               ) AS final_activity_at
          FROM public.affiliate_enrollments enrollment
          JOIN public.affiliate_retention_controls control ON control.affiliate_id=enrollment.affiliate_id
          LEFT JOIN public.affiliate_enrollment_events event ON event.affiliate_id=enrollment.affiliate_id
         WHERE p_now IS NOT NULL AND enrollment.state='closed' AND NOT control.legal_hold
         GROUP BY enrollment.affiliate_id
    )
    SELECT lifecycle.affiliate_id,lifecycle.closed_at,lifecycle.final_activity_at
      FROM lifecycle
     WHERE lifecycle.closed_at IS NOT NULL
       AND lifecycle.final_activity_at<=p_now-interval '7 years'
       AND NOT EXISTS (
           SELECT 1 FROM public.affiliate_support_requests request
            WHERE request.affiliate_id=lifecycle.affiliate_id AND request.state IN ('submitted','in_review')
       )
       AND NOT EXISTS (
           SELECT 1 FROM public.affiliate_credit_reservations reservation
            WHERE reservation.affiliate_id=lifecycle.affiliate_id AND reservation.state IN ('reserved','credited')
       )
       AND NOT EXISTS (
           SELECT 1 FROM public.affiliate_credit_reversal_adjustments adjustment
            WHERE adjustment.affiliate_id=lifecycle.affiliate_id AND adjustment.state='pending'
       )
       AND NOT EXISTS (
           SELECT 1 FROM (
               SELECT earned.currency,
                      sum(earned.amount_minor-COALESCE((
                          SELECT sum(allocation.amount_minor)
                            FROM public.affiliate_credit_allocations allocation
                            JOIN public.affiliate_credit_reservations reservation
                              ON reservation.reservation_id=allocation.reservation_id
                           WHERE allocation.earning_entry_id=earned.entry_id AND reservation.state='settled'
                      ),0))-COALESCE((
                          SELECT sum(adjustment.amount_minor)
                            FROM public.affiliate_credit_reversal_adjustments adjustment
                           WHERE adjustment.affiliate_id=lifecycle.affiliate_id
                             AND adjustment.currency=earned.currency
                             AND adjustment.kind='support_check_recovery' AND adjustment.state='applied'
                      ),0) AS effective_available_minor
                 FROM public.affiliate_commission_entries earned
                WHERE earned.affiliate_id=lifecycle.affiliate_id AND earned.kind='earned'
                  AND NOT EXISTS (
                      SELECT 1 FROM public.affiliate_commission_entries terminal
                       WHERE terminal.reverses_entry_id=earned.entry_id OR
                             (terminal.source_entry_id=earned.entry_id AND terminal.kind='void')
                  )
                GROUP BY earned.currency
           ) balance
           WHERE balance.effective_available_minor>0
       )
$$;

CREATE FUNCTION spyglass_minimize_due_affiliates(p_now timestamptz,p_limit integer) RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    candidate record;
    locked_candidate record;
    tombstone_id_value uuid;
    minimized_count bigint := 0;
    adverse_event_ids text[];
BEGIN
    IF p_now IS NULL OR p_limit NOT BETWEEN 1 AND 1000 THEN
        RAISE EXCEPTION 'Affiliate minimization policy is invalid' USING ERRCODE='22023';
    END IF;
    FOR candidate IN
        SELECT * FROM public.spyglass_affiliate_minimization_candidates(p_now)
         ORDER BY candidate_final_activity_at,candidate_affiliate_id
         LIMIT p_limit
    LOOP
        PERFORM 1
          FROM public.affiliate_enrollments enrollment
          JOIN public.affiliate_retention_controls control ON control.affiliate_id=enrollment.affiliate_id
         WHERE enrollment.affiliate_id=candidate.candidate_affiliate_id
           AND enrollment.state='closed' AND NOT control.legal_hold
         FOR UPDATE OF enrollment,control;
        IF NOT FOUND THEN
            CONTINUE;
        END IF;

        SELECT * INTO locked_candidate
          FROM public.spyglass_affiliate_minimization_candidates(p_now) eligible
         WHERE eligible.candidate_affiliate_id=candidate.candidate_affiliate_id;
        IF NOT FOUND THEN
            CONTINUE;
        END IF;

        tombstone_id_value := gen_random_uuid();
        INSERT INTO public.affiliate_minimization_tombstones
            (tombstone_id,policy_version,closed_at,final_activity_at,minimized_at,
             attribution_count,commission_entry_count,credit_reservation_count,support_request_count,code_count)
        SELECT tombstone_id_value,1,locked_candidate.candidate_closed_at,
               locked_candidate.candidate_final_activity_at,p_now,
               (SELECT count(*) FROM public.affiliate_attributions value
                 WHERE value.affiliate_id=locked_candidate.candidate_affiliate_id),
               (SELECT count(*) FROM public.affiliate_commission_entries value
                 WHERE value.affiliate_id=locked_candidate.candidate_affiliate_id),
               (SELECT count(*) FROM public.affiliate_credit_reservations value
                 WHERE value.affiliate_id=locked_candidate.candidate_affiliate_id),
               (SELECT count(*) FROM public.affiliate_support_requests value
                 WHERE value.affiliate_id=locked_candidate.candidate_affiliate_id),
               (SELECT count(*) FROM public.affiliate_public_code_history value
                 WHERE value.affiliate_id=locked_candidate.candidate_affiliate_id);

        INSERT INTO public.affiliate_minimization_financial_totals
            (tombstone_id,currency,earned_minor,reversed_minor,settled_minor,recovery_minor)
        SELECT tombstone_id_value,entry.currency,
               COALESCE(sum(entry.amount_minor) FILTER (WHERE entry.kind='earned'),0)::bigint,
               COALESCE(sum(entry.amount_minor) FILTER (WHERE entry.kind='reversal'),0)::bigint,
               COALESCE((
                   SELECT sum(reservation.amount_minor)
                     FROM public.affiliate_credit_reservations reservation
                    WHERE reservation.affiliate_id=locked_candidate.candidate_affiliate_id
                      AND reservation.currency=entry.currency AND reservation.state='settled'
               ),0)::bigint,
               COALESCE((
                   SELECT sum(adjustment.amount_minor)
                     FROM public.affiliate_credit_reversal_adjustments adjustment
                    WHERE adjustment.affiliate_id=locked_candidate.candidate_affiliate_id
                      AND adjustment.currency=entry.currency
                      AND adjustment.kind='support_check_recovery' AND adjustment.state='applied'
               ),0)::bigint
          FROM public.affiliate_commission_entries entry
         WHERE entry.affiliate_id=locked_candidate.candidate_affiliate_id
         GROUP BY entry.currency;

        INSERT INTO public.affiliate_retired_code_fingerprints
            (code_fingerprint,fingerprint_version,minimized_at)
        SELECT digest(convert_to('spyglass:affiliate-code:v1:'||history.public_code,'UTF8'),'sha256'),1,p_now
          FROM public.affiliate_public_code_history history
         WHERE history.affiliate_id=locked_candidate.candidate_affiliate_id
        ON CONFLICT (code_fingerprint) DO NOTHING;

        SELECT array_agg(DISTINCT adverse.provider_event_id) INTO adverse_event_ids
          FROM public.affiliate_provider_adverse_events adverse
         WHERE EXISTS (
             SELECT 1
               FROM public.affiliate_commission_invoice_payments payment
               JOIN public.affiliate_commission_entries entry ON entry.entry_id=payment.earning_entry_id
              WHERE entry.affiliate_id=locked_candidate.candidate_affiliate_id
                AND payment.provider_payment_intent_id=adverse.provider_payment_intent_id
         ) OR EXISTS (
             SELECT 1
               FROM public.affiliate_provider_adverse_invoice_lines adverse_line
               JOIN public.affiliate_commission_invoice_lines invoice_line
                 ON invoice_line.provider_invoice_line_id=adverse_line.provider_invoice_line_id
               JOIN public.affiliate_commission_entries entry ON entry.entry_id=invoice_line.earning_entry_id
              WHERE entry.affiliate_id=locked_candidate.candidate_affiliate_id
                AND adverse_line.provider_event_id=adverse.provider_event_id
         );

        INSERT INTO public.affiliate_minimization_execution_context (backend_pid,transaction_id)
        VALUES (pg_backend_pid(),txid_current());
		DELETE FROM public.affiliate_support_request_events event
		 USING public.affiliate_support_requests request
		 WHERE event.request_id=request.request_id
		   AND request.affiliate_id=locked_candidate.candidate_affiliate_id;
		DELETE FROM public.affiliate_support_requests
		 WHERE affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_credit_reversal_adjustment_events event
         USING public.affiliate_credit_reversal_adjustments adjustment
         WHERE event.adjustment_id=adjustment.adjustment_id
           AND adjustment.affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_credit_reversal_adjustments
         WHERE affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_credit_reservation_events event
         USING public.affiliate_credit_reservations reservation
         WHERE event.reservation_id=reservation.reservation_id
           AND reservation.affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_credit_allocations allocation
         USING public.affiliate_credit_reservations reservation
         WHERE allocation.reservation_id=reservation.reservation_id
           AND reservation.affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_credit_reservations
         WHERE affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_commission_invoice_payments payment
         USING public.affiliate_commission_entries entry
         WHERE payment.earning_entry_id=entry.entry_id
           AND entry.affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_commission_invoice_lines invoice_line
         USING public.affiliate_commission_entries entry
         WHERE invoice_line.earning_entry_id=entry.entry_id
           AND entry.affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_commission_entries
         WHERE affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_attributions
         WHERE affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_enrollment_events
         WHERE affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_public_code_history
         WHERE affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_retention_control_events
         WHERE affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_retention_controls
         WHERE affiliate_id=locked_candidate.candidate_affiliate_id;
        DELETE FROM public.affiliate_enrollments
         WHERE affiliate_id=locked_candidate.candidate_affiliate_id;

        IF adverse_event_ids IS NOT NULL THEN
            DELETE FROM public.affiliate_provider_adverse_invoice_lines line
             WHERE line.provider_event_id=ANY(adverse_event_ids)
               AND NOT EXISTS (
                   SELECT 1 FROM public.affiliate_commission_invoice_lines invoice_line
                    WHERE invoice_line.provider_invoice_line_id=line.provider_invoice_line_id
               );
            DELETE FROM public.affiliate_provider_adverse_events adverse
             WHERE adverse.provider_event_id=ANY(adverse_event_ids)
               AND NOT EXISTS (
                   SELECT 1 FROM public.affiliate_provider_adverse_invoice_lines line
                    WHERE line.provider_event_id=adverse.provider_event_id
               )
               AND NOT EXISTS (
                   SELECT 1 FROM public.affiliate_commission_invoice_payments payment
                    WHERE payment.provider_payment_intent_id=adverse.provider_payment_intent_id
               );
        END IF;
        DELETE FROM public.affiliate_minimization_execution_context
         WHERE backend_pid=pg_backend_pid() AND transaction_id=txid_current();
        minimized_count := minimized_count+1;
    END LOOP;
    RETURN minimized_count;
END;
$$;

CREATE FUNCTION spyglass_affiliate_minimization_stats(p_now timestamptz)
RETURNS TABLE(total bigint,eligible bigint,oldest_eligible_age_seconds bigint)
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog, public
AS $$
    SELECT
        (SELECT count(*)::bigint FROM public.affiliate_enrollments enrollment WHERE enrollment.state='closed'),
        count(*)::bigint,
        COALESCE(extract(epoch FROM p_now-min(candidate.candidate_final_activity_at))::bigint,0)
      FROM public.spyglass_affiliate_minimization_candidates(p_now) candidate
$$;

CREATE TRIGGER affiliate_retention_control_events_immutable
BEFORE UPDATE OR DELETE ON affiliate_retention_control_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();
CREATE TRIGGER affiliate_minimization_tombstones_immutable
BEFORE UPDATE OR DELETE ON affiliate_minimization_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();
CREATE TRIGGER affiliate_minimization_financial_totals_immutable
BEFORE UPDATE OR DELETE ON affiliate_minimization_financial_totals
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();
CREATE TRIGGER affiliate_retired_code_fingerprints_immutable
BEFORE UPDATE OR DELETE ON affiliate_retired_code_fingerprints
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();

REVOKE ALL ON TABLE affiliate_retention_controls,affiliate_retention_control_events,
    affiliate_minimization_execution_context,affiliate_minimization_tombstones,
    affiliate_minimization_financial_totals,affiliate_retired_code_fingerprints FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_record_affiliate_retention_control() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_set_affiliate_retention_hold(uuid,uuid,bigint,boolean,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_restrict_affiliate_retention(uuid,uuid,bigint,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_reject_retired_affiliate_public_code() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_affiliate_retention_context_active() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_affiliate_minimization_candidates(timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_minimize_due_affiliates(timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_affiliate_minimization_stats(timestamptz) FROM PUBLIC;

COMMIT;
