# Phase 3 — Vue customer-surface and SPA plan

- Status: Approved; active construction plan after the local backend boundary
- Decision date: 2026-08-24
- Application: Infinite Ocean: Spyglass
- Predecessor: [Phase 3 local backend completion plan](phase-3-local-backend-completion-plan.md)
- Contract guide: [Spyglass API and MCP interaction guide](api-mcp-interaction-guide.md)
- Privacy/commercial extension: [Privacy, analytics and affiliate architecture](privacy-analytics-affiliates.md)
- Deployment work: Deferred until this plan's local product-surface exit gate passes

## 1. Objective

Replace the desktop-first private browser shell and the provisional acquisition experience with one coherent customer-facing Vue 3 and TypeScript product surface. The authenticated application operates as a single-page application; the public landing, feature and pricing routes are rendered or pre-rendered for discovery, performance and resilient first load. The complete experience is mobile-first, uses the generated HTTP contracts without inventing domain behavior, makes **Your Turn** the primary signed-in workflow, measures landing/checkout/onboarding through a consented first-party analytics boundary, and remains available to EU customers through GDPR-capable operation.

The product surface must feel deliberately designed at phone widths. Narrow layouts are not reduced desktop grids: navigation, information density, action placement and detail disclosure must change to match the user's immediate task.

## 2. Fixed decisions

- The private Spyglass application is a Vue 3, TypeScript SPA.
- The new UI program includes the public Infinite Ocean landing page, complete Spyglass feature/package breakdown, pricing, signup handoff, checkout initiation and checkout-return experience.
- GDPR-capable consent, analytics, data-subject rights and lifecycle evidence are required in this phase; EU support is not deferred behind a US-only launch.
- Launch analytics prioritize landing-page acquisition, checkout conversion and onboarding completion, with optional Your Turn usability events that never contain task content.
- The phase includes the minimal backend extension required for consent receipts, first-party analytics enforcement, Affiliate enrollment/codes, immutable referral attribution and recurring commission accounting.
- Affiliate attribution is commercial transaction state and remains valid when optional analytics consent is refused or withdrawn.
- Public acquisition and the private application remain separate security, caching and deployment surfaces. They share a Vue design system and customer journey, not private session data or cache policy.
- Public acquisition routes must be server-rendered or pre-rendered where practical; essential product, feature and pricing content cannot depend on client-side JavaScript to become discoverable.
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

### 3.4 Public acquisition, features and checkout

The public experience must be a first-class product surface, not a decorative splash page attached to the application.

#### Landing page

- Communicate what Spyglass does, who it helps and the primary business outcome within the first mobile viewport.
- Make Your Turn the central product story: Spyglass gathers operational work and brings people only the questions, reviews and approvals that require them.
- Show a credible visual product preview using controlled demonstration content; never expose a live customer Account or imply unavailable behavior.
- Explain the operating loop from captured context through governed action and human attention.
- Provide clear paths to explore features, compare plans, create a free Account, select a paid offer and sign in.
- Establish trust through concrete security, governance, privacy and reliability claims that are traceable to implemented capabilities. Do not invent testimonials, customer logos or performance claims.
- Keep the page visually distinctive without excessive motion, autoplay media, crowded card walls or repeated competing calls to action.

#### Full feature breakdown

- Publish a complete browsable feature index covering Your Turn, Work, Knowledge, Baseline, Agents, schedules, Finance, Marketing, Integrations, Account administration, security, export and lifecycle behavior.
- Organize features first by customer outcome and workflow, then expose package and technical detail. Do not present an undifferentiated inventory of backend endpoints.
- Give each launch package a durable landing route with its purpose, primary workflows, included capabilities, governance boundaries, limitations and applicable plan availability.
- Show cross-package workflows so users can understand how Work, Agents, Knowledge and Your Turn operate together.
- Source package names, availability and pricing claims from the published Catalog or reviewed versioned content; unpublished capabilities remain absent.
- Link feature descriptions to pricing without forcing users to restart their discovery context.

#### Pricing and checkout

