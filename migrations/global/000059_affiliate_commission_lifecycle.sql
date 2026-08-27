BEGIN;

ALTER TABLE affiliate_provider_adverse_events
    DROP CONSTRAINT affiliate_provider_adverse_events_kind_check,
    DROP CONSTRAINT affiliate_provider_adverse_object_shape,
    DROP CONSTRAINT affiliate_provider_adverse_payment_intent_shape,
    ALTER COLUMN provider_payment_intent_id DROP NOT NULL,
    ADD COLUMN provider_invoice_id text,
    ADD CONSTRAINT affiliate_provider_adverse_events_kind_check CHECK (kind IN ('refund','dispute','credit_note')),
    ADD CONSTRAINT affiliate_provider_adverse_object_shape CHECK (
        (kind='refund' AND left(provider_object_id,3)='re_') OR
        (kind='dispute' AND left(provider_object_id,3)='dp_') OR
        (kind='credit_note' AND left(provider_object_id,3)='cn_')
    ),
    ADD CONSTRAINT affiliate_provider_adverse_target_shape CHECK (
        (kind IN ('refund','dispute') AND provider_payment_intent_id IS NOT NULL AND provider_invoice_id IS NULL) OR
        (kind='credit_note' AND provider_payment_intent_id IS NULL AND left(provider_invoice_id,3)='in_')
    ),
    ADD CONSTRAINT affiliate_provider_adverse_payment_intent_shape CHECK (
        provider_payment_intent_id IS NULL OR (
            left(provider_payment_intent_id,3)='pi_' AND length(provider_payment_intent_id)<=200 AND
            provider_payment_intent_id !~ '[[:space:]]'
        )
    ),
    ADD CONSTRAINT affiliate_provider_adverse_invoice_shape CHECK (
        provider_invoice_id IS NULL OR (length(provider_invoice_id)<=200 AND provider_invoice_id !~ '[[:space:]]')
    );

CREATE TABLE affiliate_provider_adverse_invoice_lines (
    provider_event_id text NOT NULL REFERENCES affiliate_provider_adverse_events (provider_event_id),
    provider_invoice_line_id text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (provider_event_id,provider_invoice_line_id),
    CONSTRAINT affiliate_provider_adverse_invoice_lines_shape CHECK (
        left(provider_invoice_line_id,3)='il_' AND length(provider_invoice_line_id)<=200 AND
        provider_invoice_line_id !~ '[[:space:]]'
    )
);

CREATE INDEX affiliate_provider_adverse_invoice_line_lookup
    ON affiliate_provider_adverse_invoice_lines (provider_invoice_line_id,provider_event_id);

CREATE TRIGGER affiliate_provider_adverse_invoice_lines_immutable
BEFORE UPDATE OR DELETE ON affiliate_provider_adverse_invoice_lines
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();

ALTER TABLE affiliate_commission_rules
    ADD COLUMN commission_rate_basis_points integer NOT NULL DEFAULT 2000,
    ADD CONSTRAINT affiliate_commission_rules_rate CHECK (commission_rate_basis_points BETWEEN 1 AND 10000);

ALTER TABLE affiliate_commission_entries
    ADD COLUMN source_entry_id uuid REFERENCES affiliate_commission_entries (entry_id),
    DROP CONSTRAINT affiliate_commission_entries_kind_check,
    DROP CONSTRAINT affiliate_commission_entries_state_check,
    DROP CONSTRAINT affiliate_commission_entries_kind_shape,
    ADD CONSTRAINT affiliate_commission_entries_kind_check CHECK (kind IN ('earned','maturity','void','reversal')),
    ADD CONSTRAINT affiliate_commission_entries_state_check CHECK (state IN ('pending','settled')),
    ADD CONSTRAINT affiliate_commission_entries_kind_shape CHECK (
        (kind='earned' AND state='pending' AND reverses_entry_id IS NULL AND source_entry_id IS NULL) OR
        (kind='reversal' AND state='settled' AND reverses_entry_id IS NOT NULL AND source_entry_id IS NULL) OR
        (kind IN ('maturity','void') AND state='settled' AND reverses_entry_id IS NULL AND source_entry_id IS NOT NULL)
    );

