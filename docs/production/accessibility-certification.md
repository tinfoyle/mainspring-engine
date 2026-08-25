# Accessibility target and release certification

Status: local rendered-browser automation is executable; complete browser, assistive-technology, device, and deployed-origin evidence is still required

Spyglass targets **WCAG 2.2 Level AA** for the public Infinite Ocean website and every customer journey required to acquire, enter, operate, administer, and leave the product. Accessibility is a release property of an exact deployed artifact, not a one-time design review.

## Repository gate

The active `ui/` workspace runs TypeScript/Vue type checking, Vitest component and API-client tests, ESLint with the Vue accessibility recommended rules, both production builds and a high-severity dependency audit in the Docker test target. Native labels may associate controls by either valid nesting or explicit ID; any interaction-rule suppression must remain local to a documented conditional semantic state. The private tests cover the application navigation shell plus the principal Your Turn, Work, Knowledge, Baseline, Agents, Schedules, Finance, Integrations, Marketing, Account team, Billing, Security, Account export, Account lifecycle, Checkout and Affiliate list/detail/mutation states. Those fixtures now run 39 layout-independent axe-core WCAG 2.0/2.1/2.2 A/AA scans across the shell and all eighteen view families, including alternate route, role, package, program and workflow states. Detached Vue wrappers are cloned into the test document for the scan, and cleanup is mandatory even on failure. The standard `website-test` Docker target additionally builds the exact private Vue and public Nitro production artifacts and runs a digest-pinned Playwright checkpoint at 1280×900 in Chromium, Firefox and WebKit, 390×844 Chromium phone emulation, 320×800 Chromium reflow, and 1280×900 Chromium forced-colors emulation. 118 applicable launch-critical checks cover Your Turn, Checkout, GDPR Privacy, the fail-closed Affiliate state, owner-security onboarding, Account authority and Memberships, server-confirmed multi-Account switching and failed-switch restoration, Billing, Account portability and Account closure, durable Work queue/detail, governed Knowledge queue/claim review, resumable Business Baseline, Agents and Boardroom/conversation context, Schedules, Finance entry detail, Integration connection/execution detail, Marketing campaign/release detail, read-only evidence preservation, explicit missing-package upgrade boundaries, recoverable Account-load failure, capacity denial with local-draft retention, authoritative Work conflict reload, recoverable checkout-provider failure, the complete seventeen-route public acquisition/feature/policy inventory, consent-gated measurement, signup handoff, horizontal reflow, both mobile navigation focus loops, browser runtime errors, intentional synthetic API coverage, and browser-computed axe WCAG A/AA rules without excluding color contrast. The onboarding state proves that the security-completion event follows an authoritative incomplete-to-ready reload and remains distinct from first application entry. The Account-administration cluster proves that owner authority, paid-account state, export availability and closure consequences remain understandable across the six profiles. The operating-context and package-workspace clusters preserve exact Work outcome and responsibility, separate unreviewed Knowledge from accepted fact, expose attributable Baseline progress, and keep governed list/detail context durable across the signed-in product. They use no identity, credential, database, provider, or deployed environment. The gate found and corrected actual 4.3:1 Checkout-label, member-email-link, Agent-confidence and Marketing-release-eyebrow defects, a 2.82:1 landing-step defect, a narrow-screen governed-identifier overflow, an unreachable scrollable Baseline progress track, and an unmeasured desktop navigation CTA. The local composed smoke gate also fetches every current durable Vue route through the TLS application origin, including nested Work, Knowledge, Baseline, Boardroom, Conversation, Schedule, Finance, Integrations and Marketing routes plus Account administration, Billing, identity Security, Account exports and Account lifecycle. These checks protect important semantics, routing, layout, consent behavior and focus behavior but do not execute physical WebAuthn or Google OAuth ceremonies, hosted Stripe interaction, 200% text-only zoom, real touch hardware, mobile Safari/TalkBack behavior, or an assistive-technology accessibility tree. They therefore cannot establish WCAG conformance by themselves.

The Nuxt acquisition surface now has route-inventory, consent-state, Catalog-resilience, canonical/structured-discovery and rendered local TLS contracts across all seventeen crawlable routes. Its Docker verification starts the exact production Nitro build and requires every route to render successfully with English language metadata, a non-empty title, one header/main/footer/level-one-heading frame, a first-body skip link, a programmatically focusable main target, unique IDs and no axe-core WCAG 2.0/2.1/2.2 A/AA violation that jsdom can evaluate. The exact same Nitro output also has a real-browser checkpoint for every landing, feature, pricing and policy route in all three desktop engines and the Chromium phone profile. It proves analytics stays silent without a decision and after rejection, accepted acquisition CTAs emit the reviewed content-free handoff events, opaque offer selection survives rejection, public drawers contain and restore focus, pages do not overflow horizontally, and browser-computed axe including `color-contrast` passes.

