# Marketing module

- Status: provider-neutral kernel constructed; persistence and every executable surface remain closed
- Package boundary: Marketing
- Decision: [ADR-0007](decisions/0007-marketing-governed-release.md)

## Launch contract

Marketing owns Account-scoped campaigns, immutable creative asset revisions and exact human-approved release plans. It does not own email/web credentials, provider delivery, public-site content, subscription billing or general document storage.

Campaigns freeze a bounded objective, audience and supported channel set. Drafts may originate from a human or an Agent; an Agent draft must carry the exact Run and invocation. Creative revisions contain no provider credential and no mutable body: each one freezes the content SHA-256, byte count, media type, opaque storage reference and accessibility alternative where required.

A release freezes one campaign version, sorted unique asset revisions and its exact channel set. A User submits it; only an Owner or Administrator can bind an Attention approval and activate the campaign. External delivery remains an Integration effect and must reauthorize the exact release at execution time.

## Construction sequence

1. Typed campaign lifecycle, immutable creative revisions, Agent provenance and exact approval-bound release snapshots. **Constructed.**
2. Account-owned forced-RLS persistence, immutable redacted events, optimistic replay, movement fencing and exact erasure/restore participation.
3. Stable detail/list queries, bounded cursors and the classified package-authorized application service.
4. Generated HTTP and MCP operations plus the private package-aware Marketing workspace.
5. Narrow Agent draft tools and Attention-governed release proposals; no workload-direct approval or delivery.
6. Integration execution records for email/web, credential/capability checks, retry/unknown reconciliation and delivery observability.
7. Catalog/entitlement lifecycle, retention, prototype reconciliation, Stage recovery/erasure and production role grants.

## Kernel checkpoint

The provider-free Go package implements optimistic campaign revision and governed draft/active/paused/completed/archived transitions. Activation accepts only an approved release from the same Account and campaign whose frozen campaign version and normalized channels exactly match the current campaign.

Asset content is append-only by construction: each revision has a distinct identity and monotonically increasing revision number over the same Account/campaign/asset tuple. Images require alternative text, media types are bounded, zero digests and empty content are rejected, and the previous value is never mutated.

Release plans normalize and freeze supported channels and unique revision identities. Only a User can submit; only an Owner or Administrator can approve with a valid consequential-approval identity. Workloads may prepare drafts only with complete Run/invocation provenance. The deployment inventory deliberately keeps Marketing non-executable until the remaining construction sequence is complete.

## Invariants

- Every aggregate and revision is explicitly Account scoped.
- Campaign activation cannot cross Account, campaign, version or channel boundaries.
- Approved release material never follows later campaign or asset edits.
- A workload never performs a governance transition or external delivery.
- Connector credentials and provider payloads never enter Marketing state.
- Operational events and metrics never contain objective, audience or creative content.
