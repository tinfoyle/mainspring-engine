# Integration credential broker contract

The dedicated Integration connector worker may receive provider credentials only through a one-operation lease. PostgreSQL stores the SHA-256 of an opaque reference, never the reference or credential. Cell migration 65 extends the execute-only claim result with that digest and the non-secret provider code while preserving the worker role's denial on direct `integration_credentials` reads.

## Mounted layout

The production-capable adapter accepts one absolute, read-only root. The root contains `index.json` and relative material files:

```text
/run/spyglass/integration-credentials/
  index.json
  smtp/credential-generation-1.json
```

The strict index format is:

```json
{
  "version": 1,
  "credentials": [
    {
      "account_id": "00000000-0000-4000-8000-000000000000",
      "credential_id": "00000000-0000-4000-8000-000000000000",
      "generation": 1,
      "provider": "smtp",
      "reference": "secret://stage/integrations/example/smtp/1",
      "file": "smtp/credential-generation-1.json"
    }
  ]
}
```

The credential attestation entered through HTTP, MCP or the private workspace is lowercase hex of `SHA-256(UTF-8(reference))`. The index is not secret, but it is workload-private. Material files must be regular files, at most 64 KiB, and inaccessible to other users; modes `0400`, `0440`, `0600` and `0640` are accepted. Relative paths, including resolved projected-volume symlinks, must remain inside the configured root.

The adapter reloads and validates the complete index for every lease, so an atomic Docker/Kubernetes secret-volume update can rotate an entry without a process restart. A stale claim cannot receive the replacement because its reference attestation, provider, credential identity or generation will differ. Leased bytes are copied once, passed to one bounded connector call and zeroed on close.

## Closed provider material

Provider code `smtp` selects the implicit-TLS SMTP adapter. Its material file is strict JSON and binds the exact non-secret connection scope again:

```json
{
  "version": 1,
  "address": "smtp.example.net:465",
  "server_name": "smtp.example.net",
  "username": "account-specific-user",
  "password": "account-specific-password",
  "from_address": "campaigns@example.net",
  "from_name": "Example campaigns",
  "audience_reference": "audience:customers-v1",
  "recipients": ["customer@example.org"]
}
```

An optional `root_ca_pem` adds a private trust anchor without disabling system trust. Sender and audience must equal the immutable connection revision. Recipient addresses remain in the workload-private material rather than PostgreSQL, Marketing state, payloads, logs or customer APIs. The adapter requires exactly one approved copy asset, uses the execution UUID as a deterministic Message-ID, and treats any failure after SMTP DATA begins as uncertain. SMTP has no portable non-application query, so reconciliation never authorizes an automatic resend; bounded uncertainty proceeds to dual-controlled manual resolution.

Provider code `web_https` selects the create-only HTTPS publication protocol. Its strict material is:

```json
{
  "version": 1,
  "https_origin": "https://publish.example.net",
  "path_prefix": "/campaigns",
  "health_path": "/campaigns/health",
  "bearer_token": "account-specific-token"
}
```

`health_path` must be a normalized child of `path_prefix`. The worker uses authenticated `HEAD` only against that path for its non-mutating provider check; it does not treat a publication resource as a health endpoint.

The origin and path must exactly equal the immutable non-secret scope. The worker sends the verified version-1 delivery envelope to `PUT <origin><prefix>/<execution-id>.json` with `If-None-Match: *`, the execution UUID as `Idempotency-Key`, and an RFC-style `Content-Digest`. Redirects are forbidden. DNS is resolved once per operation, every answer must be public, and the connection is pinned to those answers while TLS verifies the original hostname. A successful response must echo the exact digest. Ambiguous execution is reconciled only by `HEAD` on the same resource: an exact digest proves success, `404`/`410` proves non-application and permits one bounded retry, and every other result remains uncertain.

## Environment placement

- Hostinger stage: Compose mounts an owner-created directory or Docker secret projection read-only at `/run/spyglass/integration-credentials` only in each per-cell connector worker. The app API, browser, MCP gateway, Agent services and runners receive no mount.
- LKE production: a namespace-scoped Secret or secret-store CSI volume is projected read-only into the same worker path with `defaultMode: 0400` or an equivalent `fsGroup`-compatible `0440`. The worker ServiceAccount has no generic Secret list/get permission.
- Local certification: the deterministic no-network mock remains available for queue/state certification. The real adapters have unit-level protocol and uncertainty coverage; an applied disposable SMTP/HTTPS provider certificate is still required before customer activation.

The worker executable selects the mounted broker and exact-version S3 reader only with `SPYGLASS_CONNECTOR_ADAPTER=production` in Stage, preproduction or production. Stage Compose and the LKE reference mount credentials only into the connector workers and grant only bounded provider egress. Environment fixtures and applied provider certificates remain required before customer activation.
