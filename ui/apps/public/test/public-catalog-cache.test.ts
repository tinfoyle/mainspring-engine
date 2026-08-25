import { describe, expect, it } from "vitest";
import {
  PublicCatalogCache,
  PublicCatalogUnavailableError,
  PublicCatalogValidationError,
  normalizePublicCatalog,
  resolvePublicCatalog
} from "../server/utils/publicCatalogCache";

function catalog(version = 1): Record<string, unknown> {
  return {
    version,
    published_at: "2026-08-25T12:00:00Z",
    packages: [{ code: "work", name: "Work", description: "Governed work", features: ["Queues"], version: 1 }],
    limits: [{ code: "work_items", combine: "maximum", kind: "capacity", name: "Work items", package_code: "work", unit: "items" }],
    plans: [{ code: "team", version: 1, name: "Team", description: "Team plan", packages: { work: "enabled" } }],
    offers: [{ code: "team-monthly-v1", plan_code: "team", plan_version: 1, currency: "USD", amount_minor: 5000, billing_interval: "month", effective_from: "2026-08-25T12:00:00Z" }]
  };
}

describe("public Catalog last-known-good projection", () => {
  it("normalizes only reviewed public fields and rejects provider-specific fields", () => {
    const input = { ...catalog(), unreviewed_copy: "not projected" };
    expect(normalizePublicCatalog(input)).not.toHaveProperty("unreviewed_copy");
    expect(() => normalizePublicCatalog({ ...catalog(), stripe_price_id: "price_secret" })).toThrow(PublicCatalogValidationError);
  });

  it("serves a recent verified publication after a transient upstream failure", async () => {
    const cache = new PublicCatalogCache(300_000);
    await expect(resolvePublicCatalog(async () => catalog(), cache, () => 1_000)).resolves.toMatchObject({ state: "fresh", ageSeconds: 0, catalog: { version: 1 } });
    await expect(resolvePublicCatalog(async () => { throw new Error("upstream unavailable"); }, cache, () => 61_500)).resolves.toMatchObject({ state: "stale", ageSeconds: 60, catalog: { version: 1 } });
  });

  it("never replaces a snapshot with an older or same-version-mutated publication", async () => {
    const cache = new PublicCatalogCache(300_000);
    cache.accept(catalog(2), 1_000);
    expect(() => cache.accept(catalog(1), 2_000)).toThrow("Catalog version moved backward");
    const changed = catalog(2);
    (changed.offers as Array<Record<string, unknown>>)[0]!.amount_minor = 9900;
    expect(() => cache.accept(changed, 2_000)).toThrow("Catalog changed without a version increment");

    await expect(resolvePublicCatalog(async () => catalog(1), cache, () => 3_000)).resolves.toMatchObject({ state: "stale", catalog: { version: 2 } });
  });

  it("fails closed when no recent verified publication exists", async () => {
    const empty = new PublicCatalogCache(300_000);
    await expect(resolvePublicCatalog(async () => { throw new Error("offline"); }, empty, () => 1_000)).rejects.toBeInstanceOf(PublicCatalogUnavailableError);

    const expired = new PublicCatalogCache(300_000);
    expired.accept(catalog(), 1_000);
    await expect(resolvePublicCatalog(async () => { throw new Error("offline"); }, expired, () => 301_001)).rejects.toBeInstanceOf(PublicCatalogUnavailableError);
  });
});
