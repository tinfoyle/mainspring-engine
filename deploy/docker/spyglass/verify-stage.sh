#!/usr/bin/env bash
set -euo pipefail

stack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd "$stack_dir/../../.." && pwd)"
release_file="${1:?usage: verify-stage.sh /absolute/path/to/release.env /absolute/path/to/stage.env}"
env_file="${2:?usage: verify-stage.sh /absolute/path/to/release.env /absolute/path/to/stage.env}"
test "${release_file#/}" != "$release_file" || { echo "release file path must be absolute" >&2; exit 1; }
test "${env_file#/}" != "$env_file" || { echo "stage env path must be absolute" >&2; exit 1; }
test -f "$release_file" || { echo "release file is missing" >&2; exit 1; }
test -f "$env_file" || { echo "stage env file is missing" >&2; exit 1; }
case "$release_file" in "$repository_root"/deploy/releases/*.env) ;; *) echo "release file must come from deploy/releases in this checkout" >&2; exit 1;; esac
git -C "$repository_root" ls-files --error-unmatch "${release_file#"$repository_root/"}" >/dev/null
case "$(stat -c %a "$env_file")" in 400|600) ;; *) echo "stage env must be mode 400 or 600" >&2; exit 1;; esac

value() {
  local count
  count="$(grep -c "^$1=" "$env_file" || true)"
  test "$count" = 1 || { echo "$1 must occur exactly once in stage env" >&2; exit 1; }
  sed -n "s/^$1=//p" "$env_file"
}
release_value() {
  local count
  count="$(grep -c "^$1=" "$release_file" || true)"
  test "$count" = 1 || { echo "$1 must occur exactly once in release env" >&2; exit 1; }
  sed -n "s/^$1=//p" "$release_file"
}
digest='^ghcr\.io/tinfoyle/[a-z0-9._-]+@sha256:[0-9a-f]{64}$'
for name in SPYGLASS_APPLICATION_IMAGE SPYGLASS_WEBSITE_IMAGE; do
  candidate="$(release_value "$name")"
  [[ "$candidate" =~ $digest ]] || { echo "$name must be an exact tinfoyle GHCR digest" >&2; exit 1; }
  ! grep -q "^$name=" "$env_file" || { echo "$name must come only from the reviewed release file" >&2; exit 1; }
done
[[ "$(release_value SPYGLASS_RELEASE_VERSION)" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || { echo "release version is invalid" >&2; exit 1; }
[[ "$(release_value SPYGLASS_RELEASE_REVISION)" =~ ^[0-9a-f]{40}$ ]] || { echo "release revision is invalid" >&2; exit 1; }
if grep -Eq '(^|[=:])REPLACE([_A-Z0-9-]*)([[:space:]]|$)' "$release_file" "$env_file"; then
  echo "release or stage env still contains a REPLACE placeholder" >&2
  exit 1
fi
test "$(value SPYGLASS_PROCESS_ENV)" != development || { echo "stage cannot use development mode" >&2; exit 1; }
test "$(value SPYGLASS_ENVIRONMENT)" = stage || { echo "stage environment must be stage" >&2; exit 1; }
test "$(value SPYGLASS_STRIPE_MODE)" = test || { echo "stage must use Stripe test mode" >&2; exit 1; }
[[ "$(value SPYGLASS_STRIPE_SECRET_KEY)" =~ ^sk_test_ ]] || { echo "stage Stripe key must be a test key" >&2; exit 1; }

secret_dir="$(value SPYGLASS_STAGE_SECRETS_DIRECTORY)"
secrets_gid="$(value SPYGLASS_STAGE_SECRETS_GID)"
test "${secret_dir#/}" != "$secret_dir" || { echo "stage secrets directory must be absolute" >&2; exit 1; }
[[ "$secrets_gid" =~ ^[0-9]+$ ]] || { echo "stage secrets group must be numeric" >&2; exit 1; }
test -s "$secret_dir/workload-ca/ca.crt" || { echo "stage workload CA is missing" >&2; exit 1; }
test ! -e "$secret_dir/workload-ca/ca.key" || { echo "stage workload CA private key must not remain in deployable secrets" >&2; exit 1; }

declare -A dns uri usage
dns[admission-api]=admission-api
dns[app-router]=''
dns[mcp-gateway]=''
dns[app-api-a]=app-api-a
dns[app-api-b]=app-api-b
dns[agent-dispatch-worker-a]=''
dns[agent-dispatch-worker-b]=''
dns[schedule-execution-worker-a]=''
dns[schedule-execution-worker-b]=''
dns[tool-router]=tool-router
dns[runner-controller-a]=''
dns[runner-controller-b]=''
dns[runner-broker-a]=runner-broker-a
dns[runner-broker-b]=runner-broker-b
dns[docker-runner-launcher-a]=docker-runner-launcher-a
dns[docker-runner-launcher-b]=docker-runner-launcher-b
dns[model-gateway]=model-gateway
uri[admission-api]='spiffe://infiniteocean.net/spyglass/workloads/admission-api'
uri[app-router]='spiffe://infiniteocean.net/spyglass/workloads/app-router'
uri[mcp-gateway]='spiffe://infiniteocean.net/spyglass/workloads/mcp-gateway'
uri[app-api-a]='spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/app-api'
uri[app-api-b]='spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/app-api'
uri[agent-dispatch-worker-a]='spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/agent-dispatch-worker'
uri[agent-dispatch-worker-b]='spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/agent-dispatch-worker'
uri[schedule-execution-worker-a]='spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/schedule-execution-worker'
uri[schedule-execution-worker-b]='spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/schedule-execution-worker'
uri[tool-router]='spiffe://infiniteocean.net/spyglass/workloads/app-router'
uri[runner-controller-a]='spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/runner-controller'
uri[runner-controller-b]='spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/runner-controller'
uri[runner-broker-a]='spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/runner-broker'
uri[runner-broker-b]='spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/runner-broker'
uri[docker-runner-launcher-a]='spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/docker-runner-launcher'
uri[docker-runner-launcher-b]='spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/docker-runner-launcher'
uri[model-gateway]='spiffe://infiniteocean.net/spyglass/workloads/model-gateway'
usage[admission-api]=server
usage[app-router]=client
usage[mcp-gateway]=client
usage[app-api-a]=both
usage[app-api-b]=both
usage[agent-dispatch-worker-a]=client
usage[agent-dispatch-worker-b]=client
usage[schedule-execution-worker-a]=client
usage[schedule-execution-worker-b]=client
usage[tool-router]=both
usage[runner-controller-a]=client
usage[runner-controller-b]=client
usage[runner-broker-a]=both
usage[runner-broker-b]=both
usage[docker-runner-launcher-a]=server
usage[docker-runner-launcher-b]=server
usage[model-gateway]=server

for workload in admission-api app-router mcp-gateway app-api-a app-api-b agent-dispatch-worker-a agent-dispatch-worker-b schedule-execution-worker-a schedule-execution-worker-b tool-router runner-controller-a runner-controller-b runner-broker-a runner-broker-b docker-runner-launcher-a docker-runner-launcher-b model-gateway; do
  for file in ca.crt tls.crt tls.key; do
    test -s "$secret_dir/workload/$workload/$file" || { echo "missing workload identity: $workload/$file" >&2; exit 1; }
  done
  test "$(stat -c %a "$secret_dir/workload/$workload")" = 750 || { echo "$workload identity directory must be mode 750" >&2; exit 1; }
  test "$(stat -c %a "$secret_dir/workload/$workload/tls.key")" = 640 || { echo "$workload private key must be mode 640" >&2; exit 1; }
  test "$(stat -c %g "$secret_dir/workload/$workload/tls.key")" = "$secrets_gid" || { echo "$workload private key group is incorrect" >&2; exit 1; }
  cmp -s "$secret_dir/workload-ca/ca.crt" "$secret_dir/workload/$workload/ca.crt" || { echo "$workload trust bundle does not match the stage CA" >&2; exit 1; }
  openssl verify -CAfile "$secret_dir/workload/$workload/ca.crt" "$secret_dir/workload/$workload/tls.crt" >/dev/null || { echo "$workload certificate chain is invalid" >&2; exit 1; }
  openssl x509 -checkend 604800 -noout -in "$secret_dir/workload/$workload/tls.crt" >/dev/null || { echo "$workload certificate expires within seven days" >&2; exit 1; }
  cert_key="$(openssl x509 -in "$secret_dir/workload/$workload/tls.crt" -pubkey -noout | openssl pkey -pubin -outform DER 2>/dev/null | sha256sum | cut -d' ' -f1)"
  private_key="$(openssl pkey -in "$secret_dir/workload/$workload/tls.key" -pubout -outform DER 2>/dev/null | sha256sum | cut -d' ' -f1)"
  test "$cert_key" = "$private_key" || { echo "$workload certificate and private key do not match" >&2; exit 1; }
  certificate="$(openssl x509 -in "$secret_dir/workload/$workload/tls.crt" -noout -text)"
  grep -Fq "URI:${uri[$workload]}" <<<"$certificate" || { echo "$workload certificate has the wrong SPIFFE identity" >&2; exit 1; }
  if test -n "${dns[$workload]}"; then
    grep -Fq "DNS:${dns[$workload]}" <<<"$certificate" || { echo "$workload certificate has the wrong DNS identity" >&2; exit 1; }
    openssl verify -CAfile "$secret_dir/workload/$workload/ca.crt" -verify_hostname "${dns[$workload]}" "$secret_dir/workload/$workload/tls.crt" >/dev/null || { echo "$workload DNS verification failed" >&2; exit 1; }
  fi
  case "${usage[$workload]}" in
    server) grep -Fq 'TLS Web Server Authentication' <<<"$certificate" && ! grep -Fq 'TLS Web Client Authentication' <<<"$certificate" ;;
    client) grep -Fq 'TLS Web Client Authentication' <<<"$certificate" && ! grep -Fq 'TLS Web Server Authentication' <<<"$certificate" ;;
    both) grep -Fq 'TLS Web Server Authentication' <<<"$certificate" && grep -Fq 'TLS Web Client Authentication' <<<"$certificate" ;;
  esac || { echo "$workload certificate has the wrong extended key usage" >&2; exit 1; }
done
for identity_dir in runner-identities-a runner-identities-b; do
  test -d "$secret_dir/$identity_dir" || { echo "missing runner identity directory: $identity_dir" >&2; exit 1; }
  test "$(stat -c %a "$secret_dir/$identity_dir")" = 770 || { echo "$identity_dir must be mode 770" >&2; exit 1; }
  test "$(stat -c %g "$secret_dir/$identity_dir")" = "$secrets_gid" || { echo "$identity_dir group is incorrect" >&2; exit 1; }
done

network="$(value SPYGLASS_HOST_EDGE_NETWORK)"
docker network inspect "$network" >/dev/null
rendered="$(mktemp)"
trap 'rm -f "$rendered"' EXIT
docker compose --project-name spyglass-stage --env-file "$release_file" --env-file "$env_file" --file "$stack_dir/compose.yml" --file "$stack_dir/compose.stage.yml" --file "$stack_dir/compose.stage-runner.yml" --profile knowledge-processing config --format json >"$rendered"
python3 - "$rendered" "$network" "$(release_value SPYGLASS_APPLICATION_IMAGE)" "$(release_value SPYGLASS_WEBSITE_IMAGE)" "$secrets_gid" <<'PY'
import json
import sys

path, edge_network, application_image, website_image, secrets_gid = sys.argv[1:]
with open(path, encoding="utf-8") as source:
    config = json.load(source)
services = config["services"]
runner_services = {
    "tool-router", "runner-controller-a", "runner-controller-b",
    "runner-broker-a", "runner-broker-b", "docker-runner-launcher-a",
    "docker-runner-launcher-b", "model-gateway", "knowledge-document-worker-a",
    "knowledge-document-worker-b", "baseline-maintenance-worker-a",
    "baseline-maintenance-worker-b",
}
application_services = runner_services | {
    "account-api", "account-lifecycle-worker", "admission-api", "app-api-a", "app-api-b",
    "app-router", "billing-worker", "cell-a-migrate", "cell-b-migrate", "entitlement-worker",
    "global-migrate", "identity-maintenance-worker", "mcp-gateway", "notification-worker",
    "work-reconciler-a", "work-reconciler-b", "route-receipt-worker-a", "route-receipt-worker-b",
    "agent-dispatch-worker-a", "agent-dispatch-worker-b", "schedule-execution-worker-a",
    "schedule-execution-worker-b", "agent-projection-worker-a", "agent-projection-worker-b",
}
required = runner_services | {"malware-scanner", "document-extractor"}
missing = sorted(required - services.keys())
if missing:
    raise SystemExit(f"stage runner topology is incomplete: {', '.join(missing)}")
for name, service in services.items():
    image = service.get("image", "")
    if "@sha256:" not in image:
        raise SystemExit(f"{name} does not use an immutable image digest")
    for port in service.get("ports", []):
        if str(port.get("published", "")) in {"80", "443"}:
            raise SystemExit(f"{name} attempts to publish public port {port['published']}")
for name in ("global-db", "cell-a-db", "cell-b-db"):
    health_test = " ".join(str(part) for part in services[name].get("healthcheck", {}).get("test", []))
    if "pg_isready -h 127.0.0.1" not in health_test:
        raise SystemExit(f"{name} healthcheck can pass against the temporary initdb server")
socket_holders = set()
for name, service in services.items():
    for volume in service.get("volumes", []):
        if volume.get("source") == "/var/run/docker.sock" or volume.get("target") == "/var/run/docker.sock":
            socket_holders.add(name)
if socket_holders != {"docker-runner-launcher-a", "docker-runner-launcher-b"}:
    raise SystemExit(f"unexpected Docker socket holders: {sorted(socket_holders)}")
if services["website"]["image"] != website_image:
    raise SystemExit("website image does not match the release file")
if services["mcp-gateway"].get("scale") != 2:
    raise SystemExit("stage MCP gateway must run exactly two replicas")
for name in application_services:
    if services[name]["image"] != application_image:
        raise SystemExit(f"{name} image does not match the application release digest")
for name in ("runner-broker-a", "runner-broker-b"):
    if services[name]["environment"].get("SPYGLASS_TOOL_ROUTER_ORIGIN") != "https://tool-router:8443":
        raise SystemExit(f"{name} does not use the workload-mTLS tool router")
provider_members = {
    name for name, service in services.items()
    if "provider-egress" in service.get("networks", {})
}
if provider_members != {"account-api", "billing-worker", "notification-worker", "model-gateway", "runner-broker-a", "runner-broker-b"}:
    raise SystemExit(f"provider egress membership is not least-authority: {sorted(provider_members)}")
for suffix in ("a", "b"):
    network_name = f"runner-{suffix}-egress"
    if not config["networks"][network_name].get("internal"):
        raise SystemExit(f"{network_name} must be internal")
    members = {
        name for name, service in services.items()
        if network_name in service.get("networks", {})
    }
    expected = {f"runner-broker-{suffix}", f"docker-runner-launcher-{suffix}"}
    if members != expected:
        raise SystemExit(f"{network_name} membership is invalid: {sorted(members)}")
secret_consumers = {
    "admission-api", "app-router", "mcp-gateway", "app-api-a", "app-api-b", "tool-router",
    "runner-controller-a", "runner-controller-b", "runner-broker-a", "runner-broker-b",
    "docker-runner-launcher-a", "docker-runner-launcher-b", "model-gateway",
}
for name in secret_consumers:
    if secrets_gid not in {str(group) for group in services[name].get("group_add", [])}:
        raise SystemExit(f"{name} does not receive the stage secrets group")
for name in ("tool-router", "runner-controller-a", "runner-controller-b", "runner-broker-a", "runner-broker-b"):
    environment = services[name].get("environment", {})
    if "SPYGLASS_ERASURE_CHECKPOINT_SEQUENCE" not in environment or "SPYGLASS_ERASURE_CHECKPOINT_ROOT" not in environment:
        raise SystemExit(f"{name} does not carry its database restore checkpoint")
for name in ("baseline-maintenance-worker-a", "baseline-maintenance-worker-b"):
    environment = services[name].get("environment", {})
    for prefix in ("SPYGLASS_GLOBAL_ERASURE_", "SPYGLASS_CELL_ERASURE_"):
        if prefix + "CHECKPOINT_SEQUENCE" not in environment or prefix + "CHECKPOINT_ROOT" not in environment:
            raise SystemExit(f"{name} does not carry both database restore checkpoints")
if config["networks"]["host-edge"].get("name") != edge_network:
    raise SystemExit("stage host edge network does not match the reviewed secret file")
PY
echo "stage configuration verified"
