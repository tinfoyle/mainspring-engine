#!/usr/bin/env bash
set -euo pipefail

overlay_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
bash "$overlay_dir/generate-linode-cells.sh" --check
temporary="$(mktemp -d)"
trap 'rm -rf -- "$temporary"' EXIT
for environment in linode-preproduction linode-production; do
  kubectl kustomize "$overlay_dir/$environment" >"$temporary/$environment.yaml"
  test "$(grep -c '^kind: Cluster$' "$temporary/$environment.yaml")" -eq 3
  grep -Fq 'kind: Ingress' "$temporary/$environment.yaml"
  grep -Fq 'kind: Certificate' "$temporary/$environment.yaml"
  grep -Fq 'ghcr.io/tinfoyle/spyglass-engine@sha256:' "$temporary/$environment.yaml"
  grep -Fq 'ghcr.io/tinfoyle/infinite-ocean-website@sha256:' "$temporary/$environment.yaml"
  for cell in a b; do
    for workload in app-api work-reconciler route-receipt-worker agent-dispatch-worker agent-projection-worker runner-controller runner-broker; do
      grep -Fq "name: $workload-cell-$cell" "$temporary/$environment.yaml"
    done
    grep -Fq "name: spyglass-cell-$cell-runtime" "$temporary/$environment.yaml"
    grep -Fq "name: spyglass-work-reconciler-cell-$cell-restore-checkpoints" "$temporary/$environment.yaml"
  done
  grep -Fq 'name: global-workloads-to-postgres' "$temporary/$environment.yaml"
  grep -Fq 'name: public-ingress' "$temporary/$environment.yaml"
  grep -Fq 'name: runner-control-to-kubernetes-api' "$temporary/$environment.yaml"
  for job in spyglass-global-migration spyglass-cell-a-migration spyglass-cell-b-migration spyglass-catalog-publication spyglass-release-identity; do
    grep -Fq "name: $job" "$temporary/$environment.yaml"
  done
  if grep -Eq 'registry.invalid|sha256:0{64}|image: .+:latest([[:space:]]|$)|^kind: Secret$' "$temporary/$environment.yaml"; then
    echo "$environment contains an unpinned image or plaintext Secret" >&2
    exit 1
  fi
  case "$environment" in
    linode-preproduction)
      application_namespace=spyglass-preproduction
      runner_namespace=spyglass-runners-preproduction
      rbac_name=spyglass-preproduction-runner-token-reviewer
      ;;
    linode-production)
      application_namespace=spyglass-production
      runner_namespace=spyglass-runners-production
      rbac_name=spyglass-production-runner-token-reviewer
      ;;
  esac
  grep -Fq "name: $application_namespace" "$temporary/$environment.yaml"
  grep -Fq "name: $runner_namespace" "$temporary/$environment.yaml"
  grep -Fq "name: $rbac_name" "$temporary/$environment.yaml"
  grep -Fq ".${application_namespace}.svc.cluster.local" "$temporary/$environment.yaml"
  grep -Fq "value: $runner_namespace" "$temporary/$environment.yaml"
  if grep -Eq 'spyglass-reference|spyglass-runners-reference|cell-reference' "$temporary/$environment.yaml"; then
    echo "$environment retains a reference namespace or internal origin" >&2
    exit 1
  fi
done
echo "Linode overlays render without mutable images or plaintext Secrets"
