BEGIN;

CREATE TABLE users (
    id uuid PRIMARY KEY,
    primary_email text NOT NULL,
    display_name text NOT NULL,
    state text NOT NULL CHECK (state IN ('pending_verification', 'active', 'suspended')),
    email_verified_at timestamptz,
    security_version bigint NOT NULL DEFAULT 1 CHECK (security_version > 0),
    created_at timestamptz NOT NULL,
    CONSTRAINT users_email_normalized CHECK (primary_email = lower(btrim(primary_email)))
);
CREATE UNIQUE INDEX users_primary_email_unique ON users (primary_email);

CREATE TABLE registration_challenges (
    id uuid PRIMARY KEY,
    proposed_user_id uuid NOT NULL,
    primary_email text NOT NULL,
    display_name text NOT NULL,
    account_name text NOT NULL,
    region text NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT registration_email_normalized CHECK (primary_email = lower(btrim(primary_email)))
);
CREATE INDEX registration_challenges_expiry ON registration_challenges (expires_at) WHERE consumed_at IS NULL;

CREATE TABLE cells (
    id text PRIMARY KEY,
    region text NOT NULL,
    state text NOT NULL CHECK (state IN ('active', 'draining', 'disabled')),
    assigned_accounts integer NOT NULL DEFAULT 0 CHECK (assigned_accounts >= 0),
    soft_account_limit integer NOT NULL CHECK (soft_account_limit > 0),
    created_at timestamptz NOT NULL
);

CREATE TABLE accounts (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    display_name text NOT NULL,
    account_type text NOT NULL CHECK (account_type IN ('free', 'paid', 'internal', 'partner')),
    state text NOT NULL CHECK (state IN ('active', 'restricted', 'suspended', 'closing', 'closed')),
    cell_id text NOT NULL REFERENCES cells (id),
    placement_generation bigint NOT NULL DEFAULT 1 CHECK (placement_generation > 0),
    entitlement_version bigint NOT NULL DEFAULT 1 CHECK (entitlement_version > 0),
    created_by_user_id uuid NOT NULL REFERENCES users (id),
    created_at timestamptz NOT NULL
);
CREATE INDEX accounts_cell ON accounts (cell_id, state);

CREATE TABLE memberships (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    user_id uuid NOT NULL REFERENCES users (id),
    role text NOT NULL CHECK (role IN ('owner', 'administrator', 'billing_admin', 'member', 'viewer')),
    state text NOT NULL CHECK (state IN ('invited', 'active', 'suspended', 'removed')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL,
    UNIQUE (account_id, user_id)
);
CREATE UNIQUE INDEX memberships_one_active_owner ON memberships (account_id) WHERE role = 'owner' AND state = 'active';
CREATE INDEX memberships_user_active ON memberships (user_id, account_id) WHERE state = 'active';

CREATE TABLE catalog_publications (
    version bigint PRIMARY KEY CHECK (version > 0),
    state text NOT NULL CHECK (state IN ('draft', 'published', 'retired')),
    published_at timestamptz,
    content jsonb NOT NULL,
    content_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL
);

CREATE TABLE billing_profiles (
    account_id uuid PRIMARY KEY REFERENCES accounts (id),
    stripe_customer_id text UNIQUE,
    billing_email text,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE subscriptions (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    provider text NOT NULL CHECK (provider = 'stripe'),
    provider_subscription_id text NOT NULL UNIQUE,
    state text NOT NULL,
    offer_code text NOT NULL,
    offer_version bigint NOT NULL,
    current_period_start timestamptz,
    current_period_end timestamptz,
    cancel_at timestamptz,
    provider_object_version text,
    last_synced_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX subscriptions_account ON subscriptions (account_id, state);

CREATE TABLE billing_event_inbox (
    provider_event_id text PRIMARY KEY,
    event_type text NOT NULL,
    provider_created_at timestamptz NOT NULL,
    provider_object_id text,
    mode text NOT NULL CHECK (mode IN ('test', 'live')),
    payload_hash bytea NOT NULL,
    payload_reference text NOT NULL,
    signature_verified_at timestamptz NOT NULL,
    processing_state text NOT NULL CHECK (processing_state IN ('accepted', 'processing', 'processed', 'failed')),
    attempt_count integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz,
    last_error_code text,
    processed_at timestamptz,
    created_at timestamptz NOT NULL
);
CREATE INDEX billing_event_inbox_pending ON billing_event_inbox (next_attempt_at, provider_created_at) WHERE processing_state IN ('accepted', 'failed');

CREATE TABLE entitlement_grants (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    package_code text NOT NULL,
    package_version bigint NOT NULL CHECK (package_version > 0),
    mode text NOT NULL CHECK (mode IN ('enabled', 'read_only', 'suspended')),
    source text NOT NULL CHECK (source IN ('free_plan', 'subscription', 'trial', 'promotion', 'support_override', 'grandfathered')),
    source_reference text NOT NULL,
    limits jsonb NOT NULL DEFAULT '{}',
    starts_at timestamptz NOT NULL,
    ends_at timestamptz,
    priority integer NOT NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT entitlement_grant_window CHECK (ends_at IS NULL OR ends_at > starts_at)
);
CREATE INDEX entitlement_grants_effective ON entitlement_grants (account_id, package_code, starts_at, ends_at);

CREATE TABLE entitlement_snapshots (
    account_id uuid NOT NULL REFERENCES accounts (id),
    version bigint NOT NULL CHECK (version > 0),
    catalog_version bigint NOT NULL REFERENCES catalog_publications (version),
    evaluated_at timestamptz NOT NULL,
    source_hash bytea NOT NULL,
    effective_packages jsonb NOT NULL,
    PRIMARY KEY (account_id, version)
);

CREATE TABLE account_directory (
    account_id uuid PRIMARY KEY REFERENCES accounts (id),
    cell_id text NOT NULL REFERENCES cells (id),
    placement_generation bigint NOT NULL CHECK (placement_generation > 0),
    state text NOT NULL CHECK (state IN ('active', 'draining', 'frozen', 'moving', 'disabled')),
    data_region text NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX account_directory_cell ON account_directory (cell_id, state);

COMMIT;
