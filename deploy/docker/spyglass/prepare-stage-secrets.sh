#!/usr/bin/env bash
set -euo pipefail

provider_file="${1:?usage: prepare-stage-secrets.sh /absolute/path/to/stage.providers.env /absolute/path/to/secrets [edge-network]}"
target="${2:?usage: prepare-stage-secrets.sh /absolute/path/to/stage.providers.env /absolute/path/to/secrets [edge-network]}"
edge_network="${3:-infiniteocean_public}"

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

global_database_password="$(random_password)"
cell_a_database_password="$(random_password)"
cell_b_database_password="$(random_password)"
account_api_password="$(random_password)"
app_router_password="$(random_password)"
admission_password="$(random_password)"
billing_password="$(random_password)"
notification_password="$(random_password)"
entitlement_password="$(random_password)"
account_lifecycle_password="$(random_password)"
identity_maintenance_password="$(random_password)"
work_reconciler_password="$(random_password)"
app_api_password="$(random_password)"
route_receipt_password="$(random_password)"
agent_dispatch_password="$(random_password)"
agent_projection_password="$(random_password)"
knowledge_document_password="$(random_password)"
baseline_maintenance_password="$(random_password)"
prototype_migration_password="$(random_password)"
runner_controller_a_password="$(random_password)"
runner_controller_b_password="$(random_password)"
runner_broker_a_password="$(random_password)"
runner_broker_b_password="$(random_password)"
object_store_access_key="$(openssl rand -hex 10)"
object_store_secret_key="$(random_password)"
object_store_app_access_key="$(openssl rand -hex 10)"
object_store_app_secret_key="$(random_password)"
object_store_worker_access_key="$(openssl rand -hex 10)"
object_store_worker_secret_key="$(random_password)"
object_store_migration_access_key="$(openssl rand -hex 10)"
object_store_migration_secret_key="$(random_password)"
object_store_kms_secret_key="spyglass:$(random_key)"
notification_key="$(random_key)"
network_actor_key="$(random_key)"
passkey_key="$(random_key)"
runner_a_key="$(random_key)"
runner_b_key="$(random_key)"
route_key="$(random_key)"
tool_a_key="$(random_key)"
tool_b_key="$(random_key)"
launcher_controller_a_token="$(random_token)"
launcher_broker_a_token="$(random_token)"
launcher_controller_b_token="$(random_token)"
launcher_broker_b_token="$(random_token)"

