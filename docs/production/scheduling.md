# Scheduling module

- Status: typed recurrence/template and forced-RLS definition/lease foundation constructed; occurrence dispatch, customer surfaces and operations remain
- Owner: Scheduling application module
- Package boundary: schedule definitions use the package of their target; the first target is Agents

## Final contract

A customer schedule is an Account-owned, optimistic aggregate. It stores an explicit IANA timezone, a typed recurrence, a missed-run policy and an immutable execution template. It never stores an environment-local timezone or an unclassified cron string.

The initial recurrence vocabulary is:

- daily at an exact local hour/minute;
- weekly on one or more explicit weekdays at an exact local hour/minute;
- DST gap policy `skip` or `next_valid`;
- DST overlap policy `first` or `second`; and
- missed-run policy `skip` or `catch_up_one`.

The recurrence kernel searches real instants around each local calendar date. A spring-forward wall time therefore has zero real candidates, while a fall-back wall time has two. Policy selection is explicit and covered by `America/New_York` transition fixtures; behavior does not depend on the host's `TZ` setting.

The first execution template creates a new dated Agent Conversation/Run through the canonical Agents `StartRun` application service. It freezes Boardroom, run mode, selected Persona identities, subject, prompt and bounded Work/Knowledge/Document/Baseline attachment identities in the schedule definition. Each occurrence still creates a new Run, whose ordinary command path freezes the then-authorized exact Persona versions, context bytes, entitlement version, manager and operation identities. The schedule worker cannot impersonate the creator and cannot bypass current package, placement, capacity or object authorization.

## Construction sequence

1. Typed recurrence, timezone/DST behavior, missed-run policy and immutable Agent template. **Constructed.**
2. Forced-RLS schedule definitions, immutable events and identifier-only due queue with lease fencing. **Constructed through definition create/get/pause/resume and claim/fail retry; occurrence completion/advance remains with step 3.**
3. A workload-authorized occurrence command that reuses Agents admission and StartRun semantics without presenting a browser session or impersonating a User.
4. Deterministic occurrence, Conversation, Run and operation identities derived from schedule plus scheduled instant.
5. Pause, resume, update, delete and trigger-now commands; trigger-now is a separate occurrence and never changes recurrence state.
6. HTTP, optional MCP and private UI surfaces with generated contracts and enabled/read-only/suspended package tests.
7. Account movement, export, erasure, retention, dead-letter recovery, stage failure rehearsal and LKE scaling evidence.

## Persistence checkpoint

Migration `000051_scheduling_foundation.sql` adds Account-owned schedule definitions and immutable redacted events under forced RLS, plus a deliberately content-free cross-Account due queue. Customer definition access goes through a classified Account transaction repository: create is exact-replay idempotent, reads are Account isolated, pause/resume require the expected aggregate version, and Boardroom/Persona targets must be active and published in the same Account and Boardroom. Definitions cannot be edited through that repository in this slice.

An active definition creates or refreshes exactly one queue row; pausing removes it. The worker role receives execute-only claim, failure and content-free statistics functions, never table access. Claims use `FOR UPDATE SKIP LOCKED`, an exact lease UUID, bounded expiry and monotonically increasing attempt count. Failure requires the exact Account, schedule, scheduled instant and live lease and converges to bounded retry or dead letter. No occurrence is created yet: successful completion, recurrence advance, `catch_up_one`, deterministic Run creation and workload authorization remain the next construction boundary.

The new tables participate in Account movement write fencing and cascade-safe erasure counting. Fresh PostgreSQL integration proves repository replay, optimistic pause/resume, cross-Account denial, event immutability, due ordering, lease fencing, least privilege and pause cancellation. The complete `ubunturojo` Docker certificate passes normal and race suites, migrations, formatting, vet, API contracts, website build/lint/audit and object-policy checks.

## Invariants

- Definitions are Account isolated and versioned; editing never rewrites a prior occurrence.
- One scheduled instant can create at most one occurrence and one Run across retries or replicas.
- `catch_up_one` permits at most one immediate recovery occurrence after downtime; it does not replay an unbounded backlog.
- Paused, deleted, read-only, suspended or unauthorized schedules cannot dispatch.
- The due queue contains identifiers and timing only, never prompts, context values or provider credentials.
- Definition reads happen inside Account RLS after a queue lease is claimed.
- Trigger-now has its own deterministic identity and audit event and does not move `next_run_at`.
- Worker failures are classified, bounded and recoverable without direct business-table edits.
