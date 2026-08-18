# Route and Workload Identity Rotation Operations

Status: executable one-shot route-key and workload-certificate canary implemented.

## Purpose and invariants

`spyglass route-canary` verifies candidate route-signing and workload TLS material before a production cutover. It uses the existing protected Account context path for a dedicated internal canary Account, so success proves all of the following together:

- The candidate signing key ID is present in the destination verifier keyring.
- The proof has the expected issuer, cell audience, request binding, placement generation, and entitlement version.
- The candidate client certificate chains to the server's current trust bundle and contains an allowed workload SPIFFE identity.
- The server certificate chains to the candidate client's trust bundle and matches the exact destination DNS name.
- The cell can read the canary Account namespace through forced RLS and durably consume the replay receipt.

The `admission` target verifies the same candidate key against admission-api's per-cell keyring without reserving, releasing, or reading usage. Its private endpoint still requires the configured workload certificate identity.

The canary receives no database credential, browser session, Stripe secret, SMTP secret, customer record, or Kubernetes mutation authority. It reports only target kind, cell ID, non-secret key ID, and placement generation. It never logs the Account ID, token, signing key, certificate, or response body.

## Canary Account

Each cell has one dedicated internal platform Account used only for operational probes. It must:

- Exist in the global control plane and the target cell namespace with matching cell, placement generation, and entitlement version.
- Remain `active` during a planned rotation and contain no customer or business data.
- Be excluded from commercial reporting, ordinary invitations, and customer-visible Account selection.
- Use the normal Account placement workflow when its generation changes; operators must update the canary inputs rather than guessing values.
- Retain its replay receipts through the normal bounded route-receipt worker.

Do not use a customer Account as a canary and do not create an orphan cell namespace by direct production SQL.

## Invocation

Run the binary as a short-lived, reviewed Job. Environment configuration supplies candidate secrets from the managed secret system:

```text
SPYGLASS_ROUTE_CANARY_TARGET=cell
SPYGLASS_ROUTE_CANARY_ORIGIN=https://app-api-cell.example.internal
SPYGLASS_CELL_ID=cell-us-east-01
SPYGLASS_ROUTE_CANARY_ACCOUNT_ID=<internal-canary-account-uuid>
SPYGLASS_ROUTE_CANARY_PLACEMENT_GENERATION=<current-positive-version>
SPYGLASS_ROUTE_CANARY_ENTITLEMENT_VERSION=<current-positive-version>
SPYGLASS_ROUTE_ISSUER=spyglass-app-router
SPYGLASS_ROUTE_SIGNING_KEY_ID=<candidate-key-id>
SPYGLASS_ROUTE_SIGNING_KEY=<candidate-standard-base64-32-byte-key>
SPYGLASS_WORKLOAD_CERT_FILE=/var/run/secrets/spyglass/workload/tls.crt
SPYGLASS_WORKLOAD_KEY_FILE=/var/run/secrets/spyglass/workload/tls.key
SPYGLASS_WORKLOAD_CA_FILE=/var/run/secrets/spyglass/workload/ca.crt
SPYGLASS_ROUTE_CANARY_TIMEOUT=10s
spyglass route-canary
```

Use `SPYGLASS_ROUTE_CANARY_TARGET=admission` and the private admission-api HTTPS origin to check that service's keyring. The certificate must carry an app-api workload identity allowed by admission-api. The `cell` target uses an app-router workload identity allowed by app-api.

The command exits zero only after a cell response matches the expected Account, cell, placement generation, entitlement version, and actor kind, or an admission response confirms the expected cell and candidate key ID. Redirects, plain HTTP, invalid DNS/CA identity, TLS below 1.3, unknown keys, stale placement, unavailable Account state, replay-store failure, oversized responses, mismatched response authority, and timeouts fail nonzero.

## Route-signing key rotation

1. Generate the candidate 32-byte key in the managed secret system and assign a new non-secret key ID. Never copy either value into source control, manifests, tickets, or shell history.
2. Add the candidate to `SPYGLASS_ROUTE_VERIFY_KEYS` for every app-api and admission-api while retaining the previous key. Complete the rollout and confirm every expected replica is ready on the intended revision.
3. Run the `cell` canary against every cell and the `admission` canary for every configured cell audience. For a load-balanced Service, combine successful probes with rollout/pod-revision evidence; one Service response alone does not prove every replica updated.
4. Switch app-router replicas to the candidate `SPYGLASS_ROUTE_SIGNING_KEY_ID` and key. Verify ordinary routed reads and one controlled idempotent Work command through admission.
5. Wait longer than the maximum 30-second proof lifetime plus the two-second skew allowance. Confirm no router replica still uses the previous revision.
6. Remove the previous key from app-api and admission-api keyrings, roll the workloads, and rerun the candidate canaries.
7. As a negative check, a canary signed with the retired key must fail with an unknown-key rejection. Do not print or preserve the retired key merely to automate this check; use the managed system's controlled retirement workflow.

If a candidate canary fails before signer cutover, leave the previous signer active and repair the verifier rollout. If failures begin after cutover, restore the previous router signer while both keys remain accepted. Never remove the previous verifier until the proof-expiry window and rollback decision have completed.

## Workload CA and leaf rotation

1. Add the candidate CA certificate to every client and server `SPYGLASS_WORKLOAD_CA_FILE`, retaining the previous CA. Confirm projected files contain both public certificates; private CA keys never enter workloads.
2. Issue candidate server leaves with the same exact Service DNS SANs and candidate client leaves with the same exact allowed SPIFFE identities and extended-key usages.
3. Before replacing active leaves, run `cell` canaries with a candidate app-router client leaf and `admission` canaries with candidate app-api client leaves. Their trust bundles must contain both CAs.
4. Replace server and client certificate/key projections. New handshakes reload files automatically. Drain idle connections or roll workloads to accelerate evidence; urgent revocation always requires terminating existing connections or pods.
5. Rerun both canary targets through the candidate leaves and confirm TLS 1.3, exact DNS identity, exact SPIFFE identity, and the candidate route key.
6. After every workload uses candidate leaves and old connections have drained, remove the previous CA from trust bundles.
7. Rerun candidate canaries. A client presenting a previous-CA leaf must then fail the TLS handshake.

Automated tests exercise the overlap sequence: previous material succeeds, candidate client material succeeds while both roots are trusted, candidate server material succeeds, candidate-only trust succeeds, and a retired client certificate fails after old-root removal.

## Operational evidence

Retain the reviewed change, secret-version identifiers, workload revisions, per-cell/admission success timestamps, negative retirement check, proof-expiry wait, and rollback decision in the deployment record. Do not retain raw canary tokens or secret material. A failed canary blocks the rotation cohort; it is not bypassed with plaintext, disabled identity checks, a customer Account, or a direct database edit.
