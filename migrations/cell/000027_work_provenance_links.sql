BEGIN;

ALTER TABLE spyglass.work_item_events
    DROP CONSTRAINT work_item_events_event_type_check,
    ADD CONSTRAINT work_item_events_event_type_check
        CHECK (event_type IN ('created','transitioned','assigned','provenance_attached','conversation_linked'));

-- Conversation and Run are already cell-owned aggregates. Deferred composite
-- keys preserve Account isolation while allowing the established exact-erasure
-- transaction to remove Agent rows before Work rows and validate at commit.
ALTER TABLE spyglass.work_items
    ADD CONSTRAINT work_items_conversation_link_fk
        FOREIGN KEY (account_id, conversation_id)
        REFERENCES spyglass.agent_conversations (account_id, id)
        DEFERRABLE INITIALLY DEFERRED
        NOT VALID,
    ADD CONSTRAINT work_items_run_link_fk
        FOREIGN KEY (account_id, run_id)
        REFERENCES spyglass.agent_runs (account_id, id)
        DEFERRABLE INITIALLY DEFERRED
        NOT VALID;

ALTER TABLE spyglass.work_items
    VALIDATE CONSTRAINT work_items_conversation_link_fk;
ALTER TABLE spyglass.work_items
    VALIDATE CONSTRAINT work_items_run_link_fk;

COMMIT;
