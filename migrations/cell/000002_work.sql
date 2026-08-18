BEGIN;

CREATE TABLE spyglass.work_item_number_counters (
    account_id uuid PRIMARY KEY,
    next_number bigint NOT NULL CHECK (next_number > 0),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces (account_id)
);

CREATE TABLE spyglass.work_items (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    number bigint NOT NULL CHECK (number > 0),
    parent_id uuid,
    depth smallint NOT NULL CHECK (depth BETWEEN 0 AND 3),
    kind text NOT NULL CHECK (kind IN ('todo', 'ticket')),
    title text NOT NULL CHECK (char_length(title) BETWEEN 2 AND 240),
    description text NOT NULL CHECK (char_length(description) <= 20000),
    state text NOT NULL CHECK (state IN ('open', 'in_progress', 'waiting', 'done', 'canceled')),
    priority text NOT NULL CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    responsibility text NOT NULL CHECK (responsibility IN ('user', 'persona', 'shared', 'external')),
    assignee_user_id uuid,
    assignee_persona_id uuid,
    external_assignee_ref text,
    source text NOT NULL CHECK (source IN ('manual', 'baseline', 'schedule', 'conversation', 'run', 'system')),
    created_by_actor_kind text NOT NULL CHECK (created_by_actor_kind IN ('user', 'workload')),
    created_by_actor_id text NOT NULL CHECK (char_length(created_by_actor_id) BETWEEN 1 AND 200),
    baseline_requirement_id uuid,
    schedule_id uuid,
    conversation_id uuid,
    run_id uuid,
    due_at timestamptz,
    completed_at timestamptz,
    capacity_reservation_id uuid NOT NULL,
    capacity_released_at timestamptz,
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    PRIMARY KEY (account_id, id),
    UNIQUE (account_id, number),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces (account_id),
    FOREIGN KEY (account_id, parent_id) REFERENCES spyglass.work_items (account_id, id),
    CHECK (parent_id IS NOT NULL OR depth = 0),
    CHECK (parent_id IS NULL OR parent_id <> id),
    CHECK (
        (responsibility = 'user' AND assignee_user_id IS NOT NULL AND assignee_persona_id IS NULL AND external_assignee_ref IS NULL) OR
        (responsibility = 'persona' AND assignee_user_id IS NULL AND assignee_persona_id IS NOT NULL AND external_assignee_ref IS NULL) OR
        (responsibility = 'shared' AND assignee_user_id IS NULL AND assignee_persona_id IS NULL AND external_assignee_ref IS NULL) OR
        (responsibility = 'external' AND assignee_user_id IS NULL AND assignee_persona_id IS NULL AND char_length(external_assignee_ref) BETWEEN 2 AND 200)
    ),
    CHECK (
        (source IN ('manual', 'system')) OR
        (source = 'baseline' AND baseline_requirement_id IS NOT NULL) OR
        (source = 'schedule' AND schedule_id IS NOT NULL) OR
        (source = 'conversation' AND conversation_id IS NOT NULL) OR
        (source = 'run' AND run_id IS NOT NULL)
    ),
    CHECK ((state = 'done' AND completed_at IS NOT NULL) OR (state <> 'done' AND completed_at IS NULL)),
    CHECK (capacity_released_at IS NULL OR state IN ('done', 'canceled'))
);

CREATE INDEX work_items_queue ON spyglass.work_items (account_id, updated_at DESC, id DESC);
CREATE INDEX work_items_children ON spyglass.work_items (account_id, parent_id, created_at, id) WHERE parent_id IS NOT NULL;
CREATE INDEX work_items_state_priority ON spyglass.work_items (account_id, state, priority, due_at);
CREATE INDEX work_items_user_assignment ON spyglass.work_items (account_id, assignee_user_id, state) WHERE assignee_user_id IS NOT NULL;
CREATE INDEX work_items_persona_assignment ON spyglass.work_items (account_id, assignee_persona_id, state) WHERE assignee_persona_id IS NOT NULL;
CREATE INDEX work_items_pending_capacity_release ON spyglass.work_items (account_id, updated_at) WHERE state IN ('done', 'canceled') AND capacity_released_at IS NULL;

CREATE FUNCTION spyglass.validate_work_item_parent() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE parent_depth smallint;
BEGIN
    IF NEW.parent_id IS NULL THEN
        IF NEW.depth <> 0 THEN RAISE EXCEPTION 'root work item depth must be zero' USING ERRCODE = '23514'; END IF;
        RETURN NEW;
    END IF;
    SELECT depth INTO parent_depth FROM spyglass.work_items
      WHERE account_id = NEW.account_id AND id = NEW.parent_id;
    IF NOT FOUND THEN RAISE EXCEPTION 'work item parent not found' USING ERRCODE = '23503'; END IF;
    IF parent_depth >= 3 OR NEW.depth <> parent_depth + 1 THEN
        RAISE EXCEPTION 'work item hierarchy exceeds maximum depth or has invalid depth' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER work_items_validate_parent
BEFORE INSERT OR UPDATE OF parent_id, depth ON spyglass.work_items
FOR EACH ROW EXECUTE FUNCTION spyglass.validate_work_item_parent();

CREATE TABLE spyglass.work_item_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('created', 'transitioned', 'assigned')),
    from_version bigint NOT NULL CHECK (from_version >= 0),
    to_version bigint NOT NULL CHECK (to_version = from_version + 1),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user', 'workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 200),
    reason text NOT NULL CHECK (char_length(reason) <= 1000),
    correlation_id text NOT NULL CHECK (char_length(correlation_id) BETWEEN 1 AND 200),
    redacted_payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id, work_item_id) REFERENCES spyglass.work_items (account_id, id)
);
CREATE INDEX work_item_events_item ON spyglass.work_item_events (account_id, work_item_id, occurred_at, id);

ALTER TABLE spyglass.work_item_number_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.work_item_number_counters FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.work_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.work_items FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.work_item_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.work_item_events FORCE ROW LEVEL SECURITY;

CREATE POLICY work_item_number_counters_isolation ON spyglass.work_item_number_counters
    USING (account_id = nullif(current_setting('app.account_id', true), '')::uuid)
    WITH CHECK (account_id = nullif(current_setting('app.account_id', true), '')::uuid);
CREATE POLICY work_items_isolation ON spyglass.work_items
    USING (account_id = nullif(current_setting('app.account_id', true), '')::uuid)
    WITH CHECK (account_id = nullif(current_setting('app.account_id', true), '')::uuid);
CREATE POLICY work_item_events_isolation ON spyglass.work_item_events
    USING (account_id = nullif(current_setting('app.account_id', true), '')::uuid)
    WITH CHECK (account_id = nullif(current_setting('app.account_id', true), '')::uuid);

COMMIT;
