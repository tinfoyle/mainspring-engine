# Marketing module

- Status: governed kernel, persistence, routed HTTP/MCP/private-browser, Agent drafts and Attention activation constructed; Integrations delivery remains closed
- Package boundary: Marketing
- Decision: [ADR-0007](decisions/0007-marketing-governed-release.md)

## Launch contract

Marketing owns Account-scoped campaigns, immutable creative asset revisions and exact human-approved release plans. It does not own email/web credentials, provider delivery, public-site content, subscription billing or general document storage.

Campaigns freeze a bounded objective, audience and supported channel set. Drafts may originate from a human or an Agent; an Agent draft must carry the exact Run and invocation. Creative revisions contain no provider credential and no mutable body: each one freezes the content SHA-256, byte count, media type, opaque storage reference and accessibility alternative where required.

A release freezes one campaign version, sorted unique asset revisions and its exact channel set. A User submits it; only an Owner or Administrator can bind an Attention approval and activate the campaign. External delivery remains an Integration effect and must reauthorize the exact release at execution time.

## Construction sequence

1. Typed campaign lifecycle, immutable creative revisions, Agent provenance and exact approval-bound release snapshots. **Constructed.**
2. Account-owned forced-RLS persistence, immutable redacted events, optimistic replay, movement fencing and exact erasure/restore participation. **Constructed.**
3. Stable detail/list queries, bounded cursors and the classified package-authorized application service. **Constructed.**
4. Generated HTTP and MCP operations plus the private package-aware Marketing workspace. **Constructed.**
5. Narrow Agent draft tools and Attention-governed release proposals; no workload-direct approval or delivery. **Constructed.**
6. Integration execution records for email/web, credential/capability checks, retry/unknown reconciliation and delivery observability. **Persistence and governed preparation constructed; application worker and adapters remain.**
7. Catalog/entitlement lifecycle, retention, prototype reconciliation, Stage recovery/erasure and production role grants.

## Kernel checkpoint

The provider-free Go package implements optimistic campaign revision and governed draft/active/paused/completed/archived transitions. Activation accepts only an approved release from the same Account and campaign whose frozen campaign version and normalized channels exactly match the current campaign.

Asset content is append-only by construction: each revision has a distinct identity and monotonically increasing revision number over the same Account/campaign/asset tuple. Images require alternative text, media types are bounded, zero digests and empty content are rejected, and the previous value is never mutated.

Release plans normalize and freeze supported channels and unique revision identities. Only a User can submit; only an Owner or Administrator can approve with a valid consequential-approval identity. Workloads may prepare drafts only with complete Run/invocation provenance. The deployment inventory deliberately keeps Marketing non-executable until the remaining construction sequence is complete.

## Persistence checkpoint

Cell migration `000059_marketing_foundation.sql` adds eight Account-owned tables for campaigns/channels, stable assets, immutable revisions, release snapshots/channels/assets and content-redacted events. Every table has forced RLS and the Account namespace write fence. Agent-created records carry a database foreign key over the exact `(Account, invocation, Run)` tuple, so individually valid but mismatched provenance cannot persist.

Revision insertion is serialized per Account/asset and must advance by exactly one; revisions, release contents and events cannot be updated or deleted outside the normal Account movement/erasure cascade. Campaign updates enforce optimistic version transitions and the lifecycle state graph. Activation resolves the approved release under the same transaction, requires the prior campaign version and identical channel set, and rechecks that its Attention approval is still approved and unexpired. An active campaign must be paused before that release can be cancelled.

The campaign/release dependency graph deliberately remains one-way for movement: `active_release_id` is validated by the guarded transition rather than a reverse foreign key, avoiding a table cycle while preserving transaction-time integrity. Fresh PostgreSQL 17 tests prove sequential revision fencing, immutable snapshots/events, redacted-event policy, cross-Account denial, all-table movement topology and exact whole-Account erasure counts for all eight tables.

## Application/repository checkpoint

The Marketing application service now applies the canonical package requirement to every read and mutation. Human drafts require Owner, Administrator or Member authority; governance transitions require a real User, with approval/activation/cancellation and campaign state management restricted to Owner or Administrator. Workload drafts carry no inherited Membership role and are accepted only when the authenticated `runner-invocation:<uuid>` identity exactly matches the supplied Agent provenance tuple.

The classified PostgreSQL repository executes every command in an Account-scoped transaction. Campaign and release creation, campaign revision, asset revision, release submit/approve/cancel and campaign activate/pause/complete/archive preserve immutable event UUIDs and exact retry outcomes. Conflicting request-ID reuse, altered replay input, stale versions, cross-Account reads, mismatched asset/campaign references and unsupported state transitions fail closed. Release creation resolves every exact asset revision inside the campaign; activation locks both campaign and release before the database rechecks current Attention approval.

