BEGIN;

CREATE TABLE mcp_oauth_authorizations (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    client_id text NOT NULL CHECK (octet_length(client_id) BETWEEN 1 AND 2048),
    client_name text NOT NULL CHECK (octet_length(client_name) BETWEEN 1 AND 160),
    redirect_uri text NOT NULL CHECK (octet_length(redirect_uri) BETWEEN 1 AND 2048),
    resource text NOT NULL CHECK (octet_length(resource) BETWEEN 1 AND 2048),
    scope text NOT NULL CHECK (scope = 'spyglass:mcp'),
    client_state text NOT NULL CHECK (octet_length(client_state) <= 2048),
    code_challenge text NOT NULL CHECK (octet_length(code_challenge) = 43),
    status text NOT NULL CHECK (status IN ('pending','approved','denied')),
    expires_at timestamptz NOT NULL,
    decided_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT mcp_oauth_authorizations_time_order CHECK (expires_at > created_at),
    CONSTRAINT mcp_oauth_authorizations_decision CHECK (
        (status='pending' AND decided_at IS NULL) OR
        (status IN ('approved','denied') AND decided_at IS NOT NULL)
    )
);
CREATE INDEX mcp_oauth_authorizations_user_pending
    ON mcp_oauth_authorizations (user_id,created_at,id) WHERE status='pending';
CREATE INDEX mcp_oauth_authorizations_cleanup
    ON mcp_oauth_authorizations (expires_at,id);

CREATE TABLE mcp_oauth_grants (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id text NOT NULL CHECK (octet_length(client_id) BETWEEN 1 AND 2048),
    client_name text NOT NULL CHECK (octet_length(client_name) BETWEEN 1 AND 160),
    resource text NOT NULL CHECK (octet_length(resource) BETWEEN 1 AND 2048),
    scope text NOT NULL CHECK (scope = 'spyglass:mcp'),
    security_version bigint NOT NULL CHECK (security_version > 0),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    last_used_at timestamptz
);
CREATE INDEX mcp_oauth_grants_user_active
    ON mcp_oauth_grants (user_id,created_at,id) WHERE revoked_at IS NULL;

CREATE TABLE mcp_oauth_codes (
    code_hash bytea PRIMARY KEY CHECK (octet_length(code_hash) = 32),
    grant_id uuid NOT NULL REFERENCES mcp_oauth_grants (id) ON DELETE CASCADE,
    redirect_uri text NOT NULL CHECK (octet_length(redirect_uri) BETWEEN 1 AND 2048),
    code_challenge text NOT NULL CHECK (octet_length(code_challenge) = 43),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT mcp_oauth_codes_time_order CHECK (expires_at > created_at)
);
CREATE INDEX mcp_oauth_codes_cleanup ON mcp_oauth_codes (expires_at);

CREATE TABLE mcp_oauth_access_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    grant_id uuid NOT NULL REFERENCES mcp_oauth_grants (id) ON DELETE CASCADE,
    family_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    last_used_at timestamptz,
    CONSTRAINT mcp_oauth_access_tokens_time_order CHECK (expires_at > created_at)
);
CREATE INDEX mcp_oauth_access_tokens_grant_active
    ON mcp_oauth_access_tokens (grant_id,expires_at) WHERE revoked_at IS NULL;
CREATE INDEX mcp_oauth_access_tokens_family_active
    ON mcp_oauth_access_tokens (family_id,expires_at) WHERE revoked_at IS NULL;
CREATE INDEX mcp_oauth_access_tokens_cleanup ON mcp_oauth_access_tokens (expires_at);

CREATE TABLE mcp_oauth_refresh_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    grant_id uuid NOT NULL REFERENCES mcp_oauth_grants (id) ON DELETE CASCADE,
    family_id uuid NOT NULL,
    sequence bigint NOT NULL CHECK (sequence > 0),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT mcp_oauth_refresh_tokens_time_order CHECK (expires_at > created_at),
    UNIQUE (family_id,sequence)
);
CREATE INDEX mcp_oauth_refresh_tokens_grant_active
    ON mcp_oauth_refresh_tokens (grant_id,expires_at) WHERE revoked_at IS NULL;
CREATE INDEX mcp_oauth_refresh_tokens_family
    ON mcp_oauth_refresh_tokens (family_id,sequence);
CREATE INDEX mcp_oauth_refresh_tokens_cleanup ON mcp_oauth_refresh_tokens (expires_at);

CREATE TABLE mcp_oauth_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    grant_id uuid REFERENCES mcp_oauth_grants (id) ON DELETE SET NULL,
    event_type text NOT NULL CHECK (event_type IN (
        'authorization_approved','authorization_denied','code_exchanged',
        'token_refreshed','refresh_reuse_detected','token_revoked'
    )),
    occurred_at timestamptz NOT NULL
);
CREATE INDEX mcp_oauth_events_user_time ON mcp_oauth_events (user_id,occurred_at DESC,id DESC);

CREATE FUNCTION spyglass_authenticate_mcp_access_token(
    p_token_hash bytea,
    p_resource text,
    p_scope text,
    p_now timestamptz
) RETURNS TABLE(user_id uuid)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    UPDATE public.mcp_oauth_access_tokens AS token
       SET last_used_at = p_now
      FROM public.mcp_oauth_grants AS grant_record,
           public.users AS identity
     WHERE token.token_hash = p_token_hash
       AND octet_length(p_token_hash) = 32
       AND token.grant_id = grant_record.id
       AND identity.id = grant_record.user_id
       AND identity.state = 'active'
       AND identity.security_version = grant_record.security_version
       AND grant_record.resource = p_resource
       AND grant_record.scope = p_scope
       AND grant_record.revoked_at IS NULL
       AND token.revoked_at IS NULL
       AND token.expires_at > p_now
    RETURNING grant_record.user_id
$$;

REVOKE ALL ON FUNCTION spyglass_authenticate_mcp_access_token(bytea,text,text,timestamptz) FROM PUBLIC;

COMMIT;
