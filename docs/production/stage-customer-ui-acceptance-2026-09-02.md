# Stage customer UI acceptance — 2026-09-02

- Environment: `https://app.stage.infiniteocean.net`
- Stage release: `b165661` (`0.3.0-rc.21`; application revision `ae3738b2bcbd2e38aedba6509da8fa84f2af8f14`)
- Client: Codex in-app Chromium browser, authenticated with the existing Google-linked owner identity
- Account: existing paid `Infinite Ocean` Stage Account
- Result: **failed — core customer workflows are not yet ready for acceptance**
- Infrastructure changes or deployment: none

No password, code, session token, cookie, provider credential, private key or payment detail was read or recorded during this review.

## Local remediation status — 2026-09-02

All findings below have now been corrected on `main` locally. Stage remains on the release identified above and has not been changed; the original observations remain the Stage acceptance result until a new candidate is deployed and retested.

- The application router now admits the exact Baseline-current route and every implemented Finance and Marketing customer route. Exhaustive route tests cover the allowed method and path combinations.
- Integration list responses are emitted as arrays by the cell API and normalized defensively by the TypeScript client. An authenticated Chromium regression proves that legacy `null` lists render a visible empty state instead of a blank application region.
- The previously corrected least-privilege MFA-posture grants remain included.
- Schedule creation now loads named Boardrooms and presents named Persona choices; customers no longer enter UUIDs.
- Sensitive-action guidance now describes identity confirmation and the supported passkey, text-message and email-code methods instead of requiring a passkey specifically.
- Paid package navigation waits for authoritative session loading before showing any plan-upgrade boundary.

Local evidence:

- `go test ./...`
- API and application TypeScript type checks
- changed-file ESLint
- focused Vue tests for navigation, Account guidance, Schedules and Integration response normalization
- Vite production build
- authenticated Chromium empty-account journey across Baseline, Finance, Marketing and Integrations
- disposable Docker `make verify-product-journey`, including all 94 MCP tools and the complete external-agent journey certificate

## Launch-blocking findings

### 1. Work creation and Agent execution fail because Stage admission cannot read MFA posture

Severity: critical.

Reproduction:

1. Open **Work** and choose **New work**.
2. Enter an ordinary owner task and select **Assign to me**.
3. Choose **Create work**.

Observed UI: `the Work command could not be completed`.

Stage logs identify the underlying error:

```text
Work capacity admission failed: ERROR: permission denied for table user_mfa_methods (SQLSTATE 42501)
```

The same defect prevents a successfully accepted Boardroom run from leaving `Planned`. Agent dispatch repeatedly reports `AI Token admission is unavailable`.

The narrow admission and MCP read grants were corrected and certified locally in commit `f1ff80b`, but that correction has not been deployed to Stage.

### 2. Baseline creation falls into an application-router 404

Severity: critical.

Reproduction:

1. Open **Baseline**.
2. Choose **Start my Baseline**.

Observed UI: `Baseline could not load` followed by `Request failed with status 404`.

Repository inspection shows that the application router accepts `POST baseline-assessments` and `GET baseline-assessments/{uuid}`, but rejects the UI's `GET baseline-assessments/current` route because `current` is not a UUID. The cell API already implements the current-assessment endpoint. The router allowlist needs the missing exact read route and regression coverage.

### 3. Finance and Marketing UI routes are absent from the application-router allowlist

Severity: critical.

Finance opens with `PACKAGE ENABLED the requested application route does not exist`. Attempting to establish an operating ledger returns the same error and preserves the form.

Marketing opens with `PACKAGE ENABLED Request failed with status 404`. Attempting to create a campaign also returns 404 and preserves the form.

The cell API and Vue/API clients implement these workflows, but the application router has no Finance or Marketing route requirements. These packages therefore work through the MCP certificate but are unreachable through the customer UI.

### 4. Integrations renders an empty main region

Severity: critical.

Both client-side navigation and a clean direct load leave only the application shell; the main region has no heading, status, error or controls. Browser diagnostics record:

```text
TypeError: Cannot read properties of null (reading 'length')
```

The Integrations view needs to normalize nullable list responses and retain a visible error boundary. Its live response contract should receive browser coverage, because the current mocked UI tests supply arrays and do not expose this failure.

## Important usability findings

### Schedule creation exposes internal identifiers

The new-schedule form asks a customer to type a **Boardroom ID** and comma-separated **Persona IDs**. These are UUID implementation details. A normal owner should choose a named Boardroom and named Personas from searchable/selectable controls.

### Strong-auth copy disagrees with the configured identity

The Google-linked identity has one email-code method and no passkey. Security correctly says:

> Use a passkey, text message, or email code to confirm sensitive changes.

Account invitation initially says `Confirm with a passkey before changing team access`. The Exports and Privacy introductions also say passkey confirmation is required, while the rejected command uses the more accurate `recent security confirmation is required` and links to Security. All strong-auth guidance should reflect the methods the identity can actually use and the backend policy being enforced.

### Paid navigation briefly appears locked while session data loads

A clean Security load briefly rendered `Explore plans — 6 more areas` before the paid package navigation appeared. This is transient, but it can make a paid customer think access was lost. Loading state should not be presented as a commercial lock.

## Working surfaces observed

- Google reauthentication returned to the intended application route.
- Your Turn loaded a clear empty state and filtering controls.
- Knowledge loaded its proposal and accepted-fact empty states.
- Boardroom creation, Persona publication and synthesis-manager assignment succeeded.
- A Boardroom run was accepted with frozen policy and Persona versions before the admission defect stopped dispatch.
- Billing showed the active monthly subscription, 10,000 included AI Tokens, optional top-up and optional commissioning package.
- Security correctly recognized that the Google identity has no Spyglass password and offered the configured email-code method.
- Account membership, Account lifecycle, closed Affiliate enrollment and Privacy/consent history loaded.
- Export confirmation correctly required a recent security confirmation before creating sensitive data.

## Stage data created during the review

- Boardroom: `Weekly Operations`
- Persona: `Shop Coordinator`, version 1, set as synthesis manager
- Conversation: `Tomorrow's field schedule`; accepted but stuck in `Planned`

No Work item, ledger, Marketing campaign, teammate invitation, export, closure or privacy-rights request was created. A Baseline start may have created an assessment before the subsequent `current` read failed.

## Required next acceptance slice

1. Add the exact Baseline-current, Finance and Marketing application-router routes with route-level tests.
2. Normalize Integrations list responses and add a live-response browser test that asserts a visible empty or error state.
3. Keep the locally corrected MFA-posture least-privilege grants in the next Stage candidate.
4. Replace Schedule UUID entry with named entity selection.
5. Make strong-auth guidance method-aware.
6. Run the local backend product certificate and a new authenticated browser journey before producing the next Stage release candidate.

Do not treat a passing MCP certificate as customer-UI acceptance: this pass proved that several implemented cell capabilities remain unreachable or unusable through the Vue application boundary.
