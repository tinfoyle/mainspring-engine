# Prototype Knowledge, document and Baseline migration

- Status: deterministic source export and transformation manifest implemented; destination import, processing reconciliation and owner review remain
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

1. Every importable reconstructed document has the exact Account-derived object and revision identities, passes malware/extraction processing, publishes ready, and has the expected source/text digest and chunk count.
2. Every importable Evidence and proposed Claim is present with the deterministic target identity, source kind/reference/revision, content digest and canonical value.
3. Every source inventory row is either mapped to a destination receipt or appears in `unresolved.json`; totals must reconcile without silent omission.
4. Owner review resolves or explicitly declines the unresolved fact and Baseline answer set before a new governed assessment can become ready.
5. A destination checkpoint records the manifest digest, imported target identities, unresolved count and reconciliation result. A failed or partial run retries the same manifest and IDs rather than generating replacements.

The export checkpoint is constructed and PostgreSQL-tested. The destination importer/receipt ledger and post-processing reconciler are the next construction slice; no stage or production data migration should be attempted before that checkpoint is complete.
