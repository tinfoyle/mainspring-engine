# Finance module

- Status: typed kernel, forced-RLS persistence, governed lifecycle, stable query summaries, generated HTTP, typed MCP transport, private package-aware workspace, least-authority Agent draft tools and Attention-governed posting execution constructed; shared MCP production bootstrap, prototype transformation and applied certification remain
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
5. Generated HTTP, optional MCP and private Finance surfaces with package/read-only/suspended behavior. **Constructed.**
6. Agent draft tools and Attention-governed proposals; no workload-direct posting. **Constructed, including durable post-approval execution and exact Finance-event reconciliation.**
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

The MCP transport now publishes 21 typed Finance tools over the identical query and command service: eight reads and thirteen governed mutations. OAuth bearer authority is rebound to the requested Account and Finance package for every call; mutation tools retain caller operation UUIDs and expected versions, expose bounded opaque cursor families, and map failures to a content-free Finance vocabulary. The MCP draft tool fixes provenance to `mcp` and cannot post implicitly. Close, archive, post and reverse tools advertise destructive semantics, while descriptions state the human-manager and evidence gates enforced by the canonical service. Tests prove the complete deterministic schema surface, routed authorization, MCP provenance, opaque pagination, missing-version rejection and backend-error redaction. A shared production MCP bootstrap remains rather than a Finance-specific duplicate server.

## Private application checkpoint

The authenticated `/app/finance` workspace is now a first-class package route instead of a dead Overview anchor. It derives enabled and read-only behavior from the selected Account's Finance entitlement, scopes every browser request to that Account and uses the generated HTTP boundary with same-origin session credentials. Ledger summaries, chart balances, journal state and reconciliations are discoverable without mutation access; read-only rendering omits every command form.

Enabled users can create and revise Ledgers, bind evidence to monotonic period closes, archive fully retired Ledgers, maintain posting accounts, create exactly balanced two-line drafts, post and reverse through separately confirmed version-bound actions, and propose or confirm evidence-backed reconciliations. Existing-resource commands first obtain the canonical ETag and submit it through `If-Match`; every mutation receives a fresh UUID `Idempotency-Key`. The browser never persists customer content or session material in web storage, renders API values through DOM text nodes and announces command results through an assistive-technology live region. Template/client contract tests cover package/read-only gates, lifecycle endpoint use, optimistic versions and unsafe DOM/storage exclusions.

## Agent draft checkpoint

Published Personas may now receive three narrowly defined Finance capabilities: list Ledger summaries, list the active chart for one Ledger and create one balanced journal draft. The runner broker records the reads as read-only and the draft as an additive mutation; the global tool router independently reauthorizes the Finance package for each call, resolves current placement and forwards only a fresh signed Account/cell proof. Bearer or pod credentials never cross into the cell.

Draft creation uses a private cell route, fixes source to `agent`, derives the invocation from authenticated runner identity and requires an explicit Run ID. Migration `000057_finance_agent_provenance.sql` adds a composite database foreign key that proves the invocation belongs to that exact Run, defeating a valid-but-mismatched provenance pair even under direct SQL. The Finance service accepts workloads only at its read and draft boundaries; Ledger/chart management, reconciliation and all posting/reversal commands remain user-only. Fresh PostgreSQL 17 certification passes the complete migration suite, including matched provenance acceptance and mismatched Run/invocation rejection.

## Agent posting checkpoint

Published Personas may separately receive `finance.entry.post` as a proposed-action capability. It is not a runner tool and cannot be invoked directly. A proposal freezes only `{entry_id, expected_version}` in Attention; an Owner or Administrator must approve that exact payload before the runner-broker's post-approval worker can lease it. The approving User is recorded as the journal posting actor, while the approval operation UUID is reused as both the runner-action idempotency key and immutable Finance event/correlation UUID.

Migration `000058_approved_action_execution_queue.sql` closes the previously missing handoff between approval and execution. It also decouples the durable approval window from the expired runner exchange while retaining the exact invocation/digest binding. Its cross-Account queue contains identifiers and timing only; the broker receives customer payload bytes solely through an execute-only security-definer claim, then re-hashes them before calling any handler. The first lease may execute once. A lost response or expired lease can invoke only Finance's exact event lookup, never `PostEntry` again. A missing exact event is a proven no-effect result for the transactional Finance store; database uncertainty remains unknown. Unknown reconciliation is delayed and bounded to three total attempts before the existing dual-control manual-resolution surface becomes the only resolution path.

The broker database role gains only the claim/completion functions plus the RLS-scoped Finance columns and child rows required to validate and post a journal entry. Runner containers receive no database authority. Integration tests prove that an approved action is claimable after its originating runner is terminal, concurrent/early claims stay unavailable, retries switch permanently to reconciliation and unresolved effects stop automatically after the bounded attempt count.
