# Prototype Knowledge, document and Baseline migration

- Status: deterministic export plus replay-safe destination import/reconciliation implemented; executed cohort evidence and owner review remain
- Phase: 3.2
- Source: one retained Mainspring prototype tenant PostgreSQL database
- Destination: one existing Spyglass Account in its assigned cell

The prototype database is not a production-compatible persistence boundary. Migration therefore takes a repeatable-read snapshot and produces a sealed transformation bundle instead of copying tables. The exporter is read-only and requires prototype schema versions 023, 025, 027, 029 and 032.

Run the exporter from `ubunturojo` with the source credential in the environment rather than the process argument list:

```sh
export SPYGLASS_PROTOTYPE_DATABASE_URL='postgres://...'
go run ./cmd/prototype-transform \
  -tenant-id '<prototype-tenant-uuid>' \
  -account-id '<spyglass-account-uuid>' \
  -output '/private/path/spyglass-prototype-bundle'
```

The output directory must not already exist. It is created privately and published by an atomic rename only after every source checksum and bundle invariant passes. It contains:

- `manifest.json`: source transaction checkpoint, applied schema versions, exact table inventory, deterministic target IDs, normalized fact candidates, document plans, Baseline review plans, totals and a SHA-256 over the complete manifest;
- `objects/`: checksum-addressed reconstructed UTF-8 document bodies;
- `unresolved.json`: every ambiguous connector source, public capture, missing owner identity, legacy evidence decision, interview transcript, research result and Baseline/Work relationship that cannot cross the final trust boundary automatically;
- `rollback-checkpoint.json`: the source checkpoint and manifest digest, explicitly recording that export made no destination writes.

## Transformation policy

- A mutable prototype fact never becomes an accepted Spyglass Fact. Importable records become proposed Claims and still require the ordinary human decision boundary.
- Prototype owner confirmation lacks the immutable confirming User identity required by final `owner_statement` Evidence. Those values are carried into the review report and Baseline answer plan, but are not impersonated or auto-imported.
- A document-backed fact is importable only when its legacy `source_ref` identifies a checksum-valid, non-deleted document revision included in the same bundle.
- Agent-derived facts may be imported only as visibly non-authoritative Agent Evidence and proposed Claims. They cannot independently become accepted Facts.
- Email, Google Drive and public-web facts remain unresolved because the prototype did not freeze the final source grant, provider revision and capture integrity tuple.
- The prototype retained extracted document text, not necessarily its original binary. Every non-deleted revision becomes a separate deterministic text reconstruction; the original media type remains metadata and the missing binary is reported. Deleted content is inventoried by digest but never written to the bundle.
- Legacy chunks are not copied. The manifest records their source count and the exact expected chunk count produced by the final chunker. Destination processing must regenerate the index and reconcile both counts.
- A prototype Baseline is not marked ready in the destination. Answers are mapped to governed question keys, requirements are mapped to the current evidence catalog, and the Account restarts from reviewed Claims. Every legacy evidence link requires revalidation against an exact final Evidence identity.

## Destination acceptance

P3.2 migration is complete only when the destination operation is replay-safe and proves all of the following against the sealed manifest:

1. Every importable reconstructed document has the exact Account-derived object and revision identities, passes malware/extraction processing, reaches an indexed ready revision, and has the expected source/text digest and chunk count. The Account document remains unpublished until a human reviews it.
2. Every importable Evidence and proposed Claim is present with the deterministic target identity, source kind/reference/revision, content digest and canonical value.
3. Every source inventory row is either mapped to a destination receipt or appears in `unresolved.json`; totals must reconcile without silent omission.
4. Owner review resolves or explicitly declines the unresolved fact and Baseline answer set before a new governed assessment can become ready.
5. A destination checkpoint records the manifest digest, imported target identities, unresolved count and reconciliation result. A failed or partial run retries the same manifest and IDs rather than generating replacements.

## Destination operation

The same release image contains `/prototype-import`. Run it as a short-lived operator container from `ubunturojo` for local rehearsal, from the Hostinger Docker network for stage, or as a suspended Kubernetes Job using the exact admitted production image. It requires an existing active Account with Knowledge enabled, the Account's assigned cell database, and the same versioned S3-compatible object store used by the document worker.

```sh
export SPYGLASS_PROTOTYPE_IMPORT_GLOBAL_DATABASE_URL='postgres://...'
export SPYGLASS_PROTOTYPE_IMPORT_CELL_DATABASE_URL='postgres://...'
export SPYGLASS_PROTOTYPE_IMPORT_OBJECT_ENDPOINT='minio:9000'
export SPYGLASS_PROTOTYPE_IMPORT_OBJECT_REGION='us-east-1'
export SPYGLASS_PROTOTYPE_IMPORT_OBJECT_BUCKET='spyglass-knowledge'
export SPYGLASS_PROTOTYPE_IMPORT_OBJECT_ACCESS_KEY='...'
export SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECRET_KEY='...'
export SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECURE='false'
export SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SSE='false'

/prototype-import \
  -bundle /private/read-only/spyglass-prototype-bundle \
  -certificate /private/evidence/prototype-reconciliation.json
```

The bundle loader refuses links, extra files, unknown JSON fields, mismatched unresolved/checkpoint files, unreferenced objects and checksum drift. Import uses the named `prototype-migration` workload through the normal Knowledge application services. It cannot create owner statements, decide Claims, publish documents or mark a Baseline ready.

The first invocation normally admits the immutable source objects, unpublished document revisions, Evidence and proposed Claims, records exact target receipts, moves the run to `imported`, and exits with reconciliation pending. Run the ordinary Knowledge document worker until those revisions are malware-scanned, extracted and indexed, then invoke the exact command again. Replay reuses the same deterministic targets. Completion verifies both physical object versions and bodies, every regenerated chunk and digest, every Evidence/Claim/citation tuple, the sealed unresolved count and the complete receipt set before moving the run to `reconciled` and atomically writing a private no-overwrite certificate.

The certificate is destination evidence, not authorization to make imported material authoritative. An Owner or Administrator must inspect the unresolved report, publish accepted reconstructed documents, accept or reject each proposed Claim, and start a new governed Baseline assessment from the reviewed Facts. Agent-only Claims cannot become authoritative by themselves.

Selective deletion of immutable Knowledge history is deliberately not a rollback mechanism. Before owner decisions, an interrupted run converges by exact retry and has no published/accepted effect. A cohort requiring full reversal must use an isolated target Account and the governed Account-erasure path while retaining the source checkpoint and bundle. The prototype source is not retired until the reconciliation certificate, owner review, Account-level migration certificate and rollback observation window are complete.

The exporter, strict bundle loader, workload-only importer, forced-RLS receipt/event ledger, post-processing object/chunk/citation reconciler and workload-driven Baseline maintenance scheduler are constructed and PostgreSQL 17 tested. P3.2 still requires an executed synthetic/internal migration and owner-review certificate; connected stage or production data is not mutated during construction.
