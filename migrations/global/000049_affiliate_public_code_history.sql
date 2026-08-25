BEGIN;

CREATE TABLE affiliate_public_code_history (
    public_code text PRIMARY KEY,
    affiliate_id uuid NOT NULL REFERENCES affiliate_enrollments (affiliate_id),
    enrollment_version bigint NOT NULL CHECK (enrollment_version > 0),
    activated_at timestamptz NOT NULL,
    replaced_at timestamptz,
    CONSTRAINT affiliate_public_code_history_code_normalized CHECK (public_code = upper(btrim(public_code))),
    CONSTRAINT affiliate_public_code_history_code_shape CHECK (public_code ~ '^[A-Z0-9][A-Z0-9-]{4,22}[A-Z0-9]$'),
    CONSTRAINT affiliate_public_code_history_time_order CHECK (replaced_at IS NULL OR replaced_at >= activated_at),
    UNIQUE (affiliate_id,enrollment_version)
);
CREATE INDEX affiliate_public_code_history_affiliate
    ON affiliate_public_code_history (affiliate_id,enrollment_version DESC);

INSERT INTO affiliate_public_code_history
    (public_code,affiliate_id,enrollment_version,activated_at,replaced_at)
SELECT public_code,affiliate_id,version,created_at,NULL
FROM affiliate_enrollments;

CREATE FUNCTION spyglass_record_affiliate_public_code() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    INSERT INTO public.affiliate_public_code_history
        (public_code,affiliate_id,enrollment_version,activated_at)
    VALUES (NEW.public_code,NEW.affiliate_id,NEW.version,NEW.created_at);
    RETURN NEW;
END;
$$;

CREATE TRIGGER affiliate_enrollments_record_public_code
AFTER INSERT ON affiliate_enrollments
FOR EACH ROW EXECUTE FUNCTION spyglass_record_affiliate_public_code();

CREATE FUNCTION spyglass_protect_affiliate_public_code_history() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    IF TG_OP = 'DELETE' OR
       OLD.public_code IS DISTINCT FROM NEW.public_code OR
       OLD.affiliate_id IS DISTINCT FROM NEW.affiliate_id OR
       OLD.enrollment_version IS DISTINCT FROM NEW.enrollment_version OR
       OLD.activated_at IS DISTINCT FROM NEW.activated_at OR
       OLD.replaced_at IS NOT NULL OR NEW.replaced_at IS NULL OR
       NEW.replaced_at < OLD.activated_at THEN
        RAISE EXCEPTION 'Affiliate public code history is immutable' USING ERRCODE='P0001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER affiliate_public_code_history_protected
BEFORE UPDATE OR DELETE ON affiliate_public_code_history
FOR EACH ROW EXECUTE FUNCTION spyglass_protect_affiliate_public_code_history();

REVOKE ALL ON TABLE affiliate_public_code_history FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_record_affiliate_public_code() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_protect_affiliate_public_code_history() FROM PUBLIC;

COMMIT;
