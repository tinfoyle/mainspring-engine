# Runner Broker Identity and Exchange Boundary

- Status: Pod-bound encrypted exchange, live-policy read tool, and approval-bound action ledger executable; kind executors, consequential adapters, deployment wiring, and applied evidence pending
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

The Job also mounts one read-only, namespace-local immutable ConfigMap key `ca.crt` at a fixed broker-CA path. The ConfigMap name is part of the launch-contract digest, and `runner-invocation` refuses to start without the explicit CA file. This trust bundle authenticates the private broker endpoint; it grants no workload identity and contains no client key. Rotation uses a new overlapping immutable ConfigMap name and therefore a new Job contract.

## Online verification chain

For every payload or result operation, the broker verifier:

1. Accepts one bounded bearer token and a canonical invocation UUID.
2. Submits the opaque token to Kubernetes `TokenReview` with the one exact broker audience.
3. Requires `authenticated=true`, an audience intersection containing that exact value, and username `system:serviceaccount:<dedicated runner namespace>:<runner ServiceAccount>`.
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
get    core/pods                                (one dedicated runner namespace)
get    batch/jobs                               (one dedicated runner namespace)
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

Producer and broker roles receive execute-only functions and no direct queue or exchange-table access. `spyglass runner-broker` exposes `POST /internal/v1/runner/invocations/{id}/request`, idempotent `PUT /internal/v1/runner/invocations/{id}/result`, and `POST /internal/v1/runner/invocations/{id}/capabilities:invoke` over TLS 1.3. It rejects redirects, queries, unbounded or noncanonical JSON, malformed/duplicate bearer credentials, and backend-detail disclosure. Responses are non-cacheable. The runner-side client rereads the projected token file for every operation, refuses redirects, disables ambient proxies by default, and maps only content-free problem codes.

## Execution and capability boundary

The `runner-invocation` process fetches one admitted request, selects only a compiled kind-specific executor, supplies that executor a broker-backed capability client, canonicalizes one bounded object result, and submits exactly one terminal envelope. Unsupported kinds, executor machine failures, and invalid output submit `execution_failed`; private executor errors never cross the boundary. The first concrete kind, `work.summary.snapshot`, accepts only a UUID operation ID and requires the exact `work.summary.read` grant. It cannot accept an Account selector or call another capability.

Every capability call carries a UUID operation ID, a canonical object input capped at 256 KiB, and one capability code from the admitted request. The gateway repeats online TokenReview, exact Pod/Job identity verification, durable launch/cancellation state checking, envelope decryption, and capability membership checking for every call. It then applies a fixed registered handler timeout and caps canonical object output at 256 KiB. Provider credentials remain in the separate model-gateway service and never reach the broker database, runner, or runner Job.

Read-only and consequential definitions are distinct. A consequential definition cannot be constructed unless its handler implements separate execute and side-effect-free reconcile methods and the gateway has an action authorizer. The action service consumes an exact immutable projection from the future Attention-owned approval aggregate: canonical input SHA-256 plus hash version, evidence digest, proposer, approver, policy version, and expiry. Its forced-RLS ledger leases the stable operation UUID as the provider idempotency key. The first admission is `execute`; an expired lease, `unknown`, or even `succeeded` retry is `reconcile`, never another execution. Non-definitive adapter errors default to `unknown`; only an adapter that explicitly proves a definitive rejection may settle `failed`.

Mandatory pre/post capability audit writes are content-free and accepted only when the exchange is already bound to the same Pod UID. Action admission independently repeats the same exact Pod, Account, invocation, durable launch/cancellation, request-expiry, approval-expiry, capability, and input-digest checks in one database transaction. Post-execution action settlement and capability facts use bounded contexts detached from caller cancellation, so an admitted external result is not discarded when the runner disconnects.

The capability transport is mounted with the first concrete read-only handler, `work.summary.read`. The handler never reads a cell database. It signs a distinct, short-lived `SPYGLASS-TOOL` context bound to Account, invocation, Pod, operation, capability, HTTP target, semantic headers, and exact `{}` input. The app router consumes that proof once in the global `tool_context_receipts` ledger, reloads current Account state and the latest entitlement snapshot through the workload-only authorizer, resolves current placement, and emits a fresh workload route context for the fixed cell Work-summary query. Tool proofs use a separate issuer/keyring and cannot authenticate browser, route-context, or Kubernetes boundaries.