CREATE UNIQUE INDEX affiliate_commission_entries_terminal_source
    ON affiliate_commission_entries (source_entry_id)
    WHERE kind IN ('maturity','void');

CREATE TABLE affiliate_commission_invoice_payments (
    earning_entry_id uuid NOT NULL REFERENCES affiliate_commission_entries (entry_id),
    provider_payment_intent_id text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (earning_entry_id,provider_payment_intent_id),
    UNIQUE (provider_payment_intent_id),
    CONSTRAINT affiliate_commission_invoice_payments_shape CHECK (
        left(provider_payment_intent_id,3)='pi_' AND
        length(provider_payment_intent_id) <= 200 AND
        provider_payment_intent_id !~ '[[:space:]]'
    )
);

INSERT INTO affiliate_commission_invoice_payments (earning_entry_id,provider_payment_intent_id,created_at)
SELECT entry_id,provider_payment_intent_id,created_at
FROM affiliate_commission_entries
WHERE kind='earned' AND provider_payment_intent_id IS NOT NULL;

CREATE TABLE affiliate_commission_invoice_lines (
    earning_entry_id uuid NOT NULL REFERENCES affiliate_commission_entries (entry_id),
    provider_invoice_line_id text NOT NULL,
    provider_price_id text NOT NULL,
    eligible_amount_minor bigint NOT NULL CHECK (eligible_amount_minor BETWEEN 1 AND 99999999),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (earning_entry_id,provider_invoice_line_id),
    UNIQUE (provider_invoice_line_id),
    CONSTRAINT affiliate_commission_invoice_lines_id_shape CHECK (
        left(provider_invoice_line_id,3)='il_' AND length(provider_invoice_line_id) <= 200 AND provider_invoice_line_id !~ '[[:space:]]'
    ),
    CONSTRAINT affiliate_commission_invoice_lines_price_shape CHECK (
        left(provider_price_id,6)='price_' AND length(provider_price_id) <= 200 AND provider_price_id !~ '[[:space:]]'
    )
);

CREATE TRIGGER affiliate_commission_invoice_payments_immutable
BEFORE UPDATE OR DELETE ON affiliate_commission_invoice_payments
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();

CREATE TRIGGER affiliate_commission_invoice_lines_immutable
BEFORE UPDATE OR DELETE ON affiliate_commission_invoice_lines
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();

CREATE TABLE affiliate_settlement_policies (
    version bigint PRIMARY KEY CHECK (version > 0),
    mode text NOT NULL CHECK (mode='account_credit_with_support_check'),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    check_threshold_minor bigint NOT NULL CHECK (check_threshold_minor BETWEEN 1 AND 99999999),
    effective_from timestamptz NOT NULL,
    created_by text NOT NULL CHECK (length(btrim(created_by)) BETWEEN 3 AND 200 AND created_by !~ E'[\r\n]'),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    created_at timestamptz NOT NULL
);

INSERT INTO affiliate_settlement_policies
    (version,mode,currency,check_threshold_minor,effective_from,created_by,reason,created_at)
VALUES (1,'account_credit_with_support_check','USD',10000,'2026-08-26T00:00:00Z','launch-policy','Owner-approved launch threshold',statement_timestamp());

CREATE TABLE affiliate_settlement_policy_current (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    policy_version bigint NOT NULL REFERENCES affiliate_settlement_policies (version),
    updated_at timestamptz NOT NULL
);
INSERT INTO affiliate_settlement_policy_current (singleton,policy_version,updated_at) VALUES (true,1,statement_timestamp());

