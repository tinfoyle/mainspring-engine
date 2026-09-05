# UbuntuRojo and Stage release handoff

Status: current as of Spyglass `0.3.0-rc.46` on 2026-09-05.

This is the short operator path for working on Spyglass locally and moving a
review candidate to the Hostinger Stage VPS. It is not a production/LKE
procedure.

## Ground rules

- Run repository, Git, Docker, build, test, publication, SSH and SCP commands
  in the `UbuntuRojo` WSL distribution. The repository is mounted at
  `/mnt/c/Users/Tinfo/Documents/Mainspring`.
- Prefer opening UbuntuRojo and working there directly. From Windows, enter it
  with `wsl.exe -d UbuntuRojo`; do not translate the release commands into
  PowerShell.
- The Stage SSH alias is `infiniteocean` in UbuntuRojo's SSH configuration.
  For a multi-command deployment, use `ssh -tt infiniteocean` and run the
  commands at the remote prompt. Complex remote Bash nested inside a
  PowerShell command is easy to misquote: PowerShell can consume assignments,
  quotes or command separators while SSH still exits in a misleading state.
- If the native SSH config lacks this alias, the configured alias can be used
  from UbuntuRojo with `ssh -F /mnt/c/Users/Tinfo/.ssh/config -tt infiniteocean`
  (and the same `-F` argument for `scp`). The Stage identity file must remain
  on the native Linux filesystem. This fallback was verified during the
  2026-09-04 functional audit.
- `rg` is not presently installed in UbuntuRojo. Use `grep` and `find` when it
  is unavailable.
- Direct commits to `main` are the accepted one-owner workflow. Preserve
  unrelated working-tree changes and never publish from a dirty tree.
- GitHub Actions is disabled and workflow YAML is forbidden by the architecture
  tests. UbuntuRojo is the build, test, scan and publication environment.
- Never print, commit or copy Stage secret values into a release manifest.

## Current connected state

Stage uses four HTTPS names:

- `https://stage.infiniteocean.net`
- `https://app.stage.infiniteocean.net`
- `https://mcp.stage.infiniteocean.net`
- `https://ops.stage.infiniteocean.net` (passkey-only staff console)

The active release is `0.3.0-rc.46`. Its four images were built from source commit
`81383e20e275c5eda65c37a6a0308aa0c6a9e423`; the reviewed manifest and active
VPS checkout are commit `fdbf6bd16140ffdc2ad46d494fe2897092dd3c7d`.
`/opt/spyglass-stage/current` resolves to that immutable checkout. The database
ledgers are global `69` and cell A/B `86/86`. Stage has 51 long-running
containers; all 50 health checks pass and the internal edge is the exception.

The automatic dispatch introduced in RC.41 remains active for eligible
agent-owned Work. The agent either
completes it or creates a specific information request in Your Turn. Capacity
deferrals do not consume retries. A genuine execution failure is attempted
three times and then becomes an explicit Your Turn recovery question. Work
marked for the User remains human-owned and is not auto-dispatched.

The current reviewed fixture has two agent-owned items waiting for human input:
the pricing review needs token-cost facts, and the launch-readiness review needs
a narrower first outcome after three model failures. That is intentional
waiting, not an idle agent queue.

RC.44 completes the [functional audit and plain-language UI review](stage-functional-audit-2026-09-04.md). It repairs saved schedule runs, no-tool agent creation and summary selection, Marketing file review and human approval, Finance supporting notes and archive guidance, and Knowledge fact navigation. Live synthetic workflows passed after deployment; the public phone layout and customer-facing copy were also corrected.

RC.45 simplifies the pricing page: a padded monthly-plan card, a separate optional setup section, clear AI Token pricing, and a compact heading. Public tests, type checking, lint, build and desktop/phone accessibility and signup checks passed; the live Stage pricing page was also reviewed. The Catalog amounts and checkout contract are unchanged.

RC.46 adds the isolated staff UI/API, modular Traffic & logs, Analytics and Help
screens, audited administrator-only IP reports, and protected edge access logs.
See the [Stage admin guide](stage-admin-guide.md) and
[verification record](stage-admin-verification-2026-09-05.md). The live consent,
ingestion, cohort-reporting, withdrawal and erasure certificate passed on all
three analytics surfaces. Live denial and related-origin passkey checks passed;
the owner still has zero passkeys and zero staff roles. Successful owner admin
sign-in and authenticated live report acceptance remain pending physical
enrollment and offline role assignment. No authentication requirement was bypassed.

Cell migration 86 permits narrowly scoped human Marketing approvals without a model invocation. Once those rows exist, RC.41 is not a compatible rollback target. Any rollback must retain this origin and its Marketing decision authorization; selecting an older checkout alone is insufficient.

## Local change and test loop

Start in UbuntuRojo:

```bash
cd /mnt/c/Users/Tinfo/Documents/Mainspring
git status --short
go test ./...
git diff --check
```

Run focused UI, migration or Docker checks appropriate to the change before the
full suite. The disposable PostgreSQL migration tests are important for Work,
Attention, Baseline and queue changes; an in-memory test alone is not evidence
that triggers, RLS and replay behavior compose correctly.

Commit and push the finished source change before publishing. The local Git
credential helper reads the native-Linux GHCR token without printing it:

```bash
git add <reviewed-files>
git commit -m "Describe the change"
GIT_TERMINAL_PROMPT=0 \
  git -c credential.helper="$PWD/.git/codex-gh-credential-helper.sh" \
  push origin main
```

