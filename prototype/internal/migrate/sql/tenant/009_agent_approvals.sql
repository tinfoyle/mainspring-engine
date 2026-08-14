ALTER TABLE external_actions
    ADD COLUMN invocation_id UUID REFERENCES agent_invocations(id) ON DELETE SET NULL,
    ADD COLUMN persona_version_id UUID REFERENCES persona_versions(id) ON DELETE SET NULL,
    ADD COLUMN reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN payload_hash BYTEA;

ALTER TABLE external_actions DROP CONSTRAINT external_actions_status_check;
ALTER TABLE external_actions ADD CONSTRAINT external_actions_status_check
    CHECK (status IN ('prepared', 'awaiting_approval', 'executing', 'succeeded', 'failed', 'unknown', 'reconciled', 'manual_review', 'rejected'));

ALTER TABLE approval_requests
    ADD CONSTRAINT approval_requests_action_fk FOREIGN KEY (action_id) REFERENCES external_actions(id) ON DELETE CASCADE;

CREATE INDEX approval_requests_status_requested_idx ON approval_requests(status, requested_at DESC);
CREATE INDEX external_actions_invocation_idx ON external_actions(invocation_id) WHERE invocation_id IS NOT NULL;
