# ADR-0014: Use one tenant work queue for to-dos and tickets

Status: Proposed
Date: 2026-08-06

## Context

Business owners need a lightweight place to remember their own next actions and a structured place to track work raised by employees, personas, scheduled checks, and integrations. Separate to-do and ticket products would force the owner to monitor two queues, make automated work harder to find, and duplicate assignment and status behavior.

Agent-created work also needs provenance. A ticket must be traceable to the persona, schedule, boardroom, conversation, and durable run that raised it without making those execution records the source of truth for the visible task.

## Decision

Store both to-dos and tickets as tenant-scoped `work_items`. Each item has a `kind` of `todo` or `ticket` and shares a small lifecycle: `open`, `in_progress`, `waiting`, `done`, or `canceled`. Items also record priority, an optional due date, optional user or persona assignment, and their creation source.

The owner UI presents one work queue with kind, status, and text filters. To-dos optimize for quick capture; tickets communicate that the work needs structured tracking. Both retain completed history.

Persona, schedule, and system entry points use the same tenant persistence service and attach optional boardroom, conversation, and run identifiers. The Go harness must still validate `ticket.create` before an agent can create an item; provenance does not replace capability authorization.

## Consequences

- Owners have one operational inbox instead of separate task and ticket products.
- Automated and human-created work can be sorted and reviewed consistently.
- A simple shared lifecycle may eventually need ticket-only fields such as SLA targets, requester identity, comments, attachments, or external-system references.
- Tenant databases remain the source of truth for visible work; Temporal coordinates the automation that may create or wait on it.
- Completed and canceled items remain auditable, while the normal view stays focused on active work.

## Alternatives considered

- **Separate `todos` and `tickets` tables and screens:** rejected for the MVP because it duplicates lifecycle behavior and fragments the owner's attention.
- **Store tasks only in Temporal workflow state:** rejected because workflow history is not the tenant's queryable business record and should not drive the user interface.
- **Let each integration own its tickets:** rejected because external identifiers can be retained as references later, but Mainspring still needs a coherent tenant view.

## Revisit when

- Customers need SLA policies, queues, watchers, threaded comments, or ticket relationships.
- Two-way synchronization with an external service desk becomes a primary workflow.
- Assignment expands beyond a single owner or persona to teams and role-based queues.
