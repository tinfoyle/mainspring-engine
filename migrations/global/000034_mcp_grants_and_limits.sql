BEGIN;

ALTER TABLE network_actor_rate_limits
    DROP CONSTRAINT network_actor_rate_limits_scope_check,
    ADD CONSTRAINT network_actor_rate_limits_scope_check CHECK (scope IN (
        'identity_login','identity_recovery','mcp_authorization','mcp_token','mcp_revocation'
    ));

ALTER TABLE mcp_oauth_events
    DROP CONSTRAINT mcp_oauth_events_event_type_check,
    ADD CONSTRAINT mcp_oauth_events_event_type_check CHECK (event_type IN (
        'authorization_approved','authorization_denied','code_exchanged',
        'token_refreshed','refresh_reuse_detected','token_revoked','grant_revoked'
    ));

COMMIT;
