import assert from "node:assert/strict";
import test from "node:test";
import { publicCatalogFixture } from "./fixtures/public-catalog.mjs";

async function render(path = "/") {
  return fetch(`${process.env.TEST_ORIGIN}${path}`, { headers: { accept: "text/html" } });
}

test("renders the Infinite Ocean Spyglass home page", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("strict-transport-security"), "max-age=31536000; includeSubDomains");
  assert.equal(response.headers.get("x-content-type-options"), "nosniff");
  assert.equal(response.headers.get("x-frame-options"), "DENY");
  assert.equal(response.headers.get("cross-origin-opener-policy"), "same-origin");
  assert.equal(response.headers.get("cross-origin-resource-policy"), "same-origin");
  assert.match(response.headers.get("permissions-policy"), /camera=\(\)/);
  assert.match(response.headers.get("content-security-policy"), /default-src 'self'.*frame-ancestors 'none'.*script-src 'self' 'nonce-[^']+' 'strict-dynamic'/);
  const html = await response.text();
  assert.match(html, /<title>Infinite Ocean: Spyglass<\/title>/i);
  assert.match(html, /See the whole business/);
  assert.match(html, /Move what matters/);
  assert.match(html, /Create a free account/);
  assert.match(html, /Work/);
  assert.match(html, /Agents/);
  assert.match(html, /Finance/);
  assert.match(html, /Marketing/);
  assert.doesNotMatch(html, /codex-preview|react-loading-skeleton|Mainspring/i);
});

test("renders the public package and signup journeys", async () => {
  const [packages, signup, pricing] = await Promise.all([render("/packages"), render("/signup"), render("/pricing")]);
  assert.equal(packages.status, 200);
  assert.equal(signup.status, 200);
  assert.equal(pricing.status, 200);
  assert.match(await packages.text(), /Feature packages/);
  const signupHtml = await signup.text();
  assert.match(signupHtml, /No payment information required/);
  assert.match(signupHtml, /app\.infiniteocean\.net\/signup/);
  assert.doesNotMatch(signupHtml, /method="post"[^>]*app\.infiniteocean\.net/i);
  assert.doesNotMatch(signupHtml, /name="email"|name="account_name"/i);
  const pricingHtml = await pricing.text();
  assert.match(pricingHtml, /No card required/);
  assert.match(pricingHtml, /app\.infiniteocean\.net\/signup\?offer=team-monthly-v1/);
});

test("proxies only the anonymous published Catalog from the configured account origin", async () => {
  await fetch(`${process.env.TEST_CATALOG_ORIGIN}/__reset`, { method: "POST" });
  const response = await fetch(`${process.env.TEST_ORIGIN}/api/catalog`, { headers: { accept: "application/json" } });
  assert.equal(response.status, 200);
  const requests = await fetch(`${process.env.TEST_CATALOG_ORIGIN}/__requests`).then((value) => value.json());
  assert.deepEqual(requests, [{ method: "GET", url: "/api/v1/catalog/public" }]);
  assert.match(response.headers.get("cache-control"), /stale-while-revalidate/);
  assert.equal(response.headers.get("strict-transport-security"), "max-age=31536000; includeSubDomains");
  assert.equal(response.headers.get("x-content-type-options"), "nosniff");
  assert.equal(response.headers.get("content-security-policy"), null);
  assert.deepEqual(await response.json(), publicCatalogFixture);
});
