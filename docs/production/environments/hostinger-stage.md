# Hostinger Stage Environment

- SSH target: `infiniteocean` (commands originate in `ubunturojo`)
- Public origin: `https://stage.infiniteocean.net`
- Application origin: `https://app.stage.infiniteocean.net`
- Compose project: `spyglass-stage`

## Verified host facts (2026-08-20)

The read-only inventory found Ubuntu kernel 7.0 on x86-64, 2 vCPU, 7.7 GiB RAM, 96 GiB ext4 storage with about 89 GiB available, Docker 29.1.3 and Compose 2.40.3. The deployment user belongs to `docker` and has non-interactive sudo. The existing `infiniteocean` Compose project owns ports 80/443 through Caddy and owns the public mail ports through Stalwart. Spyglass must not replace, restart or bind over those services.

The stage override therefore joins the existing external `infiniteocean_public` network under alias `spyglass-stage-edge`. The existing Caddy remains the sole ACME/public edge and proxies the two stage hosts to that alias using `Caddyfile.hostinger-snippet`. The checked-in internal Caddy routes website/global APIs and the Account-scoped Work/Agent families without exposing any container port on the host.

The clean detached RC.5 release-record checkout `14c39aceff34a4ebf2c90955979d50422ee9628c` is prepared at `/opt/spyglass-stage/releases/14c39aceff34a4ebf2c90955979d50422ee9628c`. The older RC.3 checkout `ac7bb50821033bebd843397b9e2126c1e63ae54d` remains inactive, rejected release history because its website image failed the later admission scan; do not select it for deployment. `/opt/spyglass-stage/secrets` is an empty mode-700 directory owned by the deployment user. No `current` link was created and no Spyglass container was started. On 2026-08-21 both stage origins resolved to `2.25.154.173`, the VPS public address. The reviewed host routes were added to `/opt/infiniteocean/caddy/Caddyfile`, validated and reloaded; the pre-change file is retained as `/opt/infiniteocean/caddy/Caddyfile.bak.20260821T142740Z.pre-spyglass-stage`. HTTPS currently returns the expected `502` until the `spyglass-stage-edge` backend starts.

The protected provider input now exists at `/opt/spyglass-stage/provider-input/stage.providers.env` with directory mode 700 and file mode 600. Existing VPS SMTP username, password and from-address values were copied into it without display. Stalwart already listened with implicit TLS on container port 465; its Compose service and UFW policy now publish that port, the Stalwart-only recreation returned healthy, and the prior Compose file is retained as `/opt/infiniteocean/compose.yml.bak.20260821T143155Z.pre-smtps-465`. The admitted RC.5 application and website images are authenticated and pre-pulled at their exact reviewed digests. Stripe test webhook/key and a non-production OpenAI key remain deliberately unset.

## Files kept outside Git

The mode-600 provider input has already been copied outside both Git and the generated secret set. Replace only the remaining `REPLACE` values with Stripe test and non-production OpenAI values, then create an immutable versioned secret set:

```bash
# Edit the provider input without printing it to logs.
secret_set=/opt/spyglass-stage/secrets/2026-08-21-01
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

Private keys are mode `400` or `600`. Application and website values come only from a reviewed, tracked `deploy/releases/<version>.env` file containing exact GHCR `@sha256:` references and their source revision; the secret stage file cannot override them. Stripe is test mode. SMTP requires TLS. The verifier checks certificate chains, key matches, seven-day minimum lifetime, exact DNS/SPIFFE/EKU contracts, immutable images, complete two-cell runner topology, provider-egress membership, internal runner networks, Docker socket ownership and absence of public port bindings.

## Deployment

From a clean checkout on the VPS:

```bash
cd deploy/docker/spyglass
release_file="$(realpath ../../releases/0.2.5-rc.5.env)"
secret_set=/opt/spyglass-stage/secrets/2026-08-21-01
./verify-stage.sh "$release_file" "$secret_set/stage.env"
./deploy-stage.sh "$release_file" "$secret_set/stage.env"
```

Before the first `deploy-stage.sh`, merge the reviewed host snippet into `/opt/infiniteocean/caddy/Caddyfile`, run `docker exec infiniteocean-caddy-1 caddy validate --config /etc/caddy/Caddyfile`, and reload the existing Caddy only after validation. DNS for both names must resolve to the VPS. This is an explicit host-owner operation because the file also serves unrelated Infinite Ocean applications.

Record both image digests, Git revisions, Catalog version and the three restore checkpoints before promotion. Database backup hooks and schedules are supplied by the owner after the containers are in place. Rollback selects the preceding recorded digest pair and reruns the same verifier/deployer; database restoration is quarantined and follows signed erasure-checkpoint replay.
