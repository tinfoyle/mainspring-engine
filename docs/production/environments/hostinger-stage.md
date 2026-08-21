# Hostinger Stage Environment

- SSH target: `infiniteocean` (commands originate in `ubunturojo`)
- Public origin: `https://stage.infiniteocean.net`
- Application origin: `https://app.stage.infiniteocean.net`
- Compose project: `spyglass-stage`

## Verified host facts and live state (2026-08-21)

The read-only inventory found Ubuntu kernel 7.0 on x86-64, 2 vCPU, 7.7 GiB RAM, 96 GiB ext4 storage with about 89 GiB available, Docker 29.1.3 and Compose 2.40.3. The deployment user belongs to `docker` and has non-interactive sudo. The existing `infiniteocean` Compose project owns ports 80/443 through Caddy and owns the public mail ports through Stalwart. Spyglass must not replace, restart or bind over those services.

The stage override therefore joins the existing external `infiniteocean_public` network under alias `spyglass-stage-edge`. The existing Caddy remains the sole ACME/public edge and proxies the two stage hosts to that alias using `Caddyfile.hostinger-snippet`. The checked-in internal Caddy routes website/global APIs and the Account-scoped Work/Agent families without exposing any container port on the host.

The active clean checkout is `/opt/spyglass-stage/releases/d127a4c7159b20329412b55436e0db4a98e0dfeb`, selected by `/opt/spyglass-stage/current`. The older RC.3 checkout remains rejected release history because its website image failed the later admission scan; do not select it. Both stage origins resolve to `2.25.154.173`. The reviewed host routes are live in `/opt/infiniteocean/caddy/Caddyfile`, import the host's shared `security_headers` snippet, and return HSTS. Timestamped pre-change Caddy backups remain on the VPS.

The protected provider input exists at `/opt/spyglass-stage/provider-input/stage.providers.env` with directory mode 700 and file mode 600. SMTP, Stripe sandbox/webhook and non-production OpenAI values are present without disclosure. Stalwart implicit TLS is published on port 465 and healthy; the prior Compose file is retained as `/opt/infiniteocean/compose.yml.bak.20260821T143155Z.pre-smtps-465`. The admitted RC.5 application and website images run at their exact reviewed digests. There are 31 long-running Spyglass containers: all 30 healthchecked workloads are healthy and the internal edge is running. The three PostgreSQL services use persistent volumes.

## Files kept outside Git

The mode-600 provider input and generated secrets remain outside Git. Active immutable secret set `2026-08-21-02` was generated after all provider values were supplied and is the only set used by the live stack. To rotate it, choose a new empty versioned target; never edit an existing set:

```bash
# Edit the provider input without printing it to logs.
secret_set=/opt/spyglass-stage/secrets/YYYY-MM-DD-NN
./prepare-stage-secrets.sh \
  /opt/spyglass-stage/provider-input/stage.providers.env \
  "$secret_set" \
  infiniteocean_public
```

`prepare-stage-secrets.sh` reads the provider file as data, generates independent database passwords, per-cell runner encryption/signing keys, disjoint launcher tokens and a mode-600 `stage.env`, and refuses a non-empty target. It creates a temporary stage CA, issues exact DNS/SPIFFE/EKU leaves, then removes the CA private key. A new versioned target is required for rotation; an existing secret set is never edited in place. The generated tree contains:

```text
<secret-set>/workload-ca/ca.crt
<secret-set>/workload/{admission-api,app-router,app-api-a,app-api-b,tool-router}/{ca.crt,tls.crt,tls.key}
<secret-set>/workload/{runner-controller-a,runner-controller-b}/{ca.crt,tls.crt,tls.key}
<secret-set>/workload/{runner-broker-a,runner-broker-b}/{ca.crt,tls.crt,tls.key}
<secret-set>/workload/{docker-runner-launcher-a,docker-runner-launcher-b,model-gateway}/{ca.crt,tls.crt,tls.key}
<secret-set>/{runner-identities-a,runner-identities-b}/
<secret-set>/stage.env
```

Workload private keys are mode `640` in mode-`750` identity directories. Their group is derived from the protected provider file and supplied only as a supplemental group to the 12 containers that mount workload identities; keys remain unreadable to every other host user and container. Writable per-cell runner identity directories are mode `770` under the same group, while `stage.env` remains mode `600`. Application and website values come only from a reviewed, tracked `deploy/releases/<version>.env` file containing exact GHCR `@sha256:` references and their source revision; the secret stage file cannot override them. Stripe is test mode. SMTP requires TLS. The verifier checks certificate chains, key matches, group/mode contracts, seven-day minimum lifetime, exact DNS/SPIFFE/EKU contracts, immutable images, complete two-cell runner topology, provider-egress membership, internal runner networks, Docker socket ownership and absence of public port bindings.

## Deployment

From the clean checkout selected by `current`:

```bash
cd /opt/spyglass-stage/current/deploy/docker/spyglass
release_file="$(realpath ../../releases/0.2.5-rc.5.env)"
secret_set=/opt/spyglass-stage/secrets/2026-08-21-02
./verify-stage.sh "$release_file" "$secret_set/stage.env"
./deploy-stage.sh "$release_file" "$secret_set/stage.env"
```

The initial shared-edge merge is complete. For future edge changes, validate the host file inside `infiniteocean-caddy-1` before reload. Update the existing file in place: atomic replacement changes the bind-mounted inode and leaves the running Caddy container attached to stale content until it is recreated. The edge file also serves unrelated Infinite Ocean applications, so retain a timestamped backup for each change.

Record both image digests, Git revisions, Catalog version and the three restore checkpoints before promotion. Database backup hooks and schedules are supplied by the owner after the containers are in place. Rollback selects the preceding recorded digest pair and reruns the same verifier/deployer; the successful RC.5 -> RC.4 -> RC.5 rehearsal did not restore or replace database volumes. Database restoration, when required, is quarantined and follows signed erasure-checkpoint replay.

## Phase 2.5 evidence retained on the VPS

Evidence files are mode 600 under `/opt/spyglass-stage/evidence`:

| Path | SHA-256 |
|---|---|
| `0.2.5-rc.4/anonymous-boundary.json` | `b36353fec16fd5220cd2094a6f4e70374ef3d6914ccc2930ee608e7f8f877b92` |
| `0.2.5-rc.5/anonymous-boundary-post-rollback.json` | `83d680e9a215ed47cb943d32157ac817c91e35fa6ab64dae604f61e041af1d3f` |
| `0.2.5-rc.5/app-api-replica-restart.json` | `8201f58712c0752aa42c5faeb5a09308aa34eb87f1593835f4130a95f4ec95cf` |
| `0.2.5-rc.5/provider-readiness.json` | `73a53bcbd690685565fad59c6da7aa43e216bbf72e927c183d5a057c45ee529d` |

Stage currently has zero users, Accounts, provider-price mappings, billing profiles and subscriptions. Phase 3 creates final Catalog mappings through signed operator authorization and then adds revocable synthetic customer fixtures for email/passkey/Stripe certification.
