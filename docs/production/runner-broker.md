# Runner Broker Identity and Exchange Boundary

- Status: Pod-bound Kubernetes identity projection and verifier executable; encrypted invocation exchange and HTTP broker pending
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

## Encrypted exchange contract (next implementation slice)

The identity verifier deliberately returns no payload. The cell broker must add a separate application and persistence boundary with these invariants:

- provision queue identity and an encrypted request envelope atomically;
- keep prompts, tool inputs, provider credentials, customer files, and model output out of `runner_invocation_queue`;
- bind first request retrieval to the verified Pod UID and permit only identical retries by that Pod;
- allow retrieval/result only while durable state is `launch_uncertain` or `launched`;
- reject `canceling`, `canceled`, expired, mismatched-profile, mismatched-Job, and mismatched-Pod operations before decryption;
- expose only a versioned, size-bounded schema and a server-authorized capability allowlist;
- accept one encrypted, digest-bound result, with identical idempotent retry and conflicting-result rejection;
- append content-free lifecycle audit facts while keeping plaintext out of logs and technical control records;
- include every Account-owned exchange row in erasure, restore replay, retention, and key-rotation policy.

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
- Broker cancellation and node-partition tests prove all external capabilities are revoked independently of process termination.
