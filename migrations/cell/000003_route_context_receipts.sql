BEGIN;

CREATE TABLE spyglass.route_context_receipts (
    account_id uuid NOT NULL,
    request_id uuid NOT NULL,
    placement_generation bigint NOT NULL CHECK (placement_generation > 0),
    entitlement_version bigint NOT NULL CHECK (entitlement_version > 0),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user', 'workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 200),
    method text NOT NULL CHECK (char_length(method) BETWEEN 1 AND 20),
    target_sha256 bytea NOT NULL CHECK (octet_length(target_sha256) = 32),
    body_sha256 bytea NOT NULL CHECK (octet_length(body_sha256) = 32),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > issued_at),
    consumed_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, request_id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces (account_id)
);
CREATE INDEX route_context_receipts_expiry ON spyglass.route_context_receipts (account_id, expires_at);

ALTER TABLE spyglass.route_context_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.route_context_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY route_context_receipts_isolation ON spyglass.route_context_receipts
    USING (account_id = nullif(current_setting('app.account_id', true), '')::uuid)
    WITH CHECK (account_id = nullif(current_setting('app.account_id', true), '')::uuid);

COMMIT;
