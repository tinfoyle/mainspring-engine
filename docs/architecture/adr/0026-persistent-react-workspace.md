# ADR-0026: Use a persistent React workspace for interactive tenant surfaces

Status: Accepted
Date: 2026-08-13

## Context

The original HTML-over-the-wire tenant interface was appropriate for the first vertical slice, but Mainspring's core workflows are now long-lived conversations, autonomous work, streamed agent progress, evidence interviews, approvals, document recall, and shared financial records. Full document navigations and fragment replacement made these workflows feel discontinuous, reset local UI state, and complicated reliable transcript scrolling.

## Decision

Authenticated, high-interaction tenant surfaces use a React 19 and TypeScript workspace compiled by Vite into static assets embedded in the Go binary. Go remains the production runtime and source of authorization and business rules.

The browser uses History API navigation, TanStack Query for server-state caching, optimistic mutations where rollback is safe, and server-sent events for ticket, boardroom, and owner-attention snapshots. The server exposes tenant-scoped `/api/v2` JSON contracts and keeps CSRF checks on mutations. Browser navigation receives the persistent workspace; non-browser clients and explicit `?legacy=1` requests retain server-rendered HTML for diagnostics and smoke-test compatibility.

Motion is restrained, respects `prefers-reduced-motion`, and communicates state changes rather than delaying work. Drafts remain local until sent. Agent progress, searches, sources, approvals, and errors remain visible rather than being hidden behind indefinite spinners.

## Consequences

- The sidebar and top bar persist across core navigation.
- Work, ticket conversations, Your Turn, boardrooms, documents, finance, baseline, agents, home, and inbox update without full-page reloads.
- Go continues to serve one deployable binary; Node is a build-time dependency only.
- JSON response models are intentionally separate from database models and can evolve as a versioned UI contract.
- Legacy pages remain available while secondary administration and integration surfaces migrate incrementally.
- Client interaction, accessibility, reduced-motion, and no-reload contracts require dedicated regression tests.

## Alternatives considered

- Continue expanding HTMX fragment swaps: rejected for the core workspace because local state, nested streams, optimistic feedback, and stable scroll behavior had become difficult to coordinate.
- Introduce a separate Next.js service: rejected because Mainspring does not need a second production runtime or server-side rendering tier.
- Rewrite the entire tenant application at once: rejected in favor of route-by-route migration with a legacy escape hatch.

## Revisit when

Revisit the custom router if route nesting, data loading, or error-boundary needs exceed its deliberately small scope, or if the UI is split into independently deployed applications.
