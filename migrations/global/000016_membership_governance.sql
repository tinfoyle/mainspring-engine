BEGIN;

ALTER TABLE memberships ADD CONSTRAINT memberships_account_id_id_unique UNIQUE (account_id,id);

CREATE TABLE account_membership_events (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    actor_user_id uuid NOT NULL REFERENCES users (id),
    action text NOT NULL CHECK (action IN ('role_changed','membership_removed','ownership_transferred')),
    target_membership_id uuid NOT NULL,
    previous_owner_membership_id uuid,
    previous_role text NOT NULL CHECK (previous_role IN ('owner','administrator','billing_admin','member','viewer')),
    new_role text CHECK (new_role IN ('owner','administrator','billing_admin','member','viewer')),
    reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 3 AND 300 AND reason = btrim(reason)),
    occurred_at timestamptz NOT NULL,
    CONSTRAINT account_membership_event_shape CHECK (
        (action = 'role_changed' AND previous_owner_membership_id IS NULL AND new_role IS NOT NULL AND new_role <> 'owner' AND previous_role <> 'owner' AND previous_role <> new_role)
        OR (action = 'membership_removed' AND previous_owner_membership_id IS NULL AND new_role IS NULL AND previous_role <> 'owner')
        OR (action = 'ownership_transferred' AND previous_owner_membership_id IS NOT NULL AND previous_owner_membership_id <> target_membership_id AND new_role = 'owner' AND previous_role <> 'owner')
    ),
    CONSTRAINT account_membership_events_target_scope FOREIGN KEY (account_id,target_membership_id) REFERENCES memberships (account_id,id),
    CONSTRAINT account_membership_events_previous_owner_scope FOREIGN KEY (account_id,previous_owner_membership_id) REFERENCES memberships (account_id,id)
);
CREATE INDEX account_membership_events_account_time ON account_membership_events (account_id, occurred_at DESC, id DESC);

CREATE FUNCTION reject_account_membership_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Account Membership events are immutable';
END;
$$;

CREATE TRIGGER account_membership_events_immutable
BEFORE UPDATE OR DELETE ON account_membership_events
FOR EACH ROW EXECUTE FUNCTION reject_account_membership_event_mutation();

COMMIT;
