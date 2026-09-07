# Stage Kimi verification — 2026-09-07

Status: passed on Stage RC63. Production was not changed.

The owner later authorized a [workspace test-data reset](stage-owner-workspace-reset.md). The live conversation/document fixtures below were cleared; this report records the successful pre-reset verification.

## Release and configuration

- Source: `e3473ecd955708ca302091d0ea728a026301f2a4`.
- Reviewed release manifest and active checkout: `9cdeb949f04f12c2c8dd271439bcb344fe1644a6`.
- Manifest: `deploy/releases/0.3.0-rc.63.env`, four immutable images.
- Active protected environment: `/opt/spyglass-stage/secrets/2026-09-07-kimi-01/stage.env`.
- Provider/model: `kimi` / `kimi-k3`. Complexity efforts: low for simple/efficient/balanced, high for thorough, max for advanced.
- OpenAI remains configured for previously admitted runs and configuration rollback. No automatic cross-provider fallback.

The supplied VPS key initially authenticated but model calls failed for insufficient balance. After the owner confirmed funding, K3 calls succeeded. The key stayed on the VPS and was never printed or committed.

## Compatibility defect and fix

The direct synthetic K3 check exposed a protocol difference: combining strict final JSON Schema with business tools caused K3 to skip a required lookup, including invented facts in one response. The Kimi adapter now expresses the final schema as a private terminal function, `spyglass_final_result`. The existing harness still executes all real tools, enforces capabilities, checks output and citations, and requires approval for consequential actions.

A two-step synthetic certification performed the requested lookup and then returned exactly Tuesday 08:30 and $17.40. It preserved opaque continuation and reported cached input usage. The existing OpenAI request behavior is unchanged. See [integration details and data-handling limits](kimi-provider.md).

## Live owner Boardroom test

Signed in through Google as the existing Infinite Ocean owner. Used Weekly Operations / Shop Coordinator version 2 with its existing five tools and approval policy. Inspected the editor without saving another version. Attached the existing published synthetic document, revision 1.

Conversation: [Kimi K3 — Stage tool and document verification](https://app.stage.infiniteocean.net/app/agents/boardrooms/eee01c43-a20a-49c5-b040-211fde575e3c/conversations/4b2e5de9-a1cd-8311-850d-bd80f5cd1c02).

Prompt asked the agent to use Read schedules, count paused schedules, and give the attached document’s delivery window and handling charge with a source. It explicitly prohibited schedule changes, email and other record changes.

Observed:

- Audit: model call succeeded → `schedules.read` authorized and succeeded → second model call succeeded.
- Answer: all four existing schedules paused; Tuesday at 08:30; handling charge $17.40, separate from material prices.
- Sources panel quoted the matching published document passage.
- Run `bf1b6ce4-bf4a-4587-9b7b-3d427e286ef5`: succeeded.
- Invocation `0f4c572e-3a63-875e-98ea-d007ce16d242`: succeeded; expected and returned provider/model `kimi` / `kimi-k3`.
- Result queue: projected, no failure code.
- Usage across both model calls: 10,397 input + 1,025 output = 11,422 provider tokens. Provider cost recorded: 36,198 USD micros ($0.036198), including cached-input pricing.
- Customer AI Token reservation: settled 72 of a 2,500 maximum; no active reservation left for this invocation. Customer AI Tokens are distinct from provider token counts.
- All four owner schedules remain paused. Only the owner’s test conversation/run records were added; no consequential tool ran and no report email was requested.

This certifies the Kimi provider through the real custom harness, a read-only platform tool, attached evidence, citation validation, result projection and billing settlement. It does not independently certify every tool or high/max-effort workflow. Existing tool/action tests remain the coverage for those paths.

## Validation and operating state

- Full `go test ./...` passed; focused adapter, bootstrap and model gateway tests passed after the final adapter prompt change.
- Automated coverage includes separate provider credential dispatch, Kimi-only startup, rejection of incomplete configuration, terminal function handling, reserved-name rejection, unstructured final rejection, preserved OpenAI behavior and cached-price accounting.
- Full Stage secret/Compose contract passed on the VPS using isolated temporary test credentials and a temporary network.
- Publication vulnerability and secret scans passed for the four images.
- Prepared OpenAI rollback environment passed Stage preflight; the live stack was not switched back.
- Public website and application login returned HTTP 200.
- Deployment completed; `/opt/spyglass-stage/current` points to the reviewed RC63 checkout.
- Database ledgers: global 72, cell A/B 90/90; no migration introduced.
- Stage containers: 53 running, 52 healthy, zero unhealthy. Internal edge has no container health check.

Operator logs are under `/home/rojo/.cache/spyglass-stage-audit/logs/`: `kimi-go-all.log`, `publish-rc63.log`, `kimi-stage-contract-vps.log`, `deploy-rc63.log`, `kimi-live-verification.log`. Synthetic provider response evidence stays in the protected VPS directory `/home/benjamin/.cache/spyglass-kimi-certification/`; it is not a customer-facing log or a committed artifact.
