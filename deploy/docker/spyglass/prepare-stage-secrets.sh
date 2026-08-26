#!/usr/bin/env bash
set -euo pipefail

provider_file="${1:?usage: prepare-stage-secrets.sh /absolute/path/to/stage.providers.env /absolute/path/to/secrets [edge-network] [previous-stage.env]}"
target="${2:?usage: prepare-stage-secrets.sh /absolute/path/to/stage.providers.env /absolute/path/to/secrets [edge-network] [previous-stage.env]}"
edge_network="${3:-infiniteocean_public}"
previous_env="${4:-}"

fail() {
  echo "$*" >&2
  exit 1
}

[[ "$provider_file" = /* ]] || fail "provider file path must be absolute"
[[ "$target" = /* && "$target" != / ]] || fail "secrets directory must be an absolute bounded path"
[[ "$edge_network" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$ ]] || fail "edge network name is invalid"
test -f "$provider_file" || fail "provider file is missing"
case "$(stat -c %a "$provider_file")" in 400|600) ;; *) fail "provider file must be mode 400 or 600";; esac
secrets_gid="$(stat -c %g "$provider_file")"
if [[ -n "$previous_env" ]]; then
  [[ "$previous_env" = /* ]] || fail "previous stage environment path must be absolute"
  test -f "$previous_env" || fail "previous stage environment is missing"
  case "$(stat -c %a "$previous_env")" in 400|600) ;; *) fail "previous stage environment must be mode 400 or 600";; esac
fi
command -v openssl >/dev/null || fail "openssl is required"

provider_value() {
  local name="$1" count candidate
  count="$(grep -c "^${name}=" "$provider_file" || true)"
  [[ "$count" = 1 ]] || fail "$name must occur exactly once in the provider file"
  candidate="$(sed -n "s/^${name}=//p" "$provider_file")"
  [[ -n "$candidate" && "$candidate" != *REPLACE* ]] || fail "$name is missing or still contains REPLACE"
  printf '%s' "$candidate"
}

stripe_webhook_secret="$(provider_value SPYGLASS_STRIPE_WEBHOOK_SECRET)"
stripe_secret_key="$(provider_value SPYGLASS_STRIPE_SECRET_KEY)"
smtp_address="$(provider_value SPYGLASS_SMTP_ADDRESS)"
smtp_server_name="$(provider_value SPYGLASS_SMTP_SERVER_NAME)"
smtp_username="$(provider_value SPYGLASS_SMTP_USERNAME)"
smtp_password="$(provider_value SPYGLASS_SMTP_PASSWORD)"
smtp_from_address="$(provider_value SPYGLASS_SMTP_FROM_ADDRESS)"
smtp_from_name="$(provider_value SPYGLASS_SMTP_FROM_NAME)"
openai_api_key="$(provider_value SPYGLASS_OPENAI_API_KEY)"
openai_origin="$(provider_value SPYGLASS_OPENAI_ORIGIN)"
openai_pricing="$(provider_value SPYGLASS_OPENAI_MODEL_PRICING_JSON)"
agent_execution_policies="$(provider_value SPYGLASS_AGENT_EXECUTION_POLICIES_JSON)"

[[ "$stripe_webhook_secret" =~ ^whsec_[A-Za-z0-9_-]+$ ]] || fail "Stripe webhook secret must be a test endpoint whsec_ value"
[[ "$stripe_secret_key" =~ ^sk_test_[A-Za-z0-9_-]+$ ]] || fail "Stripe key must be an sk_test_ value"
[[ "$smtp_address" =~ ^[A-Za-z0-9.-]+:[0-9]{1,5}$ ]] || fail "SMTP address must be a host and port"
[[ "$smtp_server_name" =~ ^[A-Za-z0-9.-]+$ ]] || fail "SMTP server name is invalid"
[[ "$smtp_username" =~ ^[A-Za-z0-9._~!@%+=,:/-]+$ ]] || fail "SMTP username contains unsupported dotenv characters"
[[ "$smtp_password" =~ ^[A-Za-z0-9._~!@%+=,:/-]+$ ]] || fail "SMTP password contains unsupported dotenv characters"
[[ "$smtp_from_address" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+$ ]] || fail "SMTP from address is invalid"
[[ "$smtp_from_name" =~ ^[A-Za-z0-9._[:space:]-]+$ ]] || fail "SMTP from name contains unsupported dotenv characters"
[[ "$openai_api_key" =~ ^[A-Za-z0-9_-]+$ ]] || fail "OpenAI key contains unsupported dotenv characters"
[[ "$openai_origin" =~ ^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$ ]] || fail "OpenAI origin must be an exact HTTPS origin"
[[ "$openai_pricing" =~ ^\{[A-Za-z0-9._:\",{}-]+\}$ ]] || fail "OpenAI model pricing must be compact injection-safe JSON"
[[ "$agent_execution_policies" =~ ^\{[A-Za-z0-9._:\",{}\[\]-]+\}$ ]] || fail "Agent execution policies must be compact injection-safe JSON"

if [[ -e "$target" ]]; then
  [[ -d "$target" ]] || fail "secrets target exists and is not a directory"
  [[ -z "$(find "$target" -mindepth 1 -maxdepth 1 -print -quit)" ]] || fail "secrets target must be absent or empty"
fi
parent="$(dirname "$target")"
mkdir -p "$parent"
work="$(mktemp -d "${target}.initializing.XXXXXX")"
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT
umask 077

random_password() { openssl rand -hex 32; }
random_key() { openssl rand -base64 32 | tr -d '\n'; }
random_token() { openssl rand -hex 32; }

secret_value() {
  local name="$1" kind="$2" count candidate
  if [[ -n "$previous_env" ]]; then
    count="$(grep -c "^${name}=" "$previous_env" || true)"
    [[ "$count" -le 1 ]] || fail "$name occurs more than once in the previous stage environment"
    if [[ "$count" = 1 ]]; then
      candidate="$(sed -n "s/^${name}=//p" "$previous_env")"
      [[ -n "$candidate" && "$candidate" != *REPLACE* ]] || fail "$name is invalid in the previous stage environment"
      printf '%s' "$candidate"
      return
    fi
  fi
  case "$kind" in
    password|token) random_password ;;
    key) random_key ;;
    versioned_key) printf '1=%s' "$(random_key)" ;;
    access) openssl rand -hex 10 ;;
    kms) printf 'spyglass:%s' "$(random_key)" ;;
    *) fail "unsupported generated secret kind for $name" ;;
  esac
}

global_database_password="$(secret_value SPYGLASS_GLOBAL_DATABASE_PASSWORD password)"
cell_a_database_password="$(secret_value SPYGLASS_CELL_A_DATABASE_PASSWORD password)"
cell_b_database_password="$(secret_value SPYGLASS_CELL_B_DATABASE_PASSWORD password)"
account_api_password="$(secret_value SPYGLASS_ACCOUNT_API_DATABASE_PASSWORD password)"
app_router_password="$(secret_value SPYGLASS_APP_ROUTER_DATABASE_PASSWORD password)"
mcp_gateway_password="$(secret_value SPYGLASS_MCP_GATEWAY_DATABASE_PASSWORD password)"
admission_password="$(secret_value SPYGLASS_ADMISSION_DATABASE_PASSWORD password)"
billing_password="$(secret_value SPYGLASS_BILLING_WORKER_DATABASE_PASSWORD password)"
notification_password="$(secret_value SPYGLASS_NOTIFICATION_WORKER_DATABASE_PASSWORD password)"
entitlement_password="$(secret_value SPYGLASS_ENTITLEMENT_WORKER_DATABASE_PASSWORD password)"
account_lifecycle_password="$(secret_value SPYGLASS_ACCOUNT_LIFECYCLE_WORKER_DATABASE_PASSWORD password)"
account_export_build_password="$(secret_value SPYGLASS_ACCOUNT_EXPORT_BUILD_WORKER_DATABASE_PASSWORD password)"
account_export_expiry_password="$(secret_value SPYGLASS_ACCOUNT_EXPORT_EXPIRY_WORKER_DATABASE_PASSWORD password)"
identity_maintenance_password="$(secret_value SPYGLASS_IDENTITY_MAINTENANCE_WORKER_DATABASE_PASSWORD password)"
work_reconciler_password="$(secret_value SPYGLASS_WORK_RECONCILER_DATABASE_PASSWORD password)"
app_api_password="$(secret_value SPYGLASS_APP_API_DATABASE_PASSWORD password)"
route_receipt_password="$(secret_value SPYGLASS_ROUTE_RECEIPT_WORKER_DATABASE_PASSWORD password)"
agent_dispatch_password="$(secret_value SPYGLASS_AGENT_DISPATCH_WORKER_DATABASE_PASSWORD password)"
schedule_execution_password="$(secret_value SPYGLASS_SCHEDULE_EXECUTION_WORKER_DATABASE_PASSWORD password)"
agent_projection_password="$(secret_value SPYGLASS_AGENT_PROJECTION_WORKER_DATABASE_PASSWORD password)"
knowledge_document_password="$(secret_value SPYGLASS_KNOWLEDGE_DOCUMENT_WORKER_DATABASE_PASSWORD password)"
baseline_maintenance_password="$(secret_value SPYGLASS_BASELINE_MAINTENANCE_WORKER_DATABASE_PASSWORD password)"
integration_connector_password="$(secret_value SPYGLASS_INTEGRATION_CONNECTOR_WORKER_DATABASE_PASSWORD password)"
prototype_migration_password="$(secret_value SPYGLASS_PROTOTYPE_MIGRATION_DATABASE_PASSWORD password)"
runner_controller_a_password="$(secret_value SPYGLASS_CELL_A_RUNNER_CONTROLLER_DATABASE_PASSWORD password)"
runner_controller_b_password="$(secret_value SPYGLASS_CELL_B_RUNNER_CONTROLLER_DATABASE_PASSWORD password)"
runner_broker_a_password="$(secret_value SPYGLASS_CELL_A_RUNNER_BROKER_DATABASE_PASSWORD password)"
runner_broker_b_password="$(secret_value SPYGLASS_CELL_B_RUNNER_BROKER_DATABASE_PASSWORD password)"
object_store_access_key="$(secret_value SPYGLASS_OBJECT_STORE_ACCESS_KEY access)"
object_store_secret_key="$(secret_value SPYGLASS_OBJECT_STORE_SECRET_KEY password)"
object_store_app_access_key="$(secret_value SPYGLASS_OBJECT_STORE_APP_ACCESS_KEY access)"
object_store_app_secret_key="$(secret_value SPYGLASS_OBJECT_STORE_APP_SECRET_KEY password)"
object_store_worker_access_key="$(secret_value SPYGLASS_OBJECT_STORE_WORKER_ACCESS_KEY access)"
object_store_worker_secret_key="$(secret_value SPYGLASS_OBJECT_STORE_WORKER_SECRET_KEY password)"
object_store_connector_access_key="$(secret_value SPYGLASS_OBJECT_STORE_CONNECTOR_ACCESS_KEY access)"
object_store_connector_secret_key="$(secret_value SPYGLASS_OBJECT_STORE_CONNECTOR_SECRET_KEY password)"
object_store_migration_access_key="$(secret_value SPYGLASS_OBJECT_STORE_MIGRATION_ACCESS_KEY access)"
object_store_migration_secret_key="$(secret_value SPYGLASS_OBJECT_STORE_MIGRATION_SECRET_KEY password)"
account_export_source_object_store_access_key="$(secret_value SPYGLASS_ACCOUNT_EXPORT_SOURCE_OBJECT_STORE_ACCESS_KEY access)"
account_export_source_object_store_secret_key="$(secret_value SPYGLASS_ACCOUNT_EXPORT_SOURCE_OBJECT_STORE_SECRET_KEY password)"
account_export_object_store_build_access_key="$(secret_value SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUILD_ACCESS_KEY access)"
account_export_object_store_build_secret_key="$(secret_value SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUILD_SECRET_KEY password)"
account_export_object_store_expiry_access_key="$(secret_value SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_EXPIRY_ACCESS_KEY access)"
account_export_object_store_expiry_secret_key="$(secret_value SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_EXPIRY_SECRET_KEY password)"
account_export_object_store_download_access_key="$(secret_value SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_DOWNLOAD_ACCESS_KEY access)"
account_export_object_store_download_secret_key="$(secret_value SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_DOWNLOAD_SECRET_KEY password)"
account_export_download_keys="$(secret_value SPYGLASS_ACCOUNT_EXPORT_DOWNLOAD_KEYS versioned_key)"
object_store_kms_secret_key="$(secret_value SPYGLASS_OBJECT_STORE_KMS_SECRET_KEY kms)"
notification_key="$(secret_value SPYGLASS_NOTIFICATION_ENCRYPTION_KEY key)"
network_actor_key="$(secret_value SPYGLASS_NETWORK_ACTOR_KEY key)"
privacy_preference_key="$(secret_value SPYGLASS_PRIVACY_PREFERENCE_KEY key)"
passkey_keys="$(secret_value SPYGLASS_PASSKEY_ENCRYPTION_KEYS versioned_key)"
runner_a_keys="$(secret_value SPYGLASS_CELL_A_RUNNER_ENCRYPTION_KEYS versioned_key)"
runner_b_keys="$(secret_value SPYGLASS_CELL_B_RUNNER_ENCRYPTION_KEYS versioned_key)"
route_key="$(secret_value SPYGLASS_ROUTE_SIGNING_KEY key)"
tool_a_key="$(secret_value SPYGLASS_CELL_A_TOOL_CONTEXT_SIGNING_KEY key)"
tool_b_key="$(secret_value SPYGLASS_CELL_B_TOOL_CONTEXT_SIGNING_KEY key)"
launcher_controller_a_token="$(secret_value SPYGLASS_CELL_A_DOCKER_LAUNCHER_CONTROLLER_TOKEN token)"
launcher_broker_a_token="$(secret_value SPYGLASS_CELL_A_DOCKER_LAUNCHER_BROKER_TOKEN token)"
launcher_controller_b_token="$(secret_value SPYGLASS_CELL_B_DOCKER_LAUNCHER_CONTROLLER_TOKEN token)"
launcher_broker_b_token="$(secret_value SPYGLASS_CELL_B_DOCKER_LAUNCHER_BROKER_TOKEN token)"

cat >"$work/stage.env" <<EOF
SPYGLASS_ENVIRONMENT=stage
SPYGLASS_PROCESS_ENV=stage
SPYGLASS_PUBLIC_ORIGIN=https://stage.infiniteocean.net
SPYGLASS_APP_ORIGIN=https://app.stage.infiniteocean.net
SPYGLASS_MCP_RESOURCE_METADATA_URL=https://mcp.stage.infiniteocean.net/.well-known/oauth-protected-resource
SPYGLASS_MCP_RESOURCE_ORIGIN=https://mcp.stage.infiniteocean.net
SPYGLASS_MCP_AUTHORIZATION_SERVER=https://app.stage.infiniteocean.net
SPYGLASS_MCP_TRUSTED_ORIGINS=https://app.stage.infiniteocean.net
SPYGLASS_PASSKEY_RP_ID=app.stage.infiniteocean.net
SPYGLASS_CELL_A_ROUTE_ORIGIN=https://app-api-a:8443
SPYGLASS_CELL_B_ROUTE_ORIGIN=https://app-api-b:8443
SPYGLASS_WORK_ADMISSION_ORIGIN=https://admission-api:8443
SPYGLASS_HOST_EDGE_NETWORK=$edge_network
SPYGLASS_STAGE_SECRETS_DIRECTORY=$target
SPYGLASS_STAGE_SECRETS_GID=$secrets_gid
SPYGLASS_INTEGRATION_CREDENTIALS_DIRECTORY=/opt/spyglass-stage/integration-credentials
SPYGLASS_OBJECT_STORE_ENDPOINT=object-store:9000
SPYGLASS_OBJECT_STORE_BUCKET=spyglass-documents
SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET=spyglass-account-exports
SPYGLASS_OBJECT_STORE_ACCESS_KEY=$object_store_access_key
SPYGLASS_OBJECT_STORE_SECRET_KEY=$object_store_secret_key
SPYGLASS_OBJECT_STORE_APP_ACCESS_KEY=$object_store_app_access_key
SPYGLASS_OBJECT_STORE_APP_SECRET_KEY=$object_store_app_secret_key
SPYGLASS_OBJECT_STORE_WORKER_ACCESS_KEY=$object_store_worker_access_key
SPYGLASS_OBJECT_STORE_WORKER_SECRET_KEY=$object_store_worker_secret_key
SPYGLASS_OBJECT_STORE_CONNECTOR_ACCESS_KEY=$object_store_connector_access_key
SPYGLASS_OBJECT_STORE_CONNECTOR_SECRET_KEY=$object_store_connector_secret_key
SPYGLASS_OBJECT_STORE_MIGRATION_ACCESS_KEY=$object_store_migration_access_key
SPYGLASS_OBJECT_STORE_MIGRATION_SECRET_KEY=$object_store_migration_secret_key
SPYGLASS_ACCOUNT_EXPORT_SOURCE_OBJECT_STORE_ACCESS_KEY=$account_export_source_object_store_access_key
SPYGLASS_ACCOUNT_EXPORT_SOURCE_OBJECT_STORE_SECRET_KEY=$account_export_source_object_store_secret_key
SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUILD_ACCESS_KEY=$account_export_object_store_build_access_key
SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUILD_SECRET_KEY=$account_export_object_store_build_secret_key
SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_EXPIRY_ACCESS_KEY=$account_export_object_store_expiry_access_key
SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_EXPIRY_SECRET_KEY=$account_export_object_store_expiry_secret_key
SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_DOWNLOAD_ACCESS_KEY=$account_export_object_store_download_access_key
SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_DOWNLOAD_SECRET_KEY=$account_export_object_store_download_secret_key
SPYGLASS_ACCOUNT_EXPORT_DOWNLOAD_KEYS=$account_export_download_keys
SPYGLASS_ACCOUNT_EXPORT_DOWNLOAD_ACTIVE_KEY_ID=1
SPYGLASS_ACCOUNT_EXPORT_DOWNLOAD_CAPABILITY_LIFETIME=2m
SPYGLASS_OBJECT_STORE_KMS_SECRET_KEY=$object_store_kms_secret_key
SPYGLASS_OBJECT_STORE_SECURE=false
SPYGLASS_OBJECT_STORE_SERVER_SIDE_ENCRYPTION=true
SPYGLASS_CLAMAV_DISABLE_UPDATES=false
SPYGLASS_GLOBAL_DATABASE_PASSWORD=$global_database_password
SPYGLASS_CELL_A_DATABASE_PASSWORD=$cell_a_database_password
SPYGLASS_CELL_B_DATABASE_PASSWORD=$cell_b_database_password
SPYGLASS_ACCOUNT_API_DATABASE_PASSWORD=$account_api_password
SPYGLASS_APP_ROUTER_DATABASE_PASSWORD=$app_router_password
SPYGLASS_MCP_GATEWAY_DATABASE_PASSWORD=$mcp_gateway_password
SPYGLASS_ADMISSION_DATABASE_PASSWORD=$admission_password
SPYGLASS_BILLING_WORKER_DATABASE_PASSWORD=$billing_password
SPYGLASS_NOTIFICATION_WORKER_DATABASE_PASSWORD=$notification_password
SPYGLASS_ENTITLEMENT_WORKER_DATABASE_PASSWORD=$entitlement_password
SPYGLASS_ACCOUNT_LIFECYCLE_WORKER_DATABASE_PASSWORD=$account_lifecycle_password
SPYGLASS_ACCOUNT_EXPORT_BUILD_WORKER_DATABASE_PASSWORD=$account_export_build_password
SPYGLASS_ACCOUNT_EXPORT_EXPIRY_WORKER_DATABASE_PASSWORD=$account_export_expiry_password
SPYGLASS_IDENTITY_MAINTENANCE_WORKER_DATABASE_PASSWORD=$identity_maintenance_password
SPYGLASS_WORK_RECONCILER_DATABASE_PASSWORD=$work_reconciler_password
SPYGLASS_APP_API_DATABASE_PASSWORD=$app_api_password
SPYGLASS_ROUTE_RECEIPT_WORKER_DATABASE_PASSWORD=$route_receipt_password
SPYGLASS_AGENT_DISPATCH_WORKER_DATABASE_PASSWORD=$agent_dispatch_password
SPYGLASS_SCHEDULE_EXECUTION_WORKER_DATABASE_PASSWORD=$schedule_execution_password
SPYGLASS_AGENT_PROJECTION_WORKER_DATABASE_PASSWORD=$agent_projection_password
SPYGLASS_KNOWLEDGE_DOCUMENT_WORKER_DATABASE_PASSWORD=$knowledge_document_password
SPYGLASS_BASELINE_MAINTENANCE_WORKER_DATABASE_PASSWORD=$baseline_maintenance_password
SPYGLASS_INTEGRATION_CONNECTOR_WORKER_DATABASE_PASSWORD=$integration_connector_password
SPYGLASS_PROTOTYPE_MIGRATION_DATABASE_PASSWORD=$prototype_migration_password
SPYGLASS_CELL_A_RUNNER_CONTROLLER_DATABASE_PASSWORD=$runner_controller_a_password
SPYGLASS_CELL_B_RUNNER_CONTROLLER_DATABASE_PASSWORD=$runner_controller_b_password
SPYGLASS_CELL_A_RUNNER_BROKER_DATABASE_PASSWORD=$runner_broker_a_password
SPYGLASS_CELL_B_RUNNER_BROKER_DATABASE_PASSWORD=$runner_broker_b_password
SPYGLASS_GLOBAL_MIGRATION_DATABASE_URL=postgres://spyglass_migrator:$global_database_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_MIGRATION_DATABASE_URL=postgres://spyglass_migrator:$cell_a_database_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_MIGRATION_DATABASE_URL=postgres://spyglass_migrator:$cell_b_database_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_ACCOUNT_API_DATABASE_URL=postgres://spyglass_account_api:$account_api_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_ADMISSION_DATABASE_URL=postgres://spyglass_admission_api:$admission_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_APP_ROUTER_DATABASE_URL=postgres://spyglass_app_router:$app_router_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_MCP_GATEWAY_DATABASE_URL=postgres://spyglass_mcp_gateway:$mcp_gateway_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_APP_API_DATABASE_URL=postgres://spyglass_app_api:$app_api_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_APP_API_DATABASE_URL=postgres://spyglass_app_api:$app_api_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_BILLING_WORKER_DATABASE_URL=postgres://spyglass_billing_worker:$billing_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_NOTIFICATION_WORKER_DATABASE_URL=postgres://spyglass_notification_worker:$notification_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_ENTITLEMENT_WORKER_DATABASE_URL=postgres://spyglass_entitlement_worker:$entitlement_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_ACCOUNT_LIFECYCLE_WORKER_DATABASE_URL=postgres://spyglass_account_lifecycle_worker:$account_lifecycle_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_ACCOUNT_EXPORT_BUILD_GLOBAL_DATABASE_URL=postgres://spyglass_account_export_build_worker:$account_export_build_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_ACCOUNT_EXPORT_BUILD_DATABASE_URL=postgres://spyglass_account_export_build_worker:$account_export_build_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_ACCOUNT_EXPORT_BUILD_DATABASE_URL=postgres://spyglass_account_export_build_worker:$account_export_build_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_ACCOUNT_EXPORT_EXPIRY_DATABASE_URL=postgres://spyglass_account_export_expiry_worker:$account_export_expiry_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_IDENTITY_MAINTENANCE_WORKER_DATABASE_URL=postgres://spyglass_identity_maintenance_worker:$identity_maintenance_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_WORK_RECONCILER_GLOBAL_DATABASE_URL=postgres://spyglass_work_reconciler:$work_reconciler_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_WORK_RECONCILER_DATABASE_URL=postgres://spyglass_work_reconciler:$work_reconciler_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_WORK_RECONCILER_DATABASE_URL=postgres://spyglass_work_reconciler:$work_reconciler_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_ROUTE_RECEIPT_DATABASE_URL=postgres://spyglass_route_receipt_worker:$route_receipt_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_ROUTE_RECEIPT_DATABASE_URL=postgres://spyglass_route_receipt_worker:$route_receipt_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_AGENT_DISPATCH_DATABASE_URL=postgres://spyglass_agent_dispatch_worker:$agent_dispatch_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_AGENT_DISPATCH_DATABASE_URL=postgres://spyglass_agent_dispatch_worker:$agent_dispatch_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_SCHEDULE_EXECUTION_DATABASE_URL=postgres://spyglass_schedule_execution_worker:$schedule_execution_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_SCHEDULE_EXECUTION_DATABASE_URL=postgres://spyglass_schedule_execution_worker:$schedule_execution_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_AGENT_PROJECTION_DATABASE_URL=postgres://spyglass_agent_projection_worker:$agent_projection_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_AGENT_PROJECTION_DATABASE_URL=postgres://spyglass_agent_projection_worker:$agent_projection_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_KNOWLEDGE_DOCUMENT_DATABASE_URL=postgres://spyglass_knowledge_document_worker:$knowledge_document_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_KNOWLEDGE_DOCUMENT_DATABASE_URL=postgres://spyglass_knowledge_document_worker:$knowledge_document_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_BASELINE_MAINTENANCE_GLOBAL_DATABASE_URL=postgres://spyglass_baseline_maintenance_worker:$baseline_maintenance_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_BASELINE_MAINTENANCE_DATABASE_URL=postgres://spyglass_baseline_maintenance_worker:$baseline_maintenance_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_BASELINE_MAINTENANCE_DATABASE_URL=postgres://spyglass_baseline_maintenance_worker:$baseline_maintenance_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_INTEGRATION_CONNECTOR_GLOBAL_DATABASE_URL=postgres://spyglass_integration_connector_worker:$integration_connector_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_INTEGRATION_CONNECTOR_DATABASE_URL=postgres://spyglass_integration_connector_worker:$integration_connector_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_INTEGRATION_CONNECTOR_DATABASE_URL=postgres://spyglass_integration_connector_worker:$integration_connector_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_PROTOTYPE_MIGRATION_GLOBAL_DATABASE_URL=postgres://spyglass_prototype_migration:$prototype_migration_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_PROTOTYPE_MIGRATION_DATABASE_URL=postgres://spyglass_prototype_migration:$prototype_migration_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_PROTOTYPE_MIGRATION_DATABASE_URL=postgres://spyglass_prototype_migration:$prototype_migration_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_RUNNER_CONTROLLER_DATABASE_URL=postgres://spyglass_runner_controller:$runner_controller_a_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_RUNNER_CONTROLLER_DATABASE_URL=postgres://spyglass_runner_controller:$runner_controller_b_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_RUNNER_BROKER_DATABASE_URL=postgres://spyglass_runner_broker:$runner_broker_a_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_RUNNER_BROKER_DATABASE_URL=postgres://spyglass_runner_broker:$runner_broker_b_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_STRIPE_WEBHOOK_SECRET=$stripe_webhook_secret
SPYGLASS_STRIPE_SECRET_KEY=$stripe_secret_key
SPYGLASS_STRIPE_MODE=test
SPYGLASS_NOTIFICATION_ENCRYPTION_KEY=$notification_key
SPYGLASS_NETWORK_ACTOR_KEY=$network_actor_key
SPYGLASS_PRIVACY_PREFERENCE_KEY=$privacy_preference_key
SPYGLASS_ANALYTICS_HANDOFF_COOKIE_DOMAIN=stage.infiniteocean.net
SPYGLASS_AFFILIATE_ENROLLMENT_OPEN=false
SPYGLASS_AFFILIATE_ATTRIBUTION_ENABLED=false
SPYGLASS_AFFILIATE_SETTLEMENT_MODE=unconfigured
SPYGLASS_AFFILIATE_TERMS_VERSION=1
SPYGLASS_AFFILIATE_RULE_VERSION=1
SPYGLASS_PASSKEY_ENCRYPTION_KEYS=$passkey_keys
SPYGLASS_PASSKEY_ENCRYPTION_ACTIVE_VERSION=1
SPYGLASS_CELL_A_RUNNER_ENCRYPTION_KEYS=$runner_a_keys
SPYGLASS_CELL_A_RUNNER_ENCRYPTION_ACTIVE_VERSION=1
SPYGLASS_CELL_B_RUNNER_ENCRYPTION_KEYS=$runner_b_keys
SPYGLASS_CELL_B_RUNNER_ENCRYPTION_ACTIVE_VERSION=1
SPYGLASS_SMTP_ADDRESS=$smtp_address
SPYGLASS_SMTP_SERVER_NAME=$smtp_server_name
SPYGLASS_SMTP_USERNAME=$smtp_username
SPYGLASS_SMTP_PASSWORD=$smtp_password
SPYGLASS_SMTP_FROM_ADDRESS=$smtp_from_address
SPYGLASS_SMTP_FROM_NAME=$smtp_from_name
SPYGLASS_ROUTE_ISSUER=spyglass-stage-router
SPYGLASS_ROUTE_SIGNING_KEY_ID=stage-1
SPYGLASS_ROUTE_SIGNING_KEY=$route_key
SPYGLASS_ROUTE_VERIFY_KEYS=stage-1=$route_key
SPYGLASS_ADMISSION_CELL_IDS=cell-us-east-01,cell-us-west-01
SPYGLASS_TOOL_CONTEXT_ISSUER=spyglass-stage-runner-broker
SPYGLASS_CELL_A_TOOL_CONTEXT_SIGNING_KEY_ID=stage-a-1
SPYGLASS_CELL_A_TOOL_CONTEXT_SIGNING_KEY=$tool_a_key
SPYGLASS_CELL_B_TOOL_CONTEXT_SIGNING_KEY_ID=stage-b-1
SPYGLASS_CELL_B_TOOL_CONTEXT_SIGNING_KEY=$tool_b_key
SPYGLASS_TOOL_CONTEXT_VERIFY_KEYS=stage-a-1=$tool_a_key,stage-b-1=$tool_b_key
SPYGLASS_CELL_A_DOCKER_LAUNCHER_CONTROLLER_TOKEN=$launcher_controller_a_token
SPYGLASS_CELL_A_DOCKER_LAUNCHER_BROKER_TOKEN=$launcher_broker_a_token
SPYGLASS_CELL_B_DOCKER_LAUNCHER_CONTROLLER_TOKEN=$launcher_controller_b_token
SPYGLASS_CELL_B_DOCKER_LAUNCHER_BROKER_TOKEN=$launcher_broker_b_token
SPYGLASS_OPENAI_API_KEY=$openai_api_key
SPYGLASS_OPENAI_ORIGIN=$openai_origin
SPYGLASS_OPENAI_MODEL_PRICING_JSON=$openai_pricing
SPYGLASS_AGENT_EXECUTION_POLICIES_JSON=$agent_execution_policies
EOF

ca_dir="$work/workload-ca"
mkdir -p "$ca_dir"
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$ca_dir/ca.key" >/dev/null 2>&1
openssl req -new -x509 -sha256 -days 365 -key "$ca_dir/ca.key" \
  -subj '/CN=Spyglass Hostinger stage workload CA/O=Infinite Ocean stage' \
  -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign,digitalSignature' \
  -out "$ca_dir/ca.crt" >/dev/null 2>&1

issue() {
  local name="$1" dns_name="$2" uri="$3" usages="$4"
  local directory="$work/workload/$name" config="$work/$name.cnf"
  mkdir -p "$directory"
  cat >"$config" <<EOF
[req]
prompt=no
distinguished_name=subject
req_extensions=leaf
[subject]
CN=$name
O=Infinite Ocean stage
[leaf]
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=$usages
subjectAltName=@names
[names]
URI.1=$uri
EOF
  if [[ -n "$dns_name" ]]; then
    printf 'DNS.1=%s\n' "$dns_name" >>"$config"
  fi
  openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout "$directory/tls.key" -out "$work/$name.csr" -config "$config" >/dev/null 2>&1
  openssl x509 -req -sha256 -days 90 -in "$work/$name.csr" \
    -CA "$ca_dir/ca.crt" -CAkey "$ca_dir/ca.key" -CAcreateserial \
    -extfile "$config" -extensions leaf -out "$directory/tls.crt" >/dev/null 2>&1
  cp "$ca_dir/ca.crt" "$directory/ca.crt"
  chmod 600 "$directory/tls.key"
  chmod 444 "$directory/tls.crt" "$directory/ca.crt"
}

issue app-router '' 'spiffe://infiniteocean.net/spyglass/workloads/app-router' clientAuth
issue mcp-gateway '' 'spiffe://infiniteocean.net/spyglass/workloads/mcp-gateway' clientAuth
issue tool-router tool-router 'spiffe://infiniteocean.net/spyglass/workloads/app-router' serverAuth,clientAuth
issue app-api-a app-api-a 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/app-api' serverAuth,clientAuth
issue app-api-b app-api-b 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/app-api' serverAuth,clientAuth
issue admission-api admission-api 'spiffe://infiniteocean.net/spyglass/workloads/admission-api' serverAuth
issue agent-dispatch-worker-a '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/agent-dispatch-worker' clientAuth
issue agent-dispatch-worker-b '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/agent-dispatch-worker' clientAuth
issue schedule-execution-worker-a '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/schedule-execution-worker' clientAuth
issue schedule-execution-worker-b '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/schedule-execution-worker' clientAuth
issue runner-controller-a '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/runner-controller' clientAuth
issue runner-controller-b '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/runner-controller' clientAuth
issue runner-broker-a runner-broker-a 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/runner-broker' serverAuth,clientAuth
issue runner-broker-b runner-broker-b 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/runner-broker' serverAuth,clientAuth
issue docker-runner-launcher-a docker-runner-launcher-a 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/docker-runner-launcher' serverAuth
issue docker-runner-launcher-b docker-runner-launcher-b 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/docker-runner-launcher' serverAuth
issue model-gateway model-gateway 'spiffe://infiniteocean.net/spyglass/workloads/model-gateway' serverAuth

# Drive cursors are cell/grant-bound by AEAD and survive connector restarts.
# The OAuth client file is intentionally operator-furnished only when the
# Google adapter is enabled; disabled Stage deployments need no Google secret.
mkdir -p "$work/integration-source"
previous_source_dir=""
if [[ -n "$previous_env" ]]; then
  previous_source_dir="$(dirname "$previous_env")/integration-source"
fi
if [[ -n "$previous_source_dir" && -f "$previous_source_dir/cursor.key" && ! -L "$previous_source_dir/cursor.key" ]]; then
  [[ "$(stat -c %s "$previous_source_dir/cursor.key")" = 32 ]] || fail "previous Integration source cursor key must be exactly 32 bytes"
  cp -- "$previous_source_dir/cursor.key" "$work/integration-source/cursor.key"
else
  openssl rand 32 >"$work/integration-source/cursor.key"
fi
chmod 640 "$work/integration-source/cursor.key"
if [[ -n "$previous_source_dir" && -f "$previous_source_dir/google-oauth-client.json" && ! -L "$previous_source_dir/google-oauth-client.json" ]]; then
  case "$(stat -c %a "$previous_source_dir/google-oauth-client.json")" in 400|440|600|640) ;; *) fail "previous Google OAuth client file has unsafe permissions";; esac
  [[ "$(stat -c %s "$previous_source_dir/google-oauth-client.json")" -le 16384 ]] || fail "previous Google OAuth client file is too large"
  cp -- "$previous_source_dir/google-oauth-client.json" "$work/integration-source/google-oauth-client.json"
  chmod 640 "$work/integration-source/google-oauth-client.json"
fi

rm -f -- "$work"/*.csr "$work"/*.cnf
mkdir -p "$work/runner-identities-a" "$work/runner-identities-b"
rm -f -- "$ca_dir/ca.key" "$ca_dir/ca.srl"
chgrp -R "$secrets_gid" "$work/workload" "$work/integration-source" "$work/runner-identities-a" "$work/runner-identities-b"
find "$work/workload" -mindepth 1 -maxdepth 1 -type d -exec chmod 750 {} +
find "$work/workload" -mindepth 2 -maxdepth 2 -type f -name tls.key -exec chmod 640 {} +
chmod 770 "$work/runner-identities-a" "$work/runner-identities-b"
chmod 600 "$work/stage.env"
chmod 700 "$work" "$ca_dir" "$work/workload" "$work/integration-source"
if [[ -d "$target" ]]; then
  rmdir "$target"
fi
mv "$work" "$target"
trap - EXIT
echo "prepared stage secrets at $target"
