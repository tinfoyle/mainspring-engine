# ADR-0001: Use Go and an HTML-over-the-wire frontend

Status: Proposed  
Date: 2026-08-06

## Context

Mainspring will initially run on a cost-conscious Hostinger VPS. The application needs a public site, billing and onboarding screens, tenant login, boardroom chat, schedules, approvals, documents, and operational dashboards. It should avoid a resource-heavy production JavaScript runtime while remaining responsive on desktop and mobile.

## Decision

Use Go for the control plane, tenant gateway, tenant boardroom service, RAG service, provisioner, and Temporal workers.

Build the web interface using:

- `templ` for compiled Go components
- HTMX for forms, navigation, partial page replacement, and progressive enhancement
- Server-Sent Events for boardroom messages, progress, and notifications
- Tailwind CSS compiled to static CSS using its standalone CLI
- Small, focused vanilla JavaScript modules where browser-local behavior is necessary
- Go `embed` for shipping static assets inside the application binary

Browser-to-server commands use ordinary HTTP requests. Server-to-browser boardroom updates use SSE. SSE events have durable sequence identifiers so reconnecting clients can request events after their last received cursor.

Node.js is not required at production runtime. A future feature may use build-time JavaScript tooling or an isolated rich client component without changing the server architecture.

## Consequences

- The production artifact is a small set of Go binaries or a single multi-mode binary.
- Server-rendered HTML remains the canonical view representation.
- The backend does not need a duplicate client-side state model for most screens.
- Live UI event persistence and replay must be designed explicitly; SSE is transport rather than storage.
- Highly interactive features such as a visual workflow designer may require a targeted client-side component later.

## Alternatives considered

- **Next.js/React runtime:** rejected for the MVP because it adds a Node production runtime and a separate application layer.
- **A full client-side SPA:** rejected because most Mainspring interactions map well to server-rendered forms, fragments, and event streams.
- **Go standard templates only:** viable, but `templ` gives composable, typed components and better organization.

## Revisit when

- Offline-first operation becomes a product requirement.
- The application gains complex local editing or visualization that is inefficient with HTML-over-the-wire updates.
- Frontend staffing or ecosystem requirements materially change.

