# Hostinger Stage Environment

- SSH target: `infiniteocean` (commands originate in `ubunturojo`)
- Public origin: `https://stage.infiniteocean.net`
- Application origin: `https://app.stage.infiniteocean.net`
- MCP resource origin: `https://mcp.stage.infiniteocean.net`
- Compose project: `spyglass-stage`

## Verified host facts and live state (2026-09-03)

The read-only inventory found Ubuntu kernel 7.0 on x86-64, 2 vCPU, 7.7 GiB RAM, 96 GiB ext4 storage with about 89 GiB available, Docker 29.1.3 and Compose 2.40.3. The deployment user belongs to `docker` and has non-interactive sudo. The existing `infiniteocean` Compose project owns ports 80/443 through Caddy and owns the public mail ports through Stalwart. Spyglass must not replace, restart or bind over those services.

The stage override therefore joins the existing external `infiniteocean_public` network under alias `spyglass-stage-edge`. The existing Caddy remains the sole ACME/public edge and proxies the website, application and MCP stage hosts to that alias using `Caddyfile.hostinger-snippet`. The checked-in internal Caddy routes website/global APIs, the Account-scoped Work/Agent families and public MCP traffic without exposing any container port on the host.

RC.30 is deployed from the clean checkout `/opt/spyglass-stage/releases/0f979a5b057cf092a5202235d8e82f5ff42bcf5b`, and `/opt/spyglass-stage/current` selects that revision-controlled operator checkout. Earlier clean checkouts and secret sets are retained for controlled recovery, but rollback across the current schema requires an explicit compatibility decision rather than merely selecting an old directory. All three Stage origins resolve to `2.25.154.173`. The reviewed host routes are live in `/opt/infiniteocean/caddy/Caddyfile`, import the host's shared `security_headers` snippet, and return HSTS. Timestamped pre-change Caddy backups remain on the VPS. A temporary `mcp-client.stage.infiniteocean.net` Client ID Metadata Document supports the outstanding external-client certificate and must be removed after that evidence is sealed.

The protected provider input exists at `/opt/spyglass-stage/provider-input/stage.providers.env` with directory mode 700 and file mode 600. SMTP, Stripe sandbox/webhook, non-production model access and the reviewed non-secret model price book are present without disclosure. The private five-level Agent complexity policy currently maps all levels to the configured `gpt-5.6` provider model with increasing reasoning effort; it is an environment input, not a hard-coded product price. Stalwart implicit TLS is published on port 465 and healthy; the prior Compose file is retained as `/opt/infiniteocean/compose.yml.bak.20260821T143155Z.pre-smtps-465`.

Stage SMS delivery uses the Infinite Ocean Telnyx messaging profile and its assigned US long-code number. The protected provider input supplies `SPYGLASS_TELNYX_API_KEY` and `SPYGLASS_TELNYX_FROM`; these values belong only to the healthy notification worker and are not committed to a release environment file. The messaging profile is restricted to the United States and its START, STOP and HELP responses describe security-code traffic only. Configuration, provider ownership, the separate unchecked consent control and the exact deployed consent copy are verified. Real-handset receipt and redemption remain the final operator acceptance check.

The RC.30 application, public Nuxt UI and private Vue UI images run at the exact digests recorded in `deploy/releases/0.3.0-rc.30.env`; all three were built from application source revision `ca385a9839d049319138e6134e5ab1468847759f`, include provenance and SBOM attestations, and passed high/critical vulnerability plus secret scanning with zero findings. RC.30 replaces the fixed Baseline questionnaire with an adaptive conversation led by the Account's first `Operations Guide`. The interview classifies the business, asks only relevant follow-ups, records exact owner replies as governed Knowledge, proposes useful setup work, and creates only explicitly approved Work assigned to that Guide. There are 49 long-running Spyglass containers: all 48 healthchecked workloads are healthy and the internal edge is running without a healthcheck. The three PostgreSQL services retained their persistent volumes at global migration 68 and cell migrations 80/80. The public and login origins and MCP protected-resource metadata return `200`, and the running application/private UI containers match the RC.30 digests. Authenticated owner review of the adaptive interview remains open. The production legal-site evidence remains published from the revision-controlled page at `https://www.infiniteocean.net/sms-consent/`.

Catalog v3 is published with completed empty rollout and exact Stripe test-mode mappings for the $50 monthly team subscription, optional $250 commissioning package and $10/10,000-token starter package. Tax behavior is exclusive. Stage used a protected, Stage-only bootstrap signing identity with issuer `https://stage-catalog-bootstrap.infiniteocean.net` to exercise the governed draft/review/approval/publication path. That identity is not a production credential and must be replaced by the production operator identity plane before promotion.