Fresh PostgreSQL 17 coverage runs the complete draft-to-submitted-to-approved-to-active lifecycle, rejects cancellation while active, pauses then cancels, restores exact snapshots, advances two content revisions, proves replay and hides the Account from another Account. Stable asset/revision pages remain before generated transports can be added.

## Query checkpoint

The read boundary now exposes bounded campaign pages ordered by stable `(updated_at DESC,id)`, per-campaign release pages ordered by `(created_at DESC,id)` and campaign/optional-asset revision pages ordered by `(asset_id,revision DESC)`. Optional campaign-state and asset filtering are validated against the closed lifecycle/identity vocabulary. Defaults and hard maxima are application-owned, cursors are structurally complete, and the repository fetches one extra identity so `NextCursor` is emitted only when another row actually exists. Each selected aggregate is restored through the typed kernel under the same read-only Account transaction. Migration 60 adds the matching all-campaign and campaign-asset keyset indexes.

Fresh PostgreSQL 17 traversal tests create two campaigns, two releases and two immutable asset revisions, prove exact first/remainder pages without duplicates and preserve the complete channel/asset snapshot.

## HTTP read checkpoint

Five generated, session-authenticated cell routes now expose campaign list/detail, per-campaign asset-revision and release pages, and release detail. The app runtime constructs the Marketing repository/service and places it behind the same signed request-bound Account context used by other cell packages. A route can read only the Account carried by that accepted proof; mismatched path Accounts are concealed as not found, and the application service independently reauthorizes the Marketing package.

Campaign state and optional asset filters use the closed typed vocabularies. Opaque cursors carry an explicit version and collection kind, reject unknown fields and cannot be replayed across campaign, release or asset-revision collections. Detail responses expose weak version ETags. Asset content digests cross the HTTP boundary as canonical lowercase SHA-256 hex rather than Go byte arrays, while content itself and provider credentials remain absent. The OpenAPI source generates the matching Go route inventory and TypeScript route/schema types; all five response families pass the repository's OpenAPI response validator.

## HTTP mutation checkpoint

Eleven generated commands complete the customer HTTP lifecycle: create/revise/archive campaigns, append immutable asset revisions, create/submit/approve/cancel releases and activate/pause/complete campaigns. Every mutation binds its `Idempotency-Key` to the signed route operation UUID; versioned commands require exactly one weak `If-Match` ETag and return the resulting version. Create responses distinguish first application from exact replay while preserving stable locations where a detail route exists.

Browser-created campaigns, assets and releases are explicitly stamped with human provenance. Content SHA-256 input must be exactly 32 lowercase bytes in hex before it enters the domain. JSON rejects unknown fields, trailing values, query parameters and the wrong media type. Governance still executes only through the application role checks and exact Attention approval binding; the HTTP adapter cannot supply Agent provenance or elevate a workload. Focused contract tests cover all eleven operations plus missing versions, malformed hashes and cross-Account concealment. MCP, private Marketing workspace, Agent proposals and Integration delivery remain closed, so Marketing correctly remains non-executable.

## MCP checkpoint

The Bearer-only production MCP adapter publishes 16 typed Marketing tools: five bounded/detail reads; campaign, asset-revision and release draft creation; and the eight remaining human lifecycle commands. Every tool is registered in the global gateway requirement table with the same Marketing read/mutation classification repeated inside the cell. Operation identities and expected versions remain explicit typed inputs so retries reach the same application replay boundary.

Human MCP drafts receive human provenance and reject a supplied Run. A workload draft is accepted only when its authenticated identity has the exact `runner-invocation:<uuid>` form and the caller supplies a valid Run; the adapter derives the invocation identity rather than trusting it as tool input. Application policy still rejects workload revision, submission, approval, activation, cancellation and campaign governance. Asset hashes remain lowercase hex, list cursors are versioned and collection-bound, and errors expose only stable safe codes. Protocol tests prove the complete deterministic schema surface, global routing classification, routed Marketing claims, opaque cursors, human and Agent provenance, version requirements and backend-error redaction.

## Private workspace checkpoint

`/app/marketing` now gives the selected Account a package-aware browser workspace over the generated Marketing HTTP boundary. Enabled Accounts can create and revise campaign intent, append immutable creative revisions, freeze release snapshots and perform the human-governed lifecycle. Submitted Agent-proposed activations lead to Your Turn; the browser never asks a User to paste an internal approval identity. Read-only Accounts retain campaign, asset and release inspection while every mutation control and form is omitted server-side; locked Accounts receive no Marketing client script. The shell keeps activation distinct from external delivery and does not hold provider credentials or creative bodies.

