CREATE TABLE human_input_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_work_item_id UUID NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
    work_item_id UUID NOT NULL UNIQUE REFERENCES work_items(id) ON DELETE CASCADE,
    run_id UUID REFERENCES boardroom_runs(id) ON DELETE SET NULL,
    invocation_id UUID UNIQUE REFERENCES agent_invocations(id) ON DELETE SET NULL,
    questions JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(questions) = 'array' AND jsonb_array_length(questions) > 0),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'answered', 'declined')),
    response JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(response) = 'array'),
    response_document_ids JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(response_document_ids) = 'array'),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    answered_at TIMESTAMPTZ,
    answered_by UUID REFERENCES users(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX human_input_requests_one_pending_parent_idx
    ON human_input_requests(parent_work_item_id)
    WHERE status = 'pending';

CREATE INDEX human_input_requests_status_idx
    ON human_input_requests(status, requested_at DESC);
