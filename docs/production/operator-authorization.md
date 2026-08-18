# Platform Operator Authorization

- Status: executable Phase 2 authorization boundary
- Scope: one-shot Spyglass administrator processes; never customer User or Account authorization

## 1. Trust boundary

`SPYGLASS_OPERATOR_ID` is audit metadata, not authentication. Every `catalog-admin`, `passkey-admin`, `work-release-admin`, and `account-erasure-admin` invocation now fails before opening a database unless it verifies a short-lived authorization envelope issued by the external Infinite Ocean operator identity plane.

The issuer enrolls platform administrators, requires phishing-resistant authentication, applies workforce lifecycle policy, records approvals, and holds the Ed25519 private signing keys. Spyglass receives only a rotation keyring of public keys. A customer passkey, recovery code, Account role, database password, environment confirmation, or operator-name string cannot mint this authority.

## 2. Signed envelope

The compact token is `base64url(header).base64url(claims).base64url(signature)`. The signature covers the first two segments exactly.

```json
{"alg":"EdDSA","typ":"SPYGLASS-OPERATOR-AUTH","kid":"operator-2026-08"}
```

| Claim | Contract |
|---|---|
| `version` | Exact integer `1` |
| `issuer` | Exact configured operator issuer |
| `authorization_id` | Unique UUID correlating external approval and the durable Spyglass audit reason |
| `actor` | Exact externally authenticated operator; matches `SPYGLASS_OPERATOR_ID` |
| `action` | Exact `<mode>:<action>`, such as `catalog-admin:publish` |
| `environment` | Exact deployment environment |
| `reason_sha256` | Lowercase SHA-256 of the exact operator reason |
| `scope_sha256` | Lowercase SHA-256 of the canonical operation scope |
| `assurance` | Exact `phishing_resistant` |
| `mode` | `standard` or `break_glass` |
| `issued_at`, `expires_at` | Unix seconds; positive lifetime no longer than ten minutes |

JSON is strict. Unknown fields, trailing values, unknown algorithms/types/keys, malformed identifiers, changed inputs, stale/future evidence, and invalid signatures fail closed. Thirty seconds of clock skew is permitted. The token is never logged. After verification, Spyglass adds `[authorization=<id>;mode=<mode>]` to the reason stored by the existing immutable operator audit workflow.

## 3. Scope preparation and issuance

Run the intended command once without `SPYGLASS_OPERATOR_AUTHORIZATION`. It performs local input validation, logs `actor`, exact `action`, `environment`, `reason_sha256`, and `scope_sha256`, then stops before database composition. Submit those values and the human-readable reason through the external approval workflow. Inject the returned envelope only into the isolated one-shot job and rerun with identical inputs.

The canonical scope is compact JSON with lexically sorted keys. It binds:

| Process | Bound fields |
|---|---|
| `passkey-admin` | Active envelope-key version, complete configured version set, re-encryption batch |
| `catalog-admin` | Catalog version or draft content digest, Offer, Stripe mode/Price, effective time |
| `work-release-admin` | Inspection limit or exact Account/Work/reservation target |
| `account-erasure-admin` | Request, Account confirmation, cell, expected/policy versions, export evidence, backup deadline, lease, restore-directive path |

Secrets and database URLs are absent. Existing command validation separately checks environment/Account confirmations, signed restore directives, key material, and workflow state. Mutation replay remains bounded to the already-authorized exact operation and is rejected or made idempotent by each workflow's version/state fences. Authorization envelopes must be destroyed with the one-shot job.

## 4. Standard operation

```text
SPYGLASS_OPERATOR_ID
SPYGLASS_OPERATOR_REASON
SPYGLASS_ENVIRONMENT
SPYGLASS_CONFIRM_ENVIRONMENT
SPYGLASS_OPERATOR_AUTH_ISSUER
SPYGLASS_OPERATOR_AUTH_VERIFY_KEYS=<kid>=<standard-base64-Ed25519-public-key>[,...]
SPYGLASS_OPERATOR_AUTHORIZATION=<compact-signed-envelope>
```

The confirmation must exactly match the environment. Catalog administration uses the same environment confirmation as the other administrator processes.

## 5. Break glass

Break glass is not a weaker signature or bypass. The issuer must record an incident, authenticate the responder with a pre-enrolled phishing-resistant factor, and include at least two distinct approvers who are also distinct from the responder. The envelope uses `mode=break_glass`, a bounded `incident_id`, and the approver identities.

Spyglass rejects that valid envelope unless the isolated job also sets:

```text
SPYGLASS_ALLOW_BREAK_GLASS=true
SPYGLASS_CONFIRM_BREAK_GLASS_ENVIRONMENT=<exact environment>
```

The verified log contains only authorization ID, mode, signing-key ID, expiry, incident ID, and approval count. The existing immutable database event contains the action, actor, exact operational change, augmented reason, and authorization ID. The external identity plane retains factor and approval evidence. Emergency authority never comes from a customer recovery code and never creates an Account Membership.

## 6. Key rotation and failure handling

1. Add the next Ed25519 public key under a new key ID while retaining the old public key.
2. Issue a canary authorization with the new signing key and run a read-only inspection action.
3. Move issuance to the new key.
4. Wait beyond the ten-minute maximum lifetime plus clock skew and active one-shot-job lifetime.
5. Remove the old public key. Private-key destruction remains an issuer responsibility.

An unavailable issuer stops administrator execution; it does not permit unsigned fallback. If every enrolled administrator factor is lost, use only the issuer's separately governed workforce recovery ceremony. If that ceremony invokes break glass, two independent approvals and explicit deployment confirmation remain mandatory. A leaked private signing key requires immediate issuer revocation, removal of its public key from every environment, job-token invalidation, and review of correlated authorization IDs and database audit events.
