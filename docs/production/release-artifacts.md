# Release artifacts and provenance

Status: RC.44 is the active Hostinger Stage review candidate; its matched AMD64 image triple passed local admission with attached BuildKit SBOM/provenance but is deliberately not production-promotable because it is single-platform and unsigned. RC.5/RC.4 remain the independently verified Phase 2.5 baseline/rollback pair; final-product certification remains a promotion gate.

Spyglass uses one shared, multi-mode application image for Account API, routers, cell APIs, private brokers, workers, runner execution, migrations, and one-shot operator commands. Runtime arguments choose the workload class. Kubernetes ServiceAccounts, NetworkPolicies, mounted credentials, database roles, and workload certificates—not separate per-customer builds—bound each process's authority. Ordinary Accounts never create an image, Deployment, namespace, or long-running container.

## Image contract

The root `Dockerfile`:

- pins both the Dockerfile frontend and official Go 1.26.6 Alpine multi-architecture builder by registry digest;
- copies only `go.mod`, `go.sum`, `cmd`, `internal`, and embedded `migrations` through an allowlist-only `.dockerignore`;
- builds with `CGO_ENABLED=0`, `-trimpath`, no VCS-dependent build mutation, and an empty Go build ID;
- injects version, full Git revision, and UTC build time into `internal/platform/buildinfo`;
- copies only the binary and public CA roots into a `scratch` runtime;
- runs as numeric non-root UID/GID `65532`; and
- contains no shell, package manager, source tree, prototype, website, environment file, credential, or customer-specific material.

Run `spyglass version` inside an image to retrieve its injected JSON identity. Every process also logs this identity before composition. The Kubernetes overlay must reference the multi-architecture manifest by `@sha256:` digest; tags are discovery labels and are never deployment identities.

UbuntuRojo runs repository verification, asserts the non-root image user, executes the version probe, builds and health-checks the production UI images, and scans those runtimes. The architecture test rejects mutable runtime bases, broad context copies, `latest` publication, missing SBOM/provenance/scanning stages, or any GitHub Actions workflow YAML reintroduced under `.github/workflows`.

## Publishing

`deploy/docker/spyglass/publish-stage-release.sh` is the active release publisher. It runs from UbuntuRojo against a clean, pushed `main`, uses ephemeral GHCR authentication, refuses existing version or revision tags, never publishes `latest`, and builds all three images from one source revision through an isolated BuildKit container. `SPYGLASS_RELEASE_PLATFORMS` selects the target platforms; each pushed OCI index receives maximal BuildKit provenance and an attached SBOM, then the pinned Trivy policy scans its immutable digest for high/critical vulnerabilities and secrets.

The generated `deploy/releases/<version>.env` file is the reviewed handoff and contains the exact application, public UI and private UI digest references:

```text
SPYGLASS_APPLICATION_IMAGE=ghcr.io/tinfoyle/spyglass-engine@sha256:<manifest-digest>
SPYGLASS_WEBSITE_IMAGE=ghcr.io/tinfoyle/infinite-ocean-public-ui@sha256:<manifest-digest>
SPYGLASS_PRIVATE_UI_IMAGE=ghcr.io/tinfoyle/infinite-ocean-private-ui@sha256:<manifest-digest>
```

Copy those exact references into the reviewed environment overlay and staging certification input. Never reconstruct a digest from a tag after review. Stage may deliberately select Linux AMD64; a production candidate must select every supported LKE architecture and add a trusted operator-managed Cosign signature before independent verification and promotion.

GitHub Actions is disabled repository-wide. The repository contains no workflow YAML and the architecture suite fails if one is reintroduced. Historical workflow runs and identities below remain provenance evidence for old immutable candidates only; they are not an active build, test, publication, or deployment path.

The first successfully signed pair is recorded in `deploy/releases/0.2.5-rc.2.env`: application digest `sha256:213a90c40198339ab92a48242310186a6cf9c0e29217ea32575e510631093add` and website digest `sha256:dfd0cf0480f7eff767db367b2ff8f4ccfa5c13ae3d96c66596185194e536f134`, both built from `5ce697933661e5b6d467804ad3608666f9c2dddd`. RC.2 predates the enforced admission scan and is retained only as publication history, not an approved rollback target.