The convenience token in the Windows-mounted repository is not publication
input. The publisher requires the GHCR token at
`~/.config/spyglass/ghcr-token` on UbuntuRojo's native filesystem, as a regular
non-symlink file with mode `600` and one line.

## Cut a Stage release

Choose a new immutable SemVer without a `v` prefix. Never reuse an RC number:

```bash
cd /mnt/c/Users/Tinfo/Documents/Mainspring
./deploy/docker/spyglass/publish-stage-release.sh 0.3.0-rc.N
```

The publisher refuses anything except clean, pushed `main`; refuses untracked
files and existing version/revision tags; builds all four Linux/AMD64 images (application, public UI, private UI and operations UI);
attaches SBOM and provenance; scans the immutable digests for high/critical
vulnerabilities and secrets; and writes
`deploy/releases/0.3.0-rc.N.env`.

That generated manifest is not optional bookkeeping. Review it, then commit and
push it:

```bash
git add deploy/releases/0.3.0-rc.N.env
git commit -m "Record Spyglass 0.3.0-rc.N release"
GIT_TERMINAL_PROMPT=0 \
  git -c credential.helper="$PWD/.git/codex-gh-credential-helper.sh" \
  push origin main
git rev-parse HEAD
```

There are now two relevant revisions:

1. `SPYGLASS_RELEASE_REVISION` is the source commit embedded in the images.
2. The newer Git commit containing the manifest is the exact Stage operator
   checkout. Do not confuse them or deploy from an arbitrary later working tree.

## Transfer and deploy

Create a Git bundle from the manifest commit in UbuntuRojo and transfer it. Use
the manifest commit as `<checkout-commit>`:

```bash
git bundle create /tmp/spyglass-rcN.bundle --all
scp /tmp/spyglass-rcN.bundle \
  infiniteocean:/opt/spyglass-stage/releases/spyglass-rcN.bundle
ssh -tt infiniteocean
```

At the VPS prompt, create a new checkout. Release directories are immutable and
must not be reused:

```bash
test ! -e /opt/spyglass-stage/releases/<checkout-commit>
git clone -q \
  /opt/spyglass-stage/releases/spyglass-rcN.bundle \
  /opt/spyglass-stage/releases/<checkout-commit>
cd /opt/spyglass-stage/releases/<checkout-commit>
git checkout -q <checkout-commit>
rm /opt/spyglass-stage/releases/spyglass-rcN.bundle
git rev-parse HEAD
```

The tracked release manifest and protected Stage environment must be supplied
separately. The active protected file is currently
`/opt/spyglass-stage/secrets/2026-08-31-01/stage.env`; it is mode `600`, is not
in Git, and contains provider and workload secrets. Do not edit an active secret
set in place.

From the exact checkout on the VPS:

```bash
release_file=/opt/spyglass-stage/releases/<checkout-commit>/deploy/releases/0.3.0-rc.N.env
stage_env=/opt/spyglass-stage/secrets/2026-08-31-01/stage.env
./deploy/docker/spyglass/verify-stage.sh "$release_file" "$stage_env"
./deploy/docker/spyglass/deploy-stage.sh "$release_file" "$stage_env"
ln -sfn /opt/spyglass-stage/releases/<checkout-commit> \
  /opt/spyglass-stage/current
```

`verify-stage.sh` requires absolute paths and requires the release manifest to
be a tracked file inside that same checkout. The protected Stage file cannot
override image digests. `deploy-stage.sh` verifies again, pulls the exact GHCR
digests and performs the Compose update.

Hostinger's existing `infiniteocean` Caddy owns ports 80/443 and other Infinite
Ocean services. Spyglass joins its external network; do not replace or restart
the host edge as part of an ordinary application release.

## Verify after deployment

Do not use `https://app.stage.infiniteocean.net/healthz` as a public smoke test;
that path currently returns `404`. Check the real public surfaces, Compose state,
exact checkout and migration ledgers:

```bash
curl -fsS -o /dev/null https://stage.infiniteocean.net/
curl -fsS -o /dev/null https://app.stage.infiniteocean.net/login
readlink -f /opt/spyglass-stage/current
docker ps --filter label=com.docker.compose.project=spyglass-stage \
  --format '{{.Names}}' | wc -l
docker ps --filter label=com.docker.compose.project=spyglass-stage \
  --filter health=unhealthy --format '{{.Names}}'
docker exec spyglass-stage-cell-a-db-1 \
  psql -U spyglass_migrator -d spyglass -At -F '|' \
  -c "select count(*),max(version) from public.spyglass_schema_migrations;"
```

Repeat the ledger query for `cell-b-db` and `global-db`. The table is
`public.spyglass_schema_migrations`, not `schema_migrations`. Then perform the
authenticated feature journey relevant to the release. A Compose update may
leave the review browser at a login screen, so reauthenticate and reload before
calling a UI regression.

Only after the live checks pass should the deployment record be updated. A
rollback is not merely changing `current`: migrations use retained volumes, so
the selected artifact, schema, Catalog and configuration must be known
compatible.

## Related operator documents

- [Hostinger Stage environment](environments/hostinger-stage.md)
- [Release artifacts and provenance](release-artifacts.md)
- [Local, Stage and production deployment report](stage-production-deployment-report.md)
- [Work-to-Agent execution](work-agent-execution.md)
- [Stage test-account reset](environments/hostinger-stage.md#resetting-a-synthetic-signup-fixture)
