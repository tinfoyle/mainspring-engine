# Runner Broker Identity and Exchange Boundary

- Status: Pod-bound encrypted exchange plus generic execution/capability boundaries executable; concrete executors, action ledger, deployment wiring, and applied evidence pending
- Product: Infinite Ocean: Spyglass
- Parent: [Fair Runner Control Plane](runner-control.md)

## Decision

Ephemeral runners authenticate to one cell-local broker with a Kubernetes projected ServiceAccount token. Spyglass does not mint a parallel bearer-token system and does not mount the default Kubernetes API credential into runner Pods.

The Job sets `automountServiceAccountToken: false` and explicitly projects one token with:

- audience exactly equal to the configured HTTPS broker URL;
- a 600-second requested lifetime, continuously rotated by the kubelet;
- Pod lifetime binding;
- mode `0400` in a read-only volume owned for the fixed runner UID;
- a ServiceAccount with no Kubernetes RBAC.

Its intended audience is the broker rather than the Kubernetes API server. Deployment must prove the broker URL is not configured as an API-server audience; the runner's empty RBAC remains an independent defense. The runner must reread the projected file for each broker operation so kubelet rotation is effective.

## Online verification chain

For every payload or result operation, the broker verifier:

1. Accepts one bounded bearer token and a canonical invocation UUID.
2. Submits the opaque token to Kubernetes `TokenReview` with the one exact broker audience.
3. Requires `authenticated=true`, an audience intersection containing that exact value, and username `system:serviceaccount:<cell namespace>:<runner ServiceAccount>`.
4. Extracts exactly one Pod name and Pod UID from the reviewed private claims.
5. Gets that exact Pod—never lists Pods—and rejects a missing, terminating, UID-mismatched, differently labeled, or differently serviced Pod.
6. Requires one controller owner reference to the deterministic Job for the requested invocation.
7. Gets that exact Job—never lists Jobs—and verifies its UID, non-terminating state, invocation/profile labels, and bounded launch-contract SHA-256 annotation.
8. Returns only the verified invocation, profile, Job, and Pod identity to the application broker.

Kubernetes recommends TokenReview when current validity of an object-bound token matters; unlike offline JWT validation, it checks that the bound Pod and ServiceAccount still exist. The verifier also rejects `deletionTimestamp` immediately rather than relying on Kubernetes' deletion grace. See [Managing Service Accounts](https://kubernetes.io/docs/reference/access-authn-authz/service-accounts-admin/) and the [TokenReview API](https://kubernetes.io/docs/reference/kubernetes-api/definitions/token-review-v1-authentication/).

TokenReview results are not cached. A runner performs only a small bounded number of exchange operations, and online review preserves prompt revocation after Pod deletion. Raw runner or reviewer tokens never enter URLs, errors, logs, metrics, traces, database rows, or response bodies.

## Broker Kubernetes authority

The eventual broker ServiceAccount needs only:

```text
create authentication.k8s.io/tokenreviews       (cluster-scoped)
get    core/pods                                (one cell namespace)
get    batch/jobs                               (one cell namespace)
```

It receives no list/watch, Pod mutation, Job mutation, Secret access, exec/attach/port-forward, TokenRequest creation, node access, or customer database authority. The runner ServiceAccount receives no RBAC at all. NetworkPolicy permits runner-to-broker HTTPS and broker-to-Kubernetes API HTTPS; it does not make either identity authoritative by itself.

## Encrypted exchange contract

The identity verifier deliberately returns no payload. The application broker and cell persistence adapter now enforce a separate exchange boundary:

- provision queue identity and an encrypted request envelope atomically through one security-definer database function;
- keep prompts, tool inputs, provider credentials, customer files, and model output out of `runner_invocation_queue`;
- store envelopes in the forced-RLS, Account-owned `spyglass.runner_invocation_exchanges` table;
- bind first request retrieval to the verified Pod UID and permit only identical retries by that Pod;
- allow retrieval/result only while durable state is `launch_uncertain` or `launched`;
- reject `canceling`, `canceled`, expired, mismatched-profile, mismatched-Job, and mismatched-Pod operations before decryption;
- expose only a versioned schema with 768 KiB input/output, 1 MiB envelope, 24-hour lifetime, and 32 canonical capability-code bounds; the provisioning caller remains responsible for passing only policy-authorized capabilities;
- accept one encrypted, digest-bound result, with identical idempotent retry and conflicting-result rejection;
- include every Account-owned exchange row in erasure and restore replay.

