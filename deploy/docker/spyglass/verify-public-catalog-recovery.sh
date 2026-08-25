#!/usr/bin/env bash
set -euo pipefail

tls_port="${SPYGLASS_LOCAL_TLS_PORT:-8444}"
public_origin="https://web.infiniteocean.localhost:${tls_port}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml")
account_api_container="$("${compose[@]}" ps --quiet account-api)"
website_container="$("${compose[@]}" ps --quiet website)"
test -n "$account_api_container"
test -n "$website_container"
test "$(wc -w <<<"$account_api_container")" -eq 1
test "$(wc -w <<<"$website_container")" -eq 1
test "$(docker inspect --format '{{.State.Running}}' "$account_api_container")" = "true"
test "$(docker inspect --format '{{.State.Running}}' "$website_container")" = "true"

evidence_dir="$(mktemp -d /tmp/spyglass-catalog-recovery.XXXXXX)"
case "$evidence_dir" in
  /tmp/spyglass-catalog-recovery.*) ;;
  *) printf 'unexpected evidence directory %s\n' "$evidence_dir" >&2; exit 1 ;;
esac
root_ca="$evidence_dir/caddy-root.crt"
paused=0

cleanup() {
  if [[ "$paused" = "1" ]]; then
    docker unpause "$account_api_container" >/dev/null 2>&1 || true
  fi
  rm -f -- "$evidence_dir"/*
  rmdir -- "$evidence_dir" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

"${compose[@]}" cp edge:/data/caddy/pki/authorities/local/root.crt "$root_ca" >/dev/null
curl_common=(--silent --show-error --cacert "$root_ca" --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" --max-time 8)

curl --fail "${curl_common[@]}" --dump-header "$evidence_dir/fresh.headers" \
  "$public_origin/catalog.json" >"$evidence_dir/fresh.json"
grep -qi '^x-spyglass-catalog-state: fresh' "$evidence_dir/fresh.headers"
grep -qi '^x-spyglass-catalog-age: 0' "$evidence_dir/fresh.headers"
grep -qi '^cache-control: public, max-age=60, stale-if-error=300' "$evidence_dir/fresh.headers"
jq -e '.version > 0 and (.offers | length) > 0' "$evidence_dir/fresh.json" >/dev/null

paused=1
docker pause "$account_api_container" >/dev/null
curl --fail "${curl_common[@]}" --dump-header "$evidence_dir/stale.headers" \
  "$public_origin/catalog.json" >"$evidence_dir/stale.json"
grep -qi '^x-spyglass-catalog-state: stale' "$evidence_dir/stale.headers"
grep -qi '^cache-control: public, max-age=15, must-revalidate' "$evidence_dir/stale.headers"
grep -qi '^warning: 110 - "Response is stale"' "$evidence_dir/stale.headers"
cmp --silent "$evidence_dir/fresh.json" "$evidence_dir/stale.json"
curl --fail "${curl_common[@]}" "$public_origin/pricing" >"$evidence_dir/stale-pricing.html"
grep -Fq 'Last verified Catalog' "$evidence_dir/stale-pricing.html"
grep -Fq 'Current publication is temporarily delayed' "$evidence_dir/stale-pricing.html"
curl --fail "${curl_common[@]}" "$public_origin/features/work" >"$evidence_dir/stale-feature.html"
grep -Fq 'Availability reflects the last verified Catalog version' "$evidence_dir/stale-feature.html"

docker restart "$website_container" >/dev/null
website_ready=0
for _ in $(seq 1 40); do
  if curl --fail "${curl_common[@]}" "$public_origin/" >/dev/null 2>&1; then
    website_ready=1
    break
  fi
  sleep 0.25
done
test "$website_ready" = "1"

unavailable_status="$(curl "${curl_common[@]}" --dump-header "$evidence_dir/unavailable.headers" \
  --output "$evidence_dir/unavailable.json" --write-out '%{http_code}' "$public_origin/catalog.json")"
test "$unavailable_status" = "503"
grep -qi '^cache-control: no-store' "$evidence_dir/unavailable.headers"
grep -qi '^retry-after: 30' "$evidence_dir/unavailable.headers"
grep -qi '^x-spyglass-catalog-state: unavailable' "$evidence_dir/unavailable.headers"
curl --fail "${curl_common[@]}" "$public_origin/pricing" >"$evidence_dir/unavailable-pricing.html"
grep -Fq 'Paid offers are temporarily hidden' "$evidence_dir/unavailable-pricing.html"
grep -Fq 'We will not show or submit an unverified price.' "$evidence_dir/unavailable-pricing.html"
curl --fail "${curl_common[@]}" "$public_origin/features/work" >"$evidence_dir/unavailable-feature.html"
grep -Fq 'Current plan availability is temporarily unavailable.' "$evidence_dir/unavailable-feature.html"

docker unpause "$account_api_container" >/dev/null
paused=0
recovered=0
for _ in $(seq 1 40); do
  if curl --fail "${curl_common[@]}" --dump-header "$evidence_dir/recovered.headers" \
    "$public_origin/catalog.json" >"$evidence_dir/recovered.json" 2>/dev/null && \
    grep -qi '^x-spyglass-catalog-state: fresh' "$evidence_dir/recovered.headers"; then
    recovered=1
    break
  fi
  sleep 0.25
done
test "$recovered" = "1"
cmp --silent "$evidence_dir/fresh.json" "$evidence_dir/recovered.json"

printf '%s\n' "public Catalog fresh/stale/unavailable/recovery verification passed"
