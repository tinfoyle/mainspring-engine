# ADR-0012: Keep mailbox credentials and delivery inside the tenant runtime

Status: Accepted for MVP
Date: 2026-08-06

## Context

Authorized personas need to inspect a company inbox, prepare replies, and in some cases send email. The provider process must not receive reusable mailbox credentials, and workflow retries must not duplicate external delivery.

## Decision

Each tenant runtime owns one active mailbox integration for the MVP. Incoming mail uses IMAP and outbound delivery uses authenticated SMTP; both require TLS or STARTTLS. Credentials are encrypted with AES-256-GCM under a runtime-supplied master key before being stored in the tenant database.

Mailbox access is split into `email.read`, `email.draft`, and `email.send` capabilities. Sending is disabled by default during onboarding. Every attempted send passes through the external-action ledger with a stable idempotency key and is mirrored in a tenant outbox. An uncertain SMTP outcome enters manual-review state instead of being retried automatically.

The local demo uses an explicit mock connector so the full setup, inbox, and audited-send flow can be tested without external credentials. Production defaults to the network IMAP/SMTP connector.

## Consequences

- Agent providers never need raw IMAP or SMTP credentials.
- Tenant mailbox data and audit records remain inside the tenant boundary.
- Duplicate delivery is guarded even when HTTP or workflow requests are retried.
- OAuth-based providers, attachments, folders, threading, and mailbox synchronization are deferred extensions behind the same connector interface.
