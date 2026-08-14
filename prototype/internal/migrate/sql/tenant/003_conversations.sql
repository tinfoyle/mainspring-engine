CREATE TABLE conversations (
    id UUID PRIMARY KEY,
    boardroom_id UUID NOT NULL REFERENCES boardrooms(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'user' CHECK (source IN ('user', 'schedule')),
    schedule_id UUID REFERENCES schedules(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'archived')),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE boardroom_runs ADD COLUMN conversation_id UUID;

INSERT INTO conversations (id, boardroom_id, title, source, created_by, created_at, updated_at)
SELECT r.id, r.boardroom_id,
       LEFT(regexp_replace(r.prompt, '\s+', ' ', 'g'), 120),
       CASE WHEN r.created_by IS NULL THEN 'schedule' ELSE 'user' END,
       r.created_by, r.created_at, COALESCE(r.completed_at, r.created_at)
FROM boardroom_runs r;

UPDATE boardroom_runs SET conversation_id = id;

ALTER TABLE boardroom_runs
    ALTER COLUMN conversation_id SET NOT NULL,
    ADD CONSTRAINT boardroom_runs_conversation_fk
        FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE;

CREATE INDEX conversations_boardroom_updated_idx
    ON conversations(boardroom_id, updated_at DESC);

CREATE INDEX boardroom_runs_conversation_created_idx
    ON boardroom_runs(conversation_id, created_at);

CREATE UNIQUE INDEX boardroom_runs_one_active_per_conversation_idx
    ON boardroom_runs(conversation_id)
    WHERE status IN ('pending', 'preparing', 'running', 'awaiting_approval');