Every existing-resource command uses the freshly loaded aggregate version in a weak `If-Match` precondition, and every mutation receives a new route-bound idempotency UUID. Creative digests remain canonical lowercase SHA-256 input. Customer values are rendered through DOM text nodes, and no session, customer or draft material is placed in browser storage. Template/client tests prove package and read-only gating, lifecycle endpoint coverage, version/idempotency preconditions and DOM/storage safety. This is a desktop-first construction checkpoint; the final supported-device and mobile visual pass remains part of the Phase 3 product-surface acceptance matrix.

## Attention activation checkpoint

Published Personas may now propose `marketing.release.activate` with only `{campaign_id,campaign_version,release_id,release_version}`. It is not a runner tool and neither the Agent nor the browser can provide an Approval identity. Approval projects the exact canonical payload into the existing durable approved-action queue; the post-run worker resolves the immutable Attention binding and performs release approval plus campaign activation atomically under Account RLS. The approving Owner or Administrator is recorded as both actors, while deterministic child event identities derive from the approval operation UUID.

The worker refuses stale campaign or release versions, mismatched campaign ownership, changed channels, expired/canceled approval or a release outside the submitted state. Side-effect-free reconciliation requires both exact immutable events, so a lost transaction response cannot cause duplicate execution and a partial release/campaign transition cannot exist. Focused handler tests reject caller-supplied approval fields; a fresh PostgreSQL 17 test proves the exact authorization, atomic activation and reconciliation path.

## Agent draft-tool checkpoint

Published Personas may receive three bounded Marketing reads—campaigns, immutable asset revisions and release snapshots—and three additive draft tools for campaign, asset-revision and release creation. Every invocation is reauthorized at the private tool router against current Account placement and Marketing package mode before a one-use cell proof is minted. Read calls are capped at 100 records. Mutation calls reach dedicated internal draft-only routes; the public human commands are not reused and no governance transition is registered as a runner tool.

The cell derives the invocation identity from the authenticated `runner-invocation:<uuid>` principal and requires an explicit valid Run in each draft. It supplies both values to the canonical Marketing application service, whose database foreign key independently proves the exact `(Account, invocation, Run)` tuple. Tool input rejects unknown fields and invalid identities before routing; the campaign identity remains in the signed path rather than being duplicated in mutation bodies. Persona policy, tool-router dispatch and cell transport tests prove current-package reauthorization, read-only versus additive classification, private-route mapping, human rejection and derived Agent provenance. Construction step 5 is complete. Integration delivery, Catalog/retention and applied acceptance remain, so Marketing stays non-executable.

## Integration persistence checkpoint

Migration 62 now persists a Marketing delivery as an Integrations-owned effect that freezes the exact approved release version, `marketing.release.activate` Attention approval, channel-compatible connector revision, credential generation and canonical payload digest. The worker claim boundary rechecks the campaign is still active on that release and every authority binding is still current before returning work. Unknown outcomes reconcile only, a provider-confirmed no-effect result is required before re-execution, and bounded uncertainty enters manual resolution.

This closes the durable ledger and claim semantics within construction step 6, including Account movement and erasure participation. The ledger alone does not make Marketing executable; secret-broker binding, provider worker/adapters, Catalog/retention and applied acceptance still remain.

## Delivery preparation checkpoint

After an approved release is active, a human Owner or Administrator can now select one active channel-compatible Integrations connection and prepare its immutable delivery execution. The service requires matching current Account placement and entitlement versions from enabled Marketing and Integrations package checks, while PostgreSQL independently rechecks the active Marketing release/version, exact unexpired Attention approval, release channel, connector revision and credential generation. Marketing still stores no connector or credential identity.

The execution digest is computed from a canonical content-free manifest over the exact release/connector authority and sorted immutable asset digests. Exact retries replay; altered request reuse conflicts. This completes the governed preparation portion of construction step 6, but provider execution remains closed until the broker handoff and connector worker/adapters are constructed and certified.

## Connector worker checkpoint

A provider-neutral connector worker kernel now consumes the prepared execution through execute-only leased database functions. It separates execute from side-effect-free reconciliation, rejects `not_applied` from an execute adapter, treats malformed post-call results as unknown and keeps payload material only in connector-runtime memory. The deterministic local mock can certify success, definite failure, ambiguous acceptance and provider-confirmed absence without network I/O.

Marketing remains non-executable because the concrete current-authority/health client, manifest payload source, one-operation secret broker, worker bootstrap and Docker wiring are still open. No generic app or Agent runner receives provider authority.

## Invariants

- Every aggregate and revision is explicitly Account scoped.
- Campaign activation cannot cross Account, campaign, version or channel boundaries.
- Approved release material never follows later campaign or asset edits.
- A workload never performs a governance transition or external delivery.
- Connector credentials and provider payloads never enter Marketing state.
- Operational events and metrics never contain objective, audience or creative content.
