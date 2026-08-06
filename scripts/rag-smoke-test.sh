#!/usr/bin/env bash

set -euo pipefail

network="${MAINSPRING_DOCKER_NETWORK:-mainspring-dev_platform}"
tenant_id="${MAINSPRING_SMOKE_TENANT_ID:-00000000-0000-0000-0000-000000000001}"
token="${MAINSPRING_SMOKE_RAG_TOKEN:-local-development-rag-secret-change-before-production}"
curl_image="${MAINSPRING_CURL_IMAGE:-curlimages/curl:latest}"

rag_curl() {
  docker run --rm --network "$network" "$curl_image" --silent --show-error --fail-with-body \
    --retry 20 --retry-delay 1 --retry-connrefused --retry-all-errors \
    -H "Authorization: Bearer $token" \
    -H "X-Mainspring-Tenant-ID: $tenant_id" \
    "$@"
}

ingested="$(rag_curl -H 'Content-Type: application/json' \
  --data '{"name":"Demo operations notes","media_type":"text/plain","content":"Completed plumbing jobs must be invoiced within two business days. The dispatcher reviews tomorrow schedule conflicts before 4 PM."}' \
  http://rag-demo:8083/documents/text)"
grep -q '"chunk_count":1' <<<"$ingested"
document_id="$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' <<<"$ingested")"
[[ -n "$document_id" ]]

listed="$(rag_curl http://rag-demo:8083/documents)"
grep -q '"name":"Demo operations notes"' <<<"$listed"

viewed="$(rag_curl "http://rag-demo:8083/documents/$document_id")"
grep -q 'Completed plumbing jobs must be invoiced' <<<"$viewed"

results="$(rag_curl 'http://rag-demo:8083/search?q=business%20days')"
grep -q '"document_name":"Demo operations notes"' <<<"$results"

printf 'Mainspring RAG smoke test passed: idempotent ingestion, listing, viewing, and tenant-scoped retrieval.\n'
