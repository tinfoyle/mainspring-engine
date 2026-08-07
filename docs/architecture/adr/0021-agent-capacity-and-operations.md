# ADR-0021: Enforce tenant execution capacity and usage budgets

Status: Proposed
Date: 2026-08-07

## Context

Durability without admission control can turn retries, schedules, or a busy tenant into unbounded provider spend and host contention.

## Decision

Store a tenant execution policy covering concurrent invocations, queued runs, monthly token allowance, and monthly estimated cost. Serialize admission with PostgreSQL advisory locks, count active reservations against quotas, reconcile usage after success, and release failed attempts. Maintain a persistent provider circuit that opens after repeated failures. The runner controller also enforces a global slot limit. Expose run, invocation, latency, usage, denial, and circuit state on an owner/admin operations page and emit structured warnings for capacity, quota, provider, and cleanup events.

## Consequences

Concurrency and spend have enforceable ceilings across worker restarts. Estimated cost is deliberately configurable until provider-specific pricing is added. Queue-full and quota errors surface before additional work is accepted.

## Alternatives considered

- In-memory semaphores only: rejected because multiple workers and restarts would bypass them.
- Observe usage without blocking: rejected because alerts arrive after capacity or cost has already escaped.

## Revisit when

Plans require shared organization budgets, priority queues, or provider-specific price tables.
