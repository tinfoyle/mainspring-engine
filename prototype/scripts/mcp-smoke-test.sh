#!/usr/bin/env bash

set -euo pipefail

base_url="${MAINSPRING_SMOKE_URL:-http://127.0.0.1:8088}"
tenant_host="${MAINSPRING_SMOKE_HOST:-demo.localhost}"
token="${MAINSPRING_MCP_TOKEN:-local-development-mcp-token-change-before-production}"

unauthorized_status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Host: $tenant_host" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' "$base_url/mcp")"
[[ "$unauthorized_status" == "401" ]]

initialize="$(curl --silent --show-error --fail \
  -H "Host: $tenant_host" -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"mainspring-smoke","version":"1"}}}' \
  "$base_url/mcp")"
grep -q '"name":"mainspring"' <<<"$initialize"

tools="$(curl --silent --show-error --fail \
  -H "Host: $tenant_host" -H "Authorization: Bearer $token" -H 'MCP-Protocol-Version: 2025-06-18' \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' "$base_url/mcp")"
for tool in mainspring_list_agents mainspring_search_documents mainspring_get_work_item \
  mainspring_search_web mainspring_read_web_page mainspring_message_ticket mainspring_get_run mainspring_decide_approval; do
  grep -q "\"name\":\"$tool\"" <<<"$tools"
done
for tool in mainspring_query_finance mainspring_manage_finance; do
  grep -q "\"name\":\"$tool\"" <<<"$tools"
done

work_items="$(curl --silent --show-error --fail \
  -H "Host: $tenant_host" -H "Authorization: Bearer $token" -H 'MCP-Protocol-Version: 2025-06-18' \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mainspring_list_work_items","arguments":{"status":"active","kind":"all"}}}' \
  "$base_url/mcp")"
grep -q '"work_items"' <<<"$work_items"
grep -q '"summary"' <<<"$work_items"

printf 'Mainspring MCP smoke test passed: bearer auth, protocol negotiation, tool discovery, and tenant work-queue access.\n'