CREATE TABLE affiliate_settlement_policy_events (
    event_id uuid PRIMARY KEY,
    policy_version bigint NOT NULL REFERENCES affiliate_settlement_policies (version),
    check_threshold_minor bigint NOT NULL CHECK (check_threshold_minor BETWEEN 1 AND 99999999),
    actor text NOT NULL CHECK (length(btrim(actor)) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    occurred_at timestamptz NOT NULL
);

CREATE FUNCTION spyglass_publish_affiliate_settlement_policy(
    p_event_id uuid,
    p_expected_version bigint,
    p_new_version bigint,
    p_check_threshold_minor bigint,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(version bigint,mode text,currency text,check_threshold_minor bigint,effective_from timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE current_version bigint;
BEGIN
    IF p_event_id IS NULL OR p_expected_version < 1 OR p_new_version <> p_expected_version+1 OR
       p_check_threshold_minor NOT BETWEEN 1 AND 99999999 OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION 'invalid Affiliate settlement policy change' USING ERRCODE='22023';
    END IF;
    SELECT policy_version INTO current_version FROM public.affiliate_settlement_policy_current WHERE singleton=true FOR UPDATE;
    IF current_version <> p_expected_version THEN
        RAISE EXCEPTION 'Affiliate settlement policy changed' USING ERRCODE='P0001';
    END IF;
    INSERT INTO public.affiliate_settlement_policies
        (version,mode,currency,check_threshold_minor,effective_from,created_by,reason,created_at)
    VALUES (p_new_version,'account_credit_with_support_check','USD',p_check_threshold_minor,
            statement_timestamp(),btrim(p_actor),btrim(p_reason),statement_timestamp());
    UPDATE public.affiliate_settlement_policy_current
       SET policy_version=p_new_version,updated_at=statement_timestamp()
     WHERE singleton=true;
    INSERT INTO public.affiliate_settlement_policy_events
        (event_id,policy_version,check_threshold_minor,actor,reason,environment,occurred_at)
    VALUES (p_event_id,p_new_version,p_check_threshold_minor,btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    RETURN QUERY SELECT policy.version,policy.mode,policy.currency,policy.check_threshold_minor,policy.effective_from
      FROM public.affiliate_settlement_policies policy WHERE policy.version=p_new_version;
END;
$$;

CREATE TABLE affiliate_credit_reservations (
    reservation_id uuid PRIMARY KEY,
    affiliate_id uuid NOT NULL REFERENCES affiliate_enrollments (affiliate_id),
    settlement_account_id uuid REFERENCES accounts (id) ON DELETE SET NULL,
    provider_invoice_id text,
    policy_version bigint NOT NULL REFERENCES affiliate_settlement_policies (version),
    kind text NOT NULL CHECK (kind IN ('invoice_credit','support_check')),
    state text NOT NULL CHECK (state IN ('reserved','credited','settled','released')),
    amount_minor bigint NOT NULL CHECK (amount_minor BETWEEN 1 AND 99999999),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    provider_customer_id text,
    provider_balance_transaction_id text,
    customer_reauthenticated_at timestamptz,
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT affiliate_credit_reservations_invoice_shape CHECK (
        (kind='invoice_credit' AND left(provider_invoice_id,3)='in_') OR
        (kind='support_check' AND provider_invoice_id IS NULL)
    ),
    CONSTRAINT affiliate_credit_reservations_provider_shape CHECK (
        (kind='invoice_credit' AND state='reserved' AND left(provider_customer_id,4)='cus_' AND
         provider_balance_transaction_id IS NULL AND customer_reauthenticated_at IS NULL) OR
		(kind='invoice_credit' AND state IN ('credited','settled') AND left(provider_customer_id,4)='cus_' AND
		 left(provider_balance_transaction_id,6)='cbtxn_' AND customer_reauthenticated_at IS NULL) OR
        (kind='invoice_credit' AND state='released' AND provider_balance_transaction_id IS NULL AND customer_reauthenticated_at IS NULL) OR
        (kind='support_check' AND state IN ('reserved','settled','released') AND provider_customer_id IS NULL AND
         provider_balance_transaction_id IS NULL AND customer_reauthenticated_at IS NOT NULL)
    ),
    UNIQUE (affiliate_id,provider_invoice_id),
    UNIQUE (provider_balance_transaction_id)
);
CREATE INDEX affiliate_credit_reservations_affiliate
    ON affiliate_credit_reservations (affiliate_id,created_at,reservation_id)
    WHERE state IN ('reserved','credited');

CREATE TABLE affiliate_credit_allocations (
    reservation_id uuid NOT NULL REFERENCES affiliate_credit_reservations (reservation_id),
    earning_entry_id uuid NOT NULL REFERENCES affiliate_commission_entries (entry_id),
    amount_minor bigint NOT NULL CHECK (amount_minor BETWEEN 1 AND 99999999),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (reservation_id,earning_entry_id)
);

CREATE TABLE affiliate_credit_reservation_events (
    event_id uuid PRIMARY KEY,
    reservation_id uuid NOT NULL REFERENCES affiliate_credit_reservations (reservation_id),
    version bigint NOT NULL CHECK (version > 0),
    action text NOT NULL CHECK (action IN ('reserved','credited','settled','released')),
    state text NOT NULL CHECK (state IN ('reserved','credited','settled','released')),
    provider_reference text,
    actor text,
    reason text,
    environment text,
    occurred_at timestamptz NOT NULL,
    UNIQUE (reservation_id,version),
    CONSTRAINT affiliate_credit_reservation_events_operator_shape CHECK (
        (actor IS NULL AND reason IS NULL AND environment IS NULL) OR
        (length(btrim(actor)) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]' AND
         length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]' AND
         environment ~ '^[a-z][a-z0-9-]{0,99}$')
    )
);

CREATE TABLE affiliate_credit_reversal_adjustments (
    adjustment_id uuid PRIMARY KEY,
    reversal_entry_id uuid NOT NULL REFERENCES affiliate_commission_entries (entry_id),
    reservation_id uuid NOT NULL REFERENCES affiliate_credit_reservations (reservation_id),
    affiliate_id uuid NOT NULL REFERENCES affiliate_enrollments (affiliate_id),
    settlement_account_id uuid REFERENCES accounts (id) ON DELETE SET NULL,
    kind text NOT NULL CHECK (kind IN ('customer_balance_debit','support_check_recovery')),
    state text NOT NULL CHECK (state IN ('pending','applied')),
    amount_minor bigint NOT NULL CHECK (amount_minor BETWEEN 1 AND 99999999),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    provider_customer_id text,
    provider_balance_transaction_id text,
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (reversal_entry_id,reservation_id),
    UNIQUE (provider_balance_transaction_id),
    CONSTRAINT affiliate_credit_reversal_adjustments_shape CHECK (
        (kind='customer_balance_debit' AND state='pending' AND settlement_account_id IS NOT NULL AND
         left(provider_customer_id,4)='cus_' AND provider_balance_transaction_id IS NULL) OR
        (kind='customer_balance_debit' AND state='applied' AND settlement_account_id IS NOT NULL AND
         left(provider_customer_id,4)='cus_' AND left(provider_balance_transaction_id,6)='cbtxn_') OR
        (kind='support_check_recovery' AND state='applied' AND settlement_account_id IS NULL AND
         provider_customer_id IS NULL AND provider_balance_transaction_id IS NULL)
    )
);

CREATE TABLE affiliate_credit_reversal_adjustment_events (
    event_id uuid PRIMARY KEY,
    adjustment_id uuid NOT NULL REFERENCES affiliate_credit_reversal_adjustments (adjustment_id),
    version bigint NOT NULL CHECK (version > 0),
    action text NOT NULL CHECK (action IN ('prepared','applied')),
    state text NOT NULL CHECK (state IN ('pending','applied')),
    provider_reference text,
    occurred_at timestamptz NOT NULL,
    UNIQUE (adjustment_id,version)
);

CREATE TRIGGER affiliate_settlement_policies_immutable
BEFORE UPDATE OR DELETE ON affiliate_settlement_policies
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();
CREATE TRIGGER affiliate_settlement_policy_events_immutable
BEFORE UPDATE OR DELETE ON affiliate_settlement_policy_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();
CREATE TRIGGER affiliate_credit_allocations_immutable
BEFORE UPDATE OR DELETE ON affiliate_credit_allocations
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();
CREATE TRIGGER affiliate_credit_reservation_events_immutable
BEFORE UPDATE OR DELETE ON affiliate_credit_reservation_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();
CREATE TRIGGER affiliate_credit_reversal_adjustment_events_immutable
BEFORE UPDATE OR DELETE ON affiliate_credit_reversal_adjustment_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();

CREATE FUNCTION spyglass_reserve_affiliate_support_check(
    p_event_id uuid,
    p_reservation_id uuid,
    p_affiliate_id uuid,
    p_customer_session_id uuid,
    p_amount_minor bigint,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(
    reservation_id uuid,affiliate_id uuid,state text,amount_minor bigint,currency text,
    policy_version bigint,version bigint,created_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    enrollment_row public.affiliate_enrollments%ROWTYPE;
    policy_row public.affiliate_settlement_policies%ROWTYPE;
    strong_at timestamptz;
    available_minor bigint;
    recovery_minor bigint;
    recovery_remaining bigint;
    remaining_minor bigint;
    earning record;
    allocation_minor bigint;
    created_at_value timestamptz := statement_timestamp();
BEGIN
    IF p_event_id IS NULL OR p_reservation_id IS NULL OR p_affiliate_id IS NULL OR p_customer_session_id IS NULL OR
       p_amount_minor NOT BETWEEN 1 AND 99999999 OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION 'invalid Affiliate Support check reservation' USING ERRCODE='22023';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended('spyglass:affiliate-credit:' || p_affiliate_id::text,0));
    SELECT * INTO enrollment_row FROM public.affiliate_enrollments enrollment WHERE enrollment.affiliate_id=p_affiliate_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Affiliate enrollment not found' USING ERRCODE='P0002';
    END IF;
    SELECT session_record.reauthenticated_at INTO strong_at
      FROM public.sessions session_record
      JOIN public.users identity ON identity.id=session_record.user_id
     WHERE session_record.id=p_customer_session_id AND session_record.user_id=enrollment_row.user_id
       AND identity.state='active' AND identity.security_version=session_record.security_version
       AND session_record.revoked_at IS NULL AND session_record.expires_at>created_at_value
       AND session_record.reauthentication_method='passkey'
       AND session_record.reauthenticated_at<=created_at_value
       AND session_record.reauthenticated_at>=created_at_value-interval '10 minutes';
    IF strong_at IS NULL THEN
        RAISE EXCEPTION 'recent Affiliate passkey confirmation is required' USING ERRCODE='28000';
    END IF;
    SELECT policy.* INTO policy_row
      FROM public.affiliate_settlement_policy_current current_policy
      JOIN public.affiliate_settlement_policies policy ON policy.version=current_policy.policy_version
     WHERE current_policy.singleton=true AND policy.mode='account_credit_with_support_check'
       AND policy.effective_from<=created_at_value
     FOR SHARE OF policy;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Affiliate settlement policy unavailable' USING ERRCODE='P0001';
    END IF;

    SELECT COALESCE(sum(earned.amount_minor-COALESCE(allocated.amount_minor,0)),0)::bigint INTO available_minor
      FROM public.affiliate_commission_entries earned
      JOIN public.affiliate_commission_entries maturity ON maturity.source_entry_id=earned.entry_id AND maturity.kind='maturity'
      LEFT JOIN LATERAL (
          SELECT sum(allocation.amount_minor) FILTER (WHERE reservation.state<>'released')::bigint amount_minor
            FROM public.affiliate_credit_allocations allocation
            JOIN public.affiliate_credit_reservations reservation ON reservation.reservation_id=allocation.reservation_id
           WHERE allocation.earning_entry_id=earned.entry_id
      ) allocated ON true
     WHERE earned.affiliate_id=p_affiliate_id AND earned.kind='earned' AND earned.currency=policy_row.currency
       AND NOT EXISTS (
           SELECT 1 FROM public.affiliate_commission_entries reversal
            WHERE reversal.reverses_entry_id=earned.entry_id AND reversal.kind='reversal'
       );
    SELECT COALESCE(sum(adjustment.amount_minor),0)::bigint INTO recovery_minor
      FROM public.affiliate_credit_reversal_adjustments adjustment
     WHERE adjustment.affiliate_id=p_affiliate_id AND adjustment.kind='support_check_recovery'
       AND adjustment.state='applied' AND adjustment.currency=policy_row.currency;
    available_minor := GREATEST(available_minor-recovery_minor,0);
    IF available_minor<policy_row.check_threshold_minor OR p_amount_minor>available_minor THEN
        RAISE EXCEPTION 'Affiliate Support check balance is unavailable' USING ERRCODE='P0001';
    END IF;

    INSERT INTO public.affiliate_credit_reservations
        (reservation_id,affiliate_id,settlement_account_id,provider_invoice_id,policy_version,kind,state,amount_minor,currency,
         provider_customer_id,provider_balance_transaction_id,customer_reauthenticated_at,version,created_at,updated_at)
    VALUES (p_reservation_id,p_affiliate_id,NULL,NULL,policy_row.version,'support_check','reserved',p_amount_minor,policy_row.currency,
            NULL,NULL,strong_at,1,created_at_value,created_at_value);
    remaining_minor := p_amount_minor;
    recovery_remaining := recovery_minor;
    FOR earning IN
        SELECT earned.entry_id,earned.amount_minor-COALESCE(sum(allocation.amount_minor) FILTER (WHERE reservation.state<>'released'),0)::bigint amount_minor
          FROM public.affiliate_commission_entries earned
          JOIN public.affiliate_commission_entries maturity ON maturity.source_entry_id=earned.entry_id AND maturity.kind='maturity'
          LEFT JOIN public.affiliate_credit_allocations allocation ON allocation.earning_entry_id=earned.entry_id
          LEFT JOIN public.affiliate_credit_reservations reservation ON reservation.reservation_id=allocation.reservation_id
         WHERE earned.affiliate_id=p_affiliate_id AND earned.kind='earned' AND earned.currency=policy_row.currency
           AND NOT EXISTS (
               SELECT 1 FROM public.affiliate_commission_entries reversal
                WHERE reversal.reverses_entry_id=earned.entry_id AND reversal.kind='reversal'
           )
         GROUP BY earned.entry_id,earned.amount_minor,maturity.created_at
        HAVING earned.amount_minor-COALESCE(sum(allocation.amount_minor) FILTER (WHERE reservation.state<>'released'),0)::bigint>0
         ORDER BY maturity.created_at,earned.entry_id
    LOOP
        EXIT WHEN remaining_minor=0;
        IF recovery_remaining>=earning.amount_minor THEN
            recovery_remaining := recovery_remaining-earning.amount_minor;
            CONTINUE;
        ELSIF recovery_remaining>0 THEN
            earning.amount_minor := earning.amount_minor-recovery_remaining;
            recovery_remaining := 0;
        END IF;
        allocation_minor := LEAST(remaining_minor,earning.amount_minor);
        INSERT INTO public.affiliate_credit_allocations (reservation_id,earning_entry_id,amount_minor,created_at)
        VALUES (p_reservation_id,earning.entry_id,allocation_minor,created_at_value);
        remaining_minor := remaining_minor-allocation_minor;
    END LOOP;
    IF remaining_minor<>0 THEN
        RAISE EXCEPTION 'Affiliate Support check allocation changed' USING ERRCODE='P0001';
    END IF;
    INSERT INTO public.affiliate_credit_reservation_events
        (event_id,reservation_id,version,action,state,actor,reason,environment,occurred_at)
    VALUES (p_event_id,p_reservation_id,1,'reserved','reserved',btrim(p_actor),btrim(p_reason),p_environment,created_at_value);
    RETURN QUERY SELECT p_reservation_id,p_affiliate_id,'reserved'::text,p_amount_minor,policy_row.currency,
        policy_row.version,1::bigint,created_at_value;
END;
$$;

CREATE FUNCTION spyglass_transition_affiliate_support_check(
    p_event_id uuid,
    p_reservation_id uuid,
    p_expected_version bigint,
    p_state text,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(
    reservation_id uuid,affiliate_id uuid,state text,amount_minor bigint,currency text,
    policy_version bigint,version bigint,created_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    reservation_row public.affiliate_credit_reservations%ROWTYPE;
BEGIN
    IF p_event_id IS NULL OR p_reservation_id IS NULL OR p_expected_version<1 OR p_state NOT IN ('settled','released') OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION 'invalid Affiliate Support check transition' USING ERRCODE='22023';
    END IF;
    SELECT * INTO reservation_row FROM public.affiliate_credit_reservations
     WHERE affiliate_credit_reservations.reservation_id=p_reservation_id FOR UPDATE;
    IF NOT FOUND OR reservation_row.kind<>'support_check' THEN
        RAISE EXCEPTION 'Affiliate Support check reservation not found' USING ERRCODE='P0002';
    END IF;
    IF reservation_row.version<>p_expected_version OR reservation_row.state<>'reserved' THEN
        RAISE EXCEPTION 'Affiliate Support check reservation changed' USING ERRCODE='P0001';
    END IF;
    UPDATE public.affiliate_credit_reservations
       SET state=p_state,version=affiliate_credit_reservations.version+1,updated_at=statement_timestamp()
     WHERE affiliate_credit_reservations.reservation_id=p_reservation_id
     RETURNING * INTO reservation_row;
    INSERT INTO public.affiliate_credit_reservation_events
        (event_id,reservation_id,version,action,state,actor,reason,environment,occurred_at)
    VALUES (p_event_id,p_reservation_id,reservation_row.version,p_state,p_state,btrim(p_actor),btrim(p_reason),p_environment,reservation_row.updated_at);
    RETURN QUERY SELECT reservation_row.reservation_id,reservation_row.affiliate_id,reservation_row.state,
        reservation_row.amount_minor,reservation_row.currency,reservation_row.policy_version,reservation_row.version,reservation_row.created_at;
END;
$$;

CREATE OR REPLACE FUNCTION spyglass_transition_affiliate_enrollment(
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
    IF p_state='closed' THEN
        INSERT INTO public.affiliate_commission_entries
            (entry_id,affiliate_id,attribution_id,rule_version,provider_subscription_id,provider_invoice_id,
             provider_payment_intent_id,cycle,kind,state,amount_minor,currency,reverses_entry_id,source_entry_id,available_at,created_at)
        SELECT gen_random_uuid(),earning.affiliate_id,earning.attribution_id,earning.rule_version,
               earning.provider_subscription_id,earning.provider_invoice_id,NULL,earning.cycle,'void','settled',
               earning.amount_minor,earning.currency,NULL,earning.entry_id,current_row.updated_at,current_row.updated_at
        FROM public.affiliate_commission_entries earning
        WHERE earning.affiliate_id=p_affiliate_id AND earning.kind='earned'
          AND NOT EXISTS (
              SELECT 1 FROM public.affiliate_commission_entries lifecycle
              WHERE lifecycle.source_entry_id=earning.entry_id AND lifecycle.kind IN ('maturity','void')
          )
          AND NOT EXISTS (
              SELECT 1 FROM public.affiliate_commission_entries reversal
              WHERE reversal.reverses_entry_id=earning.entry_id AND reversal.kind='reversal'
          );
    END IF;
    RETURN QUERY SELECT current_row.affiliate_id,current_row.user_id,current_row.settlement_account_id::text,
        current_row.public_code,current_row.terms_version,current_row.rule_version,current_row.state,
        current_row.version,current_row.created_at;
END;
$$;

CREATE OR REPLACE FUNCTION spyglass_export_affiliate_data(p_user_id uuid) RETURNS jsonb
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog, public
AS $$
SELECT jsonb_build_object(
    'schema_version', 3,
    'generated_at', statement_timestamp(),
    'enrollment', (
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'affiliate_id', e.affiliate_id,
            'user_id', e.user_id,
            'settlement_account_id', e.settlement_account_id,
            'public_code', e.public_code,
            'terms_version', e.terms_version,
            'rule_version', e.rule_version,
            'state', e.state,
            'version', e.version,
            'created_at', e.created_at,
            'updated_at', e.updated_at
        ))
        FROM public.affiliate_enrollments e
        WHERE e.user_id = p_user_id
    ),
    'public_codes', COALESCE((
        SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'public_code', h.public_code,
            'enrollment_version', h.enrollment_version,
            'activated_at', h.activated_at,
            'replaced_at', h.replaced_at
        )) ORDER BY h.enrollment_version)
        FROM public.affiliate_public_code_history h
        JOIN public.affiliate_enrollments e ON e.affiliate_id = h.affiliate_id
        WHERE e.user_id = p_user_id
    ), '[]'::jsonb),
    'enrollment_events', COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
            'event_id', v.event_id,
            'version', v.version,
            'action', v.action,
            'state', v.state,
            'occurred_at', v.occurred_at
        ) ORDER BY v.occurred_at, v.event_id)
        FROM public.affiliate_enrollment_events v
        JOIN public.affiliate_enrollments e ON e.affiliate_id = v.affiliate_id
        WHERE e.user_id = p_user_id
    ), '[]'::jsonb),
    'attribution_summary', (
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'total', count(*),
            'reserved', count(*) FILTER (WHERE a.state = 'reserved'),
            'locked', count(*) FILTER (WHERE a.state = 'locked'),
            'canceled', count(*) FILTER (WHERE a.state = 'canceled'),
            'earliest_at', min(a.created_at),
            'latest_at', max(a.created_at)
        ))
        FROM public.affiliate_attributions a
        JOIN public.affiliate_enrollments e ON e.affiliate_id = a.affiliate_id
        WHERE e.user_id = p_user_id
    ),
    'commission_entries', COALESCE((
        SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'entry_id', c.entry_id,
            'rule_version', c.rule_version,
            'cycle', c.cycle,
            'kind', c.kind,
            'state', c.state,
            'amount_minor', c.amount_minor,
            'currency', c.currency,
            'reverses_entry_id', c.reverses_entry_id,
            'source_entry_id', c.source_entry_id,
            'available_at', c.available_at,
            'created_at', c.created_at
        )) ORDER BY c.created_at, c.entry_id)
        FROM public.affiliate_commission_entries c
        JOIN public.affiliate_enrollments e ON e.affiliate_id = c.affiliate_id
        WHERE e.user_id = p_user_id
    ), '[]'::jsonb),
    'credit_settlements', COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
            'reservation_id', r.reservation_id,
            'settlement_account_id', r.settlement_account_id,
            'kind', r.kind,
            'state', r.state,
            'amount_minor', r.amount_minor,
            'currency', r.currency,
            'created_at', r.created_at,
            'updated_at', r.updated_at
        ) ORDER BY r.created_at,r.reservation_id)
        FROM public.affiliate_credit_reservations r
        JOIN public.affiliate_enrollments e ON e.affiliate_id=r.affiliate_id
        WHERE e.user_id=p_user_id
    ), '[]'::jsonb),
    'credit_reversals', COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
            'adjustment_id', a.adjustment_id,
            'reservation_id', a.reservation_id,
            'kind', a.kind,
            'state', a.state,
            'amount_minor', a.amount_minor,
            'currency', a.currency,
            'created_at', a.created_at,
            'updated_at', a.updated_at
        ) ORDER BY a.created_at,a.adjustment_id)
        FROM public.affiliate_credit_reversal_adjustments a
        JOIN public.affiliate_enrollments e ON e.affiliate_id=a.affiliate_id
        WHERE e.user_id=p_user_id
    ), '[]'::jsonb),
    'support_requests', COALESCE((
        SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'request_id', r.request_id,
            'kind', r.kind,
            'commission_entry_id', r.commission_entry_id,
            'state', r.state,
            'outcome', r.outcome,
            'version', r.version,
            'created_at', r.created_at,
            'updated_at', r.updated_at
        )) ORDER BY r.created_at, r.request_id)
        FROM public.affiliate_support_requests r
        WHERE r.user_id = p_user_id
    ), '[]'::jsonb),
    'support_events', COALESCE((
        SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'event_id', v.event_id,
            'request_id', v.request_id,
            'version', v.version,
            'action', v.action,
            'state', v.state,
            'outcome', v.outcome,
            'occurred_at', v.occurred_at
        )) ORDER BY v.occurred_at, v.event_id)
        FROM public.affiliate_support_request_events v
        JOIN public.affiliate_support_requests r ON r.request_id = v.request_id
        WHERE r.user_id = p_user_id
    ), '[]'::jsonb)
)
$$;

REVOKE ALL ON TABLE affiliate_provider_adverse_invoice_lines,affiliate_commission_invoice_payments,affiliate_commission_invoice_lines,
    affiliate_settlement_policies,affiliate_settlement_policy_current,affiliate_settlement_policy_events,affiliate_credit_reservations,
    affiliate_credit_allocations,affiliate_credit_reservation_events,affiliate_credit_reversal_adjustments,
    affiliate_credit_reversal_adjustment_events FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_export_affiliate_data(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_publish_affiliate_settlement_policy(uuid,bigint,bigint,bigint,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_reserve_affiliate_support_check(uuid,uuid,uuid,uuid,bigint,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_transition_affiliate_support_check(uuid,uuid,bigint,text,text,text,text) FROM PUBLIC;

COMMIT;
