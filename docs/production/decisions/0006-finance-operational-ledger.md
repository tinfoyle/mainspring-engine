# ADR-0006: Finance is an internal operational ledger

- Status: Accepted
- Date: 2026-08-22
- Owners: Finance, Attention, platform security

## Context

The prototype contains ledgers, hierarchical accounts, balanced journal drafts, posting, reversal and summary views. It stores these in one tenant database, uses a process-wide entry sequence, permits an Agent with `finance.manage` to post or void directly and writes complete before/after business payloads into its event log. It has no Account movement boundary, forced RLS, optimistic versioning, period close, evidence-bound reconciliation or separation between internal accounting records and external payment effects.

Phase 3 also has a distinct Billing module for Spyglass subscriptions and an Attention/action-ledger boundary for consequential provider effects. Turning Finance into another billing system or a general payment executor would merge unrelated authority and greatly expand the compliance target. No launch requirement identifies a formal accounting exchange format or third-party accounting system.

## Decision

Finance is an Account-owned internal operational ledger, not a formal general-ledger product and not a payment rail.

1. Each ledger freezes one ISO 4217-style three-letter currency. Amounts are signed-safe integer minor units; floating-point values are never accepted.
2. Account codes and entry numbers are unique only inside their Account-owned ledger. Every aggregate is optimistic and forced-RLS scoped.
3. Agents and ordinary members may prepare balanced drafts when package/object policy allows. Only an independently authenticated Owner or Administrator may post, reverse, close a period or confirm a reconciliation.
4. A posted entry and its lines are immutable. Correction creates a separately numbered, linked reversal; it never edits or deletes the original history.
5. Posting requires immutable Knowledge Evidence reviewed by the human poster. Period close is monotonic: entries dated on or before the close date cannot be created, posted or revised into that period. Reopening is not a launch capability; a documented correction is posted in an open period.
6. Reconciliation binds one posting account, an as-of date, external statement balance, calculated ledger balance and immutable Knowledge Evidence. A mismatch remains explicit and cannot be falsely confirmed.
7. Finance events are immutable and content-redacted. They retain identifiers, versions, amounts, currency, action and evidence identities—not descriptions, memos or arbitrary before/after payloads.
8. External payment, transfer, email or provider mutations are proposals consumed by Attention and the action ledger. Finance records the resulting evidence/reference after reconciliation but never executes the effect itself.
9. Prototype entries are transformed into reviewable drafts. A legacy posted row is not admitted as authoritative until its balance, Account scope, evidence and period placement are reviewed.
10. Formal accounting interoperability, tax filing, bank feeds, invoice collection and payroll are outside the initial launch contract. Adding one requires a new ADR, connector policy and retention/compliance review.

## Consequences

- Billing and Finance cannot corrupt each other's state or authority.
- Agent bookkeeping remains useful without giving a workload direct posting or payment authority.
- Account movement, erasure and restore can reconcile one bounded PostgreSQL ledger model.
- Historical descriptions and memos stay customer content rather than leaking into operational events.
- The product can add export/import adapters later without treating an accidental prototype schema as a public accounting standard.

## Verification

- Pure kernel tests cover currency/amount overflow, balance, role policy, evidence, period close, optimistic versions, reversal and reconciliation.
- PostgreSQL tests must cover forced RLS, cross-Account denial, immutable posted rows/events, per-ledger numbering, concurrent posting/reversal, movement fencing and exact erasure/restore counts.
- HTTP/MCP/UI tests must prove enabled/read-only/suspended package behavior and deny workload-direct posting.
- Stage certification must reconcile a synthetic statement, inject a mismatch, recover it with a posted correction and erase the fixture without leaving Account-attributed rows.