RC.3 added the final stage `tool-router` and is recorded in `deploy/releases/0.2.5-rc.3.env`, but an independent 2026-08-21 Trivy 0.74.0 scan found 5 critical and 48 high findings in each website platform manifest, with no embedded secrets. Its application image was clean. The stale Alpine packages and bundled npm/Corepack dependency trees were removed from the website runtime; RC.3 remains signed historical evidence but is not admissible for deployment or rollback.

The first admitted pair is recorded in `deploy/releases/0.2.5-rc.4.env`: application digest `sha256:dccb70ddb34b975ada3a96c21ad775b4d2d21b64a2e87fc0f866a8441afc5800` and website digest `sha256:4630890a4f8f277c446c886febc7ae98ce32a04d3153f2aaeb91630e6a6e682d`, both built from `848de07e8d637b0488c18f5967be965bcf8bd773`. The application workflow completed at [run 32441659543](https://github.com/tinfoyle/mainspring-engine/actions/runs/32441659543). The first website attempt repeated the observed BuildKit stall and was canceled before publication; attempt 2 passed the overwrite guard, two-platform admission scan and signature at [run 32441659395](https://github.com/tinfoyle/mainspring-engine/actions/runs/32441659395). RC.4 is retained as the rollback pair.

The Phase 2.5 baseline is recorded in `deploy/releases/0.2.5-rc.5.env`: application digest `sha256:ebb385049702f6948ff6618c8b3d5e6fe07ff81c8c2b8f5b73ba478835b5f435` and website digest `sha256:d9d7c941c655197c5fca88a0825ce4bb3a2fa76ca60e6b476d3b00ef4552dbff`, both built from `4bd276c6f96f6e9c4feef811864403c5fa36a1bb`. The application workflow completed at [run 32444590302](https://github.com/tinfoyle/mainspring-engine/actions/runs/32444590302). Two website attempts met the bounded BuildKit stall criterion and were canceled before publication; attempt 3 passed the overwrite guard, both platform scans and signing at [run 32444587448](https://github.com/tinfoyle/mainspring-engine/actions/runs/32444587448).

The active Phase 3 Scheduling candidate is recorded in `deploy/releases/0.3.0-rc.1.env`: application digest `sha256:f37b9927870d7f697e639d7becdc6aa5b00bca793a601fac4b74a29873b48253` and website digest `sha256:2bd61f89e9ec049e92bd408fef780cc6c109aa2be99d752a14b39d4aa7e887ab`, both built from `9f87fd2482b77eafe53c832cfde703db027d71a2`. The application workflow completed at [run 32591754241](https://github.com/tinfoyle/mainspring-engine/actions/runs/32591754241), the website workflow at [run 32591754027](https://github.com/tinfoyle/mainspring-engine/actions/runs/32591754027), and main verification at [run 32591550152](https://github.com/tinfoyle/mainspring-engine/actions/runs/32591550152). Both release workflows passed overwrite refusal, multi-platform build, attached provenance/SBOM, per-platform vulnerability/secret admission and keyless signing. Stage runs these exact digests and has applied migrations 53–55. This intermediate construction candidate is not the final product release.

`0.3.0-rc.2.env` recorded the application built from `3657d97858b30a121a0e562a8a3c541609ac707c` at [run 32605107543](https://github.com/tinfoyle/mainspring-engine/actions/runs/32605107543), but reused the RC.1 website digest. It is therefore an application-only intermediate construction candidate, not a same-revision release pair and not eligible for final promotion under this document's paired-source rule. This discrepancy was found during the Account-export worker deployment checkpoint rather than silently carried forward.

The Account-export worker candidate is recorded in `deploy/releases/0.3.0-rc.3.env`: application digest `sha256:547c5c18e3d719cd01c7b9233b2e218a6a7af21ef388f719ce79310230d4f86a` and website digest `sha256:d3fe4a9505b3f2d44c49ad251049bc19e904f51d2d5d5d7f65b51ac64be2699d`, both built from `747b473315d44c615e0a199abb73ad438095fe22`. Main verification passed at [run 32648697941](https://github.com/tinfoyle/mainspring-engine/actions/runs/32648697941); the application release passed at [run 32649185182](https://github.com/tinfoyle/mainspring-engine/actions/runs/32649185182) and the website release at [run 32649186585](https://github.com/tinfoyle/mainspring-engine/actions/runs/32649186585). Both release jobs passed overwrite refusal, AMD64/ARM64 build, attached provenance/SBOM, both-platform vulnerability/secret admission and keyless signing. This is still an intermediate construction candidate, not a production release.

RC.3 exposed two deployment defects during Hostinger application: the expiry
worker received the unprefixed restore gate, and two new Marketing capability
identifiers violated the established broker grammar. The configuration repair
is enforced by `verify-stage.sh`; the capability repair changes the invalid
underscore-delimited identifiers to reviewed hyphen-delimited identifiers and
adds a regression over every exported runner capability. RC.3 remains immutable
history and is not a complete Stage candidate because its runner brokers cannot
start with that capability set.

The corrected Account-export worker candidate is recorded in
`deploy/releases/0.3.0-rc.4.env`: application digest
`sha256:ab5c8274eec8db17dd2e5f41155cad8f662009cdec9794bf44e188589d7f2990`
and website digest
`sha256:1c3199c83594a5acefec78a9ff58c4ab59375ea6b6423b6a3560542c1c1592b6`,
both built from `069b25992fb50f82aacbebf24ee6ab7a2cdfccb0`. Main
verification is [run 32651203877](https://github.com/tinfoyle/mainspring-engine/actions/runs/32651203877),
the application release passed at
[run 32651217283](https://github.com/tinfoyle/mainspring-engine/actions/runs/32651217283)
and the website release passed at
[run 32651216899](https://github.com/tinfoyle/mainspring-engine/actions/runs/32651216899).
Both release jobs passed overwrite refusal, AMD64/ARM64 build, attached
provenance/SBOM, both-platform vulnerability/secret admission and keyless
signing. RC.4 is an intermediate construction candidate, not a production
release.

## RC.6 Stage review artifact

The first three-image product-surface candidate is recorded in
`deploy/releases/0.3.0-rc.6.env`:

| Artifact | Immutable Stage reference |
|---|---|
| Application | `ghcr.io/tinfoyle/spyglass-engine@sha256:475a174705306f81e1d118ea647a994cfcc75334c6ef7b175ef65814cdcb5fdc` |
| Public UI | `ghcr.io/tinfoyle/infinite-ocean-public-ui@sha256:5f665b11d8b7d411ce6230781162da1bbd593079c56cb2f4b2e8f80f07fabd1f` |
| Private UI | `ghcr.io/tinfoyle/infinite-ocean-private-ui@sha256:f173a9e64817d856c79b36f12d43690853fcdb1d7093f0cd4868e48fde2a2e27` |

All three were built from source revision
`791d1eee2c9c258cec73868825daa2434ced8533`. GitHub release runs were
attempted but failed before runner steps executed. To unblock the explicitly
authorized Stage review, the images were built natively for Linux AMD64 in
UbuntuRojo, scanned with the pinned Trivy policy, and pushed without replacing
an existing tag. The three scans reported zero high/critical vulnerabilities
and zero secrets. Connected Stage runs the exact digests above from deployment
checkout `8f3370e4cee1b4f3c5aa6fe310507ef38bd64263`.

This fallback does not produce a production AMD64/ARM64 manifest, attached
SBOM, maximal provenance or keyless signature. RC.6 is admissible only for the
current Stage review and cannot be promoted to LKE. Production requires a new
complete-product release whose application/public/private artifacts all pass
the operator release policy and independent verification.

## RC.7 notification-deliverability correction

RC.7 is recorded in `deploy/releases/0.3.0-rc.7.env` and was built from source
revision `17459944f1d53c48a293b6541ced9a0f5129ed22`. It adds a unique,
sender-domain-aligned RFC 5322 `Message-ID` to every SMTP notification after
Gmail acceptance showed `SMTPIN_ADDED_MISSING` on RC.6 verification and
password-reset messages.

| Artifact | Immutable Stage reference |
|---|---|
| Application | `ghcr.io/tinfoyle/spyglass-engine@sha256:7aeafaf4e2a7c0ce3541f321c75a23e4f1f0d6225958ba9a77cd3b6342f07dcc` |
| Public UI | `ghcr.io/tinfoyle/infinite-ocean-public-ui@sha256:3cfec6c91174a93fd202cb3c9ed7bf3bd2cfa5698108ff9f676dbb335d50aac0` |
| Private UI | `ghcr.io/tinfoyle/infinite-ocean-private-ui@sha256:bc464c333c821c3e68e4b0c787200b4abd12e7c5c588305360c2cef6183bbe60` |

All three native Linux AMD64 images passed the pinned Trivy high/critical
vulnerability and secret gate and are active on Stage from deployment checkout
`6e8c3eb158eb6642f731658c10f264d4a87be96d`. Like RC.6, this fallback has no
production-grade multi-architecture manifest, attached SBOM/provenance or
keyless signature and is not production-promotable.

## RC.8 landing alignment correction

RC.8 is recorded in `deploy/releases/0.3.0-rc.8.env` and was built from source
revision `e4e4f7a30c5e2e4f0a16ada04175e93076c84e66`. It removes the decorative
one-degree rotation from the landing-page Your Turn preview and adds a CSS
regression asserting that the preview transform remains `none`.

| Artifact | Immutable Stage reference |
|---|---|
| Application | `ghcr.io/tinfoyle/spyglass-engine@sha256:8629176c163e550c5d6831bbf19099442039b6c515c89c3e5492715945864c81` |
| Public UI | `ghcr.io/tinfoyle/infinite-ocean-public-ui@sha256:46b207bf79dd9f2fcd8558f263fabf7deb7cbcefbb5551b67b658357507292d1` |
| Private UI | `ghcr.io/tinfoyle/infinite-ocean-private-ui@sha256:ec9ed67340751cc3aa58ebf3e180f0ea80d62fa5d8caf3f646a2fae1b2590b07` |

All three native Linux AMD64 images passed the pinned Trivy high/critical
vulnerability and secret gate and are active on Stage from deployment checkout
`a836a92044c35914ce9267adf93803db13bbed77`. The focused landing regression
passed ten desktop, mobile, tablet and accessibility browser profiles; the live
Stage element reports `transform: none`. Like RC.6 and RC.7, this fallback is
not production-promotable because it lacks the required signed,
multi-architecture evidence set.

## RC.9 Features story and local publisher

RC.9 is recorded in `deploy/releases/0.3.0-rc.9.env` and was built from source
revision `4e9b45e69aa7882ba8cb35eaf11c3185bc1bd529`. It replaces the public
Features card wall with a Your Turn-led product story and introduces the
revision-controlled UbuntuRojo GHCR publisher used to create the release.

| Artifact | Immutable Stage reference |
|---|---|
| Application | `ghcr.io/tinfoyle/spyglass-engine@sha256:94356094fd19ac71a66a2f1a85723da436a949392047a27669169ce7bd59dbf3` |
| Public UI | `ghcr.io/tinfoyle/infinite-ocean-public-ui@sha256:9d9fdd6cc7de09b8201082927b6d2b260a9bdcb14a3a88279928daaeacf077ba` |
| Private UI | `ghcr.io/tinfoyle/infinite-ocean-private-ui@sha256:c59d305fda046525e474f02c5b070114bb40778c120efab055bdd96e50a970e4` |

`publish-stage-release.sh` required clean pushed `main`, refused existing
version/revision tags, used an isolated BuildKit container, pushed matched
Linux AMD64 OCI indexes with attached SBOM and maximal provenance, and ran the
pinned Trivy 0.74.0 high/critical vulnerability and secret policy against each
immutable digest. All three scans reported zero findings. Connected Stage runs
the exact digests above from deployment checkout
`ebe0c4505d239d484d7540c94880022b98ce2ed8`; all 46 healthchecked workloads are
healthy. Live browser acceptance found the Your Turn-led page, all twelve
feature detail links, correct canonical metadata and no horizontal overflow.

RC.9 remains a Stage-only candidate because this publication selected only
Linux AMD64 and has no trusted Cosign release signature. The attached SBOM and
provenance improve on RC.6-RC.8 but do not by themselves satisfy the final LKE
admission policy.

## RC.4 and RC.5 independent evidence

Verification from Docker in `ubunturojo` used Cosign 3.1.3 image digest `sha256:9e5c2f2edc34351160407ca3416c61855bdf9403c3c5936e0f0be7fc261611b8` and Trivy 0.74.0 image digest `sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969`:

| Artifact/platform | Signature identity | SPDX 2.3 packages | Critical | High | Secrets |
|---|---|---:|---:|---:|---:|
| Application AMD64 | `release-image.yml@refs/tags/spyglass-v0.2.5-rc.4` | 39 | 0 | 0 | 0 |
| Application ARM64 | same | 39 | 0 | 0 | 0 |
| Website AMD64 | `release-website-image.yml@refs/tags/website-v0.2.5-rc.4` | 92 | 0 | 0 | 0 |
| Website ARM64 | same | 92 | 0 | 0 | 0 |

Both RC.4 Cosign checks validated the manifest claim, Fulcio certificate, GitHub OIDC issuer and Rekor transparency-log inclusion. Both platform SLSA records bind the correct workflow reference and builder run to source revision `848de07e8d637b0488c18f5967be965bcf8bd773`.

Independent verification repeated the same result for RC.5: one valid signature per manifest, 39 application and 92 website SPDX packages per platform, and zero critical, high or secret findings across all four scans. Its SLSA records bind `spyglass-v0.2.5-rc.5` and `website-v0.2.5-rc.5` to revision `4bd276c6f96f6e9c4feef811864403c5fa36a1bb`, application run 32444590302 attempt 1 and website run 32444587448 attempt 3. Signature, provenance, SBOM and vulnerability/secret evidence are complete for both admitted pairs; these results do not substitute for environment certification.

The RC.4-to-RC.5 source delta changes only deployment release records, environment digest references and documentation. Application, website, migration and Catalog code are identical, and live stage certified RC.5 -> RC.4 -> RC.5 against retained databases. That proves only the Phase 2.5 rollback pair. RC.1 includes additive Phase 3 migrations; no RC.1-to-RC.5 rollback rehearsal is claimed, and promotion requires a rollback pair proven against the final schema/Catalog/configuration state.

## RC.44 Stage functional-review artifact

The active matched application/public/private triple is recorded in
[`deploy/releases/0.3.0-rc.44.env`](../../deploy/releases/0.3.0-rc.44.env).
Its source revision is `171992fe96389367e0bd07f0f8edbc2593a7f9e0`; the reviewed manifest and active VPS
checkout are `ab964530a32240239e2b773d30617ccf00294d3d`. The UbuntuRojo publisher built all three AMD64
images, attached SBOM/provenance, and scanned their immutable digests with zero
high/critical vulnerability or secret findings. Connected deployment verification
confirmed the exact live digests, 49 running containers and 48 healthy workloads.
RC.43 was published but not activated; RC.44 also removes the remaining public
footer slogan. See the [functional audit](stage-functional-audit-2026-09-04.md)
for completed live tests and explicit acceptance limits.

The retained schema is global 68 and cell 86/86. Cell migration 86 allows human
Marketing approvals without a model invocation. RC.41 cannot read those rows;
rollback must retain this origin and the current Marketing decision authorization.
The historical Phase 2.5 rollback pair is not compatible evidence for this schema.
RC.44 remains unsigned and single-platform, and is not production-promotable.

## Verification and promotion

Before promotion:

```text
cosign verify \
  --key /path/to/spyglass-release-signing.pub \
  ghcr.io/tinfoyle/spyglass-engine@sha256:<digest>
docker buildx imagetools inspect ghcr.io/tinfoyle/spyglass-engine@sha256:<digest>
```

Repeat the signature check for the public and private UI digests. Also inspect each SBOM/provenance predicate, confirm the Git revision and operator signer identity, scan every immutable digest with the environment's admission scanner, and execute `spyglass version`. Admission policy should allow only reviewed digests with the expected signer and current vulnerability-policy result. A valid signature proves origin, not safety or approval. The private signing key must remain outside Git and outside release manifests.

Promotion reuses the same digest through staging, internal canary, customer canary, and production. Rebuilding the same revision creates a different artifact requiring new verification and staging evidence. Rollback selects a previously retained, still-approved digest plus its compatible database/Catalog/configuration state; it never retags an unknown image as the previous version.

## Remaining release evidence

RC.4/RC.5 independent evidence, the earlier signed Phase 3 checkpoints and connected Stage evidence through RC.44 are recorded. Before final promotion, publish a complete-product immutable application/public/private triple, independently verify each signature/provenance/SBOM/scan set, archive the rendered environment overlay digest, migration set, Catalog version and `staging-cert`, and prove a compatible rollback transition. Cluster admission enforcement and the final-triple staged rollback remain launch gates.
