BEGIN;

CREATE TABLE privacy_consent_subjects (
    id uuid PRIMARY KEY,
    user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL,
    linked_at timestamptz,
    CONSTRAINT privacy_consent_subject_link_shape CHECK (
        (user_id IS NULL AND linked_at IS NULL) OR
        (user_id IS NOT NULL AND linked_at IS NOT NULL AND linked_at >= created_at)
    )
);
CREATE INDEX privacy_consent_subjects_user ON privacy_consent_subjects (user_id) WHERE user_id IS NOT NULL;

CREATE TABLE privacy_consent_decisions (
    decision_id uuid PRIMARY KEY,
    subject_id uuid NOT NULL REFERENCES privacy_consent_subjects (id) ON DELETE CASCADE,
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    surface text NOT NULL CHECK (surface IN ('public','private')),
    analytics boolean NOT NULL,
    marketing boolean NOT NULL,
    effective_at timestamptz NOT NULL
);
CREATE INDEX privacy_consent_decisions_current
    ON privacy_consent_decisions (subject_id,surface,effective_at DESC,decision_id DESC);

CREATE TABLE analytics_events (
    event_id uuid PRIMARY KEY,
    subject_id uuid NOT NULL REFERENCES privacy_consent_subjects (id) ON DELETE CASCADE,
    consent_decision_id uuid NOT NULL REFERENCES privacy_consent_decisions (decision_id) ON DELETE CASCADE,
    event_name text NOT NULL CHECK (event_name IN (
        'landing_viewed','primary_cta_selected','feature_viewed','pricing_viewed','offer_selected',
        'signup_handoff_started','registration_started','verification_completed','account_created',
        'security_enrollment_completed','checkout_reviewed','referral_code_accepted','checkout_redirected',
        'checkout_returned','subscription_projected','application_entered','your_turn_opened',
        'your_turn_item_completed'
    )),
    surface text NOT NULL CHECK (surface IN ('public','private')),
    occurred_at timestamptz NOT NULL,
    fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    ingested_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CONSTRAINT analytics_events_fields_object CHECK (
        jsonb_typeof(fields) = 'object' AND
        jsonb_array_length(jsonb_path_query_array(fields,'$.keyvalue()')) <= 8
    )
);
CREATE INDEX analytics_events_funnel ON analytics_events (event_name,occurred_at);
CREATE INDEX analytics_events_retention ON analytics_events (ingested_at,event_id);

CREATE TABLE affiliate_enrollments (
    affiliate_id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    settlement_account_id uuid REFERENCES accounts (id) ON DELETE SET NULL,
    public_code text NOT NULL,
    terms_version bigint NOT NULL CHECK (terms_version > 0),
    rule_version bigint NOT NULL CHECK (rule_version > 0),
    state text NOT NULL CHECK (state IN ('active','suspended','closed')),
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT affiliate_enrollments_code_normalized CHECK (public_code = upper(btrim(public_code))),
    CONSTRAINT affiliate_enrollments_code_shape CHECK (public_code ~ '^[A-Z0-9][A-Z0-9-]{4,22}[A-Z0-9]$'),
    CONSTRAINT affiliate_enrollments_time_order CHECK (updated_at >= created_at),
    UNIQUE (user_id),
    UNIQUE (public_code)
);

CREATE TABLE affiliate_commission_rules (
    rule_id uuid PRIMARY KEY,
    version bigint NOT NULL UNIQUE CHECK (version > 0),
    offer_code text NOT NULL,
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    eligible_invoice_minor bigint NOT NULL CHECK (eligible_invoice_minor > 0),
    commission_minor bigint NOT NULL CHECK (commission_minor > 0 AND commission_minor <= eligible_invoice_minor),
    initial_invoice_qualifies boolean NOT NULL,
    maximum_cycles integer NOT NULL CHECK (maximum_cycles >= 0),
    hold_days integer NOT NULL CHECK (hold_days BETWEEN 0 AND 180),
    effective_from timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CONSTRAINT affiliate_commission_rules_offer_shape CHECK (offer_code ~ '^[a-z0-9][a-z0-9_-]{0,99}$')
);

