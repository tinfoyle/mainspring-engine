BEGIN;

CREATE TABLE entitlement_usage_counters (
    account_id uuid NOT NULL REFERENCES accounts (id),
    package_code text NOT NULL,
    limit_code text NOT NULL,
    current_value bigint NOT NULL DEFAULT 0 CHECK (current_value >= 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,package_code,limit_code)
);

CREATE TABLE entitlement_usage_reservations (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    request_id uuid NOT NULL,
    package_code text NOT NULL,
    limit_code text NOT NULL,
    amount bigint NOT NULL CHECK (amount > 0),
    maximum_at_admission bigint NOT NULL CHECK (maximum_at_admission > 0),
    entitlement_version bigint NOT NULL CHECK (entitlement_version > 0),
    state text NOT NULL CHECK (state IN ('active','released','expired')),
    expires_at timestamptz,
    created_at timestamptz NOT NULL,
    closed_at timestamptz,
    UNIQUE (account_id,request_id),
    CONSTRAINT entitlement_usage_reservation_amount CHECK (amount <= maximum_at_admission),
    CONSTRAINT entitlement_usage_reservation_window CHECK (expires_at IS NULL OR expires_at > created_at),
    CONSTRAINT entitlement_usage_reservation_state CHECK (
        (state='active' AND closed_at IS NULL) OR
        (state IN ('released','expired') AND closed_at IS NOT NULL)
    )
);
CREATE INDEX entitlement_usage_reservations_expiry
    ON entitlement_usage_reservations (account_id,package_code,limit_code,expires_at,id)
    WHERE state='active' AND expires_at IS NOT NULL;

COMMIT;