The public mobile menu also has component contracts for modal semantics, page isolation, initial focus, first/last focus containment, Escape restoration, scroll locking and unmount cleanup; the rebuilt local desktop accessibility tree retains the Primary navigation landmark and omits mobile-only controls. Public consent component coverage requires equal visual treatment for accept/reject, purposeful focus movement between the preference panel and its reopening control, and an announced fail-closed first-choice error. The private Vue shell has equivalent drawer contracts plus route-change focus, modal focus containment/restoration and reversible background isolation. Its flat menu separates Workspace from Account administration, shows only enabled or read-only package destinations, replaces unavailable package rows with one Explore plans path and keeps Account selection outside the independently scrolling region. The phone Playwright checkpoint now proves initial drawer focus, backward boundary containment, Escape close, and opener restoration in Chromium. Standalone Affiliate support controls meet the product's 44-pixel target. These are meaningful regression controls, but the complete route/state, engine, zoom, device and assistive-technology matrix is still required; component DOM tests, a synthetic Chromium session, a desktop accessibility-tree inspection and HTTP inspection are not substitutes.

A separate connected-local Playwright gate shares the rebuilt local Caddy network namespace and exercises the exact Go-rendered identity boundary over `https://app.infiniteocean.localhost:8444`. Twelve applicable checks cover login, signup, forgot/reset password, registration verification and verified-contact confirmation in Chromium, Firefox and WebKit desktop plus Chromium phone. They require skip-link focus movement, responsive reflow, browser-computed axe including contrast, equal and reversible analytics rejection, Catalog-offer continuity, native required-field enforcement and safe incomplete-link errors. The gate creates no User, submits no password and uses no deployed environment. Its first run found that the consent card physically covered signup's primary action; desktop placement now stays over the story pane and single-column layouts put the card in normal flow. Successful email, password, passkey and owner-enrollment ceremonies remain separate connected-journey evidence.

### Legacy rollback gates

The legacy public website test suite builds the production worker and checks every published rollback route (`/`, `/about`, `/packages`, `/pricing`, `/privacy`, `/product`, `/security`, `/signup`, and `/terms`) as rendered HTML. Each route must have:

- a non-empty title and `lang="en"`;
- exactly one site header, main landmark, footer, and level-one heading;
- a first-focusable skip link to the programmatically focusable main landmark;
- landmarks outside one another in the expected page frame;
- unique element IDs; and
- no violations from the axe-core WCAG 2.0/2.1/2.2 A and AA rule set that can be evaluated without browser layout.

The jsdom gate explicitly excludes axe's `color-contrast` rule because jsdom has no layout or computed pixel-color model. Contrast is not waived: it is a required real-browser review below. ESLint's JSX accessibility rules remain a separate source-level gate. An automated pass is regression evidence, not a declaration of WCAG conformance.

Reusable page structure lives in the public site's `MarketingPage` frame. New marketing routes must use that frame or provide equivalent route-contract coverage. Illustrations must either expose one concise image alternative or remain absent from the accessibility tree; they must not contain inert controls that create false keyboard stops.

The legacy private server-rendered application has a separate Go DOM contract over 20 representative rollback states spanning identity, verified-contact confirmation, owner enrollment, security, the Account overview, Work, Agents, and lifecycle. It requires a first-body skip link, one focusable main landmark, one level-one heading, ordered headings, unique IDs, valid ARIA references, named navigation landmarks and controls, labeled form fields, exactly one current private navigation item, and no `autofocus`. Interaction contracts additionally preserve canceled Work-dialog focus, move successful commands to the updated detail, expose Work selection/loading state, announce command outcomes, replace prompt input with labeled reason dialogs, bind drafts to one Account and tab, and honor reduced-motion preferences in Agents. This gate remains useful rollback evidence, but it does not certify the replacement Vue routes.

## Required customer journeys

Certify both the successful path and its validation, denial, empty, loading, and recoverable-failure states:

| Surface | Required journey |
|---|---|
| Public acquisition | Home -> consent accept/reject/manage -> product/packages -> pricing -> optional Affiliate attribution -> free or selected paid offer -> private signup handoff |
| Identity | Registration, verification, login, logout, forgotten password, reset, passkey login, passkey enrollment/removal, and recovery-code replacement |
| Account entry | Owner security enrollment, Account create/join/select/switch, invitation acceptance, and no-Account state |
| Account administration | Member roster, role change, suspend/reactivate/remove, ownership transfer, session revocation, closure request/cancel, billing status, Checkout, Portal handoff, Affiliate enrollment/code and aggregate earning statements |
| Work | Package locked, read-only, list, detail, child navigation, create, transition, assignment, optimistic conflict, and capacity denial |
| Agents | Package locked, read-only, Boardroom/conversation selection, message history, Run submission, progress, failed Run, retry, and accept-partial-result |
| Lifecycle | Downgrade, failed payment/grace, export, closure, retention notice, and terminal access removal |

