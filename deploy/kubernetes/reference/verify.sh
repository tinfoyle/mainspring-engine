#!/usr/bin/env bash
set -euo pipefail

reference_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
rendered="$(mktemp)"
trap 'rm -f "$rendered"' EXIT

kubectl kustomize "$reference_dir" >"$rendered"
test -s "$rendered"

required_objects=(
  "name: agent-dispatch-worker-cell-reference"
  "name: agent-projection-worker-cell-reference"
  "name: runner-controller-cell-reference"
  "name: runner-broker-cell-reference"
  "name: model-gateway"
  "name: runner-to-broker"
  "name: broker-to-model-gateway"
  "name: spyglass-reference-runner-token-reviewer"
  "name: spyglass_agent_dispatch_ready"
  "name: spyglass_agent_projection_ready"
  "name: spyglass_runner_ready"
  "name: spyglass-runners-reference"
  "name: runner-default-deny"
)
for object in "${required_objects[@]}"; do
  grep -Fq "$object" "$rendered"
done

# The reference deliberately contains no credentials and never floats a
# release image. Environment overlays own Secret material and image digests.
if grep -Eq '^kind: Secret$|image: .+:latest([[:space:]]|$)' "$rendered"; then
  echo "reference render contains a Secret or floating latest image" >&2
  exit 1
fi

# RBAC stays verb-minimal: controller Job lifecycle, broker object identity,
# and TokenReview only. Broader verbs are never accepted in this base.
for forbidden in list watch patch update exec impersonate bind escalate; do
  if grep -Eq "verbs:.*(^|[^a-z])${forbidden}([^a-z]|$)" "$reference_dir/runner-rbac.yaml"; then
    echo "runner RBAC contains forbidden verb: ${forbidden}" >&2
    exit 1
  fi
done
grep -Fq 'verbs: [create, get, delete]' "$reference_dir/runner-rbac.yaml"
test "$(grep -Fc 'verbs: [get]' "$reference_dir/runner-rbac.yaml")" -eq 2
test "$(grep -Fc 'verbs: [create]' "$reference_dir/runner-rbac.yaml")" -eq 1

# The ordinary runner identity is permissionless and its Deployment is created
# only dynamically with an exact audience-bound projected token.
grep -A5 -F 'name: runner' "$reference_dir/service-accounts.yaml" | grep -Fq 'automountServiceAccountToken: false'
grep -Fq 'name: default-deny' "$rendered"