cat >"$work/stage.env" <<EOF
SPYGLASS_ENVIRONMENT=stage
SPYGLASS_PROCESS_ENV=stage
SPYGLASS_PUBLIC_ORIGIN=https://stage.infiniteocean.net
SPYGLASS_APP_ORIGIN=https://app.stage.infiniteocean.net
SPYGLASS_PASSKEY_RP_ID=app.stage.infiniteocean.net
SPYGLASS_CELL_A_ROUTE_ORIGIN=https://app-api-a:8443
SPYGLASS_CELL_B_ROUTE_ORIGIN=https://app-api-b:8443
SPYGLASS_WORK_ADMISSION_ORIGIN=https://admission-api:8443
SPYGLASS_HOST_EDGE_NETWORK=$edge_network
SPYGLASS_STAGE_SECRETS_DIRECTORY=$target
SPYGLASS_STAGE_SECRETS_GID=$secrets_gid
SPYGLASS_OBJECT_STORE_ENDPOINT=object-store:9000
SPYGLASS_OBJECT_STORE_BUCKET=spyglass-documents
SPYGLASS_OBJECT_STORE_ACCESS_KEY=$object_store_access_key
SPYGLASS_OBJECT_STORE_SECRET_KEY=$object_store_secret_key
SPYGLASS_OBJECT_STORE_APP_ACCESS_KEY=$object_store_app_access_key
SPYGLASS_OBJECT_STORE_APP_SECRET_KEY=$object_store_app_secret_key
SPYGLASS_OBJECT_STORE_WORKER_ACCESS_KEY=$object_store_worker_access_key
SPYGLASS_OBJECT_STORE_WORKER_SECRET_KEY=$object_store_worker_secret_key
SPYGLASS_OBJECT_STORE_MIGRATION_ACCESS_KEY=$object_store_migration_access_key
SPYGLASS_OBJECT_STORE_MIGRATION_SECRET_KEY=$object_store_migration_secret_key
SPYGLASS_OBJECT_STORE_KMS_SECRET_KEY=$object_store_kms_secret_key
SPYGLASS_OBJECT_STORE_SECURE=false
SPYGLASS_OBJECT_STORE_SERVER_SIDE_ENCRYPTION=true
SPYGLASS_CLAMAV_DISABLE_UPDATES=false
SPYGLASS_GLOBAL_DATABASE_PASSWORD=$global_database_password
SPYGLASS_CELL_A_DATABASE_PASSWORD=$cell_a_database_password
SPYGLASS_CELL_B_DATABASE_PASSWORD=$cell_b_database_password
SPYGLASS_ACCOUNT_API_DATABASE_PASSWORD=$account_api_password
SPYGLASS_APP_ROUTER_DATABASE_PASSWORD=$app_router_password
SPYGLASS_ADMISSION_DATABASE_PASSWORD=$admission_password
SPYGLASS_BILLING_WORKER_DATABASE_PASSWORD=$billing_password
SPYGLASS_NOTIFICATION_WORKER_DATABASE_PASSWORD=$notification_password
SPYGLASS_ENTITLEMENT_WORKER_DATABASE_PASSWORD=$entitlement_password
SPYGLASS_ACCOUNT_LIFECYCLE_WORKER_DATABASE_PASSWORD=$account_lifecycle_password
SPYGLASS_IDENTITY_MAINTENANCE_WORKER_DATABASE_PASSWORD=$identity_maintenance_password
SPYGLASS_WORK_RECONCILER_DATABASE_PASSWORD=$work_reconciler_password
SPYGLASS_APP_API_DATABASE_PASSWORD=$app_api_password
SPYGLASS_ROUTE_RECEIPT_WORKER_DATABASE_PASSWORD=$route_receipt_password
SPYGLASS_AGENT_DISPATCH_WORKER_DATABASE_PASSWORD=$agent_dispatch_password
SPYGLASS_AGENT_PROJECTION_WORKER_DATABASE_PASSWORD=$agent_projection_password
SPYGLASS_KNOWLEDGE_DOCUMENT_WORKER_DATABASE_PASSWORD=$knowledge_document_password
SPYGLASS_BASELINE_MAINTENANCE_WORKER_DATABASE_PASSWORD=$baseline_maintenance_password
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
SPYGLASS_CELL_A_APP_API_DATABASE_URL=postgres://spyglass_app_api:$app_api_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_APP_API_DATABASE_URL=postgres://spyglass_app_api:$app_api_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_BILLING_WORKER_DATABASE_URL=postgres://spyglass_billing_worker:$billing_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_NOTIFICATION_WORKER_DATABASE_URL=postgres://spyglass_notification_worker:$notification_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_ENTITLEMENT_WORKER_DATABASE_URL=postgres://spyglass_entitlement_worker:$entitlement_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_ACCOUNT_LIFECYCLE_WORKER_DATABASE_URL=postgres://spyglass_account_lifecycle_worker:$account_lifecycle_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_IDENTITY_MAINTENANCE_WORKER_DATABASE_URL=postgres://spyglass_identity_maintenance_worker:$identity_maintenance_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_WORK_RECONCILER_GLOBAL_DATABASE_URL=postgres://spyglass_work_reconciler:$work_reconciler_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_WORK_RECONCILER_DATABASE_URL=postgres://spyglass_work_reconciler:$work_reconciler_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_WORK_RECONCILER_DATABASE_URL=postgres://spyglass_work_reconciler:$work_reconciler_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_ROUTE_RECEIPT_DATABASE_URL=postgres://spyglass_route_receipt_worker:$route_receipt_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_ROUTE_RECEIPT_DATABASE_URL=postgres://spyglass_route_receipt_worker:$route_receipt_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_AGENT_DISPATCH_DATABASE_URL=postgres://spyglass_agent_dispatch_worker:$agent_dispatch_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_AGENT_DISPATCH_DATABASE_URL=postgres://spyglass_agent_dispatch_worker:$agent_dispatch_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_AGENT_PROJECTION_DATABASE_URL=postgres://spyglass_agent_projection_worker:$agent_projection_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_AGENT_PROJECTION_DATABASE_URL=postgres://spyglass_agent_projection_worker:$agent_projection_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_KNOWLEDGE_DOCUMENT_DATABASE_URL=postgres://spyglass_knowledge_document_worker:$knowledge_document_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_KNOWLEDGE_DOCUMENT_DATABASE_URL=postgres://spyglass_knowledge_document_worker:$knowledge_document_password@cell-b-db:5432/spyglass?sslmode=disable
SPYGLASS_BASELINE_MAINTENANCE_GLOBAL_DATABASE_URL=postgres://spyglass_baseline_maintenance_worker:$baseline_maintenance_password@global-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_A_BASELINE_MAINTENANCE_DATABASE_URL=postgres://spyglass_baseline_maintenance_worker:$baseline_maintenance_password@cell-a-db:5432/spyglass?sslmode=disable
SPYGLASS_CELL_B_BASELINE_MAINTENANCE_DATABASE_URL=postgres://spyglass_baseline_maintenance_worker:$baseline_maintenance_password@cell-b-db:5432/spyglass?sslmode=disable
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
SPYGLASS_PASSKEY_ENCRYPTION_KEYS=1=$passkey_key
SPYGLASS_PASSKEY_ENCRYPTION_ACTIVE_VERSION=1
SPYGLASS_CELL_A_RUNNER_ENCRYPTION_KEYS=1=$runner_a_key
SPYGLASS_CELL_A_RUNNER_ENCRYPTION_ACTIVE_VERSION=1
SPYGLASS_CELL_B_RUNNER_ENCRYPTION_KEYS=1=$runner_b_key
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
issue tool-router tool-router 'spiffe://infiniteocean.net/spyglass/workloads/app-router' serverAuth,clientAuth
issue app-api-a app-api-a 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/app-api' serverAuth,clientAuth
issue app-api-b app-api-b 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/app-api' serverAuth,clientAuth
issue admission-api admission-api 'spiffe://infiniteocean.net/spyglass/workloads/admission-api' serverAuth
issue agent-dispatch-worker-a '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/agent-dispatch-worker' clientAuth
issue agent-dispatch-worker-b '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/agent-dispatch-worker' clientAuth
issue runner-controller-a '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/runner-controller' clientAuth
issue runner-controller-b '' 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/runner-controller' clientAuth
issue runner-broker-a runner-broker-a 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/runner-broker' serverAuth,clientAuth
issue runner-broker-b runner-broker-b 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/runner-broker' serverAuth,clientAuth
issue docker-runner-launcher-a docker-runner-launcher-a 'spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/docker-runner-launcher' serverAuth
issue docker-runner-launcher-b docker-runner-launcher-b 'spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/docker-runner-launcher' serverAuth
issue model-gateway model-gateway 'spiffe://infiniteocean.net/spyglass/workloads/model-gateway' serverAuth

rm -f -- "$work"/*.csr "$work"/*.cnf
mkdir -p "$work/runner-identities-a" "$work/runner-identities-b"
rm -f -- "$ca_dir/ca.key" "$ca_dir/ca.srl"
chgrp -R "$secrets_gid" "$work/workload" "$work/runner-identities-a" "$work/runner-identities-b"
find "$work/workload" -mindepth 1 -maxdepth 1 -type d -exec chmod 750 {} +
find "$work/workload" -mindepth 2 -maxdepth 2 -type f -name tls.key -exec chmod 640 {} +
chmod 770 "$work/runner-identities-a" "$work/runner-identities-b"
chmod 600 "$work/stage.env"
chmod 700 "$work" "$ca_dir" "$work/workload"
if [[ -d "$target" ]]; then
  rmdir "$target"
fi
mv "$work" "$target"
trap - EXIT
echo "prepared stage secrets at $target"
