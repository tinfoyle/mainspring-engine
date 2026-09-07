# Kimi provider integration

Status: implementation and synthetic provider checks prepared on 2026-09-07; Stage cutover verification is recorded separately when complete.

## Architecture

The model gateway registers `openai` and optionally `kimi` as separate providers. Credentials terminate in this workload. Existing tools, capability authorization, admission, immutable run plans, approvals and result projection stay in Spyglass's custom harness. An old OpenAI run remains OpenAI even after the admission mapping changes.

Kimi K3 uses `https://api.moonshot.ai/v1/responses`. The configured origin is `https://api.moonshot.ai`, without `/v1`. The shared Responses adapter has a Kimi-only structured completion mode: it presents the final result schema as a private `spyglass_final_result` function. The adapter translates that terminal function's arguments into the existing final output; it does not dispatch a business action or charge an extra platform tool invocation. A caller cannot grant a tool with that reserved name. Final output and citations retain the existing downstream validation.

This mode is necessary because the live K3 test with both `text.format` JSON Schema and business tools produced a final answer without calling the required lookup, including invented facts in one test. Removing `text.format` and supplying the terminal function produced the required lookup followed by an exact structured answer from its result. The OpenAI request format is unchanged.

The request asks for one call at a time and sends `parallel_tool_calls:false`. Kimi's response envelope nevertheless reports that field as true. Spyglass continues to reject multiple returned calls; the flag is not treated as an authorization guarantee. Full opaque reasoning/function continuation is returned to the provider between steps; reasoning is not shown in customer output.

## Configuration

- `SPYGLASS_KIMI_API_KEY`: server-only API key.
- `SPYGLASS_KIMI_ORIGIN`: defaults to `https://api.moonshot.ai`.
- `SPYGLASS_KIMI_MODEL_PRICING_JSON`: explicit trusted price book. Both key and prices are required to enable Kimi; blank values leave it disabled.
- `SPYGLASS_AGENT_EXECUTION_POLICIES_JSON`: existing private mapping for all five complexity levels. The target provider is `kimi`, model `kimi-k3`, with no cross-provider fallback.

Reviewed K3 prices on 2026-09-07: $3 per million uncached input tokens, $0.30 cached input, $15 output. The corresponding price entry is:

```json
{"kimi-k3":{"input_micros_per_million_tokens":3000000,"cached_input_micros_per_million_tokens":300000,"output_micros_per_million_tokens":15000000}}
```

Source: [Kimi API Platform pricing](https://platform.kimi.ai/). Prices are an operator-owned snapshot and must be reviewed when changed. The gateway records all provider output usage, including billable reasoning counted in output by the API. Cached input is a subset of total input and is not double charged. Omission of the cached price preserves the old price book behavior; an explicit zero means free cached input. Customer AI Token rates are separate and do not change in this migration.

Use supported K3 efforts: `low`, `high`, `max`. The proposed mapping uses low for simple/efficient/balanced, high for thorough, and max for advanced. Existing bounded input/output limits and run cost ceilings remain effective.

Keep the existing OpenAI configuration for older admitted runs and rollback. Switching the complexity mapping does not implement cross-provider automatic fallback. Stage's standard Compose configuration retains its OpenAI credentials and adds optional Kimi credentials only to model-gateway.

## Credentials and rollout

The supplied credential is `/home/benjamin/kimi-api` on the VPS, a regular mode-600 file. It is never printed, committed or copied to the local workspace. Read it on the VPS when preparing a new mode-600 Stage environment file. Preserve the existing environment file and mounted workload identity paths. Do not edit the active file in place.

First certify synthetic calls with the actual API. Then publish a clean, pushed release, review and commit its image manifest, verify the new Stage environment, deploy the immutable images and switch new admissions. Test through the owner's Boardroom with read-only tool use and an explicitly attached synthetic document. Verify the frozen invocation provider/model, tool execution, projected output, token settlement and service health. Do not run tests against another customer's records or reactivate the paused report schedules.

`test-stage-contract.sh` uses the current complete four-image manifest by default and accepts an explicit manifest path. Its full preflight requires the protected Stage access-log directory to exist on the machine running it. The test creates isolated temporary credentials and a temporary Docker network; it does not start the application stack or use real provider keys.

## Data handling

The Responses API returned `store:false` during the live checks, and Spyglass sends no stored-response IDs. This is not a contractual guarantee of zero retention or no training. [Kimi's standard API terms, section 4](https://platform.kimi.ai/docs/agreement/modeluse) allow content use for model improvement/training unless separately agreed in writing. Record any enterprise restriction separately; do not describe this integration as zero-retention or no-training based on the API flag. Synthetic certification sends only test facts. The provider change is confined to Stage; production deployment requires its own provider and data-processing review.

## Verification so far

- The VPS key authenticated. A first model call was rejected for insufficient balance. After the owner funded the account, `kimi-k3` responded successfully.
- Live synthetic lookup and terminal-function continuation returned Tuesday 08:30 and $17.40 exactly. The second step reported 256 cached input tokens.
- Full Go suite passed, including separate-provider credential dispatch, Kimi-only startup, terminal function handling, reserved-name rejection, strict OpenAI request preservation and cached-price accounting.
- Local secret generation and preservation checks passed. The Stage contract reaches the host access-log preflight; its final host-specific verification is run on the VPS during release preparation.