CREATE TABLE affiliate_attributions (
    attribution_id uuid PRIMARY KEY,
    affiliate_id uuid NOT NULL REFERENCES affiliate_enrollments (affiliate_id),
    referred_account_id uuid NOT NULL REFERENCES accounts (id),
    checkout_request_id uuid NOT NULL REFERENCES billing_checkout_attempts (request_id) ON DELETE CASCADE,
    offer_code text NOT NULL,
    offer_version bigint NOT NULL CHECK (offer_version > 0),
    rule_version bigint NOT NULL REFERENCES affiliate_commission_rules (version),
    state text NOT NULL CHECK (state IN ('reserved','locked','canceled')),
    provider_subscription_id text,
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    locked_at timestamptz,
    CONSTRAINT affiliate_attributions_offer_shape CHECK (offer_code ~ '^[a-z0-9][a-z0-9_-]{0,99}$'),
    CONSTRAINT affiliate_attributions_lock_shape CHECK (
        (state = 'reserved' AND provider_subscription_id IS NULL AND locked_at IS NULL) OR
        (state = 'locked' AND provider_subscription_id LIKE 'sub\_%' ESCAPE '\\' AND locked_at IS NOT NULL AND locked_at >= created_at) OR
        (state = 'canceled' AND provider_subscription_id IS NULL)
    ),
    UNIQUE (checkout_request_id),
    UNIQUE (provider_subscription_id)
);
CREATE INDEX affiliate_attributions_affiliate ON affiliate_attributions (affiliate_id,created_at DESC);
CREATE INDEX affiliate_attributions_account ON affiliate_attributions (referred_account_id,created_at DESC);

CREATE TABLE affiliate_commission_entries (
    entry_id uuid PRIMARY KEY,
    affiliate_id uuid NOT NULL REFERENCES affiliate_enrollments (affiliate_id),
    attribution_id uuid NOT NULL REFERENCES affiliate_attributions (attribution_id),
    rule_version bigint NOT NULL REFERENCES affiliate_commission_rules (version),
    provider_subscription_id text NOT NULL,
    provider_invoice_id text NOT NULL,
    cycle integer NOT NULL CHECK (cycle > 0),
    kind text NOT NULL CHECK (kind IN ('earned','reversal')),
    state text NOT NULL CHECK (state IN ('pending','settled')),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    reverses_entry_id uuid UNIQUE REFERENCES affiliate_commission_entries (entry_id),
    available_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT affiliate_commission_entries_provider_shape CHECK (
        provider_subscription_id LIKE 'sub\_%' ESCAPE '\\' AND
        provider_invoice_id LIKE 'in\_%' ESCAPE '\\'
    ),
    CONSTRAINT affiliate_commission_entries_kind_shape CHECK (
        (kind = 'earned' AND state = 'pending' AND reverses_entry_id IS NULL AND available_at >= created_at) OR
        (kind = 'reversal' AND state = 'settled' AND reverses_entry_id IS NOT NULL)
    ),
    UNIQUE (provider_subscription_id,provider_invoice_id,rule_version,kind)
);
CREATE INDEX affiliate_commission_entries_statement
    ON affiliate_commission_entries (affiliate_id,created_at DESC,entry_id DESC);
CREATE INDEX affiliate_commission_entries_available
    ON affiliate_commission_entries (available_at,entry_id) WHERE state = 'pending' AND kind = 'earned';

CREATE FUNCTION spyglass_reject_privacy_evidence_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'privacy consent decisions are immutable';
END;
$$;
CREATE TRIGGER privacy_consent_decisions_immutable
BEFORE UPDATE OR DELETE ON privacy_consent_decisions
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_privacy_evidence_mutation();

CREATE FUNCTION spyglass_reject_analytics_event_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'analytics events cannot be updated';
END;
$$;
CREATE TRIGGER analytics_events_no_update
BEFORE UPDATE ON analytics_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_analytics_event_update();

CREATE FUNCTION spyglass_reject_affiliate_ledger_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'affiliate commission rules and entries are immutable';
END;
$$;
CREATE TRIGGER affiliate_commission_rules_immutable
BEFORE UPDATE OR DELETE ON affiliate_commission_rules
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();
CREATE TRIGGER affiliate_commission_entries_immutable
BEFORE UPDATE OR DELETE ON affiliate_commission_entries
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();

REVOKE ALL ON TABLE privacy_consent_subjects,privacy_consent_decisions,analytics_events,
    affiliate_enrollments,affiliate_commission_rules,affiliate_attributions,affiliate_commission_entries FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_reject_privacy_evidence_mutation(),spyglass_reject_analytics_event_update(),
    spyglass_reject_affiliate_ledger_mutation() FROM PUBLIC;

COMMIT;
