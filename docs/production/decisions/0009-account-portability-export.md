# ADR-0009: Build deterministic Account portability artifacts from reviewed projections

- Status: Accepted
- Date: 2026-08-24
- Owners: Accounts, every Feature Package, platform security, operations

## Context

Phase 3 cannot be complete until all customer data is exportable independently of package entitlement state. The existing erasure workflow requires export evidence, but it intentionally accepts only an opaque artifact reference and digest; it does not produce an artifact. A raw database dump is not a portability format: it would expose encrypted provider payloads, credential attestations, queue leases, routing receipts and internal operator records, while omitting versioned objects. Global PostgreSQL, the assigned cell PostgreSQL and object storage also cannot share one transaction.

## Decision

Spyglass produces one private, deterministic ZIP artifact from an exact Account snapshot and an explicit launch registry.

1. Export is an Account-level lifecycle capability. It remains available after commercial packages become read-only or absent and never grants a package mutation.
2. The request freezes Account, requesting User, export identity, expiry, cell, placement generation, Account version and separate global/cell snapshot timestamps. Snapshot capture must begin within one minute before the request and finish within 15 minutes. Placement or Account-version drift makes publication fail rather than silently combining different Accounts or cells.
3. Every database section is a customer-safe projection, not raw-row serialization. Canonical JSONL records contain a stable `key` and arrive in strictly increasing order. The builder sorts sections, fixes ZIP metadata, bounds every record, section, object and artifact, and records count, byte length and SHA-256 evidence in `manifest.json`.
4. Original Knowledge document revisions and Marketing asset revisions are streamed from exact versioned object references. Object paths must be relative, clean, unique within their source and strictly ordered; length and SHA-256 must match the registered metadata. Search chunks are derived and regenerated from originals.
5. Credential bindings, encrypted provider/notification/runner payloads, anti-replay receipts, leases, retry queues and internal operator workflow rows never enter an artifact. Customer-facing action, audit and delivery projections remain included after content minimization.
6. The reviewed launch registry classifies every Account-owned table as `included`, `derived`, `operational` or `secret`. A fresh-migration PostgreSQL test inventories every direct `account_id` table plus reviewed indirect/root tables. A new table without a disposition fails CI.
7. A builder writes only to a private staging target. The caller verifies the returned artifact byte count and SHA-256, publishes with create-if-absent semantics, commits the opaque reference and evidence, then deletes the staging object. A failed build is never downloadable.
8. Download authorization returns a short-lived, one-artifact capability only after current Account role and export-request state are checked. The durable export reference is not exposed in ordinary logs, metrics, browser storage or events. Artifact expiry is no more than 30 days and drives idempotent physical deletion.
9. Completion requires successful global and cell projections plus every registered object source. Missing, malformed, out-of-order, oversized or digest-mismatched source output fails the whole artifact. There is no partial-success manifest.

## Initial bounds and format

- archive schema: version 1;
- section record: at most 1 MiB canonical JSON;
- section: at most 5,000,000 records and 2 GiB uncompressed JSONL;
- object: at most 64 MiB;
- artifact: at most 100,000 objects and 32 GiB compressed output;
- entry order: sorted `sections/*.jsonl`, then sorted `objects/<source>/*`, then `manifest.json`;
- file timestamp/mode: fixed 1980 UTC and `0600` for reproducibility.

These are safety ceilings, not product promises. Pagination and multiple artifacts require a later archive schema rather than silently raising a limit.

## Consequences

- Export implementation is shared infrastructure, while each package owns its sanitized projections and exact stable keys.
- Deterministic replay permits digest comparison and erasure evidence without placing customer identifiers in the final tombstone.
- A consistent export is a coordinated bounded snapshot, not a claim of cross-database serializability. Version and placement fences detect invalid combinations.
- The artifact builder, schema registry, durable request/expiry state and exact-version object publisher/deleter are necessary but do not make `customer-export-api` executable. Projection/snapshot coordination, staging lifecycle, download authorization/customer transport and applied environment certification remain separate gates.

## Verification

- Unit tests produce byte-identical replayed archives and verify the whole-artifact digest, manifest, entry order and object integrity.
- Negative tests reject noncanonical or unordered records, missing stable keys, unsafe or unordered objects, registry/source drift and invalid snapshot bounds.
- A disposable PostgreSQL 17 test applies all global/development/cell migrations and proves exact portability disposition coverage.
- PostgreSQL lifecycle tests prove one active request per Account, active-Owner enforcement, lease recovery, bounded retry, movement/version drift rejection, exact artifact evidence, cross-Account concealment, expiry deletion and immutable audit history. Global erasure and restore-replay tests include exact request/event counts.
- Pure, race and real encrypted-MinIO tests prove create-if-absent publication, exact byte/digest reconciliation, opaque-reference confinement, exact-version deletion, idempotent missing-version replay and the separate no-list export-worker policy.
- End-to-end tests must later prove unknown-commit object publication recovery, read-only/absent package access through the customer transport and external-object erasure evidence handoff.
