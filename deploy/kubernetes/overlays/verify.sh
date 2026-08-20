#!/usr/bin/env bash
set -euo pipefail

overlay_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
temporary="$(mktemp -d)"
trap 'rm -rf -- "$temporary"' EXIT
for environment in linode-preproduction linode-production; do
  kubectl kustomize "$overlay_dir/$environment" >"$temporary/$environment.yaml"
  test "$(grep -c '^kind: Cluster$' "$temporary/$environment.yaml")" -eq 3
  grep -Fq 'kind: Ingress' "$temporary/$environment.yaml"
  grep -Fq 'kind: Certificate' "$temporary/$environment.yaml"
  grep -Fq 'ghcr.io/tinfoyle/spyglass-engine@sha256:' "$temporary/$environment.yaml"
  grep -Fq 'ghcr.io/tinfoyle/infinite-ocean-website@sha256:' "$temporary/$environment.yaml"
  if grep -Eq 'registry.invalid|image: .+:latest([[:space:]]|$)|^kind: Secret$' "$temporary/$environment.yaml"; then
    echo "$environment contains an unpinned image or plaintext Secret" >&2
    exit 1
  fi
done
echo "Linode overlays render without mutable images or plaintext Secrets"
