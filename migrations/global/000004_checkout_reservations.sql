BEGIN;

CREATE TABLE billing_checkout_attempts (
    request_id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    provider text NOT NULL CHECK (provider = 'stripe'),
    mode text NOT NULL CHECK (mode IN ('test','live')),
    offer_code text NOT NULL,
    state text NOT NULL CHECK (state IN ('active','expired')),
    provider_session_id text,
    hosted_url text,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT billing_checkout_session_pair CHECK (
        (provider_session_id IS NULL AND hosted_url IS NULL) OR
        (provider_session_id IS NOT NULL AND hosted_url IS NOT NULL)
    )
);
CREATE UNIQUE INDEX billing_checkout_one_active_account
    ON billing_checkout_attempts (account_id,provider,mode) WHERE state='active';
CREATE INDEX billing_checkout_expiry ON billing_checkout_attempts (expires_at) WHERE state='active';

COMMIT;
