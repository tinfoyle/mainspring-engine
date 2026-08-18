BEGIN;

ALTER TABLE memberships DROP CONSTRAINT memberships_account_id_user_id_key;
CREATE UNIQUE INDEX memberships_account_user_current ON memberships (account_id,user_id) WHERE state IN ('active','suspended');

ALTER TABLE account_membership_events
    DROP CONSTRAINT account_membership_events_action_check,
    DROP CONSTRAINT account_membership_event_shape;

ALTER TABLE account_membership_events
    ADD COLUMN previous_state text,
    ADD COLUMN new_state text;

DROP TRIGGER account_membership_events_immutable ON account_membership_events;
UPDATE account_membership_events
SET previous_state='active',new_state='removed'
WHERE action='membership_removed';
CREATE TRIGGER account_membership_events_immutable
BEFORE UPDATE OR DELETE ON account_membership_events
FOR EACH ROW EXECUTE FUNCTION reject_account_membership_event_mutation();

ALTER TABLE account_membership_events
    ADD CONSTRAINT account_membership_events_action_check CHECK (action IN (
        'role_changed','membership_removed','ownership_transferred',
        'membership_suspended','membership_reactivated','membership_left'
    )),
    ADD CONSTRAINT account_membership_events_previous_state_check CHECK (previous_state IS NULL OR previous_state IN ('active','suspended','removed')),
    ADD CONSTRAINT account_membership_events_new_state_check CHECK (new_state IS NULL OR new_state IN ('active','suspended','removed')),
    ADD CONSTRAINT account_membership_event_shape CHECK (
        (action = 'role_changed' AND previous_owner_membership_id IS NULL AND new_role IS NOT NULL AND new_role <> 'owner' AND previous_role <> 'owner' AND previous_role <> new_role AND previous_state IS NULL AND new_state IS NULL)
        OR (action = 'membership_removed' AND previous_owner_membership_id IS NULL AND new_role IS NULL AND previous_role <> 'owner' AND previous_state IN ('active','suspended') AND new_state = 'removed')
        OR (action = 'ownership_transferred' AND previous_owner_membership_id IS NOT NULL AND previous_owner_membership_id <> target_membership_id AND new_role = 'owner' AND previous_role <> 'owner' AND previous_state IS NULL AND new_state IS NULL)
        OR (action = 'membership_suspended' AND previous_owner_membership_id IS NULL AND previous_role <> 'owner' AND new_role = previous_role AND previous_state = 'active' AND new_state = 'suspended')
        OR (action = 'membership_reactivated' AND previous_owner_membership_id IS NULL AND previous_role <> 'owner' AND new_role = previous_role AND previous_state = 'suspended' AND new_state = 'active')
        OR (action = 'membership_left' AND previous_owner_membership_id IS NULL AND previous_role <> 'owner' AND new_role IS NULL AND previous_state = 'active' AND new_state = 'removed')
    );

COMMIT;
