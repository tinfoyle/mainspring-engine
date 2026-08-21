# Release artifacts and provenance

Status: RC.4 is the first independently verified, vulnerability-admitted application/website pair; environment certification and rollback evidence remain promotion gates

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

Pull requests build the image, assert the non-root user, execute the version probe, build and health-check the production website image, and scan that runtime. The architecture test rejects mutable runtime bases, broad context copies, `latest` publication, missing SBOM/provenance/signing/scanning stages, or release actions not pinned to full commits.

## Publishing

`.github/workflows/release-image.yml` runs for reviewed `spyglass-v*` tags or a manually approved `release` environment dispatch. It publishes only version and full-revision tags to `ghcr.io/tinfoyle/spyglass-engine`, never `latest`, and fails if either tag already exists rather than overwriting release history. BuildKit produces a Linux AMD64/ARM64 manifest with maximal provenance and an attached SBOM. A digest-pinned Trivy container scans both platform manifests for high/critical vulnerabilities and secrets. Only a passing pair reaches the keyless Sigstore Cosign signature step. The signature covers the index that carries the BuildKit provenance and SBOM descriptors.

The workflow summary is the handoff value:

```text
ghcr.io/tinfoyle/spyglass-engine@sha256:<manifest-digest>
```

Copy that exact reference into the reviewed environment overlay and the staging certification input. Never reconstruct a digest from a tag after review.

`.github/workflows/release-website-image.yml` applies the same overwrite refusal, AMD64/ARM64 build, attached SBOM, maximal BuildKit provenance, two-platform admission scan and keyless Cosign policy to `ghcr.io/tinfoyle/infinite-ocean-website`. It is triggered by a reviewed `website-v*` tag or release-environment dispatch. Application and website artifacts from one release candidate must record the same source revision, but retain independent manifest digests because they are distinct images.

The first successfully signed pair is recorded in `deploy/releases/0.2.5-rc.2.env`: application digest `sha256:213a90c40198339ab92a48242310186a6cf9c0e29217ea32575e510631093add` and website digest `sha256:dfd0cf0480f7eff767db367b2ff8f4ccfa5c13ae3d96c66596185194e536f134`, both built from `5ce697933661e5b6d467804ad3608666f9c2dddd`. RC.2 predates the enforced admission scan and is retained only as publication history, not an approved rollback target.

RC.3 added the final stage `tool-router` and is recorded in `deploy/releases/0.2.5-rc.3.env`, but an independent 2026-08-21 Trivy 0.74.0 scan found 5 critical and 48 high findings in each website platform manifest, with no embedded secrets. Its application image was clean. The stale Alpine packages and bundled npm/Corepack dependency trees were removed from the website runtime; RC.3 remains signed historical evidence but is not admissible for deployment or rollback.

The current stage candidate is recorded in `deploy/releases/0.2.5-rc.4.env`: application digest `sha256:dccb70ddb34b975ada3a96c21ad775b4d2d21b64a2e87fc0f866a8441afc5800` and website digest `sha256:4630890a4f8f277c446c886febc7ae98ce32a04d3153f2aaeb91630e6a6e682d`, both built from `848de07e8d637b0488c18f5967be965bcf8bd773`. The application workflow completed at [run 32441659543](https://github.com/tinfoyle/mainspring-engine/actions/runs/32441659543). The first website attempt repeated the observed BuildKit stall and was canceled before publication; attempt 2 passed the overwrite guard, two-platform admission scan and signature at [run 32441659395](https://github.com/tinfoyle/mainspring-engine/actions/runs/32441659395).

## RC.4 independent evidence

Verification from Docker in `ubunturojo` used Cosign 3.1.3 image digest `sha256:9e5c2f2edc34351160407ca3416c61855bdf9403c3c5936e0f0be7fc261611b8` and Trivy 0.74.0 image digest `sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969`:

| Artifact/platform | Signature identity | SPDX 2.3 packages | Critical | High | Secrets |
|---|---|---:|---:|---:|---:|
| Application AMD64 | `release-image.yml@refs/tags/spyglass-v0.2.5-rc.4` | 39 | 0 | 0 | 0 |
| Application ARM64 | same | 39 | 0 | 0 | 0 |
| Website AMD64 | `release-website-image.yml@refs/tags/website-v0.2.5-rc.4` | 92 | 0 | 0 | 0 |
| Website ARM64 | same | 92 | 0 | 0 | 0 |

Both Cosign checks validated the manifest claim, Fulcio certificate, GitHub OIDC issuer and Rekor transparency-log inclusion. Both platform SLSA records bind the correct workflow reference and builder run to source revision `848de07e8d637b0488c18f5967be965bcf8bd773`. Signature, provenance, SBOM and vulnerability/secret evidence are complete for this pair; these results do not substitute for environment certification.

## Verification and promotion

Before promotion:

```text
cosign verify \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/tinfoyle/mainspring-engine/.github/workflows/release-image.yml@refs/(tags/spyglass-v[0-9][0-9A-Za-z.-]*|heads/main)$' \
  ghcr.io/tinfoyle/spyglass-engine@sha256:<digest>
docker buildx imagetools inspect ghcr.io/tinfoyle/spyglass-engine@sha256:<digest>
```

Also inspect the SBOM/provenance predicate, confirm the Git revision and workflow identity, scan the immutable digest with the environment's admission scanner, and execute `spyglass version`. Admission policy should allow only reviewed digests with the expected workflow identity and current vulnerability-policy result. A valid signature proves origin, not safety or approval.

Promotion reuses the same digest through staging, internal canary, customer canary, and production. Rebuilding the same revision creates a different artifact requiring new verification and staging evidence. Rollback selects a previously retained, still-approved digest plus its compatible database/Catalog/configuration state; it never retags an unknown image as the previous version.

## Remaining release evidence

RC.4 signature, BuildKit provenance/SBOM identity and vulnerability/secret-scan evidence are recorded. Before promotion, archive the rendered environment overlay digest, migration set, Catalog version and `staging-cert` record. RC.4 is currently the only admitted pair; a later admitted pair must retain RC.4 as its compatible rollback target. Cluster admission enforcement and an actual staged rollback remain launch gates.