Packages or operations that are not production-complete stay unpublished and are excluded by removing the claim and entitlement—not by accepting an inaccessible path.

## Browser and assistive-technology matrix

Record exact OS, browser, assistive-technology, and device versions in the release evidence. At minimum, test:

| Platform | Browser | Assistive technology | Purpose |
|---|---|---|---|
| Windows | Current stable Chrome and Firefox | Current NVDA | Keyboard order, names/roles/states, announcements, live updates, forms, tables, and dialogs |
| macOS | Current stable Safari | VoiceOver | WebKit semantics, rotor landmarks/headings/links, focus restoration, and form recovery |
| iOS | Current stable Safari | VoiceOver | Touch exploration, dynamic viewport, orientation, zoom, and mobile navigation |
| Android | Current stable Chrome | TalkBack | Touch order, control activation, reflow, and mobile form behavior |

Also execute keyboard-only checks in each desktop engine without a screen reader. The release owner may add combinations based on supported-customer telemetry, but may not remove the baseline matrix without a dated, reviewed support-policy change.

## Review protocol

For every applicable journey:

1. Navigate from the address bar using only keyboard or assistive-technology commands. Confirm the skip link, landmark order, logical heading structure, visible focus, absence of focus traps, and meaningful focus restoration after navigation or dismissal.
2. Confirm every control exposes the expected name, role, value, state, instructions, grouping, and error association. Dynamic success, progress, denial, and failure messages must be announced without stealing focus unexpectedly.
3. Run axe in the real rendered browser with no unjustified rule exclusions. Manually check normal, hover, focus, selected, disabled, graphical-object, and adjacent-color contrast against WCAG 2.2 AA.
4. Test 200% text zoom and 400% browser zoom/reflow at a 1280 CSS-pixel viewport, plus a 320 CSS-pixel viewport. No required content or action may be clipped, overlap, or require two-dimensional scrolling except content whose meaning requires it.
5. Test reduced motion, increased contrast/forced colors where supported, dark/light system preference where supported by the surface, landscape/portrait orientation, and browser text-spacing overrides.
6. Exercise errors deliberately: missing and invalid fields, expired verification/recovery, stale version, insufficient role, read-only entitlement, capacity denial, provider outage, and disconnected/retry states. Instructions must identify the problem and a safe next action without relying on color, position, or icon alone.
7. Verify pointer targets meet WCAG 2.2 AA target-size requirements and that drag, hover, or multi-point gestures have a single-pointer alternative.

## Evidence record

Accessibility evidence is stored outside the repository with the release record and must include:

- full Git revision, immutable image digest, website/app origins, Catalog version, and test start/end time;
- matrix rows with exact platform/browser/assistive-technology versions and viewport/input settings;
- journey/state checklist results and the automated report artifact;
- issue identifier, WCAG success criterion, affected surface, severity, reproduction steps, and captured non-customer-content evidence for each failure;
- reviewer identity and independent release-owner disposition; and
- a cryptographic digest of each archived report.

Do not capture customer content, credentials, cookies, passkeys, recovery codes, payment details, email bodies, or provider payloads. Use dedicated synthetic Accounts and redact identifiers in screenshots and recordings.

Any code, image, stylesheet, content, browser-support, package publication, or target-environment change affecting a tested surface invalidates the corresponding evidence. A critical or serious defect in a required journey blocks release. A lower-severity exception requires a named owner, customer-safe workaround, deadline, and reviewed risk acceptance linked to the exact release.

## Remaining work

1. Expand the connected-local identity job beyond anonymous entry, native validation and incomplete-link recovery to successful registration, email verification, login/recovery and owner enrollment plus additional denial and transition states at every required width; cover target size, forward and backward focus order, every drawer/dialog boundary, zoom/reflow and forced colors.
2. Expand real-browser axe coverage to remaining private mutation-validation, conflict, capacity-denial and provider-failure states and mobile WebKit/device profiles. Retain the legacy structural gates as rollback evidence, not as evidence for Vue parity.
3. Perform the complete assistive-technology/device matrix against connected staging with real TLS email, passkeys, Stripe test mode, and provider-failure injection.
4. Archive the exact-artifact evidence, close defects, and rerun affected rows before canary promotion.
