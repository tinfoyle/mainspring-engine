BEGIN;

CREATE TABLE passkey_key_rotation_operator_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    action text NOT NULL CHECK (action IN ('inspect','reencrypt')),
    actor text NOT NULL CHECK (actor=btrim(actor) AND char_length(actor) BETWEEN 3 AND 200),
    reason text NOT NULL CHECK (reason=btrim(reason) AND char_length(reason) BETWEEN 8 AND 500),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    active_key_version integer NOT NULL CHECK (active_key_version>0),
    batch_limit integer CHECK (batch_limit BETWEEN 1 AND 500),
    updated_count integer NOT NULL CHECK (updated_count>=0),
    remaining_credential_count bigint NOT NULL CHECK (remaining_credential_count>=0),
    remaining_ceremony_count bigint NOT NULL CHECK (remaining_ceremony_count>=0),
    occurred_at timestamptz NOT NULL,
    CONSTRAINT passkey_rotation_batch_shape CHECK (
        (action='inspect' AND batch_limit IS NULL AND updated_count=0) OR
        (action='reencrypt' AND batch_limit IS NOT NULL)
    )
);
CREATE INDEX passkey_key_rotation_operator_events_time ON passkey_key_rotation_operator_events (occurred_at DESC,id DESC);

CREATE FUNCTION public.reject_passkey_key_rotation_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'passkey key rotation operator events are immutable';
END;
$$;
CREATE TRIGGER passkey_key_rotation_operator_events_immutable
BEFORE UPDATE OR DELETE ON passkey_key_rotation_operator_events
FOR EACH ROW EXECUTE FUNCTION public.reject_passkey_key_rotation_operator_event_change();

COMMIT;
