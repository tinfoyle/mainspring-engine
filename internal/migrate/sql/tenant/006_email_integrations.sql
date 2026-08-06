CREATE TABLE email_integrations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    email_address TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    imap_host TEXT NOT NULL,
    imap_port INTEGER NOT NULL CHECK (imap_port BETWEEN 1 AND 65535),
    imap_security TEXT NOT NULL CHECK (imap_security IN ('tls', 'starttls')),
    imap_username TEXT NOT NULL,
    imap_password_ciphertext TEXT NOT NULL,
    smtp_host TEXT NOT NULL,
    smtp_port INTEGER NOT NULL CHECK (smtp_port BETWEEN 1 AND 65535),
    smtp_security TEXT NOT NULL CHECK (smtp_security IN ('tls', 'starttls')),
    smtp_username TEXT NOT NULL,
    smtp_password_ciphertext TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'error', 'disabled')),
    last_verified_at TIMESTAMPTZ,
    last_error TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX email_integrations_status_idx ON email_integrations(status, created_at);
CREATE UNIQUE INDEX email_integrations_one_enabled_idx ON email_integrations ((true)) WHERE status <> 'disabled';

CREATE TABLE email_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id UUID NOT NULL REFERENCES email_integrations(id),
    idempotency_key TEXT NOT NULL UNIQUE,
    recipients JSONB NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'prepared'
        CHECK (status IN ('prepared', 'sending', 'sent', 'failed', 'unknown')),
    message_id TEXT,
    last_error TEXT,
    requested_by_type TEXT NOT NULL CHECK (requested_by_type IN ('user', 'persona', 'system')),
    requested_by_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ
);

CREATE INDEX email_outbox_created_idx ON email_outbox(created_at DESC);
