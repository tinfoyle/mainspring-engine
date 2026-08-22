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
  "name: baseline-maintenance-worker-cell-reference"
  "name: runner-controller-cell-reference"
  "name: runner-broker-cell-reference"
  "name: tool-router"
  "name: model-gateway"
  "name: runner-to-broker"
  "name: broker-to-model-gateway"
  "name: spyglass-reference-runner-token-reviewer"
  "name: spyglass_agent_dispatch_ready"
  "name: spyglass_agent_projection_ready"
  "name: spyglass_runner_ready"
  "name: spyglass-runners-reference"
  "name: runner-default-deny"
  "name: otel-collector"
  "name: workloads-to-otel-collector"
  "name: spyglass-production"
  "kind: PrometheusRule"
)
for object in "${required_objects[@]}"; do
  grep -Fq "$object" "$rendered"
done
grep -Fq 'value: https://tool-router.spyglass-reference.svc.cluster.local' "$rendered"

# The reference deliberately contains no credentials and never floats an
# image. Environment overlays own Secret material and Spyglass release digests;
# the third-party Collector is already pinned to a reviewed multi-platform digest.
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

# The observability gateway is a bounded, credential-separated mTLS receiver.
# Its exact component config is also validated by the pinned Collector binary
# in hosted CI; these assertions protect the security shape during local review.
collector="$reference_dir/observability/collector.yaml"
dashboard="$reference_dir/../../observability/grafana/spyglass-overview.json"
grep -Fq 'max_request_body_size: 1048576' "$collector"
grep -Fq 'client_ca_file: /var/run/secrets/spyglass/workload-client-ca/ca.crt' "$collector"
test "$(grep -Fc 'min_version: "1.3"' "$collector")" -eq 2
grep -Fq 'client_ca_file_reload: true' "$collector"
grep -Fq 'allowed_keys:' "$collector"
grep -Fq 'summary: silent' "$collector"
grep -Fq 'queue_size: 2048' "$collector"
grep -Fq 'level: warn' "$collector"
grep -Fq 'sha256:1f2c54a30e713fac6b3ae77a1ec84010c2007e29ced8ec666214fc2f6739c1cc' "$reference_dir/observability-gateway.yaml"
if grep -Eq '(^|[[:space:]])(debug|logging)(/[^:]+)?:' "$collector"; then
  echo "collector config contains a telemetry-content exporter" >&2
  exit 1
fi

jq -e '
  .uid == "spyglass-production-overview" and
  .editable == false and
  (.panels | length) >= 7 and
  ([.panels[].targets[].expr] | all(contains("spyglass.account.ref") | not))
' "$dashboard" >/dev/null

rules_dir="$(mktemp -d)"
rules_file="$rules_dir/spyglass.rules.yaml"
trap 'rm -f "$rendered"; rm -rf -- "$rules_dir"' EXIT
sed -n '/^spec:/,$p' "$reference_dir/observability/prometheus-rule.yaml" | tail -n +2 | sed 's/^  //' >"$rules_file"
cp "$reference_dir/../../observability/prometheus/spyglass.rules.test.yaml" "$rules_dir/spyglass.rules.test.yaml"
test "$(grep -c '^      - alert:' "$rules_file")" -eq 12
if command -v promtool >/dev/null 2>&1; then
  promtool check rules "$rules_file"
  promtool test rules "$rules_dir/spyglass.rules.test.yaml"
fi
