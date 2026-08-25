BEGIN;

ALTER TABLE affiliate_commission_entries
    ADD COLUMN provider_payment_intent_id text,
    ADD CONSTRAINT affiliate_commission_entries_payment_intent_shape CHECK (
        provider_payment_intent_id IS NULL OR (
            left(provider_payment_intent_id,3) = 'pi_' AND
            length(provider_payment_intent_id) <= 200 AND
            provider_payment_intent_id !~ '[[:space:]]'
        )
    );

CREATE UNIQUE INDEX affiliate_commission_entries_earned_payment_intent
    ON affiliate_commission_entries (provider_payment_intent_id)
    WHERE kind='earned' AND provider_payment_intent_id IS NOT NULL;

CREATE TABLE affiliate_provider_adverse_events (
    provider_event_id text PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('refund','dispute')),
    provider_object_id text NOT NULL,
    provider_payment_intent_id text NOT NULL,
    amount_minor bigint NOT NULL CHECK (amount_minor BETWEEN 1 AND 99999999),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL,
    CONSTRAINT affiliate_provider_adverse_event_id_shape CHECK (
        left(provider_event_id,4) = 'evt_' AND length(provider_event_id) <= 200 AND provider_event_id !~ '[[:space:]]'
    ),
    CONSTRAINT affiliate_provider_adverse_object_shape CHECK (
        (kind='refund' AND left(provider_object_id,3)='re_') OR
        (kind='dispute' AND left(provider_object_id,3)='du_')
    ),
    CONSTRAINT affiliate_provider_adverse_object_bounds CHECK (
        length(provider_object_id) <= 200 AND provider_object_id !~ '[[:space:]]'
    ),
    CONSTRAINT affiliate_provider_adverse_payment_intent_shape CHECK (
        left(provider_payment_intent_id,3)='pi_' AND
        length(provider_payment_intent_id) <= 200 AND
        provider_payment_intent_id !~ '[[:space:]]'
    ),
    CONSTRAINT affiliate_provider_adverse_time_order CHECK (recorded_at >= occurred_at),
    UNIQUE (kind,provider_object_id)
);
CREATE INDEX affiliate_provider_adverse_payment
    ON affiliate_provider_adverse_events (provider_payment_intent_id,kind,provider_object_id);

CREATE TRIGGER affiliate_provider_adverse_events_immutable
BEFORE UPDATE OR DELETE ON affiliate_provider_adverse_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();

REVOKE ALL ON TABLE affiliate_provider_adverse_events FROM PUBLIC;

COMMIT;
