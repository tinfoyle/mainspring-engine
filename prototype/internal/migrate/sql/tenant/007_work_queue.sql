CREATE TABLE work_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    number BIGSERIAL NOT NULL UNIQUE,
    kind TEXT NOT NULL DEFAULT 'todo'
        CHECK (kind IN ('todo', 'ticket')),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'in_progress', 'waiting', 'done', 'canceled')),
    priority TEXT NOT NULL DEFAULT 'normal'
        CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    source TEXT NOT NULL DEFAULT 'user'
        CHECK (source IN ('user', 'persona', 'schedule', 'system')),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    assigned_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    assigned_persona_id UUID REFERENCES personas(id) ON DELETE SET NULL,
    boardroom_id UUID REFERENCES boardrooms(id) ON DELETE SET NULL,
    conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    run_id UUID REFERENCES boardroom_runs(id) ON DELETE SET NULL,
    due_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX work_items_queue_idx
    ON work_items(status, priority, due_at, created_at DESC);

CREATE INDEX work_items_assigned_user_idx
    ON work_items(assigned_user_id, status, updated_at DESC)
    WHERE assigned_user_id IS NOT NULL;

CREATE INDEX work_items_assigned_persona_idx
    ON work_items(assigned_persona_id, status, updated_at DESC)
    WHERE assigned_persona_id IS NOT NULL;

CREATE INDEX work_items_run_idx
    ON work_items(run_id, created_at DESC)
    WHERE run_id IS NOT NULL;
