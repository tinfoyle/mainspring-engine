BEGIN;

CREATE TABLE ai_token_grants (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    origin text NOT NULL CHECK (origin IN ('included','purchased','promotion')),
    definition_code text NOT NULL CHECK (definition_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    catalog_version bigint NOT NULL CHECK (catalog_version > 0),
    source_reference text NOT NULL CHECK (length(source_reference) BETWEEN 1 AND 255),
    quantity bigint NOT NULL CHECK (quantity > 0),
    available bigint NOT NULL CHECK (available >= 0),
    reserved bigint NOT NULL CHECK (reserved >= 0),
    consumed bigint NOT NULL CHECK (consumed >= 0),
    state text NOT NULL CHECK (state IN ('active','frozen','expired','reversed','extinguished')),
    expires_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT ai_token_grants_quantity CHECK (available + reserved + consumed <= quantity),
    CONSTRAINT ai_token_grants_origin_expiry CHECK (
        (origin = 'promotion' AND expires_at IS NOT NULL) OR
        (origin = 'purchased' AND expires_at IS NULL) OR
        origin = 'included'
    ),
    CONSTRAINT ai_token_grants_state_balance CHECK (state IN ('active','frozen') OR available = 0),
    UNIQUE (account_id,origin,definition_code,source_reference)
);
CREATE INDEX ai_token_grants_spend_order
    ON ai_token_grants (account_id,expires_at,created_at,id)
    WHERE state='active' AND available > 0;

CREATE TABLE ai_token_reservations (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    request_id uuid NOT NULL,
    rate_code text NOT NULL CHECK (rate_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    rate_version bigint NOT NULL CHECK (rate_version > 0),
    complexity text NOT NULL CHECK (complexity IN ('simple','efficient','balanced','thorough','advanced')),
    rate_snapshot jsonb NOT NULL CHECK (jsonb_typeof(rate_snapshot) = 'object'),
    maximum bigint NOT NULL CHECK (maximum > 0),
    settled bigint NOT NULL DEFAULT 0 CHECK (settled >= 0),
    state text NOT NULL CHECK (state IN ('active','settled','released')),
    created_at timestamptz NOT NULL,
    closed_at timestamptz,
    CONSTRAINT ai_token_reservation_amount CHECK (settled <= maximum),
    CONSTRAINT ai_token_reservation_state CHECK (
        (state='active' AND settled=0 AND closed_at IS NULL) OR
        (state IN ('settled','released') AND closed_at IS NOT NULL)
    ),
    UNIQUE (account_id,request_id)
);

CREATE TABLE ai_token_reservation_allocations (
    reservation_id uuid NOT NULL REFERENCES ai_token_reservations (id) ON DELETE CASCADE,
    grant_id uuid NOT NULL REFERENCES ai_token_grants (id) ON DELETE CASCADE,
    ordinal integer NOT NULL CHECK (ordinal > 0),
    amount bigint NOT NULL CHECK (amount > 0),
    PRIMARY KEY (reservation_id,grant_id),
    UNIQUE (reservation_id,ordinal)
);

CREATE TABLE ai_token_ledger_entries (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    grant_id uuid REFERENCES ai_token_grants (id) ON DELETE CASCADE,
    reservation_id uuid REFERENCES ai_token_reservations (id) ON DELETE CASCADE,
    event_key text NOT NULL CHECK (length(event_key) BETWEEN 1 AND 255),
    kind text NOT NULL CHECK (kind IN ('issued','reserved','settled','released','reversed','expired','extinguished')),
    amount bigint NOT NULL CHECK (amount > 0),
    created_at timestamptz NOT NULL,
    UNIQUE (account_id,event_key)
);
CREATE INDEX ai_token_ledger_entries_account
    ON ai_token_ledger_entries (account_id,created_at DESC,id DESC);

CREATE FUNCTION spyglass_reject_ai_token_ledger_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'AI Token ledger entries are immutable';
END;
$$;
CREATE TRIGGER ai_token_ledger_entries_immutable
BEFORE UPDATE ON ai_token_ledger_entries
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_ai_token_ledger_mutation();

REVOKE ALL ON TABLE ai_token_grants,ai_token_reservations,ai_token_reservation_allocations,ai_token_ledger_entries FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_reject_ai_token_ledger_mutation() FROM PUBLIC;

COMMIT;
