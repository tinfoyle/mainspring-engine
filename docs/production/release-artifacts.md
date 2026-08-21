# Release artifacts and provenance

Status: first application/website release pair published and signed; independent scanning, environment admission, staging certification, and rollback evidence remain promotion gates

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

Pull requests build the image, assert the non-root user, execute the version probe, and compare the embedded revision with the checkout. The architecture test rejects mutable runtime bases, broad context copies, `latest` publication, missing SBOM/provenance/signing stages, or release actions not pinned to full commits.

## Publishing

`.github/workflows/release-image.yml` runs for reviewed `spyglass-v*` tags or a manually approved `release` environment dispatch. It publishes only version and full-revision tags to `ghcr.io/tinfoyle/spyglass-engine`, never `latest`, and fails if either tag already exists rather than overwriting release history. BuildKit produces a Linux AMD64/ARM64 manifest with maximal provenance and an attached SBOM. The workflow applies a keyless Sigstore Cosign signature to that immutable manifest digest. The signature covers the index that carries the BuildKit provenance and SBOM descriptors.

The workflow summary is the handoff value:

```text
ghcr.io/tinfoyle/spyglass-engine@sha256:<manifest-digest>
```

Copy that exact reference into the reviewed environment overlay and the staging certification input. Never reconstruct a digest from a tag after review.

`.github/workflows/release-website-image.yml` applies the same overwrite refusal, AMD64/ARM64 build, attached SBOM, maximal BuildKit provenance and keyless Cosign policy to `ghcr.io/tinfoyle/infinite-ocean-website`. It is triggered by a reviewed `website-v*` tag or release-environment dispatch. Application and website artifacts from one release candidate must record the same source revision, but retain independent manifest digests because they are distinct images.

The first successful pair is recorded in `deploy/releases/0.2.5-rc.2.env`: application digest `sha256:213a90c40198339ab92a48242310186a6cf9c0e29217ea32575e510631093add` and website digest `sha256:dfd0cf0480f7eff767db367b2ff8f4ccfa5c13ae3d96c66596185194e536f134`, both built from `5ce697933661e5b6d467804ad3608666f9c2dddd`. Both release workflows completed signing, both indexes expose AMD64/ARM64 plus attached SPDX/SLSA manifests, and an authenticated digest pull verified application build identity and website readiness.

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

The RC.2 digest pair and workflow signing results are recorded. Before promotion, archive independent Cosign verification output, BuildKit provenance/SBOM identity, vulnerability and secret-scan results, environment overlay digest, migration set, Catalog version, and `staging-cert` record. Cluster admission enforcement and a staged rollback using two retained compatible pairs remain launch gates.
