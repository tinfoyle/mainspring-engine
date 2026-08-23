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

## Environment placement

- Hostinger stage: Compose mounts an owner-created directory or Docker secret projection read-only at `/run/spyglass/integration-credentials` only in each per-cell connector worker. The app API, browser, MCP gateway, Agent services and runners receive no mount.
- LKE production: a namespace-scoped Secret or secret-store CSI volume is projected read-only into the same worker path with `defaultMode: 0400` or an equivalent `fsGroup`-compatible `0440`. The worker ServiceAccount has no generic Secret list/get permission.
- Local certification: the no-network mock remains the only enabled adapter until real providers exist. Mounted-broker unit and PostgreSQL claim tests certify the boundary but do not authorize external effects.

The worker executable must not enable the mounted broker until its exact provider definitions, immutable content reader, egress policy, health probe, execute/reconcile semantics and environment fixture have passed their respective certification gates.
