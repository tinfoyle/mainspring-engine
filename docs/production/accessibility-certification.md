# Accessibility target and release certification

Status: repository automation is executable; browser, assistive-technology, and device evidence is still required

Spyglass targets **WCAG 2.2 Level AA** for the public Infinite Ocean website and every customer journey required to acquire, enter, operate, administer, and leave the product. Accessibility is a release property of an exact deployed artifact, not a one-time design review.

## Repository gate

The public website test suite builds the production worker and checks every published route (`/`, `/about`, `/packages`, `/pricing`, `/privacy`, `/product`, `/security`, `/signup`, and `/terms`) as rendered HTML. Each route must have:

- a non-empty title and `lang="en"`;
- exactly one site header, main landmark, footer, and level-one heading;
- a first-focusable skip link to the programmatically focusable main landmark;
- landmarks outside one another in the expected page frame;
- unique element IDs; and
- no violations from the axe-core WCAG 2.0/2.1/2.2 A and AA rule set that can be evaluated without browser layout.

The jsdom gate explicitly excludes axe's `color-contrast` rule because jsdom has no layout or computed pixel-color model. Contrast is not waived: it is a required real-browser review below. ESLint's JSX accessibility rules remain a separate source-level gate. An automated pass is regression evidence, not a declaration of WCAG conformance.

Reusable page structure lives in the public site's `MarketingPage` frame. New marketing routes must use that frame or provide equivalent route-contract coverage. Illustrations must either expose one concise image alternative or remain absent from the accessibility tree; they must not contain inert controls that create false keyboard stops.

The private server-rendered application has a separate Go DOM contract over 20 representative states spanning identity, verified-contact confirmation, owner enrollment, security, the Account overview, Work, Agents, and lifecycle. It requires a first-body skip link, one focusable main landmark, one level-one heading, ordered headings, unique IDs, valid ARIA references, named navigation landmarks and controls, labeled form fields, exactly one current private navigation item, and no `autofocus`. Interaction contracts additionally preserve canceled Work-dialog focus, move successful commands to the updated detail, expose Work selection/loading state, announce command outcomes, replace prompt input with labeled reason dialogs, bind drafts to one Account and tab, and honor reduced-motion preferences in Agents. This gate exercises locked, read-only, empty, and writable package modes, but it is not a substitute for private-route axe or assistive-technology testing.

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

1. Turn the controlled local-browser checks for signup, verification, login, owner enrollment, locked Work, responsive overflow, and target size into a repeatable exact-artifact release job; extend it through focus order, skip-link movement, zoom/reflow, and browser-computed contrast.
2. Add real-browser axe coverage to the private application and expand its rendered-state fixtures through validation, denial, conflict, capacity, loading, and recoverable-failure states. The 20-state structural gate already covers the principal identity, verified-contact, Account, Work, Agents, security, and lifecycle layouts.
3. Perform the complete assistive-technology/device matrix against connected staging with real TLS email, passkeys, Stripe test mode, and provider-failure injection.
4. Archive the exact-artifact evidence, close defects, and rerun affected rows before canary promotion.
