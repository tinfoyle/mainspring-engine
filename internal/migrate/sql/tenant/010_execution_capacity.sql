ALTER TABLE boardroom_runs DROP CONSTRAINT boardroom_runs_status_check;
ALTER TABLE boardroom_runs ADD CONSTRAINT boardroom_runs_status_check
    CHECK (status IN ('pending', 'queued', 'preparing', 'running', 'awaiting_approval', 'completed', 'failed', 'canceled'));

CREATE TABLE tenant_execution_policy (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    max_concurrent_invocations INTEGER NOT NULL DEFAULT 2 CHECK (max_concurrent_invocations BETWEEN 1 AND 100),
    max_queued_runs INTEGER NOT NULL DEFAULT 100 CHECK (max_queued_runs BETWEEN 1 AND 10000),
    monthly_token_limit BIGINT NOT NULL DEFAULT 10000000 CHECK (monthly_token_limit > 0),
    monthly_cost_limit_micros BIGINT NOT NULL DEFAULT 100000000 CHECK (monthly_cost_limit_micros > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO tenant_execution_policy (singleton) VALUES (true) ON CONFLICT DO NOTHING;

CREATE TABLE usage_reservations (
    invocation_id UUID PRIMARY KEY REFERENCES agent_invocations(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'reconciled', 'released')),
    reserved_tokens BIGINT NOT NULL CHECK (reserved_tokens >= 0),
    reserved_cost_micros BIGINT NOT NULL CHECK (reserved_cost_micros >= 0),
    actual_tokens BIGINT NOT NULL DEFAULT 0 CHECK (actual_tokens >= 0),
    actual_cost_micros BIGINT NOT NULL DEFAULT 0 CHECK (actual_cost_micros >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reconciled_at TIMESTAMPTZ
);
CREATE INDEX usage_reservations_active_idx ON usage_reservations(status, created_at) WHERE status='active';

CREATE TABLE provider_circuits (
    provider TEXT PRIMARY KEY,
    consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    open_until TIMESTAMPTZ,
    last_error_category TEXT,
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agent_invocations_completed_usage_idx ON agent_invocations(completed_at) WHERE status='succeeded';
