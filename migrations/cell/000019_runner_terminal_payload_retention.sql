BEGIN;

-- Terminal runner requests/results may contain customer material. Replace
-- their encrypted envelopes with fixed sentinels after the operational
-- recovery window while retaining identifier-only queue state, hashes,
-- capability audit, and action history.
ALTER TABLE spyglass.runner_invocation_queue
    ADD COLUMN terminal_payload_purged_at timestamptz,
    ADD CONSTRAINT runner_invocation_payload_retention_state CHECK (
        terminal_payload_purged_at IS NULL OR processing_state IN ('completed','execution_failed','canceled')
    );

ALTER TABLE spyglass.runner_invocation_exchanges
    ADD COLUMN terminal_payload_purged_at timestamptz;

CREATE INDEX runner_invocation_terminal_payload_retention
    ON spyglass.runner_invocation_queue(completed_at,invocation_id)
    WHERE processing_state IN ('completed','execution_failed','canceled') AND terminal_payload_purged_at IS NULL;

CREATE FUNCTION public.spyglass_prune_runner_terminal_payloads(
    p_cutoff timestamptz,p_pruned_at timestamptz,p_limit integer
) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE candidate record; pruned_count bigint:=0;
BEGIN
    IF p_cutoff IS NULL OR p_pruned_at IS NULL OR p_cutoff>p_pruned_at OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 1000 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner terminal payload retention request';
    END IF;
    FOR candidate IN
        SELECT q.account_id,q.invocation_id
        FROM spyglass.runner_invocation_queue q
        WHERE q.processing_state IN ('completed','execution_failed','canceled')
          AND q.completed_at<=p_cutoff AND q.terminal_payload_purged_at IS NULL
        ORDER BY q.completed_at,q.invocation_id
        FOR UPDATE SKIP LOCKED LIMIT p_limit
    LOOP
        PERFORM set_config('app.account_id',candidate.account_id::text,true);
        UPDATE spyglass.runner_invocation_exchanges x SET
            request_ciphertext=decode(repeat('00',17),'hex'),
            request_nonce=decode(repeat('00',12),'hex'),
            request_key_version=1,
            result_ciphertext=CASE WHEN x.result_ciphertext IS NULL THEN NULL ELSE decode(repeat('00',17),'hex') END,
            result_nonce=CASE WHEN x.result_nonce IS NULL THEN NULL ELSE decode(repeat('00',12),'hex') END,
            result_key_version=CASE WHEN x.result_key_version IS NULL THEN NULL ELSE 1 END,
            terminal_payload_purged_at=p_pruned_at
        WHERE x.account_id=candidate.account_id AND x.invocation_id=candidate.invocation_id
          AND x.terminal_payload_purged_at IS NULL;
        UPDATE spyglass.runner_invocation_queue q SET terminal_payload_purged_at=p_pruned_at
        WHERE q.account_id=candidate.account_id AND q.invocation_id=candidate.invocation_id
          AND q.terminal_payload_purged_at IS NULL;
        pruned_count:=pruned_count+1;
    END LOOP;
    RETURN pruned_count;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_prune_runner_terminal_payloads(timestamptz,timestamptz,integer) FROM PUBLIC;

COMMIT;
