import assert from "node:assert/strict";
import test from "node:test";
import { isPublicCatalog, publishedPlans } from "../lib/catalog.ts";
import { exactRuntimeOrigin } from "../lib/runtime-origin.ts";
import { spyglassURLForOrigin } from "../lib/spyglass-url.ts";
import { publicCatalogFixture } from "./fixtures/public-catalog.mjs";

test("accepts the browser PublicCatalog fixture and maps current offers", () => {
  assert.equal(isPublicCatalog(publicCatalogFixture), true);
  const plans = publishedPlans(publicCatalogFixture, Date.parse("2026-08-18T00:00:00Z"));
  assert.deepEqual(plans?.map((plan) => [plan.name, plan.price, plan.offerCode]), [
    ["Free", "$0", undefined],
    ["Team", "$49", "team-monthly-v1"],
  ]);
});

test("rejects drift at the browser boundary", () => {
  assert.equal(isPublicCatalog({ ...publicCatalogFixture, limits: undefined }), false);
  assert.equal(isPublicCatalog({ ...publicCatalogFixture, internal_provider: "stripe" }), false);
  assert.equal(isPublicCatalog({ ...publicCatalogFixture, published_at: "2026-01-01" }), false);
  assert.equal(isPublicCatalog({ ...publicCatalogFixture, offers: [{ ...publicCatalogFixture.offers[0], amount_minor: -1 }] }), false);
  assert.equal(isPublicCatalog({ ...publicCatalogFixture, plans: [{ ...publicCatalogFixture.plans[0], packages: { knowledge: "invented" } }] }), false);
});

test("does not publish future or mismatched plan versions", () => {
  const future = { ...publicCatalogFixture.offers[1], effective_from: "2027-01-01T00:00:00Z" };
  const mismatched = { ...publicCatalogFixture.offers[1], plan_version: 2 };
  assert.deepEqual(publishedPlans({ ...publicCatalogFixture, offers: [future] }, Date.parse("2026-08-18T00:00:00Z")), undefined);
  assert.deepEqual(publishedPlans({ ...publicCatalogFixture, offers: [mismatched] }, Date.parse("2026-08-18T00:00:00Z")), undefined);
});

test("accepts only environment-appropriate exact runtime origins", () => {
  assert.equal(exactRuntimeOrigin("https://app.infiniteocean.net", "APP", "production"), "https://app.infiniteocean.net");
  assert.equal(exactRuntimeOrigin("http://account-api:8080", "API", "local"), "http://account-api:8080");
  assert.throws(() => exactRuntimeOrigin("http://account-api:8080", "API", "production"), /exact HTTPS origin/);
  assert.throws(() => exactRuntimeOrigin("https://app.infiniteocean.net/path", "APP", "production"), /exact HTTPS origin/);
  assert.throws(() => exactRuntimeOrigin("https://user@app.infiniteocean.net", "APP", "production"), /exact HTTPS origin/);
});

test("builds an opaque offer handoff without accepting price claims", () => {
  assert.equal(spyglassURLForOrigin("https://app.infiniteocean.net", "/signup", "team-monthly-v1"), "https://app.infiniteocean.net/signup?offer=team-monthly-v1");
});
