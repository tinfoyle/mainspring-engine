BEGIN;

CREATE TABLE spyglass.account_erasure_restore_ledger (
    sequence bigint PRIMARY KEY CHECK (sequence>=0),
    previous_root bytea NOT NULL CHECK (octet_length(previous_root)=32),
    root bytea NOT NULL UNIQUE CHECK (octet_length(root)=32),
    request_id uuid UNIQUE,
    recorded_at timestamptz NOT NULL,
    CONSTRAINT account_erasure_restore_ledger_origin CHECK ((sequence=0)=(request_id IS NULL))
);
INSERT INTO spyglass.account_erasure_restore_ledger(sequence,previous_root,root,request_id,recorded_at)
VALUES (0,decode(repeat('00',32),'hex'),decode(repeat('00',32),'hex'),NULL,statement_timestamp());

ALTER TABLE spyglass.account_erasure_tombstones
    ADD COLUMN ledger_sequence bigint,
    ADD COLUMN ledger_root bytea;

DO $$
DECLARE
    item spyglass.account_erasure_tombstones%ROWTYPE;
    current_sequence bigint := 0;
    current_root bytea := decode(repeat('00',32),'hex');
    next_root bytea;
BEGIN
    FOR item IN SELECT * FROM spyglass.account_erasure_tombstones ORDER BY erased_at,request_id LOOP
        current_sequence := current_sequence+1;
        next_root := sha256(current_root || convert_to(jsonb_build_array(
            item.request_id::text,encode(item.account_fingerprint,'hex'),item.placement_generation,item.policy_version,
            item.request_version,item.environment,COALESCE(encode(item.export_sha256,'hex'),''),
            encode(item.operator_evidence_sha256,'hex'),extract(epoch FROM item.backup_expires_at)::text
        )::text,'UTF8'));
        UPDATE spyglass.account_erasure_tombstones SET ledger_sequence=current_sequence,ledger_root=next_root WHERE request_id=item.request_id;
        INSERT INTO spyglass.account_erasure_restore_ledger(sequence,previous_root,root,request_id,recorded_at)
        VALUES (current_sequence,current_root,next_root,item.request_id,item.erased_at);
        current_root := next_root;
    END LOOP;
END;
$$;

ALTER TABLE spyglass.account_erasure_tombstones
    ALTER COLUMN ledger_sequence SET NOT NULL,
    ALTER COLUMN ledger_root SET NOT NULL,
    ADD CONSTRAINT account_erasure_tombstones_ledger_sequence_unique UNIQUE (ledger_sequence),
    ADD CONSTRAINT account_erasure_tombstones_ledger_root_unique UNIQUE (ledger_root),
    ADD CONSTRAINT account_erasure_tombstones_ledger_sequence_positive CHECK (ledger_sequence>0),
    ADD CONSTRAINT account_erasure_tombstones_ledger_root_shape CHECK (octet_length(ledger_root)=32),
    ADD CONSTRAINT account_erasure_tombstones_ledger_entry FOREIGN KEY (ledger_sequence) REFERENCES spyglass.account_erasure_restore_ledger(sequence);

CREATE FUNCTION spyglass.record_account_erasure_checkpoint() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, spyglass
AS $$
DECLARE
    previous spyglass.account_erasure_restore_ledger%ROWTYPE;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('spyglass:cell-erasure-restore-ledger',0));
    SELECT * INTO previous FROM spyglass.account_erasure_restore_ledger ORDER BY sequence DESC LIMIT 1;
    NEW.ledger_sequence := previous.sequence+1;
    NEW.ledger_root := sha256(previous.root || convert_to(jsonb_build_array(
        NEW.request_id::text,encode(NEW.account_fingerprint,'hex'),NEW.placement_generation,NEW.policy_version,
        NEW.request_version,NEW.environment,COALESCE(encode(NEW.export_sha256,'hex'),''),
        encode(NEW.operator_evidence_sha256,'hex'),extract(epoch FROM NEW.backup_expires_at)::text
    )::text,'UTF8'));
    INSERT INTO spyglass.account_erasure_restore_ledger(sequence,previous_root,root,request_id,recorded_at)
    VALUES (NEW.ledger_sequence,previous.root,NEW.ledger_root,NEW.request_id,NEW.erased_at);
    RETURN NEW;
END;
$$;

CREATE TRIGGER account_erasure_tombstone_checkpoint
BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.record_account_erasure_checkpoint();

CREATE FUNCTION spyglass.reject_account_erasure_checkpoint_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Account erasure restore evidence is immutable';
END;
$$;
CREATE TRIGGER account_erasure_tombstones_immutable
BEFORE UPDATE OR DELETE ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_account_erasure_checkpoint_mutation();
CREATE TRIGGER account_erasure_restore_ledger_immutable
BEFORE UPDATE OR DELETE ON spyglass.account_erasure_restore_ledger
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_account_erasure_checkpoint_mutation();

REVOKE ALL ON TABLE spyglass.account_erasure_restore_ledger FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.record_account_erasure_checkpoint() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.reject_account_erasure_checkpoint_mutation() FROM PUBLIC;

COMMIT;