## Files kept outside Git

The mode-600 provider input and generated secrets remain outside Git. Active immutable secret set `2026-08-31-01` carries the prior service credentials forward, adds the Telnyx API v2 key and originating number, and includes the Google login client copied from the operator-owned mode-600 input at `/opt/spyglass-stage/secrets/provider-inputs/google-login-client`; it is the only set used by the live stack. The provider file has a mode-600 pre-RC.6 backup. For an additive topology upgrade, choose a new empty versioned target and pass the previous active environment as the fourth argument. Supply the Google input as the fifth argument only when adding or rotating it; otherwise it is copied from the previous set:

```bash
# Edit the provider input without printing it to logs.
secret_set=/opt/spyglass-stage/secrets/YYYY-MM-DD-NN
previous_stage_env=/opt/spyglass-stage/secrets/2026-08-31-01/stage.env
./prepare-stage-secrets.sh \
  /opt/spyglass-stage/provider-input/stage.providers.env \
  "$secret_set" \
  infiniteocean_public \
  "$previous_stage_env"
```

`prepare-stage-secrets.sh` reads both inputs as data, preserves every existing generated database, object-store, encryption, signing and service credential, generates only credentials absent from the previous set, refreshes provider values, creates a new workload CA and exact DNS/SPIFFE/EKU leaves, and refuses a non-empty target. It then removes the CA private key. Omitting the previous environment is valid only for fresh provisioning with empty persistent stores. A full rotation of credentials already bound into retained PostgreSQL or object-store state requires a separate coordinated credential-change procedure; creating a fresh file and restarting containers is not sufficient. An existing secret set is never edited in place. The generated tree contains:

```text
<secret-set>/workload-ca/ca.crt
<secret-set>/workload/{admission-api,app-router,mcp-gateway,app-api-a,app-api-b,tool-router}/{ca.crt,tls.crt,tls.key}
<secret-set>/workload/{agent-dispatch-worker-a,agent-dispatch-worker-b,schedule-execution-worker-a,schedule-execution-worker-b}/{ca.crt,tls.crt,tls.key}
<secret-set>/workload/{runner-controller-a,runner-controller-b}/{ca.crt,tls.crt,tls.key}
<secret-set>/workload/{runner-broker-a,runner-broker-b}/{ca.crt,tls.crt,tls.key}
<secret-set>/workload/{docker-runner-launcher-a,docker-runner-launcher-b,model-gateway}/{ca.crt,tls.crt,tls.key}
<secret-set>/{runner-identities-a,runner-identities-b}/
<secret-set>/stage.env
```

Workload private keys are mode `640` in mode-`750` identity directories. Their group is derived from the protected provider file and supplied only to the 17 workload identity definitions; 18 running containers mount them because the stateless MCP gateway has two replicas. Keys remain unreadable to every other host user and container. Writable per-cell runner identity directories are mode `770` under the same group, while `stage.env` remains mode `600`. Application and website values come only from a reviewed, tracked `deploy/releases/<version>.env` file containing exact GHCR `@sha256:` references and their source revision; the secret stage file cannot override them. Stripe is test mode. SMTP requires TLS. The verifier checks certificate chains, key matches, group/mode contracts, seven-day minimum lifetime, exact DNS/SPIFFE/EKU contracts, immutable images, every application service's exact reviewed digest, exactly two Stage MCP replicas, complete two-cell runner topology, provider-egress membership, internal runner networks, Docker socket ownership and absence of public port bindings.

Each Account-export build worker uses a private mode-`0700`, UID/GID-65532
tmpfs capped at 1 GiB. That Stage-only ceiling reflects the VPS's 7.7 GiB RAM
and makes an oversized synthetic export fail closed without exhausting the
host. Local stress uses a 34 GiB tmpfs, while LKE uses a 34 GiB disk-backed
`emptyDir`; Stage is not capacity evidence for the production artifact limit.

## Deployment

From the clean checkout selected by `current`:

```bash
cd /opt/spyglass-stage/current/deploy/docker/spyglass
release_file="$(realpath ../../releases/0.3.0-rc.30.env)"
secret_set=/opt/spyglass-stage/secrets/2026-08-31-01
./verify-stage.sh "$release_file" "$secret_set/stage.env"
./deploy-stage.sh "$release_file" "$secret_set/stage.env"
```

The initial shared-edge merge is complete. For future edge changes, validate the host file inside `infiniteocean-caddy-1` before reload. Update the existing file in place: atomic replacement changes the bind-mounted inode and leaves the running Caddy container attached to stale content until it is recreated. The edge file also serves unrelated Infinite Ocean applications, so retain a timestamped backup for each change.

