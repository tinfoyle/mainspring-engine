# Phase 3 — Vue SPA product-surface plan

- Status: Approved; active construction plan after the local backend boundary
- Decision date: 2026-08-24
- Application: Infinite Ocean: Spyglass
- Predecessor: [Phase 3 local backend completion plan](phase-3-local-backend-completion-plan.md)
- Contract guide: [Spyglass API and MCP interaction guide](api-mcp-interaction-guide.md)
- Deployment work: Deferred until this plan's local product-surface exit gate passes

## 1. Objective

Replace the desktop-first private browser shell with the final customer-facing Vue 3 and TypeScript single-page application. The application is mobile-first, uses the generated HTTP contracts without inventing domain behavior, and makes **Your Turn** the primary signed-in workflow.

The product surface must feel deliberately designed at phone widths. Narrow layouts are not reduced desktop grids: navigation, information density, action placement and detail disclosure must change to match the user's immediate task.

## 2. Fixed decisions

- The private Spyglass application is a Vue 3, TypeScript SPA.
- The public Infinite Ocean website remains a separate surface; changing its framework is not part of this plan.
- The SPA consumes `api/spyglass.openapi.json` through the generated TypeScript contract and types.
- Browser API traffic remains same-origin under the application origin wherever practical. Deployment may route `/api/` to the Go application without exposing session credentials cross-origin.
- The existing server-rendered browser shell is a functional and rollback reference, not the visual foundation of the new application.
- Mobile behavior is designed first and enhanced at wider breakpoints.
- **Your Turn is the default destination after authentication and Account selection whenever the user has Account access.**
- No GHCR publication, Hostinger Stage mutation, LKE deployment or production release is part of this construction plan.

## 3. Experience rules

### 3.1 Mobile-first layout

- Begin with a usable 320px-wide, single-column layout and enhance through content-driven `min-width` breakpoints.
- No core route may require horizontal page scrolling at 320px CSS width.
- Body text defaults to a legible mobile scale; metadata may be quieter but cannot become unreadably small.
- Interactive targets are at least 44 by 44 CSS pixels unless the target is inline text with an equivalent larger action.
- Primary actions remain reachable without precision pointing and without competing clusters of equally prominent buttons.
- Tables transform into task-oriented lists, summaries or drill-down routes on narrow screens; they are not merely squeezed.
- Filters and secondary fields move into dismissible sheets or disclosure regions when they would crowd the primary task.
- Content uses progressive disclosure: summary first, decision context second, full evidence and history on demand.

### 3.2 Navigation

- Mobile uses one compact sticky application header and one clearly labeled menu control.
- The menu opens a focus-contained navigation drawer with direct, plainly named destinations; it does not use nested fly-out menus.
- Your Turn is the first product destination and shows an actionable count when one is available.
- Account selection is accessible from the header and menu without occupying permanent page width.
- Package-disabled destinations communicate why they are unavailable without filling the menu with upgrade noise.
- Desktop may enhance the same information architecture into a collapsible rail. Mobile and desktop do not maintain different route taxonomies.
- Back behavior, deep links and browser history remain predictable; drawers and dialogs do not replace routes for durable detail views.

### 3.3 Page clarity

- Each route has one clear purpose, one primary heading and at most one visually dominant action.
- Dashboards do not place every available metric and command above the fold.
- Empty, loading, stale, offline, unauthorized, read-only and error states receive intentional layouts rather than generic placeholders.
- Secondary metadata and audit history cannot displace the current decision or task.
- Destructive, consequential and externally visible actions receive explicit confirmation proportional to their risk.
- Customer values render as text and no session, customer content or governed draft is placed in persistent browser storage.

## 4. Your Turn — primary product workflow

Your Turn is the first complete feature slice and the acceptance reference for the rest of the SPA. It unifies information requests, assigned Work reviews, consequential approvals and uncertain-action recovery without erasing their different authority and safety rules.

### 4.1 Mobile queue

- Default to an actionable queue, not an overview dashboard.
- Each item states what is needed, why it needs the current user, its Account context, urgency and relevant age.
- Use short cards or rows with stable status language. Avoid dense multi-column tables.
- Keep common sorting and the active filter visible; place the full filter set in a sheet.
- Preserve scroll position and queue state when returning from an item.
- Provide useful empty states: nothing waiting, filtered result empty, temporarily unavailable and Account/package access unavailable.

### 4.2 Task detail and completion

- Narrow screens use a dedicated detail route rather than a permanent split pane.
- Lead with the requested decision or information, then show the minimum context required to act safely.
- Put evidence, policy, history and technical metadata behind clear disclosure sections without concealing required decision inputs.
- Keep the primary answer, review or approval control reachable near the bottom of the viewport when the task is actionable.
- Explain read-only, expired, superseded, conflicted and dual-control states in plain language.
- Preserve tab-scoped drafts across recoverable navigation and network failures without persisting customer material across browser restarts.
- On version conflict, reload the authoritative state and require a fresh human decision; never silently replay stale approval intent.
- After successful completion, announce the result, return to the queue and make the next item easy to open.

### 4.3 Your Turn acceptance

