# Phase 3 product-journey certification

Status: executable content-free evidence combiner; final connected observations remain a Phase 3 release gate

`cmd/journey-cert` binds browser/device, email, passkey, Membership, Account isolation, Stripe, recovery, resilience, accessibility and cleanup observations to one reviewed plan and exact immutable release pair. It does not receive fixture values and does not perform privileged environment mutations.

## Boundary

Three records have different trust and retention requirements:

1. The **reviewed plan** contains public origins, immutable image/revision identities, Catalog version, required offer codes, check kinds and execution modes. It contains no credentials and may be revision-controlled.
2. The **restricted fixture inventory** contains synthetic email addresses, Account/User identifiers, mailbox credentials, session cookies, software/hardware authenticator material, Stripe object identifiers and cleanup handles. It is mode 600 in the environment secret store and is never passed to `journey-cert`. The plan binds only its SHA-256 digest.
3. The **content-free observations** contain plan check IDs, bounded status/failure codes, attempt counts, durations and hashes of separately retained runner reports. They are mode 600 and contain no raw errors, identifiers, URLs, provider payloads or customer content.

The portable report repeats only the reviewed release identity, plan and fixture-inventory digests, Catalog version, bounded check metadata and artifact hashes. It cannot establish that an underlying runner report was truthful; release review must verify those separately retained artifacts and their execution authority.

## Profiles

- `package-slice` records a bounded development or package acceptance set. Optional checks may fail without making the slice report fail.
- `phase3-product` is the final Hostinger product gate. It requires every defined journey kind and requires every check to pass. Omitting a kind is invalid input, not a reduced-scope success.

The canonical example is [phase3-product-plan.example.json](../../deploy/certification/phase3-product-plan.example.json). Its `.example` identities and origins are deliberately non-deployable placeholders. Copy it into the restricted release record and replace every release, origin, Catalog and fixture-inventory assertion with reviewed values.

## Observation production

Each specialized runner owns its own safe execution boundary:

- `staging-cert` supplies anonymous-boundary evidence;
- browser/device automation supplies registration, passkey, Membership, Checkout and recovery evidence;
- mail inspection supplies delivery evidence without copying message bodies;
- Stripe inspection supplies object/event state without copying provider payloads;
- database assertions supply aggregate isolation/projection/recovery counts without identifiers;
- failure controllers supply provider/container/database/runner recovery evidence;
- accessibility runs supply automated and human assistive-technology reports;
- cleanup supplies session revocation, synthetic Account lifecycle and provider-object disposition evidence.

Hash each retained runner report as `sha256:<lowercase hex>` and reference it by a bounded machine-name artifact kind. A passed check requires at least one artifact. Failed or blocked checks require a bounded failure code and remain evidence; create a new observations/report pair for a rerun.

The restricted observations file has this shape; the real file must cover the reviewed plan exactly:

```json
{
  "schema_version": 1,
  "plan_sha256": "sha256:<reviewed-plan-digest>",
  "started_at": "2026-08-21T12:00:00Z",
  "completed_at": "2026-08-21T12:10:00Z",
  "checks": [
    {
      "id": "registration-email",
      "status": "passed",
      "attempts": 1,
      "duration_ms": 1200,
      "artifacts": [
        {"kind": "browser-report", "sha256": "sha256:<artifact-digest>"},
        {"kind": "mail-report", "sha256": "sha256:<artifact-digest>"}
      ]
    }
  ]
}
```

The placeholders above are documentation, not accepted digests. Unknown fields and additional JSON values are rejected.

## Execution

Run from `ubunturojo`, with the observations file on its native Linux filesystem so owner-only permissions are meaningful:

```bash
chmod 600 /restricted/phase3-observations.json
go run ./cmd/journey-cert \
  -plan /restricted/phase3-product-plan.json \
  -observations /restricted/phase3-observations.json \
  -output /restricted/phase3-product-evidence.json
```

The command exits `0` only when every required observation passed, `1` for valid failing evidence, and `2` for invalid inputs or evidence-write failure. Output uses mode 600 and exclusive creation; evidence is never overwritten.

## Catalog and Stripe sequencing

Do not create provider-price mappings by direct SQL or weaken the signed operator boundary for certification. Once the relevant Phase 3 Catalog is acceptance-ready:

1. create the draft through `catalog-admin`;
2. create reviewed Stripe test Prices;
3. map the exact draft offers through separately authorized `catalog-admin map-price` jobs;
4. request review, approve with a distinct externally authenticated operator identity, and publish;
5. bind the published Catalog version and fixture inventory digest into the journey plan;
6. run connected journeys and archive artifact hashes;
7. clean up or retain synthetic fixtures under the reviewed test-data policy.

The active Phase 3 RC.1 stage database contains no retained synthetic customer Account after the Scheduling recovery drill. Customer/Catalog/Stripe journey fixtures remain deferred until this governed sequence is ready; operational queue evidence does not substitute for that acceptance plan.
