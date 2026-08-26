BEGIN;

ALTER TABLE network_actor_rate_limits
    DROP CONSTRAINT network_actor_rate_limits_scope_check,
    ADD CONSTRAINT network_actor_rate_limits_scope_check CHECK (scope IN (
        'identity_login','identity_recovery','mcp_authorization','mcp_token','mcp_revocation',
        'affiliate_code_validation'
    ));

COMMIT;