Record all three image digests, Git revision, Catalog version and the three restore checkpoints before promotion. Database backup hooks and schedules are supplied by the owner after the containers are in place. Rollback selects a recorded, schema-compatible artifact set and reruns the same verifier/deployer; the historical RC.5 -> RC.4 -> RC.5 rehearsal did not restore or replace database volumes and does not prove an RC.9 downgrade. Database restoration, when required, is quarantined and follows signed erasure-checkpoint replay.

## Resetting a synthetic signup fixture

The Stage operator link is installed from the revision-controlled checkout:

```bash
install -d -m 755 /opt/spyglass-stage/bin
ln -sfn \
  /opt/spyglass-stage/current/deploy/docker/spyglass/reset-stage-signup.sh \
  /opt/spyglass-stage/bin/reset-stage-signup
```

Inspect first; inspection is read-only and prints the exact User, Account,
cell and placement generation it resolved:

```bash
/opt/spyglass-stage/bin/reset-stage-signup inspect tester@example.com
```

Only after verifying that this is synthetic test data, repeat both the Account
UUID and normalized email shown by inspection:

```bash
/opt/spyglass-stage/bin/reset-stage-signup execute \
  tester@example.com 00000000-0000-4000-8000-000000000000 tester@example.com
```

The command is physically pinned to containers carrying the exact
`spyglass-stage` Compose project label. It accepts only one active, verified
User with one active owner membership, one active Account of type `inactive`,
one empty entitlement snapshot, one completed provisioning record and one
otherwise-empty cell namespace. It dynamically checks every current direct
User/Account foreign key and every unbound `user_id`/`account_id` column. Any
billing, checkout, commissioning, AI-token, Affiliate, Operations, MCP,
privacy-rights, export, closure, erasure, movement, additional membership or
cell-owned row refuses the operation. Such an Account must use the applicable
support or governed Account-erasure workflow instead.

When execution is permitted, the command removes the empty cell namespace and
then deletes the signup identity/consent/analytics shell in one guarded global
transaction, including consumed registration challenges and identity rate
limits for that User. If the global transaction refuses or fails, a trap
recreates the proven-empty cell namespace. It never clears anonymous abuse
windows, unrelated consent subjects, Catalog or provider data. Each successful
execution writes a mode-600 evidence record containing the operator, UTC time,
email SHA-256, exact IDs and result under
`/opt/spyglass-stage/evidence/test-resets`; the email itself is not recorded.
This tool is solely for revocable Stage test fixtures and is not the GDPR
Account/data deletion process.

## Evidence retained on the VPS

Evidence files are mode 600 under `/opt/spyglass-stage/evidence`:

| Path | SHA-256 |
|---|---|
| `0.2.5-rc.4/anonymous-boundary.json` | `b36353fec16fd5220cd2094a6f4e70374ef3d6914ccc2930ee608e7f8f877b92` |
| `0.2.5-rc.5/anonymous-boundary-post-rollback.json` | `83d680e9a215ed47cb943d32157ac817c91e35fa6ab64dae604f61e041af1d3f` |
| `0.2.5-rc.5/app-api-replica-restart.json` | `8201f58712c0752aa42c5faeb5a09308aa34eb87f1593835f4130a95f4ec95cf` |
| `0.2.5-rc.5/provider-readiness.json` | `73a53bcbd690685565fad59c6da7aa43e216bbf72e927c183d5a057c45ee529d` |
| `0.3.0-rc.1/schedule-queue-rehearsal.json` | `68a3e20c5a445ce21c1bdb33b01178af0b3af209550c2ba345180f2c4b795ae3` |
| `0.3.0-rc.2/anonymous-boundary.json` | `dd87e48f4c57070afc22f916118b900fe6b2268bd1b7aee73f7d4e6b16e4168d` |
| `0.3.0-rc.2/mcp-public-edge-failover.json` | `5ca33277955531c993b5305e89b3530fb1cd54a4fd29a6e38182e5a8d9ad0de5` |
| `0.3.0-rc.2/agent-queue-rehearsal.json` | `1c2da6f0d753d8d64419561e5690a52fcfa818b7a3168403a2e6dc512f62c1e9` |
| `0.3.0-rc.4/account-export-workers.json` | `f6367057000f1346ed58c061280788638257198f336b024e1c25a284b773fa3c` |

The Schedule recovery fixture and temporary execute-only database role were erased after certification. Stage otherwise remains reserved for revocable synthetic acceptance fixtures. Catalog v3 and its Stripe test mappings are active; signed-in browser, hosted Checkout/webhook, onboarding, Affiliate renewal-credit, analytics/consent and external MCP journeys still require human/provider acceptance before a complete-product production candidate can be declared.
