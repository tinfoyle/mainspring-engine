BEGIN;

-- The connector worker may not read the credential registry directly. Extend
-- the security-definer claim boundary with only the provider code and digest
-- of the opaque broker reference so a mounted broker can prove that it opened
-- the exact credential binding approved by the Account.
CREATE FUNCTION public.spyglass_claim_integration_execution_v2(
  p_attempt_id uuid,p_now timestamptz,p_lease_expires_at timestamptz
) RETURNS TABLE (
  account_id uuid,execution_id uuid,attempt_id uuid,mode text,capability text,release_id uuid,release_version bigint,approval_id uuid,
  connection_id uuid,connection_revision_id uuid,connection_revision bigint,credential_id uuid,credential_generation bigint,
  credential_provider text,credential_reference_sha256 bytea,payload_sha256 bytea,idempotency_key uuid,lease_expires_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE claimed record; credential_row record;
BEGIN
  SELECT * INTO claimed FROM public.spyglass_claim_integration_execution(p_attempt_id,p_now,p_lease_expires_at);
  IF NOT FOUND THEN RETURN; END IF;

  SELECT credential.provider,credential.reference_sha256 INTO credential_row
  FROM spyglass.integration_credentials credential
  WHERE credential.account_id=claimed.account_id AND credential.connection_id=claimed.connection_id AND
    credential.id=claimed.credential_id AND credential.generation=claimed.credential_generation;
  IF NOT FOUND OR credential_row.provider IS NULL OR credential_row.reference_sha256 IS NULL OR
    octet_length(credential_row.reference_sha256)<>32 THEN
    RAISE EXCEPTION USING ERRCODE='P2005',MESSAGE='Integration credential binding changed';
  END IF;

  RETURN QUERY SELECT claimed.account_id,claimed.execution_id,claimed.attempt_id,claimed.mode,claimed.capability,
    claimed.release_id,claimed.release_version,claimed.approval_id,claimed.connection_id,claimed.connection_revision_id,
    claimed.connection_revision,claimed.credential_id,claimed.credential_generation,credential_row.provider,
    credential_row.reference_sha256,claimed.payload_sha256,claimed.idempotency_key,claimed.lease_expires_at;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_claim_integration_execution_v2(uuid,timestamptz,timestamptz) FROM PUBLIC;

COMMIT;
