# ADR-0020: Run provider CLIs in ephemeral constrained containers

Status: Proposed
Date: 2026-08-07

## Context

Codex CLI and future provider runtimes are higher-risk, resource-variable processes. Giving a long-lived tenant service shell or Docker access would enlarge both tenant and host blast radius.

## Decision

The worker calls a private runner controller. The controller alone accesses the Docker socket and creates one container per invocation with a non-root user, read-only root, dropped capabilities, no-new-privileges, CPU/memory/PID/time limits, bounded tmpfs, a disposable work volume, and an egress-only network. It injects only provider authentication, the host CA bundle when configured, invocation input, and a short-lived tool token. Completion, cancellation, startup reconciliation, and periodic reconciliation force-remove containers and anonymous volumes.

## Consequences

Provider crashes and filesystem writes are contained, while Compose can later map to Kubernetes Jobs. The controller is privileged infrastructure and must remain private. Provider credentials are still a secret-management concern.

## Alternatives considered

- Run CLIs inside the worker: rejected because process and filesystem isolation are weak.
- Give runners the Docker socket: rejected because it is effectively host control.

## Revisit when

The Kubernetes runtime backend is introduced or stronger egress policy is required.
