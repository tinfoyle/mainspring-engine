BEGIN;

-- A human-submitted Marketing release has no model invocation. Its approval
-- still binds exact immutable release contents and is executed in the same
-- Account transaction as the human decision, never through the runner queue.
ALTER TABLE spyglass.attention_consequential_approvals ALTER COLUMN invocation_id DROP NOT NULL;
ALTER TABLE spyglass.attention_consequential_approvals ADD CONSTRAINT attention_human_marketing_origin
    CHECK (invocation_id IS NOT NULL OR (proposer_kind='user' AND capability='marketing.release.activate' AND work_item_id IS NULL));

COMMIT;