- Pricing uses the public Catalog contract, displays free and paid offers consistently, and fails closed to the last-known published Catalog rather than leaking provider identifiers.
- Plan comparison explains included packages, meaningful limits, trial/grace behavior and what changes on downgrade in plain language.
- Selecting an offer preserves only an opaque, validated offer intent through registration and verification.
- Account creation remains free. Only an authenticated authorized Account owner can initiate the separately confirmed checkout mutation.
- The server revalidates the selected offer and creates or reuses the Stripe Customer and hosted Checkout Session; the browser never submits a Stripe Price ID.
- The customer is redirected to Stripe-hosted Checkout for payment details. Spyglass owns the coherent pre-checkout review, cancellation return, success return and pending/failed projection states.
- The return page never grants access from the redirect alone. It waits for signed webhook projection, explains pending state and refreshes effective packages only from the local entitlement snapshot.
- Checkout retry uses exact idempotency semantics and cannot create duplicate purchase intent from repeated taps, back navigation or network recovery.
- Billing management and later plan changes use the same understandable package language and hand off to the short-lived Stripe Customer Portal where required.

### 3.5 Privacy, analytics and Affiliates

- Use the consent behavior, lawful-purpose registry, event boundaries, GDPR lifecycle and Affiliate architecture in [Privacy, analytics and affiliate architecture](privacy-analytics-affiliates.md).
- Landing, checkout and onboarding instrumentation is complete only when consent-granted, consent-denied and consent-withdrawn journeys produce identical customer outcomes.
- Affiliate code entry appears in the authenticated checkout review and is confirmed before Stripe redirection.
- The Affiliate dashboard exposes code, terms and aggregate earning states without identifying referred customers.
- Recurring commission is earned only from the durable local projection of a qualifying paid subscription invoice; browser analytics and redirect state are never financial authority.

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

## 5. UI architecture boundary

- Create a new top-level Vue UI workspace, separate from the legacy `website/`, `prototype/ui/` and Go browser-shell assets.
- Give the public acquisition site and private Spyglass SPA independent build targets with a shared design-system package, shared reviewed product terminology and generated API client boundary.
- Organize private code by product feature and route, with shared application-shell and design-system layers. Organize public code by acquisition journey and durable content route rather than mirroring private implementation folders.
- Use the generated TypeScript inventory and types as the only transport DTO source.
- Centralize session handling, selected-Account state, package/role capability projection, problem normalization, ETag handling, idempotency keys, opaque pagination and event-stream recovery.
- Server state stays in the query/cache layer; local component state is reserved for presentation and unsubmitted interaction state.
- Durable public and private detail pages use routes. Temporary confirmation, filtering and compact editing may use dialogs or sheets.
- The Go application remains the authority for identity, Account access, packages, roles, versions, commands and consequences. The SPA does not recreate authorization or domain transitions.

## 6. Construction order

### PA0 — Privacy, analytics and Affiliate backend extension

Checkpoint (2026-08-24): the consent, event-ingestion, browser-subject rights, raw-event retention, enrollment, attribution, qualifying recurring-invoice and statement kernels plus generated contracts are implemented and pass disposable PostgreSQL 17. Authenticated private consent subjects now bind to the current User without allowing a shared-browser reference to cross-link two identities. The passkey-verified privacy-rights boundary accepts access, correction, erasure, restriction, objection and portability requests across identity, Account, Affiliate and analytics scopes, tracks a calendar-month deadline, rejects duplicate open pairs and preserves immutable submission/cancellation evidence. The Vue Privacy route exposes that history beside immediate browser erasure and the existing Account export, lifecycle and identity-correction tools. The event/processing registry and legal-copy drafts are revision controlled. Remaining PA0 release gates are authorized staff fulfillment/response evidence and Affiliate support/appeal operations, commercial approval, refund/dispute reversal projection, enrollment suspension/closure operations and final legal/vendor/transfer approval. Feature flags remain closed while these decisions are unresolved.

- Approve the lawful-purpose/event registry, consent-policy version, retention schedule and analytics provider boundary.
- Implement consent receipts and server-side optional-event enforcement.
- Implement Affiliate enrollment, generated codes, checkout attribution, versioned commission rules and an immutable recurring commission ledger.
- Generate the HTTP contracts required by the Vue consent center, checkout review and Affiliate dashboard.
- Approve account-credit versus cash settlement before either is presented as available value.

