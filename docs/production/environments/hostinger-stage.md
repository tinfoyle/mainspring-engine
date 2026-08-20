# Hostinger Stage Environment

- SSH target: `infiniteocean` (commands originate in `ubunturojo`)
- Public origin: `https://stage.infiniteocean.net`
- Application origin: `https://app.stage.infiniteocean.net`
- Compose project: `spyglass-stage`

## Verified host facts (2026-08-20)

The read-only inventory found Ubuntu kernel 7.0 on x86-64, 2 vCPU, 7.7 GiB RAM, 96 GiB ext4 storage with about 89 GiB available, Docker 29.1.3 and Compose 2.40.3. The deployment user belongs to `docker` and has non-interactive sudo. The existing `infiniteocean` Compose project owns ports 80/443 through Caddy and owns the public mail ports through Stalwart. Spyglass must not replace, restart or bind over those services.

The stage override therefore joins the existing external `infiniteocean_public` network under alias `spyglass-stage-edge`. The existing Caddy remains the sole ACME/public edge and proxies the two stage hosts to that alias using `Caddyfile.hostinger-snippet`. The checked-in internal Caddy routes website/global APIs and the Account-scoped Work/Agent families without exposing any container port on the host.

## Files kept outside Git

Create `/opt/spyglass-stage/secrets/stage.env` with mode `600` from `env/stage.example`. Generate independent random database-role passwords and cryptographic keys; do not reuse local fixtures. Create these workload identity directories with a shared stage CA and exact DNS/SPIFFE SANs:

```text
/opt/spyglass-stage/secrets/workload/admission-api/{ca.crt,tls.crt,tls.key}
/opt/spyglass-stage/secrets/workload/app-router/{ca.crt,tls.crt,tls.key}
/opt/spyglass-stage/secrets/workload/app-api-a/{ca.crt,tls.crt,tls.key}
/opt/spyglass-stage/secrets/workload/app-api-b/{ca.crt,tls.crt,tls.key}
```

Private keys are mode `400` or `600`. Application and website values are exact GHCR `@sha256:` references. Stripe is test mode. SMTP requires TLS. The deployment refuses `REPLACE`, development mode, mutable images, an absent external edge network, missing workload identities, or a public 80/443 binding.

## Deployment

From a clean checkout on the VPS:

```bash
cd deploy/docker/spyglass
./verify-stage.sh /opt/spyglass-stage/secrets/stage.env
./deploy-stage.sh /opt/spyglass-stage/secrets/stage.env
```

Before the first `deploy-stage.sh`, merge the reviewed host snippet into `/opt/infiniteocean/caddy/Caddyfile`, run `docker exec infiniteocean-caddy-1 caddy validate --config /etc/caddy/Caddyfile`, and reload the existing Caddy only after validation. DNS for both names must resolve to the VPS. This is an explicit host-owner operation because the file also serves unrelated Infinite Ocean applications.

Record both image digests, Git revisions, Catalog version and the three restore checkpoints before promotion. Database backup hooks and schedules are supplied by the owner after the containers are in place. Rollback selects the preceding recorded digest pair and reruns the same verifier/deployer; database restoration is quarantined and follows signed erasure-checkpoint replay.
