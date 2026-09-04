# Stage customer UI acceptance — 2026-09-03

- Environment: `https://app.stage.infiniteocean.net`
- Initial acceptance release: `0.3.0-rc.24`
- Latest corrected release: `0.3.0-rc.35`
- Latest application revision: `2af23ef1ed18dcf5fff8bd8be84ec2fcee89c89c`
- Latest release-manifest revision: `bc45b13693cf59f303e738154629f55427ce14b7`
- Latest Stage release directory: `/opt/spyglass-stage/releases/bc45b13693cf59f303e738154629f55427ce14b7`
- Client: Codex in-app Chromium browser, authenticated with the existing Google-linked owner identity
- Account: existing paid `Infinite Ocean` Stage Account
- Result: **passed for the tested customer application slice; complete adaptive-Baseline owner acceptance remains open**

No password, code, session token, cookie, provider credential, private key or payment detail was read or recorded during this review.

## Adaptive Operations Guide deployment — RC.35 (owner review pending)

RC.30 replaced the fixed Baseline questionnaire with an adaptive interview led by the Account's first `Operations Guide`; RC.31 repaired compatibility with historical nullable Agent-result collections, RC.32 corrected prompt placement, RC.33 added immediate replies plus compact approval events, and RC.34 repaired reply and approved-Work reconciliation. RC.35 makes Enter submit the reply while Shift+Enter inserts a new line. Submission is suppressed during an active Agent run and during IME composition.

Deployment evidence:

- Source revision: `2af23ef1ed18dcf5fff8bd8be84ec2fcee89c89c`.
- Release-manifest revision and clean VPS checkout: `bc45b13693cf59f303e738154629f55427ce14b7`.
- Release manifest: `deploy/releases/0.3.0-rc.35.env`.
- All three images carry provenance and SBOM attestations and passed HIGH/CRITICAL vulnerability plus secret scanning with zero findings.
- The protected Stage configuration verified before and after deployment.
- Existing PostgreSQL and object-store volumes were retained; migration ledgers remain 68/80/80.
- 49 long-running containers are present; all 48 healthchecked workloads are healthy and the internal edge is running.
- Public, login and MCP metadata endpoints return `200`; all three running image identities match the recorded digests.
- A clean authenticated load of the exact assessment rendered the RC.35 private bundle and its `Enter sends · Shift+Enter adds a new line` guidance with no browser-console errors; the owner's original review tab and in-progress draft were not changed.
- Twelve focused Baseline tests cover Enter submission, Shift+Enter multi-line retention, immediate optimistic replies, compact automation approval, completed-Work replay avoidance, creation-race recovery and reply cleanup when Work repair fails. Authenticated owner acceptance of the remaining adaptive questions and Your Turn continuation remains open.

| Artifact | Digest |
| --- | --- |
| Application | `sha256:a38ba77e9821a0b6756dd722ff3c3f8b1fb8f11baced106699e1ab0e79fd8fce` |
| Public UI | `sha256:9951905ce4efb811c50c8b6ba853bc70262f75390aab371e65e8ea0aa933c267` |
| Private UI | `sha256:96f4d457f6222702827c9e51039532519dcd9032bf5339ef0bb1ca8115c5a157` |

## Conversational Business Baseline deployment — RC.29

RC.29 replaces the internal-looking gap-review form with a customer conversation while retaining the same governed state machine. The live authenticated assessment now displays the named **Spyglass / Setup guide**, plain-language stage names and the question **Do you have one place to track customer problems, complaints, and feedback?** The owner chooses **Yes, we have a way**, **Not yet—add it to my plan**, or **This doesn’t apply to us**. Knowledge Evidence identifiers and disposition terminology are no longer customer inputs. A positive reply registers attributable owner-statement Evidence behind the interface and binds it to the explicit Baseline decision; retry reuses the first Evidence identity.

RC.29 deployment evidence:

- Source revision: `0867ebfcbc716294c045c53b1d2e95bc8ef2b6a2`.
- Release-manifest revision and clean VPS checkout: `b98f4a161ae88efe9d65f91c0ae65bdc26c8a5f9`.
- Release manifest: `deploy/releases/0.3.0-rc.29.env`.
- All three release images carry provenance and SBOM attestations and passed HIGH/CRITICAL vulnerability plus secret scanning with zero findings.
- The protected Stage configuration verified before mutation.
- The deployment retained the existing PostgreSQL and object-store volumes.
- 49 long-running containers are present; all 48 healthchecked workloads are healthy and the internal edge is running.
- Public, login and durable authenticated Baseline origins return `200`.
- A fresh authenticated browser reload visibly served the RC.29 conversational prompt and choices from the deployed private UI.

| Artifact | Digest |
| --- | --- |
| Application | `sha256:939730ca32451425e6d557320ba70404909b558a9ccb0be987da524a34af3b56` |
| Public UI | `sha256:9fe36cfd76a22531c8d80564e1a92d20c17b1c93aeadc2c2e13c84f2c1c41658` |
| Private UI | `sha256:c8d6fae98061a27517bafbe01ae0d9fbb76ac2182ad38f47536fa92bcb3867be` |

## Post-acceptance Baseline correction — RC.26

The RC.24 route sweep proved only that the Baseline page shell loaded. It did not prove the fresh-account or creation workflow. User review correctly found that Baseline was still broken, invalidating that part of the initial acceptance conclusion.

The follow-up browser journey found two related defects:

1. A fresh Account's expected 404 from `baseline-assessments/current` could be treated as a fatal error when an API problem crossed a JavaScript bundle boundary and failed an `instanceof` check. The API package now exposes a structural, cross-bundle-safe problem guard and the Baseline view uses it for the no-current-assessment state.
2. The Local and Stage Caddy Account API matcher did not include `baseline-assessments`, so both current-assessment reads and creation commands fell through to the Account API. Both Caddy configurations now route the complete Baseline API family to the application router. The Stage contract explicitly asserts that this route remains present.

RC.26 evidence:

- API client regression: 7 tests passed.
- Baseline view regression: 3 tests passed, including a foreign-bundle-shaped 404.
- API and application type checks passed.
- Application production build passed.
- Stage configuration and Caddy contract verification passed.
- All RC.26 images passed the HIGH/CRITICAL Trivy gate.
- An authenticated fresh-account load displayed `Start my Baseline` rather than a fatal 404.
- `Start my Baseline` created assessment `0fbd0cbc-18c8-4556-99cd-b3a529876d87` in `interview` state at version 1.
- The browser redirected to the durable assessment URL and displayed question 1 of 7.
- A separate clean load of the root Baseline route resolved the current assessment and resumed the same durable URL.
- Both verification tabs had zero browser errors, and the post-deploy service-log scan contained no panic, fatal, permission or error-level entry.

RC.26 artifacts:

| Artifact | Digest |
| --- | --- |
| Application | `sha256:cab508d73c68f57d2476b36478ab7d768aa8767cd2da679e0f6bb80b6b62fe26` |
| Public UI | `sha256:e11460c5d73ca49951c389cb670ad4e9b1be2ef145c708f3591dc2bc0bf3b2ad` |
| Private UI | `sha256:2eb0457a62edde4b01ac62434e29b14e29f4f7b3c020694c97174ebf525ff37c` |

## Initial RC.24 release artifacts

The candidate was built and published to GHCR, scanned before deployment and pinned in the Stage manifest by digest.

| Artifact | Digest |
| --- | --- |
| Application | `sha256:487b2d2733061ad3a453c1f2df3853c3fa8f460f6fd334949c255357b9c6c3a2` |
| Public UI | `sha256:6982f839b7a8ce720ff8a58967eb33d6dee502a61ef0ab5563f8ea289b1cbeba` |
| Private UI | `sha256:436fcec81b766336142e72c95d0158af443c4051d4d9e5d614481e3536cd5a53` |

Trivy reported no HIGH or CRITICAL findings in any of the three images.

## Pre-deployment evidence

- `go test ./internal/transport/cellapi`
- `go test ./internal/adapters/dockerengine`
- `go test ./cmd/spyglass ./internal/bootstrap/runnercontroller ./internal/bootstrap/runnerbrokerapi`
- Marketing API client tests: 5 passed
- Marketing view tests: 4 passed
- API and application TypeScript type checks
- Application production build
- `make -C deploy/docker/spyglass verify-docker-runner verify-stage-contract`
- Stage-only Docker runner verification gate