Exit: privacy and Affiliate state has a tested local application boundary; analytics withdrawal cannot alter commercial attribution, and invoice replay cannot duplicate commission.

### UI0 — Foundation and contract proof

Checkpoint (2026-08-24): the top-level `ui/` workspace now builds separate Nuxt-rendered public and Vue-SPA private images, shares generated API contracts and design tokens/primitives, runs type-check/test/lint/build/audit gates in its Docker test target, and is wired into the local web/app origins. The first-party consent client refuses optional emission before consent, the rendered acquisition shell includes resilient privacy controls, and the private shell loads the current User, selected Account and Account choices with Your Turn as its default route. The Attention client proves assigned authenticated queries plus stable-idempotency/current-ETag mutations and conflict recovery without copied DTOs. Complete primitive/phone fixture coverage and applied device/browser acceptance remain UI0 exit work.

- Scaffold the Vue 3 and TypeScript workspace with independent public acquisition and private SPA build targets plus shared routing, test, lint, type-check and production-build gates.
- Consume the generated API types and prove one authenticated query and one idempotent, versioned mutation locally.
- Establish design tokens and accessible primitives for buttons, fields, links, status, alerts, dialogs, drawers, disclosure and live announcements.
- Establish the consent center, optional-event client boundary and generated Affiliate client types without loading an analytics SDK before consent.
- Add phone-width visual fixtures before building feature pages.

Exit: both UI targets build reproducibly, run through their local Docker origins and exercise the appropriate generated public/private boundaries without copied DTOs.

### UI1 — Session, Account and simple navigation shell

Checkpoint (2026-08-24): signup, verification/login and security setup now expose the same equal-choice first-party privacy control on the application origin while retaining a host-only signed preference reference. Successful registration, verification, Account creation and completed passkey-plus-recovery-code enrollment emit reviewed content-free events only after their authoritative server transitions; first Vue application entry is likewise consent-gated and tab-deduplicated. A public-site choice is not silently widened across subdomains, and declining or failing the optional measurement path leaves every onboarding transition unchanged. Package-aware route guards, complete shell destinations and applied phone/browser certification remain open.

- Implement authentication/session-expiry states, Account selection and package-aware route guards.
- Implement the sticky mobile header, single navigation drawer and desktop enhancement.
- Make Your Turn the authenticated default route.
- Establish loading, offline, error, unauthorized, locked and read-only application states.
- Instrument registration, verification, Account creation, security enrollment and first application entry through the reviewed onboarding event registry.

Exit: users can authenticate, select an Account and navigate the complete empty shell at supported phone, tablet and desktop widths.

### UI2 — Your Turn vertical slice

Checkpoint (2026-08-24): the hard-coded queue fixture has been replaced by entitlement- and role-aware live information, assigned-review, approval and recovery queues using generated types. Durable detail routes inspect exact version/digest/payload context; answer, review, approval, resolution-request and independent-confirmation forms use current versions, stable retry keys, tab-local drafts, conflict reload and consented content-free completion dimensions. Exact Knowledge facts are selectable for information completion, opaque cursors are drained through a bounded client, and stale queue data remains visible across offline/refetch failure before reconnect refresh. Unfinished legacy `/app/*` routes remain served by the Go shell instead of being prematurely swallowed by the SPA. Component tests prove live Account-scoped rendering, durable detail routing, offline stale-data behavior, exact-fact completion, Work change requests, explicit payload confirmation, evidence-based recovery requests and different-operator confirmation. Broader browser recovery coverage, applied accessibility/device acceptance and locally seeded HTTP end-to-end completion remain open.

- Build the queue, filters, detail routes and completion flows for information requests, Work reviews, consequential approvals and action recovery.
- Implement tab drafts, current-ETag mutation handling, fresh idempotency keys, conflict recovery, dual control, live announcements and next-item flow.
- Complete phone, keyboard, screen-reader, reduced-motion, zoom/reflow and reconnect acceptance.

