#!/usr/bin/env bash
set -euo pipefail

stack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
env_file="${1:?usage: verify-stage.sh /absolute/path/to/stage.env}"
test "${env_file#/}" != "$env_file" || { echo "stage env path must be absolute" >&2; exit 1; }
test -f "$env_file" || { echo "stage env file is missing" >&2; exit 1; }
case "$(stat -c %a "$env_file")" in 400|600) ;; *) echo "stage env must be mode 400 or 600" >&2; exit 1;; esac

value() { sed -n "s/^$1=//p" "$env_file"; }
digest='^ghcr\.io/tinfoyle/[a-z0-9._-]+@sha256:[0-9a-f]{64}$'
for name in SPYGLASS_APPLICATION_IMAGE SPYGLASS_WEBSITE_IMAGE; do
  candidate="$(value "$name")"
  [[ "$candidate" =~ $digest ]] || { echo "$name must be an exact tinfoyle GHCR digest" >&2; exit 1; }
done
if grep -Eq '(^|[=:])REPLACE([_A-Z0-9-]*)([[:space:]]|$)' "$env_file"; then
  echo "stage env still contains a REPLACE placeholder" >&2
  exit 1
fi
test "$(value SPYGLASS_PROCESS_ENV)" != development || { echo "stage cannot use development mode" >&2; exit 1; }
test "$(value SPYGLASS_STRIPE_MODE)" = test || { echo "stage must use Stripe test mode" >&2; exit 1; }

secret_dir="$(value SPYGLASS_STAGE_SECRETS_DIRECTORY)"
test "${secret_dir#/}" != "$secret_dir" || { echo "stage secrets directory must be absolute" >&2; exit 1; }
for workload in admission-api app-router app-api-a app-api-b; do
  for file in ca.crt tls.crt tls.key; do
    test -s "$secret_dir/workload/$workload/$file" || { echo "missing workload identity: $workload/$file" >&2; exit 1; }
  done
  case "$(stat -c %a "$secret_dir/workload/$workload/tls.key")" in 400|600) ;; *) echo "$workload private key must be mode 400 or 600" >&2; exit 1;; esac
done

network="$(value SPYGLASS_HOST_EDGE_NETWORK)"
docker network inspect "$network" >/dev/null
rendered="$(mktemp)"
trap 'rm -f "$rendered"' EXIT
docker compose --project-name spyglass-stage --env-file "$env_file" --file "$stack_dir/compose.yml" --file "$stack_dir/compose.stage.yml" config >"$rendered"
if grep -Eq 'published: (80|443)$|image: .+:latest([[:space:]]|$)' "$rendered"; then
  echo "stage render attempts to own public ports or uses a mutable image" >&2
  exit 1
fi
grep -Fq "name: $network" "$rendered"
echo "stage configuration verified"
