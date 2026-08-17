BEGIN;

CREATE SCHEMA IF NOT EXISTS spyglass;

CREATE TABLE spyglass.account_namespaces (
    account_id uuid PRIMARY KEY,
    placement_generation bigint NOT NULL CHECK (placement_generation > 0),
    state text NOT NULL CHECK (state IN ('active', 'draining', 'frozen', 'moving', 'disabled')),
    created_at timestamptz NOT NULL
);

CREATE TABLE spyglass.account_audit_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    event_type text NOT NULL,
    actor_kind text NOT NULL,
    actor_id text NOT NULL,
    correlation_id text NOT NULL,
    redacted_payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces (account_id)
);
CREATE INDEX account_audit_events_time ON spyglass.account_audit_events (account_id, occurred_at DESC);

ALTER TABLE spyglass.account_namespaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.account_namespaces FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.account_audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.account_audit_events FORCE ROW LEVEL SECURITY;

CREATE POLICY account_namespaces_isolation ON spyglass.account_namespaces
    USING (account_id = nullif(current_setting('app.account_id', true), '')::uuid)
    WITH CHECK (account_id = nullif(current_setting('app.account_id', true), '')::uuid);

CREATE POLICY account_audit_events_isolation ON spyglass.account_audit_events
    USING (account_id = nullif(current_setting('app.account_id', true), '')::uuid)
    WITH CHECK (account_id = nullif(current_setting('app.account_id', true), '')::uuid);

-- Serving roles must be created outside this migration by deployment automation.
-- They must not own these tables and must never receive BYPASSRLS.

COMMIT;
