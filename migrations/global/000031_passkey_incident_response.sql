BEGIN;

ALTER TABLE user_security_events DROP CONSTRAINT user_security_events_event_type_check;
ALTER TABLE user_security_events ADD CONSTRAINT user_security_events_event_type_check CHECK (event_type IN (
    'session_created','session_reauthenticated','session_revoked','sessions_revoked','credential_recovered',
    'passkey_added','passkey_removed','passkey_renamed','passkey_compromised','passkey_authenticated',
    'passkey_reauthenticated','passkey_clone_warning','recovery_codes_rotated','recovery_code_consumed',
    'primary_email_change_requested','primary_email_changed'
));

COMMIT;
