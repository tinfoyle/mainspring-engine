BEGIN;

ALTER TABLE billing_checkout_attempts
    DROP CONSTRAINT billing_checkout_attempts_state_check,
    ADD COLUMN purchase_kind text NOT NULL DEFAULT 'subscription'
        CHECK (purchase_kind IN ('subscription','ai_token_top_up','commissioning')),
    ADD COLUMN item_version bigint CHECK (item_version > 0),
    ADD COLUMN catalog_version bigint REFERENCES catalog_publications (version),
    ADD COLUMN currency text CHECK (currency = 'USD'),
    ADD COLUMN amount_minor bigint CHECK (amount_minor > 0),
    ADD COLUMN quantity bigint CHECK (quantity > 0),
    ADD COLUMN provider_payment_intent_id text,
    ADD COLUMN commissioning_code text,
    ADD COLUMN commissioning_version bigint CHECK (commissioning_version > 0),
    ADD COLUMN commissioning_catalog_version bigint REFERENCES catalog_publications (version),
    ADD CONSTRAINT billing_checkout_attempts_state_check
        CHECK (state IN ('active','expired','completed','failed','refunded','disputed')),
    ADD CONSTRAINT billing_checkout_attempts_purchase_snapshot CHECK (
        (purchase_kind='subscription' AND item_version IS NULL AND catalog_version IS NULL AND currency IS NULL AND amount_minor IS NULL AND quantity IS NULL) OR
        (purchase_kind='ai_token_top_up' AND item_version IS NOT NULL AND catalog_version IS NOT NULL AND currency='USD' AND amount_minor IS NOT NULL AND quantity IS NOT NULL) OR
        (purchase_kind='commissioning' AND item_version IS NOT NULL AND catalog_version IS NOT NULL AND currency='USD' AND amount_minor IS NOT NULL AND quantity IS NULL)
    ),
    ADD CONSTRAINT billing_checkout_attempts_commissioning_snapshot CHECK (
        (commissioning_code IS NULL AND commissioning_version IS NULL AND commissioning_catalog_version IS NULL) OR
        (purchase_kind='subscription' AND commissioning_code IS NOT NULL AND commissioning_version IS NOT NULL AND commissioning_catalog_version IS NOT NULL)
    );

DROP INDEX billing_checkout_one_active_account;
CREATE UNIQUE INDEX billing_checkout_one_active_account_kind
    ON billing_checkout_attempts (account_id,provider,mode,purchase_kind)
    WHERE state='active';
CREATE UNIQUE INDEX billing_checkout_provider_session
    ON billing_checkout_attempts (provider,mode,provider_session_id)
    WHERE provider_session_id IS NOT NULL;
CREATE UNIQUE INDEX billing_checkout_provider_payment
    ON billing_checkout_attempts (provider,mode,provider_payment_intent_id)
    WHERE provider_payment_intent_id IS NOT NULL;
CREATE UNIQUE INDEX billing_checkout_one_completed_commissioning
    ON billing_checkout_attempts (account_id)
    WHERE purchase_kind='commissioning' AND state='completed';

CREATE TABLE account_commissioning_purchases (
    account_id uuid PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    checkout_request_id uuid NOT NULL UNIQUE REFERENCES billing_checkout_attempts (request_id) ON DELETE CASCADE,
    item_code text NOT NULL CHECK (item_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    item_version bigint NOT NULL CHECK (item_version > 0),
    catalog_version bigint NOT NULL REFERENCES catalog_publications (version),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency text NOT NULL CHECK (currency = 'USD'),
    provider_reference text NOT NULL UNIQUE,
    purchased_at timestamptz NOT NULL
);

CREATE TABLE ai_token_promotion_issuances (
    grant_id uuid PRIMARY KEY REFERENCES ai_token_grants (id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    campaign_code text NOT NULL CHECK (campaign_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    campaign_version bigint NOT NULL CHECK (campaign_version > 0),
    catalog_version bigint NOT NULL REFERENCES catalog_publications (version),
    source_reference uuid NOT NULL,
    issued_at timestamptz NOT NULL,
    UNIQUE (account_id,source_reference)
);
CREATE INDEX ai_token_promotion_campaign_count
    ON ai_token_promotion_issuances (campaign_code,campaign_version,grant_id);

REVOKE ALL ON TABLE account_commissioning_purchases,ai_token_promotion_issuances FROM PUBLIC;

COMMIT;
