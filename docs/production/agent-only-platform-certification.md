# Agent-only local platform certification

- Certificate date: 2026-09-02
- Environment: UbuntuRojo WSL 2 and the isolated `spyglass-agent-journey` Docker Compose project
- Client posture: authorized external client using documented HTTPS and MCP interfaces only
- Customer browser UI: not used
- Remote systems: not contacted
- Result: **passed — 32/32 journey checks and 94/94 published MCP tools**
- Evidence: generated on demand with `make verify-product-journey`; the earlier 2026-08-28 report remains at [agent-only-platform-certificate.json](evidence/agent-only-platform-certificate.json) as historical evidence.

## What this certifies

A fresh synthetic owner registered through the documented HTTP boundary, verified the local notification, logged in, enrolled a real software-backed ES256 user-verifying WebAuthn credential and recovery codes, selected the newly provisioned Account, completed a locally hosted Stripe Checkout simulation through the real billing and signed-webhook boundaries, received the purchased package and included AI Tokens, and then operated the product without the customer browser interface. The journey uses the same Account routing, authentication, authorization, optimistic-version, billing projection, entitlement, token-admission, runner and persistence boundaries available to an external client. It no longer inserts commercial access directly into the database.

The journey completed:

- $50 monthly Checkout creation, hosted-checkout contract inspection, signed out-of-order Stripe deliveries, duplicate replay, billing projection, purchased-package access and included AI-Token grant;
- Work creation and lifecycle, Your Turn summary, stale-version rejection and authoritative recovery;
- Boardroom and Persona publication, manager configuration, Agent planning, deterministic provider failure, human-authorized retry, metered completion and insufficient-AI-Token rejection before provider use;
- Schedule creation, pause, stale retry, resume and manual trigger;
- OAuth authorization with exact client/resource/scope consent and S256 PKCE;
- Baseline assessment and bounded source-grant inspection;
- Knowledge evidence, claims, facts, citations and retrieval;
- Finance ledger, chart, balanced entry, posting and reversal;
- Marketing campaign draft, revision and archive without external publication;
- Integration connection lifecycle without disclosing or contacting a provider;
- consequential-action proposal creation, independent-review self-approval denial and cancellation without execution;
- strongly authorized Account export request, inspection, listing and cancellation;
- missing-Bearer denial and cross-Account isolation denial; and
- every MCP tool published by authenticated `tools/list` reaching its Account-scoped handler.

The local deterministic provider deliberately returned one availability failure and then one successful metered response. Real provider credentials were neither needed nor loaded. Recovery codes, passwords, OAuth tokens, session material, passkey private material and provider secrets are absent from the evidence file.

## Tool-by-tool coverage

The generated JSON report names every tool and records its outcome. `executed` means the synthetic journey supplied a complete valid object graph and received structured success. `safe_rejection` means the authenticated call reached the correct Account-scoped handler and rejected incomplete or inapplicable synthetic input without crossing an authority or provider boundary. A safe rejection is handler/authorization coverage, not a claim that every possible business-state permutation completed successfully.

| MCP family | Published | Executed | Safe rejection |
|---|---:|---:|---:|
| Account exports | 5 | 4 | 1 |
| Attention | 19 | 10 | 9 |
| Baseline | 5 | 3 | 2 |
| Finance | 21 | 14 | 7 |
| Integrations | 20 | 6 | 14 |
| Knowledge | 8 | 7 | 1 |
| Marketing | 16 | 5 | 11 |
| **Total** | **94** | **49** | **45** |

Work, Your Turn, Agent/Boardroom and Scheduling are also exercised through their documented HTTPS application boundaries. They are not omitted merely because they are not separate tools in the composed package MCP registry.

## Defects found and corrected

The mission found six locally reproducible platform defects. Each was fixed at the shared boundary and received regression coverage:

1. Docker runner verification supplied the engine's 64-character container ID where the database contract required a UUID. The adapter now derives a stable namespace-separated UUID from the full Docker ID while retaining the raw ID only as the job identifier.
2. Agent retry digests used nanosecond timestamps while PostgreSQL persisted microseconds, making an otherwise valid retry appear corrupt. Run resolution now normalizes timestamps to PostgreSQL precision before hashing.
3. A dispatch that exhausted its retry budget after an AI-Token admission denial became dead-lettered while its Agent Run remained planned. The cell migration now terminalizes the invocation, successor cohort, Run and queue projection consistently; the processor also preserves the specific `ai_tokens_insufficient` failure class.
4. The secure local topology generated no client identity for the MCP gateway and did not authorize that workload at the application APIs. The local certificate graph, gateway TLS configuration and exact API allowlists now include the dedicated gateway identity. Stage configuration already had that identity and was not changed.
5. The admission service could not evaluate the expanded MFA security posture because its least-privilege role lacked read access to `user_mfa_methods`. The exact read-only grant and a privilege regression assertion now cover it.
6. The MCP gateway had the same migration-era security-posture grant drift. Its role now has the same narrow read-only access, and the existing MCP privilege assertion checks both the required read and forbidden writes.

Fresh Account cell provisioning was also made durable and retryable rather than depending on a pre-created test Account. The certificate observed one fail-closed `account_unavailable` response, retried through the public contract and succeeded after provisioning completed.

## Boundary of the result

The external-agent interaction surface is fully implemented and locally certified for discovery, strong authentication, OAuth, Account selection/routing, package operations, Agent execution and the documented safety boundaries. No missing agent-only transport or core product capability was identified.

This certificate does not replace the remaining Phase 3 human and provider acceptance:

1. Owner product/visual review of the authenticated customer UI, especially Your Turn, mobile navigation, team/Account administration and failure recovery.
2. Owner review of the Operations Console, including analytics, User/Account/billing inspection, privacy and Affiliate operations, and the visibly marked read-only support-inspection boundary.
3. Real Stripe test-mode Checkout, portal, signed webhook, renewal, payment-attention, refund, credit, token-bundle and commissioning journeys against approved Price mappings.
4. Real notification deliverability and at least one real model-provider acceptance journey, plus the provider integrations intended for launch.
5. Physical passkey, supported phone/browser, keyboard, screen-reader and applied accessibility acceptance.
6. Privacy-controller facts, vendor/DPA/transfer review, lawful-basis/DPIA decisions, Affiliate legal/tax/Support procedures and explicit owner approval. Affiliate enrollment and attribution remain closed until those gates pass.
7. Retained prototype/internal-Account migration, environment backup/restore, rollback, resilience, soak and game-day evidence where applicable.

Stage, GHCR, Hostinger, LKE and production release work remain a separate final track and were deliberately untouched by this mission.

## Reproduce locally

From UbuntuRojo at the repository root:

```bash
make -C deploy/docker/spyglass verify-product-journey

# Write the content-free machine-readable report when evidence is needed:
deploy/docker/spyglass/verify-agent-journey.sh --out /tmp/spyglass-product-journey.json
```

The script uses an isolated `spyglass-agent-journey` Compose project, preserves its named test volumes, recreates its containers and networks, resets the deterministic provider fixture, starts the certificate exactly once and writes an optional report with mode `0600`. Each run creates new synthetic User and Account identities, so its timestamp, identifiers and file digest will differ while the schema, 32 passed checks, 94 covered tools and 49/45 outcome split remain the acceptance contract.