Two read-only capabilities are registered: `work.summary.read` and `agents.model.turn`. The latter injects the authorized invocation and operation IDs, then crosses a workload-mTLS boundary to a stateless provider-neutral model gateway. Its first adapter uses the OpenAI Responses API with strict tool and output schemas, sequential tool calls, bounded opaque continuation, provider-side storage disabled, normalized usage, and content-free failure mapping. The gateway computes cost from an operator-owned exact-model price book, rejects missing model prices before the external call, and returns the trusted micro-unit value with token usage. The runner accumulates both values across steps and enforces the frozen Persona ceilings. The gateway has the provider credential but no Account database or Kubernetes authority.

The compiled `agent.turn.execute` runner accepts a frozen provider/model/prompt/message/tool/output policy plus preplanned unique model/tool operation UUIDs. It permits at most five sequential tool steps, maps model tool names only to snapshotted capabilities present in the invocation grant, preserves every provider continuation/tool-output pair in order, aggregates billed usage across steps, and revalidates the final envelope through the Agents domain. It has no fallback tool, provider client, Account selector, or runtime-generated operation identity.

The serving side now persists each admitted Run, immutable user message, exact Persona versions, conversation context watermark, profile, expiry, and deterministic operation IDs atomically. Shared dispatch workers claim only Account/invocation identifiers, load the frozen input inside forced RLS, compile the application-owned result schema and exact capability list, and provision through the encrypted producer boundary. Completion succeeds only when the queue settlement digest equals the canonical digest stored by the runner exchange. Browser requests can neither submit an output schema nor select runner capabilities outside the registered Persona tool allowlist.

The Agents cell schema and execute-only projection functions bind a successful or failed durable invocation to the exact accepted runner-result digest. A shared cell projection worker claims identifier-only queue entries with expiring UUID leases, receives only the Pod-bound encrypted envelope, authenticates and decrypts it with the runtime keyring, repeats strict turn/result validation, and commits through a lease-bound function. Success atomically settles the invocation, allocates one conversation sequence, inserts exactly one persona Message, advances Run state, and settles the projection queue; failure inserts no Message. Invalid or unauthentic results are dead-lettered without persisting their plaintext. Exact committed replays are idempotent, while a stale or cross-Account lease is rejected.

The durable action authorizer is constructed by the broker, but no consequential definition is mounted until a real provider adapter supports stable idempotency and side-effect-free reconciliation. Terminal queue completion remains identifier-only; a bounded SKIP-LOCKED retention function destroys encrypted request/result envelopes after the configured recovery window without deleting hashes, action state, or capability audit. An Agent envelope is ineligible for destruction until its projection commits; dead letters retain ciphertext for controlled recovery. Explicit failed-action retry, projection dead-letter operator tooling, manual action resolution/redacted views, the full Attention approval aggregate, longer-lived action/audit retention policy, and capability policy at provisioning remain.

Cancellation revocation must also be enforced by every provider/tool gateway. Pod deletion or broker denial cannot erase plaintext already in runner memory, so a canceled or partitioned runner must have no direct provider, connector, customer-service, or unrestricted internet path on which it can continue side effects.

## Acceptance gates

- Wrong audience, ServiceAccount, namespace, Pod UID, Pod label, Job owner, Job UID, profile, invocation, contract digest, and terminating object are denied.
- Malformed and oversized tokens are denied before a Kubernetes request.
- Kubernetes errors disclose neither presented token nor customer identifiers.
- Runner Job render contains no default token and exactly one broker-audience projection.
- Runner Job render mounts the exact broker CA ConfigMap read-only, and the runner refuses a missing or invalid trust bundle.
- The projected broker token is rejected when presented directly to the Kubernetes API, and the runner ServiceAccount has no authorized API action even under audience misconfiguration.
- Applied RBAC proves the broker can review tokens and get exact Pod/Job objects but cannot list or mutate workloads.
- Pod deletion causes the next TokenReview/broker operation to fail.
- A token copied from runner A cannot fetch or submit invocation B.
- Ciphertext or associated-data tampering fails closed, and old key versions remain readable during rotation.
- Every capability retry repeats Pod identity, durable state, expiry, and grant checks; cancellation prevents handler entry even if the Pod remains alive.
- Consequential handlers cannot run without digest-bound action authorization and mandatory content-free audit.
- Broker cancellation and node-partition tests prove all external capabilities are revoked independently of process termination.