- A phone user can find, understand and complete every eligible Your Turn item with one hand and without horizontal scrolling.
- Information, review, approval and recovery flows preserve their generated API headers, authority checks, confirmation and recovery semantics.
- Keyboard, screen-reader and touch journeys reach equivalent outcomes.
- Queue refresh, optimistic concurrency, duplicate submission, offline recovery and session expiry have automated browser coverage.
- Your Turn passes the supported-device and accessibility matrix before later feature slices copy its interaction patterns.

## 5. SPA architecture boundary

- Keep the SPA in a new top-level application workspace, separate from `website/`, `prototype/ui/` and the Go browser-shell assets.
- Organize code by product feature and route, with shared application-shell and design-system layers.
- Use the generated TypeScript inventory and types as the only transport DTO source.
- Centralize session handling, selected-Account state, package/role capability projection, problem normalization, ETag handling, idempotency keys, opaque pagination and event-stream recovery.
- Server state stays in the query/cache layer; local component state is reserved for presentation and unsubmitted interaction state.
- Durable detail pages use routes. Temporary confirmation, filtering and compact editing may use dialogs or sheets.
- The Go application remains the authority for identity, Account access, packages, roles, versions, commands and consequences. The SPA does not recreate authorization or domain transitions.

## 6. Construction order

### UI0 — Foundation and contract proof

- Scaffold the Vue 3 and TypeScript SPA with routing, test, lint, type-check and production-build gates.
- Consume the generated API types and prove one authenticated query and one idempotent, versioned mutation locally.
- Establish design tokens and accessible primitives for buttons, fields, links, status, alerts, dialogs, drawers, disclosure and live announcements.
- Add phone-width visual fixtures before building feature pages.

Exit: the SPA builds reproducibly, runs through the local Docker application origin and exercises the real generated boundary without copied DTOs.

### UI1 — Session, Account and simple navigation shell

- Implement authentication/session-expiry states, Account selection and package-aware route guards.
- Implement the sticky mobile header, single navigation drawer and desktop enhancement.
- Make Your Turn the authenticated default route.
- Establish loading, offline, error, unauthorized, locked and read-only application states.

Exit: users can authenticate, select an Account and navigate the complete empty shell at supported phone, tablet and desktop widths.

### UI2 — Your Turn vertical slice

- Build the queue, filters, detail routes and completion flows for information requests, Work reviews, consequential approvals and action recovery.
- Implement tab drafts, current-ETag mutation handling, fresh idempotency keys, conflict recovery, dual control, live announcements and next-item flow.
- Complete phone, keyboard, screen-reader, reduced-motion, zoom/reflow and reconnect acceptance.

Exit: Your Turn is locally feature-complete and becomes the interaction-quality reference for all later modules.

### UI3 — Work and supporting context

- Build Work queues, Work detail and lifecycle commands using the established mobile list/detail patterns.
- Add Knowledge and Baseline context required to understand and resolve work without overloading Your Turn.
- Preserve links back to the originating Your Turn item and queue position.

Exit: the core task loop from Your Turn through Work and governed knowledge is complete locally.

### UI4 — Agents and conversations

- Build mobile conversation, Persona, Boardroom and Run surfaces with bounded streaming and recovery.
- Keep consequential proposals routed through Your Turn rather than embedding unsafe shortcut approvals in chat.

Exit: users can inspect and operate Agent workflows on supported devices without bypassing governance.

### UI5 — Remaining product packages and Account management

- Migrate Finance, Marketing, Integrations, schedules, billing, membership, security, export and Account lifecycle surfaces.
- Apply the same route clarity, menu behavior, list/detail separation and responsive acceptance established by Your Turn.

Exit: every supported customer use case has a Vue route using the generated boundary; no launch package depends on a prototype page.

### UI6 — Consolidation and local release boundary

- Complete cross-feature consistency, performance budgets, accessibility, device/browser and failure-recovery certification.
- Remove or disable private legacy routes only after Vue parity, automated coverage and a documented rollback point exist.
- Produce the final local product-surface completion report and update deployment inputs without publishing or deploying them.

Exit: the application is feature-complete locally and ready for the separate Stage/production release plan.

## 7. Required test matrix

At minimum, automated responsive coverage includes 320, 360, 390 and 412 CSS-pixel phone widths, a representative tablet width and desktop widths. Real-device certification covers current iOS Safari and Android Chrome in addition to desktop browsers.

Every feature slice covers:

- loading, empty, partial, stale and server-error states;
- offline, reconnect, request cancellation and session expiry;
- unauthorized, locked, package-disabled and read-only behavior;
- keyboard-only operation, visible focus, screen-reader names/order and live announcements;
- 200% and 400% zoom/reflow, reduced motion, contrast and forced colors;
- long customer text, localization-safe wrapping and no horizontal page overflow;
- exact mutation idempotency, current ETag use and conflict recovery; and
- safe navigation with unsaved or in-flight work.

## 8. Final exit gate

Phase 3 product-surface construction is complete only when:

- Your Turn is the default, fully certified primary workflow;
- every launch use case is available through the Vue SPA;
- the mobile menu and route hierarchy remain consistent across packages;
- supported phone routes are clear, touch-usable and free of horizontal page scrolling;
- generated contracts, authorization outcomes and mutation safety remain unchanged;
- the complete local Docker, frontend, Go, contract, accessibility and browser suites pass; and
- Stage, GHCR and LKE work can begin without further application construction.

