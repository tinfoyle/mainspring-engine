BEGIN;

-- A Run owns one frozen canonical context payload. Keeping it on the existing
-- Account-owned Run row makes erasure/movement participation automatic and
-- prevents dispatch from joining mutable Work, Knowledge, or Baseline state.
ALTER TABLE spyglass.agent_runs
    ADD COLUMN context_payload bytea NOT NULL DEFAULT convert_to('{"schema_version":1,"items":[]}','UTF8'),
    ADD COLUMN context_digest bytea NOT NULL DEFAULT decode('2db0df4cab9392b45e99102e09643c719b98b9e91826b2dbc7be7fca548cd29e','hex'),
    ADD COLUMN context_item_count smallint NOT NULL DEFAULT 0,
    ADD CONSTRAINT agent_runs_context_payload_bounds CHECK (octet_length(context_payload) BETWEEN 31 AND 49152),
    ADD CONSTRAINT agent_runs_context_digest_shape CHECK (octet_length(context_digest)=32),
    ADD CONSTRAINT agent_runs_context_item_count_bounds CHECK (context_item_count BETWEEN 0 AND 64);

COMMIT;