Requests and results use AES-256-GCM with an integer key version. Associated data binds a request to invocation, Account, and profile, and binds a result to invocation and Pod UID. Idempotency compares the SHA-256 digest of canonical plaintext plus immutable expiry or outcome, not randomized ciphertext. A keyring reads retained old versions while new writes use one active version; key material remains outside PostgreSQL.

Producer and broker roles receive execute-only functions and no direct queue or exchange-table access. `spyglass runner-broker` exposes only `POST /internal/v1/runner/invocations/{id}/request` and idempotent `PUT /internal/v1/runner/invocations/{id}/result` over TLS 1.3. It rejects redirects, queries, unbounded or noncanonical JSON, malformed/duplicate bearer credentials, and backend-detail disclosure. Responses are non-cacheable. The runner-side client rereads the projected token file for every operation, refuses redirects, disables ambient proxies by default, and maps only content-free problem codes.

## Execution and capability boundary

The generic runner harness fetches one admitted request, selects only a compiled kind-specific executor, supplies that executor a broker-backed capability client, canonicalizes one bounded object result, and submits exactly one terminal envelope. Unsupported kinds, executor machine failures, and invalid output submit `execution_failed`; private executor errors never cross the boundary.

Every capability call carries a UUID operation ID, a canonical object input capped at 256 KiB, and one capability code from the admitted request. The gateway repeats online TokenReview, exact Pod/Job identity verification, durable launch/cancellation state checking, envelope decryption, and capability membership checking for every call. It then applies a fixed registered handler timeout and caps canonical object output at 256 KiB. Provider credentials remain in the handler service and never reach the runner.

Read-only and consequential definitions are distinct. A consequential definition cannot be constructed without an action authorizer responsible for approval state, durable action-ledger identity, payload-digest binding, provider idempotency, and unknown-outcome recovery. Mandatory pre/post audit writes are content-free and accepted only when the exchange is already bound to the same Pod UID. The pre-execution audit is also the final durable state gate: it takes a share lock on the exact queue row and accepts only `launch_uncertain` or `launched`, serializing admission with cancellation. Audit failure before execution prevents the handler from running. Post-execution facts remain writable after cancellation so an already-admitted outcome is not lost; post-audit failure returns unavailable, and retry safety depends on the same durable operation ID/provider contract.

The capability HTTP transport and rotating-token client method are executable but intentionally not mounted by `runner-broker` yet: no concrete production handler or consequential action authorizer exists. The next slice must implement those dependencies, mount the route, add the `runner-invocation` process with real executors, define terminal exchange/audit retention, and enforce current entitlement/capability policy at provisioning.

Cancellation revocation must also be enforced by every provider/tool gateway. Pod deletion or broker denial cannot erase plaintext already in runner memory, so a canceled or partitioned runner must have no direct provider, connector, customer-service, or unrestricted internet path on which it can continue side effects.

## Acceptance gates

- Wrong audience, ServiceAccount, namespace, Pod UID, Pod label, Job owner, Job UID, profile, invocation, contract digest, and terminating object are denied.
- Malformed and oversized tokens are denied before a Kubernetes request.
- Kubernetes errors disclose neither presented token nor customer identifiers.
- Runner Job render contains no default token and exactly one broker-audience projection.
- The projected broker token is rejected when presented directly to the Kubernetes API, and the runner ServiceAccount has no authorized API action even under audience misconfiguration.
- Applied RBAC proves the broker can review tokens and get exact Pod/Job objects but cannot list or mutate workloads.
- Pod deletion causes the next TokenReview/broker operation to fail.
- A token copied from runner A cannot fetch or submit invocation B.
- Ciphertext or associated-data tampering fails closed, and old key versions remain readable during rotation.
- Every capability retry repeats Pod identity, durable state, expiry, and grant checks; cancellation prevents handler entry even if the Pod remains alive.
- Consequential handlers cannot run without digest-bound action authorization and mandatory content-free audit.
- Broker cancellation and node-partition tests prove all external capabilities are revoked independently of process termination.
