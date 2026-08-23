# ADR-0008: Version connector scopes and reconcile uncertain external effects

- Status: Accepted
- Date: 2026-08-23
- Owners: Integrations, Marketing, Attention, platform security, operations

## Context

Spyglass must connect Account-owned work to email, Google Drive, web research and web publication without giving an Agent, provider process, browser or general application runtime reusable provider credentials. Marketing releases intentionally freeze content and channel intent but do not contain a provider target. Baseline source grants already name an Account connection and immutable read scope, while the prototype demonstrated that a durable outbox is necessary but kept credentials and provider rules inside one tenant process.

An external call can time out after the provider accepted it. Treating that as failure and sending again can duplicate email or publication. Treating it as success can lie to the customer. A connector edit after Marketing approval must also not change the destination of an already prepared effect.

## Decision

Integrations owns Account-scoped connections, credential bindings, immutable connection revisions, health observations and external execution records.

1. A connection has one closed connector kind and a bounded, human-readable capability set. Initial kinds are `email`, `google_drive`, `web_research` and `web_publish`; Marketing delivery uses only `email.send` and `web.publish`.
2. Each connection revision freezes its non-secret delivery/read scope. An email revision identifies the sender and an audience reference. A web-publication revision identifies one HTTPS origin and path prefix. Drive and research scopes are added through their own reviewed construction slices. Revising scope creates a new immutable revision rather than altering an old one.
3. A credential binding names an opaque secret-broker reference and generation. Provider credentials are never returned by an API, written to an event, supplied to an Agent, or stored in Marketing. Rotation creates a new generation; revocation is monotonic. The connector runtime resolves the reference only for one bounded operation.
4. A Marketing execution freezes the exact release, approval, connection revision and credential generation before any provider call. The selected connection must still have the channel capability, and the current Marketing and Integrations package modes, approval, credential, connector health and Account placement are rechecked before every execute or reconcile attempt.
5. One stable execution identity and payload digest are the idempotency boundary. Definitive provider rejection may enter bounded retry wait. A timeout, lost lease or ambiguous response enters `unknown` and can run only the connector's side-effect-free reconciliation operation. It is never automatically sent or published again. Unresolved uncertainty enters manual resolution: one current human Owner or Administrator proposes `succeeded` or `failed` with evidence, and a different current Owner or Administrator must confirm that exact digest-bound outcome before the execution becomes terminal.
6. Provider payloads, credentials, campaign text, audiences and asset content are absent from operational rows, events, metrics and logs. Observability carries Account-safe identities, connector kind, capability, state, attempt count, timing and bounded machine error classes.
7. A local mock connector is the required development and certification dependency. SMTP, provider API and web-publication adapters remain separately deployable connector-runtime implementations with exact egress and secret authority.
8. Revoking a connection or credential prevents new effects and further execute attempts. It does not rewrite immutable delivery history or claim that a previously unknown effect did not happen.

## Consequences

- Marketing approval and connector authorization remain separate, explicit authorities; neither module absorbs the other's state.
- Connector scope cannot drift after an execution is prepared.
- Provider replacement and credential rotation do not rewrite prior execution evidence.
- Ambiguous delivery may require a two-person human decision, which is safer than duplicate external effects or unilateral history rewriting.
- Production needs a credential broker or managed secret store and a dedicated connector runtime; ordinary app and runner pods receive neither authority.

## Verification

- Pure tests cover connection scope normalization, role policy, immutable revision binding, credential rotation/revocation, capability checks and delivery state transitions.
- PostgreSQL tests prove forced RLS, exact Account relationships, immutable revisions/attempts/resolutions/events, claim leasing, retry versus reconcile admission, lost-lease behavior, distinct resolution actors, credential revocation, movement fencing and erasure accounting.
- Local Docker and Stage use the mock connector to inject definitive failure, retryable failure, ambiguous acceptance and reconciliation outcomes without external credentials.
- Stage certification prepares one exact email and web execution from an approved synthetic release, proves no duplicate effect after an unknown outcome, revokes the credential and erases the complete fixture.
