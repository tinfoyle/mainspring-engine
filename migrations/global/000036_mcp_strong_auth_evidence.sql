BEGIN;

ALTER TABLE mcp_oauth_grants
    ADD COLUMN strong_authenticated_at timestamptz,
    ADD CONSTRAINT mcp_oauth_grants_strong_auth_order CHECK (
        strong_authenticated_at IS NULL OR strong_authenticated_at <= created_at
    );

DROP FUNCTION spyglass_authenticate_mcp_access_token(bytea,text,text,timestamptz);

CREATE FUNCTION spyglass_authenticate_mcp_access_token(
    p_token_hash bytea,
    p_resource text,
    p_scope text,
    p_now timestamptz
) RETURNS TABLE(user_id uuid,strong_authenticated_at timestamptz)
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
    RETURNING grant_record.user_id,grant_record.strong_authenticated_at
$$;

REVOKE ALL ON FUNCTION spyglass_authenticate_mcp_access_token(bytea,text,text,timestamptz) FROM PUBLIC;

COMMIT;