Exit: Your Turn is locally feature-complete and becomes the interaction-quality reference for all later modules.

### UI3 — Public acquisition, feature catalog and checkout

Checkpoint (2026-08-24): the rendered public surface now tells the Your Turn-centered operating story, exposes a twelve-route feature and governance map, explains a cross-package workflow, and renders paid offer names, packages and current prices from the server-fetched public Catalog. Landing, feature, pricing, offer-selection and signup-handoff events remain consent-gated and content-free. Registration, verification, Account creation, security enrollment and first application entry now emit only reviewed content-free events after authoritative transitions and only when the applicable first-party consent permits them. The private SPA provides an owner/billing-administrator checkout review with an actively applied optional Affiliate code, explicit offer/referral confirmation, stable request idempotency and a dedicated Stripe return route that waits for the local signed-webhook subscription projection. Its ordinary-identity Affiliate dashboard exposes only the generated code and aggregate commission states. A referral proposed by a link is never silently applied, and analytics refusal does not change referral or checkout behavior. Free/paid journey HTTP certification, Catalog last-known-good policy, Stripe failure fixtures, structured data/social imagery and approved commercial settlement/reversal policy remain open.

- Build the public landing page around the governed Spyglass operating loop and Your Turn value proposition.
- Build the complete feature/package index, durable package pages, cross-package workflows and Catalog-backed plan comparison.
- Complete free signup handoff, selected-offer continuation, authenticated pre-checkout review, Stripe Checkout initiation and return/pending/failure states.
- Add Affiliate code entry and actively confirmed link intent, explicit attribution review and disclosure-safe referral behavior without coupling commercial credit to analytics consent.
- Add structured metadata, crawlable content, social previews, performance budgets, link checks and consented acquisition analytics that collect no customer business content.
- Certify the anonymous-to-free and anonymous-to-paid customer journeys at supported phone widths before later package pages are copied from these patterns.

Exit: a visitor can understand the complete product, select the correct offer, create an Account and safely reach a projected checkout outcome without encountering a visual or terminology break between the public and private surfaces.

### UI4 — Work and supporting context

Checkpoint (2026-08-24): the private Vue Work route now provides an Account-scoped paged queue, summary, durable item detail, create, assignment and lifecycle transitions with current-version conflict recovery and tab-only drafts. The governed Knowledge route separates proposed claims from accepted facts, exposes durable claim detail with the exact canonical value, digest and citations, and submits human, versioned, retry-safe decisions. Both clients consume generated contracts, and component tests cover their principal list/detail and mutation paths. Baseline context, explicit preservation of the originating Your Turn queue position, locally seeded signed-in HTTP completion and applied browser/device accessibility evidence remain UI4 exit work.

- Build Work queues, Work detail and lifecycle commands using the established mobile list/detail patterns.
- Add Knowledge and Baseline context required to understand and resolve work without overloading Your Turn.
- Preserve links back to the originating Your Turn item and queue position.

Exit: the core task loop from Your Turn through Work and governed knowledge is complete locally.

### UI5 — Agents and conversations

Checkpoint (2026-08-24): the private Vue Agents route now creates and lists Boardrooms, inspects the active Persona policy and manager configuration, presents durable conversation transcripts, and submits, polls and recovers Runs. Boardroom and Conversation detail use durable routes, polling is bounded and canceled on unmount, and consequential proposals link into Your Turn without rendering hidden approval payloads in chat. Component tests cover Boardroom creation, transcript loading and terminal Run recovery. Persona authoring/publishing, applied streaming/failure fixtures and supported-device/browser/assistive-technology acceptance remain UI5 exit work.

- Build mobile conversation, Persona, Boardroom and Run surfaces with bounded streaming and recovery.
- Keep consequential proposals routed through Your Turn rather than embedding unsafe shortcut approvals in chat.

Exit: users can inspect and operate Agent workflows on supported devices without bypassing governance.

### UI6 — Remaining product packages and Account management

