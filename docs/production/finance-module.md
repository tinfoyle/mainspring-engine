# Finance module

- Status: scope decided; typed kernel in construction
- Package boundary: Finance
- Decision: [ADR-0006](decisions/0006-finance-operational-ledger.md)

## Launch contract

Finance owns internal customer business ledgers, charts of accounts, balanced journal drafts, immutable posting/reversal history, period close, summaries and evidence-bound reconciliation. It does not own Spyglass subscription billing or execute payments.

The first release supports one currency per ledger and integer minor-unit amounts. A posting contains two to 100 lines, each line contains exactly one positive debit or credit, and total debits equal total credits without overflow. Account types determine normal balance. A posting account must be active, permit posting and belong to the exact ledger; parent cycles are rejected transactionally.

Agents and members may create and revise drafts under Finance mutation access. Posting, reversal, period close and reconciliation confirmation require an Owner or Administrator plus a fresh optimistic version. Posting also requires accepted Knowledge Evidence. Descriptions, references and memos are customer content and never appear in operational metrics or redacted events.

## Construction sequence

1. Typed money, ledger/account, journal/reversal, period and reconciliation kernel.
2. Account-owned forced-RLS schema, immutable redacted events, per-ledger numbering, movement fencing and erasure/restore participation.
3. Classified repository and application service with idempotency, optimistic versions and exact Work/Run/Invocation/Evidence references.
4. Stable query pages, summaries and bounded cursors.
5. Generated HTTP, optional MCP and private Finance surfaces with package/read-only/suspended behavior.
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
