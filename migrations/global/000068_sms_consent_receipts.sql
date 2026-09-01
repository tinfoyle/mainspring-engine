BEGIN;

CREATE TABLE sms_consent_receipts (
    challenge_id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id uuid NOT NULL,
    destination_fingerprint bytea NOT NULL CHECK (octet_length(destination_fingerprint)=32),
    consent_version text NOT NULL CHECK (consent_version='sms-security-v1-2026-09-01'),
    consent_copy text NOT NULL CHECK (char_length(consent_copy) BETWEEN 200 AND 1000),
    consent_copy_sha256 bytea NOT NULL CHECK (octet_length(consent_copy_sha256)=32),
    source text NOT NULL CHECK (source='setup_sms_enrollment'),
    accepted_at timestamptz NOT NULL
);

CREATE INDEX sms_consent_receipts_user_time ON sms_consent_receipts (user_id,accepted_at,challenge_id);

COMMENT ON TABLE sms_consent_receipts IS
    'Append-only evidence that a user expressly accepted the server-owned SMS security consent copy; phone numbers are represented only by a keyed fingerprint.';

COMMIT;
