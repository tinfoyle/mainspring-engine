BEGIN;
ALTER TABLE operations_sessions DROP CONSTRAINT operations_sessions_authentication_method_check;
ALTER TABLE operations_sessions DROP CONSTRAINT operations_sessions_reauthentication_method_check;
ALTER TABLE operations_sessions ADD CHECK (authentication_method IN ('passkey','google_totp')),
 ADD CHECK (reauthentication_method IN ('passkey','google_totp'));
CREATE TABLE operations_authenticators (
 user_id uuid PRIMARY KEY REFERENCES operations_staff(user_id),
 secret jsonb NOT NULL DEFAULT '{}'::jsonb,
 recovery_locked boolean NOT NULL DEFAULT false,
 last_step bigint NOT NULL DEFAULT -1,
 recovery_hashes bytea[] NOT NULL DEFAULT '{}',
 failed integer NOT NULL DEFAULT 0 CHECK (failed BETWEEN 0 AND 5),
 window_start timestamptz NOT NULL,
 CHECK (jsonb_typeof(secret)='object'),
 CHECK (cardinality(recovery_hashes)<=8)
);
CREATE TABLE operations_login_challenges (
 token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
 user_id uuid REFERENCES operations_staff(user_id),
 security_version bigint,
 staff_version bigint,
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 google_at timestamptz,
 pending jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(pending)='object'),
 recovery_only boolean NOT NULL DEFAULT false,
 consumed boolean NOT NULL DEFAULT false,
 CHECK (expires_at>created_at AND expires_at<=created_at+interval '10 minutes'),
 CHECK ((google_at IS NULL AND user_id IS NULL) OR (google_at IS NOT NULL AND user_id IS NOT NULL AND security_version IS NOT NULL AND staff_version IS NOT NULL))
);
CREATE INDEX operations_login_challenges_expiry ON operations_login_challenges(expires_at);
CREATE TABLE operations_authentication_events (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES operations_staff(user_id),
 action text NOT NULL CHECK (action IN ('google_verified','enrollment_started','code_rejected','code_verified','authenticator_enrolled','recovery_used','reauthenticated')),
 occurred_at timestamptz NOT NULL
);
CREATE TRIGGER operations_authentication_events_immutable BEFORE UPDATE OR DELETE ON operations_authentication_events
 FOR EACH ROW EXECUTE FUNCTION spyglass_reject_operations_immutable_mutation();
REVOKE ALL ON operations_authenticators,operations_login_challenges,operations_authentication_events FROM PUBLIC;
COMMIT;
