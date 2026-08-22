# Finance module

- Status: typed kernel, forced-RLS persistence, governed lifecycle, stable query summaries and generated HTTP surface constructed; MCP/private product surfaces remain
- Package boundary: Finance
- Decision: [ADR-0006](decisions/0006-finance-operational-ledger.md)

## Launch contract

Finance owns internal customer business ledgers, charts of accounts, balanced journal drafts, immutable posting/reversal history, period close, summaries and evidence-bound reconciliation. It does not own Spyglass subscription billing or execute payments.

The first release supports one currency per ledger and integer minor-unit amounts. A posting contains two to 100 lines, each line contains exactly one positive debit or credit, and total debits equal total credits without overflow. Account types determine normal balance. A posting account must be active, permit posting and belong to the exact ledger; parent cycles are rejected transactionally.

Agents and members may create and revise drafts under Finance mutation access. Posting, reversal, period close and reconciliation confirmation require an Owner or Administrator plus a fresh optimistic version. Posting also requires accepted Knowledge Evidence. Descriptions, references and memos are customer content and never appear in operational metrics or redacted events.

## Construction sequence

1. Typed money, ledger/account, journal/reversal, period and reconciliation kernel.
2. Account-owned forced-RLS schema, immutable redacted events, per-ledger numbering, movement fencing and erasure/restore participation. **Constructed.**
3. Classified repository and application service with idempotency, optimistic versions and exact Work/Run/Invocation/Evidence references. **Constructed for Ledger/Account lifecycle, balanced drafts, posting, reversal and reconciliation; query pages remain.**
4. Stable query pages, summaries and bounded cursors. **Constructed.**
5. Generated HTTP, optional MCP and private Finance surfaces with package/read-only/suspended behavior. **Generated HTTP constructed.**
6. Agent draft tools and Attention-governed proposals; no workload-direct posting.
7. Prototype transformation, synthetic stage reconciliation/recovery and production role/retention grants.

## Invariants

- Every row is Account scoped even when a parent key would appear sufficient.
- Posted and reversed entries and their lines cannot be updated or deleted.
- A reversal is a new posted entry with swapped lines and a unique one-to-one link to its original.
- Closing a period is monotonic and blocks posting into the closed interval.
- Reconciliation never hides a difference; only a zero-difference record can be confirmed.
- Finance never stores provider credentials, payment instruments or Stripe subscription state.
- Account movement, erasure and restore enumerate every Finance table before the package can be enabled.

## Persistence checkpoint

Migration `000056_finance_foundation.sql` adds ten Account-owned tables for ledgers, close evidence, posting accounts, per-ledger counters, entries, lines, entry evidence, reconciliations, reconciliation evidence and redacted events. Every table has forced RLS and movement fencing. Posting is a database-validated transition: it requires two to 100 balanced lines, active posting accounts in the exact ledger, immutable Evidence, matching currency, an open period and an overflow-safe total. Posted lines/evidence and confirmed reconciliation evidence are immutable; reversal is the only permitted posted-entry state change and is linked one-to-one.

The fresh PostgreSQL 17 integration test proves posting, immutable history, linked reversal, explicit mismatch, confirmation, evidence-backed close, closed-period rejection, event immutability and cross-Account RLS denial. Erasure hooks capture exact counts for every Finance table.

## Application/persistence checkpoint

The classified Finance repository now performs every write inside a serializable Account transaction and restores durable rows through the typed kernel. Request UUIDs are immutable event identities: exact retries of Ledger, posting-account and entry creation return the first committed result without consuming another entry number; post, reversal and reconciliation confirmation retries converge on the original transition. A reversal atomically allocates its per-ledger number, inserts and posts the swapped entry, links the original and writes content-redacted evidence for both histories.

Reconciliation does not trust a caller-supplied ledger balance. The repository calculates the as-of balance from immutable posted/reversed lines using the posting account's normal balance, rejects aggregate overflow and supplies that value to the domain comparison. Multiple discrepancy records may remain explicit for one statement date, while a partial uniqueness rule permits only one confirmed reconciliation for the exact Ledger/account/date. Application authorization separates member draft/reconciliation preparation from Owner/Administrator Ledger setup, posting, reversal and confirmation.

Ledger, chart and draft lifecycle commands are now complete at the application/persistence boundary. Revision, monotonic evidence-backed close and ordered archival all require a fresh version and an immutable event identity; exact retries restore the first result. Members may revise only drafts, while posting and reversal remain human Owner/Administrator transitions. Draft revision atomically replaces balanced lines/evidence, rechecks the closed period and writes only content-redacted event fields. Posting-account moves verify an active parent in the same Ledger and reject recursive parent cycles. An account cannot be archived while it has an active child or appears in a draft, and a Ledger cannot be archived while it has any active account or draft. Containerized PostgreSQL tests cover those guards, lifecycle replay, numbering, draft replacement, restore, reversal and mismatch-to-corrected reconciliation.

The read boundary now provides Account-authorized detail retrieval plus bounded keyset pages for Ledgers, posting accounts, journal entries and reconciliations. Ledger summaries include account/draft counts and calculated income, expense and net values; chart summaries calculate each account balance according to its immutable normal-balance rule. Aggregate SQL uses PostgreSQL numeric arithmetic and refuses values outside the signed minor-unit range instead of wrapping. Ledger/chart cursors are deterministic `(code,id)` keys; journals use `(entry_date,entry_number)` descending and reconciliations use `(as_of,id)` descending. Dedicated indexes support the unfiltered Ledger/chart keysets, while existing journal and reconciliation indexes cover their page order. Fresh containerized tests prove traversal without duplicates, restored detail and posted/reversed balance behavior.

Eight generated HTTP reads now expose those detail and page operations through the canonical Finance service. The routes bind every target to routed Account authority, preserve the same package/read-only/suspended decisions as the application boundary, publish weak version ETags for mutation preconditions, and use typed opaque cursors whose shape is specific to the corresponding stable keyset. OpenAPI response-validation tests cover every page family.

Thirteen generated HTTP commands complete the same lifecycle without introducing transport-owned authority or models. Every command binds its `Idempotency-Key` to signed route authority; every mutation of an existing aggregate requires the current weak `If-Match` ETag. Ledger creation/revision/close/archive, chart creation/revision/archive, draft creation/revision/post/reversal and reconciliation creation/confirmation all call the canonical application service. No-body transition routes cannot smuggle command fields, JSON routes reject unknown fields and reversals return both immutable outcomes with the new posting's canonical location. Generated Go/TypeScript contracts are drift-free and every command response validates against OpenAPI. MCP and private Finance surfaces are the next slice.

Fresh-database certification now includes Finance in the complete cell-erasure inventory rather than testing its count triggers only in isolation. The suite seeds all ten Finance tables, grants only the erasure function role the required table access, proves exact per-table tombstone counts (including two journal lines), confirms zero residual rows for the erased Account, preserves another Account and proves replay/concurrent erasure convergence. Migration-ledger certification also counts migration 56 explicitly.
