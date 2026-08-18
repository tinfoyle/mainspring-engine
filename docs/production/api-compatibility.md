# Customer API compatibility policy

Status: executable pull-request breaking-change gate; publication and deprecation governance still require release ownership

Spyglass treats [`api/spyglass.openapi.json`](../../api/spyglass.openapi.json) as a first-party production contract, not documentation generated after implementation. Every pull request runs `cmd/apicontract` for route/source drift and `cmd/apicompat` against the target branch for backward compatibility.

## Gate behavior

The compatibility command compares every operation already present in the base contract. It rejects:

- removed method/path operations;
- changed operation IDs, service ownership, or authentication requirements;
- removed parameters or newly required parameters;
- removed required request bodies or JSON request support;
- request types, patterns, formats, bounds, required properties, enums, or additional-property policy that accept fewer previously valid inputs;
- removed success statuses, JSON bodies, response headers, or response properties;
- response types, patterns, formats, bounds, required properties, enums, or additional-property policy that can emit values outside the old client contract.

The direction matters. A request enum may grow because old clients remain valid; a response enum may not grow because an exhaustive old client would receive an unknown value. A response may add a field, while removing an old optional field is still treated as breaking because generated clients may read it.

New operations are compatible at this layer but still require the authorization, Account-isolation, error, observability, and browser tests required by [api-contract.md](api-contract.md). The existing whole-surface test separately requires every operation to remain typed.

## Deliberate bounds

This is a conservative classifier for the JSON/header subset Spyglass publishes. It is not a claim to implement every OpenAPI rule. `anyOf` changes are treated as breaking unless structurally identical after local-reference expansion. External references are already forbidden. Stripe event bodies and WebAuthn extension dictionaries remain explicit open envelopes, so provider evolution does not masquerade as a Spyglass API break.

The gate does not decide:

- whether a new operation should be first-party-only or partner-public;
- whether a changed Problem `code` needs a migration window;
- semantic behavior changes that leave the wire schema unchanged;
- release timing, deprecation duration, or customer communication.

Those remain release-review decisions backed by handler/browser fixtures and deployed telemetry.

## Intentional breaking change

Do not weaken or bypass the gate. For an intentional break:

1. choose a versioned path or a documented parallel field/status behavior;
2. keep the old behavior during the agreed migration window;
3. add contract fixtures for both versions and telemetry for remaining old-client use;
4. publish the deprecation and removal dates with an owner;
5. remove the old contract only after the exit evidence is reviewed.

Run locally with any saved base document:

```text
go run ./cmd/apicompat -base /path/to/base.openapi.json -head api/spyglass.openapi.json
```