## Authenticated browser acceptance

The following customer routes loaded their expected headings, content or explicit empty states without a browser alert, uncaught error or blank application region:

1. Your Turn
2. Work
3. Knowledge
4. Baseline
5. Agents
6. Schedules
7. Finance
8. Integrations
9. Marketing
10. Account
11. Billing
12. Security
13. Exports
14. Lifecycle
15. Affiliate
16. Privacy

The browser console was checked again after the final Agent execution. It contained no new errors or warnings.

## Customer workflow acceptance

### Work

Created `Stage RC.23 acceptance check`, moved it from Open to In Progress and then to Done. The item remains as completed Stage acceptance evidence.

- Work item: `a7e21028-d0df-4d72-899f-14d173673835`
- Display number: `0001`

### Marketing

Created `Stage RC.23 acceptance` and archived it successfully.

- Campaign: `a1b05787-d8b4-4ae1-adef-47ae9d74c589`

### Finance

Created the `Stage acceptance ledger` with code `STG23` and archived it successfully.

- Ledger: `6012fb38-a1b0-4e02-bc14-30ef922c2e40`

### Schedules

Opened schedule creation, selected the named `Weekly Operations` Boardroom and confirmed that the human-readable `Shop Coordinator` Persona was offered. No raw UUID entry was required. The unsaved draft was closed without mutation.

### Integrations

Confirmed that the empty connection list renders the explicit `No connections match this view.` state instead of a blank region.

### Agent execution

Ran the `Shop Coordinator` Persona in the `Weekly Operations` Boardroom with a bounded, no-external-action acceptance prompt.

- Conversation: `Stage RC.24 agent acceptance`
- Conversation ID: `ebd8f8d0-0562-8f9f-bd2f-cd4eaf38048c`
- Run ID: `623c38f8-cb62-4de4-bff3-c65c01698d62`
- Invocation ID: `565af77c-f4a2-89ce-a083-b0adad47e49b`
- Result: `Confirmed operational status.`
- Finding: `The Stage agent execution path is operational.`
- Application run state: `succeeded`
- Invocation state: `succeeded`
- Dispatch state: `provisioned`, one attempt
- Runner queue state: `completed`, one attempt
- Ephemeral runner container exit: `0`

## Defects found and corrected during acceptance

Three defects were found by the live journey, corrected on `main`, covered by regression tests and deployed in the final candidate:

1. The Stage edge proxy did not route Marketing application requests to the application router.
2. Empty Marketing collections could be encoded as `null`, causing Vue views to read `.length` from `null`. Cell responses now use JSON arrays and the API client also normalizes legacy nullable responses.
3. Docker runner identity and broker credential directories were mode `0700` and owned by root, so the non-root ephemeral runner could not read the mounted broker CA. The bind roots are now sealed read-only and traversable (`0555`) after their files are written; cleanup restores owner access before removal.

The failed pre-fix runner remains in Docker history as audit evidence. The final RC.24 runner succeeded with the corrected permission contract.

## Final infrastructure verification

- The Stage `current` symlink resolves to the RC.24 release directory recorded above.
- All Spyglass Stage services were running; every service with a declared health check reported healthy.
- Unhealthy container count: `0`.
- The landing page, Features, Pricing, Privacy, Terms, application login and application readiness endpoints returned HTTP 200.
- A post-deploy service-log scan found no panic, fatal error, permission denial, invalid CA, execution failure or error-level entry.

## Acceptance boundary

This pass establishes that the deployed customer shell, principal application packages, representative mutations and complete browser-to-runner Agent path work together on Stage. It is not production certification.

The following were intentionally not exercised in this pass:

- destructive account closure or personal-data deletion;
- generation and download of a full account export;
- delivery through a configured external Integration;
- a real scheduled occurrence reaching its future execution time;
- email, SMS, Google sign-up and Stripe payment journeys, which were exercised in the preceding Stage review but were not repeated here;
- production/LKE deployment, scaling, restore or disaster-recovery procedures.

Those boundaries should remain explicit in launch readiness and should be tested when their corresponding environment, provider or destructive-test window is available.
