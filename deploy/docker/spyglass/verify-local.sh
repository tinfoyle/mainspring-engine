#!/usr/bin/env bash
set -euo pipefail

edge_port="${SPYGLASS_LOCAL_EDGE_PORT:-8088}"
edge_origin="http://127.0.0.1:${edge_port}"

curl --fail --silent --show-error --header "Host: app.infiniteocean.localhost" \
  "${edge_origin}/health/ready" >/tmp/spyglass-local-app-ready.json
grep -q '"status":"ready"' /tmp/spyglass-local-app-ready.json

curl --fail --silent --show-error --dump-header /tmp/spyglass-local-website-headers.txt \
  --header "Host: web.infiniteocean.localhost" "${edge_origin}/" >/tmp/spyglass-local-website.html
grep -qi '^content-security-policy:' /tmp/spyglass-local-website-headers.txt
grep -q 'Infinite Ocean: Spyglass' /tmp/spyglass-local-website.html

curl --fail --silent --show-error --header "Host: web.infiniteocean.localhost" \
  "${edge_origin}/signup" >/tmp/spyglass-local-signup.html
grep -q "http://app.infiniteocean.localhost:${edge_port}/signup" /tmp/spyglass-local-signup.html

curl --fail --silent --show-error --header "Host: web.infiniteocean.localhost" \
  "${edge_origin}/api/catalog" >/tmp/spyglass-local-catalog.json
jq -e '.version > 0 and (.packages | length) > 0' /tmp/spyglass-local-catalog.json >/dev/null

printf '%s\n' "local Docker smoke verification passed"
