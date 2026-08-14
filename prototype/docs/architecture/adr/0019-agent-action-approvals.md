# ADR-0019: Gate external agent actions with durable approval

Status: Proposed
Date: 2026-08-07

## Context

An agent recommendation is not authority to send email or mutate an external service. Temporal retries can also duplicate effects if execution is not independently idempotent.

## Decision

Agents may propose, but never directly execute, external actions. Persist each supported proposal with a stable key, canonical payload hash, originating invocation, persona version, evidence, and action-ledger record. Only owners and admins may approve or reject. Approval binds the reviewed hash, and execution uses the ledger idempotency key. The boardroom workflow waits durably for an approval signal before completion. The MVP supports `email.send`; direct email-send tool registration is prohibited.

## Consequences

Every customer-visible mutation has provenance and a decision history. Retried approvals do not resend email. Ambiguous provider outcomes remain visible for reconciliation or manual review.

## Alternatives considered

- Ask for approval only in chat: rejected because it has no enforceable payload binding.
- Execute first and notify later: rejected because mistakes become external side effects.

## Revisit when

Tenants need policy-based preapproval for narrowly bounded, reversible actions.
