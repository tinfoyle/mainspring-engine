ALTER TABLE personas
    ADD COLUMN description TEXT NOT NULL DEFAULT '',
    ADD COLUMN provider TEXT NOT NULL DEFAULT 'inherit'
        CHECK (provider IN ('inherit', 'mock', 'codex')),
    ADD COLUMN model TEXT NOT NULL DEFAULT '',
    ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT 'inherit'
        CHECK (reasoning_effort IN ('inherit', 'minimal', 'low', 'medium', 'high', 'xhigh')),
    ADD COLUMN temperature DOUBLE PRECISION
        CHECK (temperature IS NULL OR temperature BETWEEN 0 AND 2),
    ADD COLUMN top_p DOUBLE PRECISION
        CHECK (top_p IS NULL OR top_p BETWEEN 0 AND 1),
    ADD COLUMN context_token_limit INTEGER NOT NULL DEFAULT 12000
        CHECK (context_token_limit BETWEEN 1000 AND 200000),
    ADD COLUMN max_output_tokens INTEGER NOT NULL DEFAULT 2000
        CHECK (max_output_tokens BETWEEN 128 AND 64000),
    ADD COLUMN timeout_seconds INTEGER NOT NULL DEFAULT 300
        CHECK (timeout_seconds BETWEEN 10 AND 3600),
    ADD COLUMN max_tool_calls INTEGER NOT NULL DEFAULT 5
        CHECK (max_tool_calls BETWEEN 0 AND 20),
    ADD COLUMN max_cost_micros BIGINT NOT NULL DEFAULT 1
        CHECK (max_cost_micros BETWEEN 0 AND 1000000000),
    ADD COLUMN response_style TEXT NOT NULL DEFAULT 'balanced'
        CHECK (response_style IN ('concise', 'balanced', 'detailed')),
    ADD COLUMN citation_policy TEXT NOT NULL DEFAULT 'when_available'
        CHECK (citation_policy IN ('when_available', 'required_for_research', 'always')),
    ADD COLUMN action_policy TEXT NOT NULL DEFAULT 'propose_only'
        CHECK (action_policy IN ('disabled', 'propose_only'));

ALTER TABLE persona_versions
    ADD COLUMN description TEXT NOT NULL DEFAULT '',
    ADD COLUMN runtime_config JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX personas_boardroom_enabled_position_idx
    ON personas(boardroom_id, enabled, position);
