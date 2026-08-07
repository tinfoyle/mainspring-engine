CREATE TABLE persona_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    persona_id UUID NOT NULL REFERENCES personas(id) ON DELETE RESTRICT,
    version INTEGER NOT NULL CHECK (version > 0),
    content_hash BYTEA NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL,
    system_instructions TEXT NOT NULL,
    tool_grants JSONB NOT NULL DEFAULT '[]'::jsonb,
    output_schema JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (persona_id, version),
    UNIQUE (persona_id, content_hash)
);

CREATE TABLE boardroom_run_personas (
    run_id UUID NOT NULL REFERENCES boardroom_runs(id) ON DELETE CASCADE,
    turn_number INTEGER NOT NULL CHECK (turn_number > 0),
    persona_id UUID NOT NULL REFERENCES personas(id) ON DELETE RESTRICT,
    persona_version_id UUID NOT NULL REFERENCES persona_versions(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, turn_number),
    UNIQUE (run_id, persona_id)
);

CREATE TABLE agent_invocations (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES boardroom_runs(id) ON DELETE CASCADE,
    turn_number INTEGER NOT NULL CHECK (turn_number > 0),
    persona_version_id UUID NOT NULL REFERENCES persona_versions(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'canceled')),
    provider TEXT NOT NULL,
    model TEXT,
    provider_thread_id TEXT,
    context_manifest JSONB NOT NULL DEFAULT '{}'::jsonb,
    output_schema JSONB NOT NULL,
    result_payload JSONB,
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    cached_input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    estimated_cost_micros BIGINT NOT NULL DEFAULT 0 CHECK (estimated_cost_micros >= 0),
    error_category TEXT,
    error_detail TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, turn_number)
);

CREATE INDEX agent_invocations_run_created_idx ON agent_invocations(run_id, created_at);
CREATE INDEX agent_invocations_status_created_idx ON agent_invocations(status, created_at);

CREATE TABLE agent_invocation_events (
    id BIGSERIAL PRIMARY KEY,
    invocation_id UUID NOT NULL REFERENCES agent_invocations(id) ON DELETE CASCADE,
    event_sequence INTEGER NOT NULL CHECK (event_sequence > 0),
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (invocation_id, event_sequence)
);

ALTER TABLE boardroom_messages ADD COLUMN invocation_id UUID REFERENCES agent_invocations(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX boardroom_messages_invocation_idx ON boardroom_messages(invocation_id) WHERE invocation_id IS NOT NULL;
