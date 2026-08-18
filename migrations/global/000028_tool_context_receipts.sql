BEGIN;

-- One receipt is written before a broker-to-router tool dispatch is allowed
-- to resolve Account authority. Inputs and outputs are never retained here.
CREATE TABLE tool_context_receipts (
    request_id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    invocation_id uuid NOT NULL,
    pod_uid uuid NOT NULL,
    operation_id uuid NOT NULL,
    capability text NOT NULL CHECK (capability ~ '^[a-z][a-z0-9.:/-]{0,127}$'),
    method text NOT NULL CHECK (method = 'POST'),
    target_sha256 bytea NOT NULL CHECK (octet_length(target_sha256) = 32),
    body_sha256 bytea NOT NULL CHECK (octet_length(body_sha256) = 32),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz NOT NULL,
    CHECK (expires_at > issued_at),
    CHECK (consumed_at >= issued_at - interval '5 seconds')
);
CREATE INDEX tool_context_receipts_expiry ON tool_context_receipts(expires_at,request_id);
CREATE INDEX tool_context_receipts_invocation ON tool_context_receipts(account_id,invocation_id,operation_id);

COMMIT;