Checkpoint (2026-08-24): the private Vue Affiliate route now uses the ordinary authenticated identity, exposes the generated public code and identity-safe aggregate commission ledger, and renders pending, settled and reversed entries without referred-customer data. Enrollment is strongly authenticated and feature-flagged; Account-credit mode permits only an Account the User owns, while `unconfigured` settlement blocks enrollment and makes no cash/credit promise. A crawlable public launch-terms route accurately labels candidate economics and policy as inoperative while the flags remain closed. Refund/dispute reversal projection, suspension/closure operations, support/appeal paths and commercial/legal approval remain backend release blockers.

Scheduling checkpoint (2026-08-24): `/app/schedules` and its durable detail route now use the generated Scheduling boundary for opaque pagination, create/revise, pause/resume, immediate trigger and deletion. The mobile-first editor makes IANA timezone, local recurrence, daylight-saving gap/overlap policy, missed-run policy and exact Boardroom/Persona context explicit; optional Account context is progressively disclosed and unsubmitted creation content remains tab-scoped. Commands bind the current durable version, retain one idempotency key across exact failed retries, require an operational reason, reload on conflicts and keep deletion behind explicit confirmation. Component/API tests cover durable navigation, recurrence presentation, conflict recovery, cursor opacity and retry identity. Schedules are therefore no longer a legacy-only UI6 surface. Applied browser/device/accessibility evidence and the remaining Finance, Marketing, Integrations, Baseline, billing and Account-management routes remain open.

Account-team checkpoint (2026-08-24): `/app/account` now provides a mobile-first selected-Account summary, strongly authenticated invitation creation, role-aware team roster, owner-only role and ownership controls, owner/administrator suspend/reactivate/remove controls, and ordinary-member self-service leaving. A new typed current-Membership query exposes the caller's exact durable Membership version without granting roster authority, so self-service never substitutes the unrelated Account version. Every change requires an operational reason; destructive transfer, removal and leaving require typed confirmation; version conflicts close the dialog and reload authoritative state; passkey/owner-enrollment failures lead explicitly to Security. Generated-contract, transport, API-client and component tests cover the read boundary, version binding, dual-version transfer, authority rendering, conflict recovery and strong-auth continuation. Account-team administration is therefore no longer a legacy-only UI6 surface. Applied browser/device/accessibility evidence and Finance, Marketing, Integrations, Baseline, billing, security, export and Account-lifecycle routes remain open.

- Migrate Finance, Marketing, Integrations, billing, security, export and Account lifecycle surfaces; retain the completed Affiliate, Scheduling and Account-team routes as UI6 reference implementations.
- Apply the same route clarity, menu behavior, list/detail separation and responsive acceptance established by Your Turn.

Exit: every supported customer use case has a Vue route using the generated boundary; no launch package depends on a prototype page.

### UI7 — Consolidation and local release boundary

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

Public acquisition and checkout additionally cover crawlability without client execution, metadata, structured content, broken links, Catalog fallback, offer discontinuation during signup, repeated checkout initiation, Stripe cancellation, delayed webhook projection and payment failure.

Privacy, analytics and Affiliate acceptance additionally covers zero optional emission before consent, immediate withdrawal, registry field enforcement, GDPR access/erasure/objection/retention behavior, consent-independent referral survival, self-referral denial, code suspension, invoice replay, refund/dispute reversal and referred-customer concealment.

## 8. Final exit gate

Phase 3 product-surface construction is complete only when:

- Your Turn is the default, fully certified primary workflow;
- the landing page clearly communicates the product and Your Turn value proposition on mobile;
- every launch feature and package has an accurate, browsable public explanation;
- free signup and paid checkout form one complete, safe and locally certified acquisition journey;
- landing, checkout and onboarding analytics are GDPR-capable, consented, content-free and lifecycle-complete;
- Affiliate enrollment, code attribution, recurring commission and aggregate dashboard behavior are locally complete under an approved settlement policy;
- every launch use case is available through the Vue SPA;
- the mobile menu and route hierarchy remain consistent across packages;
- supported phone routes are clear, touch-usable and free of horizontal page scrolling;
- generated contracts, authorization outcomes and mutation safety remain unchanged;
- the complete local Docker, frontend, Go, contract, accessibility and browser suites pass; and
- Stage, GHCR and LKE work can begin without further application construction.
