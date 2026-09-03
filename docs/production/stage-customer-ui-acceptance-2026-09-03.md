# Stage customer UI acceptance — 2026-09-03

- Environment: `https://app.stage.infiniteocean.net`
- Stage release: `0.3.0-rc.24`
- Application revision: `da71be21816ff117a75db1c6db981cb40a82e51f`
- Release-manifest revision: `1b8602205ad907ed078df76ceb1babe8f34c151d`
- Stage release directory: `/opt/spyglass-stage/releases/1b8602205ad907ed078df76ceb1babe8f34c151d`
- Client: Codex in-app Chromium browser, authenticated with the existing Google-linked owner identity
- Account: existing paid `Infinite Ocean` Stage Account
- Result: **passed for the tested customer application slice**

No password, code, session token, cookie, provider credential, private key or payment detail was read or recorded during this review.

## Release artifacts

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
